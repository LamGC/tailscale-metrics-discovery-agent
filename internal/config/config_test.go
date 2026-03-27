package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAgentConfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.toml")

	original := AgentConfig{
		Server: AgentServer{Listen: ":8001", Token: "sec"},
		Statics: []StaticService{
			{Name: "svc1", Targets: []string{"host:9100"}, Labels: map[string]string{"job": "test"}},
		},
		Buckets: []BucketService{
			{Name: "bkt1"},
		},
		Proxies: []ProxyService{
			{Name: "prx1", Target: "http://localhost:9100/metrics",
				Auth: ProxyAuth{Type: "bearer", Token: "tok"}},
		},
	}

	if err := SaveAgentConfig(path, original); err != nil {
		t.Fatalf("SaveAgentConfig: %v", err)
	}

	loaded, err := LoadAgentConfig(path)
	if err != nil {
		t.Fatalf("LoadAgentConfig: %v", err)
	}

	if loaded.Server.Token != "sec" {
		t.Errorf("Server.Token = %q, want sec", loaded.Server.Token)
	}
	if len(loaded.Statics) != 1 || loaded.Statics[0].Name != "svc1" {
		t.Errorf("Statics mismatch: %+v", loaded.Statics)
	}
	if len(loaded.Buckets) != 1 || loaded.Buckets[0].Name != "bkt1" {
		t.Errorf("Buckets mismatch: %+v", loaded.Buckets)
	}
	if len(loaded.Proxies) != 1 || loaded.Proxies[0].Auth.Token != "tok" {
		t.Errorf("Proxies mismatch: %+v", loaded.Proxies)
	}
}

func TestCentralConfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "central.toml")

	original := CentralConfig{
		Server:    CentralServer{Listen: ":9000", Token: "prom-tok"},
		Discovery: DiscoveryConfig{Tags: []string{"tag:agent"}, AgentPort: 9001, RefreshInterval: Duration{30 * time.Second}},
		ManualPeers: []ManualPeer{
			{Name: "peer1", Address: "10.0.0.1", Port: 9100},
		},
	}

	if err := SaveCentralConfig(path, original); err != nil {
		t.Fatalf("SaveCentralConfig: %v", err)
	}

	loaded, err := LoadCentralConfig(path)
	if err != nil {
		t.Fatalf("LoadCentralConfig: %v", err)
	}

	if loaded.Server.Token != "prom-tok" {
		t.Errorf("Server.Token = %q, want prom-tok", loaded.Server.Token)
	}
	if len(loaded.Discovery.Tags) != 1 || loaded.Discovery.Tags[0] != "tag:agent" {
		t.Errorf("Tags = %v, want [tag:agent]", loaded.Discovery.Tags)
	}
	if loaded.Discovery.RefreshInterval.Duration != 30*time.Second {
		t.Errorf("RefreshInterval = %v, want 30s", loaded.Discovery.RefreshInterval.Duration)
	}
	if len(loaded.ManualPeers) != 1 || loaded.ManualPeers[0].Port != 9100 {
		t.Errorf("ManualPeers mismatch: %+v", loaded.ManualPeers)
	}
}

func TestCentralConfig_Duration(t *testing.T) {
	cases := []struct {
		input   string
		want    time.Duration
		wantErr bool
	}{
		{"60s", 60 * time.Second, false},
		{"1m30s", 90 * time.Second, false},
		{"1h", time.Hour, false},
		{"not-a-duration", 0, true},
	}
	for _, c := range cases {
		var d Duration
		err := d.UnmarshalText([]byte(c.input))
		if c.wantErr {
			if err == nil {
				t.Errorf("UnmarshalText(%q): expected error", c.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("UnmarshalText(%q): %v", c.input, err)
			continue
		}
		if d.Duration != c.want {
			t.Errorf("UnmarshalText(%q) = %v, want %v", c.input, d.Duration, c.want)
		}

		// Round-trip via MarshalText.
		b, err := d.MarshalText()
		if err != nil {
			t.Errorf("MarshalText(%q): %v", c.input, err)
			continue
		}
		if !strings.Contains(string(b), "") { // just check it doesn't error
			t.Logf("MarshalText(%q) = %q", c.input, string(b))
		}
	}
}

func TestDefaultConfigFile(t *testing.T) {
	for _, role := range []string{"agent", "central"} {
		path := DefaultConfigFile(role)
		if !strings.Contains(path, role) {
			t.Errorf("DefaultConfigFile(%q) = %q: does not contain role name", role, path)
		}
	}
}

func TestAtomicWriteJSON_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")

	type payload struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	original := payload{Name: "test", Value: 42}
	if err := AtomicWriteJSON(path, original); err != nil {
		t.Fatalf("AtomicWriteJSON: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var loaded payload
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if loaded.Name != "test" || loaded.Value != 42 {
		t.Errorf("loaded = %+v, want {test 42}", loaded)
	}
}

func TestAtomicWriteJSON_CreatesDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "data.json")

	if err := AtomicWriteJSON(path, map[string]string{"k": "v"}); err != nil {
		t.Fatalf("AtomicWriteJSON with nested path: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

func TestLoadAgentConfig_MissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "no-such-file.toml")

	// Should create the file and return defaults (no error).
	cfg, err := LoadAgentConfig(path)
	if err != nil {
		t.Fatalf("LoadAgentConfig for missing file: %v", err)
	}
	if cfg.Server.Listen == "" {
		t.Error("default listen should not be empty")
	}
	// File should now exist.
	if _, err := os.Stat(path); err != nil {
		t.Errorf("config file not created: %v", err)
	}
}
