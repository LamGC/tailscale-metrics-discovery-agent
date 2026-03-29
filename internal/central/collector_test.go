package central

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/config"
	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/protocol"
)

// mockRoundTripper is a test double for http.RoundTripper.
type mockRoundTripper struct {
	fn func(*http.Request) (*http.Response, error)
}

func (m *mockRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return m.fn(r)
}

func mockResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// newTestCollector returns a collector without a real discoverer,
// suitable for unit tests that call methods directly.
// It uses a default HTTP/1.1 transport for compatibility with httptest.Server.
func newTestCollector() *collector {
	d := &discoverer{port: 9001}
	c := newCollector(d, "")
	c.httpClient = &http.Client{Timeout: 10 * time.Second} // HTTP/1.1 for tests
	return c
}

// --- mergePeers ---

func TestMergePeers_AutoOnly(t *testing.T) {
	c := newTestCollector()
	auto := []protocol.PeerInfo{
		{TailscaleIP: "100.1.1.1", AgentURL: "http://100.1.1.1:9001", Hostname: "node1"},
	}
	got := c.mergePeers(auto)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].AgentURL != "http://100.1.1.1:9001" {
		t.Errorf("agentURL = %q", got[0].AgentURL)
	}
}

func TestMergePeers_ManualOverridesPort(t *testing.T) {
	c := newTestCollector()
	auto := []protocol.PeerInfo{
		{TailscaleIP: "100.1.1.1", AgentURL: "http://100.1.1.1:9001", Hostname: "node1"},
	}
	c.AddManualPeer(manualPeer{Address: "100.1.1.1", Port: 9100})

	got := c.mergePeers(auto)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].AgentURL != "http://100.1.1.1:9100" {
		t.Errorf("agentURL = %q, want 9100 port", got[0].AgentURL)
	}
}

func TestMergePeers_ManualOnly(t *testing.T) {
	c := newTestCollector()
	c.AddManualPeer(manualPeer{Name: "extra", Address: "10.0.0.1", Port: 9001})

	got := c.mergePeers(nil)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Hostname != "extra" || got[0].Source != protocol.PeerSourceManual {
		t.Errorf("unexpected peer: %+v", got[0])
	}
}

// --- queryAgentServices ---

func TestQueryAgentServices_OK(t *testing.T) {
	entries := []protocol.ServiceEntry{
		{Name: "svc1", Type: protocol.ServiceTypeStatic, Target: protocol.SDTarget{Targets: []string{"host:9100"}}},
	}
	body, _ := json.Marshal(entries)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c := newTestCollector()
	peer := protocol.PeerInfo{
		Hostname:    "node1",
		TailscaleIP: "127.0.0.1",
		AgentURL:    srv.URL,
		Health:      protocol.AgentHealthUnknown,
	}

	svcList, targets, health, notMod, err := c.queryAgentServices(context.Background(), peer)
	if err != nil {
		t.Fatalf("queryAgentServices error: %v", err)
	}
	if notMod {
		t.Error("expected notModified=false on first request")
	}
	if health != protocol.AgentHealthOK {
		t.Errorf("health = %q, want ok", health)
	}
	if len(svcList) != 1 {
		t.Errorf("services count = %d, want 1", len(svcList))
	}
	if len(targets) != 1 {
		t.Errorf("targets count = %d, want 1", len(targets))
	}
}

func TestQueryAgentServices_304(t *testing.T) {
	lm := time.Now().UTC().Format(http.TimeFormat)
	callCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			// First request: return full response with Last-Modified.
			entries := []protocol.ServiceEntry{
				{Name: "svc1", Target: protocol.SDTarget{Targets: []string{"host:9100"}}},
			}
			body, _ := json.Marshal(entries)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Last-Modified", lm)
			_, _ = w.Write(body)
			return
		}
		// Second request: check If-Modified-Since and return 304.
		if ims := r.Header.Get("If-Modified-Since"); ims != "" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()

	c := newTestCollector()
	peer := protocol.PeerInfo{TailscaleIP: "127.0.0.1", AgentURL: srv.URL}

	// First call: should get data.
	_, _, _, notMod, _ := c.queryAgentServices(context.Background(), peer)
	if notMod {
		t.Error("first call should not be 304")
	}

	// Second call: should get 304.
	_, _, health, notMod, _ := c.queryAgentServices(context.Background(), peer)
	if !notMod {
		t.Error("second call should be 304")
	}
	if health != protocol.AgentHealthOK {
		t.Errorf("304 should return AgentHealthOK, got %q", health)
	}
}

