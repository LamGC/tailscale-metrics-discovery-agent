package central

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/config"
)

func TestMgmtPeerRemoveRollsBackConfigWhenRuntimeNotFound(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "central.toml")
	initialCfg := config.CentralConfig{
		ManualPeers: []config.ManualPeer{
			{
				Name:    "test-peer",
				Address: "100.64.0.1",
				Port:    9001,
			},
		},
	}
	if err := config.SaveCentralConfig(cfgPath, initialCfg); err != nil {
		t.Fatalf("save initial config: %v", err)
	}

	srv := NewServer(initialCfg)
	srv.cfgFile = cfgPath
	if !srv.col.RemoveManualPeer("100.64.0.1") {
		t.Fatal("preload runtime state: expected manual peer")
	}

	mgmt := newCentralMgmtServer(srv)
	body, _ := json.Marshal(map[string]any{
		"address": "100.64.0.1",
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mgmt/peer/remove", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	mgmt.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusNotFound, w.Body.String())
	}
	got, err := config.LoadCentralConfig(cfgPath)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if len(got.ManualPeers) != 1 {
		t.Fatalf("manual peers = %d, want 1", len(got.ManualPeers))
	}
}

func TestMgmtPeerAddSerializesConcurrentConfigUpdates(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "central.toml")
	if err := config.SaveCentralConfig(cfgPath, config.CentralConfig{}); err != nil {
		t.Fatalf("save initial config: %v", err)
	}

	srv := NewServer(config.CentralConfig{})
	srv.cfgFile = cfgPath
	mgmt := newCentralMgmtServer(srv)

	const count = 16
	var wg sync.WaitGroup
	errCh := make(chan string, count)
	for i := 0; i < count; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			body, _ := json.Marshal(map[string]any{
				"name":    fmt.Sprintf("peer-%02d", i),
				"address": fmt.Sprintf("100.64.0.%d", i+1),
				"port":    9001,
			})
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/mgmt/peer/add", strings.NewReader(string(body)))
			req.Header.Set("Content-Type", "application/json")
			mgmt.Handler.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				errCh <- fmt.Sprintf("peer-%02d status = %d; body: %s", i, w.Code, w.Body.String())
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Error(msg)
	}

	got, err := config.LoadCentralConfig(cfgPath)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if len(got.ManualPeers) != count {
		t.Fatalf("manual peers = %d, want %d; peers=%+v", len(got.ManualPeers), count, got.ManualPeers)
	}
}

func TestReloadSynchronizesConfigForMgmtPersistence(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "central.toml")
	initialCfg := config.CentralConfig{
		Server: config.CentralServer{Token: "old"},
	}
	if err := config.SaveCentralConfig(cfgPath, initialCfg); err != nil {
		t.Fatalf("save initial config: %v", err)
	}

	reloadedCfg := config.CentralConfig{
		Server:    config.CentralServer{Token: "new"},
		Discovery: config.DiscoveryConfig{AgentPort: 9101},
		ManualPeers: []config.ManualPeer{{
			Name:    "from-file",
			Address: "100.64.0.10",
			Port:    9101,
		}},
	}
	if err := config.SaveCentralConfig(cfgPath, reloadedCfg); err != nil {
		t.Fatalf("save reloaded config: %v", err)
	}

	srv := NewServer(initialCfg)
	srv.cfgFile = cfgPath
	if err := srv.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	mgmt := newCentralMgmtServer(srv)
	body, _ := json.Marshal(map[string]any{
		"name":    "from-cli",
		"address": "100.64.0.11",
		"port":    9102,
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mgmt/peer/add", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	mgmt.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("peer add status = %d; body: %s", w.Code, w.Body.String())
	}

	got, err := config.LoadCentralConfig(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got.Server.Token != "new" {
		t.Fatalf("token = %q, want reloaded token", got.Server.Token)
	}
	if got.Discovery.AgentPort != 9101 {
		t.Fatalf("agent port = %d, want reloaded port", got.Discovery.AgentPort)
	}
	if len(got.ManualPeers) != 2 {
		t.Fatalf("manual peers = %d, want 2: %+v", len(got.ManualPeers), got.ManualPeers)
	}
}