func TestQueryAgentServices_Unauthorized(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "denied", code)
		}))

		c := newTestCollector()
		peer := protocol.PeerInfo{AgentURL: srv.URL}
		_, _, health, _, _ := c.queryAgentServices(context.Background(), peer)
		srv.Close()

		if health != protocol.AgentHealthUnauthorized {
			t.Errorf("HTTP %d: health = %q, want unauthorized", code, health)
		}
	}
}

func TestQueryAgentServices_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	c := newTestCollector()
	peer := protocol.PeerInfo{AgentURL: srv.URL}
	_, _, health, _, err := c.queryAgentServices(context.Background(), peer)

	if health != protocol.AgentHealthTimeout {
		t.Errorf("health = %q, want timeout", health)
	}
	if err == nil {
		t.Error("expected error for bad JSON")
	}
}

func TestQueryAgentServices_BearerToken(t *testing.T) {
	var capturedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()

	c := newTestCollector()
	c.agentToken = "secret-token"
	peer := protocol.PeerInfo{AgentURL: srv.URL}
	c.queryAgentServices(context.Background(), peer)

	if capturedAuth != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want Bearer secret-token", capturedAuth)
	}
}

func TestQueryAgentServices_ConnectError(t *testing.T) {
	c := newTestCollector()
	peer := protocol.PeerInfo{AgentURL: "http://127.0.0.1:1"} // port 1 will be refused
	_, _, health, _, _ := c.queryAgentServices(context.Background(), peer)
	if health != protocol.AgentHealthTimeout {
		t.Errorf("health = %q, want timeout on connect error", health)
	}
}

// --- queryAgentHealth ---

func TestQueryAgentHealth_OK(t *testing.T) {
	healthMap := map[string]*protocol.ServiceHealthStatus{
		"svc1": {Status: protocol.ServiceHealthHealthy, StatusCode: 200},
	}
	body, _ := json.Marshal(healthMap)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c := newTestCollector()
	peer := protocol.PeerInfo{TailscaleIP: "127.0.0.1", AgentURL: srv.URL}

	hm, health, notMod, err := c.queryAgentHealth(context.Background(), peer)
	if err != nil {
		t.Fatalf("queryAgentHealth error: %v", err)
	}
	if notMod {
		t.Error("expected notModified=false on first request")
	}
	if health != protocol.AgentHealthOK {
		t.Errorf("health = %q, want ok", health)
	}
	if len(hm) != 1 {
		t.Errorf("healthMap len = %d, want 1", len(hm))
	}
	if hm["svc1"].Status != protocol.ServiceHealthHealthy {
		t.Errorf("svc1 status = %q, want healthy", hm["svc1"].Status)
	}
}

func TestQueryAgentHealth_304(t *testing.T) {
	lm := time.Now().UTC().Format(http.TimeFormat)
	callCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.Header().Set("Last-Modified", lm)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{}"))
			return
		}
		if ims := r.Header.Get("If-Modified-Since"); ims != "" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()

	c := newTestCollector()
	peer := protocol.PeerInfo{TailscaleIP: "127.0.0.1", AgentURL: srv.URL}

	c.queryAgentHealth(context.Background(), peer)
	_, health, notMod, _ := c.queryAgentHealth(context.Background(), peer)
	if !notMod {
		t.Error("second call should be 304")
	}
	if health != protocol.AgentHealthOK {
		t.Errorf("304 should return AgentHealthOK, got %q", health)
	}
}

func TestQueryAgentHealth_404_OldAgent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := newTestCollector()
	peer := protocol.PeerInfo{AgentURL: srv.URL}
	hm, health, _, err := c.queryAgentHealth(context.Background(), peer)

	if err != nil {
		t.Errorf("expected no error for 404, got %v", err)
	}
	if health != protocol.AgentHealthOK {
		t.Errorf("health = %q, want ok (graceful 404)", health)
	}
	if hm != nil {
		t.Errorf("healthMap should be nil for 404, got %v", hm)
	}
}

// --- worseHealth ---

func TestWorseHealth(t *testing.T) {
	tests := []struct {
		a, b protocol.AgentHealth
		want protocol.AgentHealth
	}{
		{protocol.AgentHealthOK, protocol.AgentHealthOK, protocol.AgentHealthOK},
		{protocol.AgentHealthOK, protocol.AgentHealthTimeout, protocol.AgentHealthTimeout},
		{protocol.AgentHealthUnauthorized, protocol.AgentHealthOK, protocol.AgentHealthUnauthorized},
		{protocol.AgentHealthTimeout, protocol.AgentHealthUnauthorized, protocol.AgentHealthUnauthorized},
	}
	for _, tt := range tests {
		got := worseHealth(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("worseHealth(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
		}
	}
}

// --- peer cache ---

func TestLoadSavePeerCache(t *testing.T) {
	dir := t.TempDir()
	cacheFile := filepath.Join(dir, "peers.json")

	c := newTestCollector()
	c.peersFile = cacheFile

	// Seed the cache manually.
	now := time.Now().Truncate(time.Second)
	c.cacheMu.Lock()
	c.serviceCache["100.1.1.1"] = cachedPeerServices{
		services: []protocol.ServiceEntry{
			{Name: "svc1", Type: protocol.ServiceTypeStatic},
		},
		fetchedAt: now,
	}
	c.cacheMu.Unlock()

	c.mu.Lock()
	c.peers = []protocol.PeerInfo{
		{TailscaleIP: "100.1.1.1", Hostname: "node1", Source: protocol.PeerSourceAuto},
	}
	c.mu.Unlock()

	c.savePeerCache()

	// Verify file exists.
	if _, err := os.Stat(cacheFile); err != nil {
		t.Fatalf("cache file not written: %v", err)
	}

	// Load into a fresh collector.
	c2 := newTestCollector()
	c2.peersFile = cacheFile
	c2.loadPeerCache()

	c2.cacheMu.RLock()
	cached, ok := c2.serviceCache["100.1.1.1"]
	c2.cacheMu.RUnlock()

	if !ok {
		t.Fatal("cache entry not found after load")
	}
	if len(cached.services) != 1 || cached.services[0].Name != "svc1" {
		t.Errorf("unexpected services: %+v", cached.services)
	}
}

func TestLoadPeerCache_TTLEviction(t *testing.T) {
	dir := t.TempDir()
	cacheFile := filepath.Join(dir, "peers.json")

	// Write a cache file with an expired entry.
	expired := []peerCacheEntry{{
		TailscaleIP: "100.1.1.1",
		Services:    []protocol.ServiceEntry{{Name: "old-svc"}},
		FetchedAt:   time.Now().Add(-73 * time.Hour), // beyond 72h TTL
	}}
	data, _ := json.MarshalIndent(expired, "", "  ")
	if err := os.WriteFile(cacheFile, data, 0o644); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	c := newTestCollector()
	c.peersFile = cacheFile
	c.loadPeerCache()

	c.cacheMu.RLock()
	_, ok := c.serviceCache["100.1.1.1"]
	c.cacheMu.RUnlock()

	if ok {
		t.Error("expired entry should not be loaded")
	}
}

func TestLoadPeerCache_NoFile(t *testing.T) {
	c := newTestCollector()
	c.peersFile = "/tmp/does-not-exist-12345.json"
	c.loadPeerCache() // should not panic or error
}

// --- ReplaceConfigPeers ---

func TestReplaceConfigPeers(t *testing.T) {
	c := newTestCollector()

	// Add a config peer and a CLI peer.
	c.AddManualPeer(manualPeer{Address: "10.0.0.1", fromConfig: true})
	c.AddManualPeer(manualPeer{Address: "10.0.0.2", fromConfig: false})

	// Replace config peers.
	c.ReplaceConfigPeers([]config.ManualPeer{
		{Address: "10.0.0.3", Name: "new"},
	})

	c.manualMu.RLock()
	defer c.manualMu.RUnlock()

	_, hasOldConfig := c.manualPeers["10.0.0.1"]
	_, hasCLI := c.manualPeers["10.0.0.2"]
	_, hasNew := c.manualPeers["10.0.0.3"]

	if hasOldConfig {
		t.Error("old config peer should have been replaced")
	}
	if !hasCLI {
		t.Error("CLI peer should be retained")
	}
	if !hasNew {
		t.Error("new config peer should be present")
	}
}

// --- RemoveManualPeer ---

func TestRemoveManualPeer_NotFound(t *testing.T) {
	c := newTestCollector()
	if c.RemoveManualPeer("10.0.0.99") {
		t.Error("RemoveManualPeer: expected false for non-existent peer")
	}
}
