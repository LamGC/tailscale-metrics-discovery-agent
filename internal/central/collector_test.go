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
func newTestCollector() *collector {
	d := &discoverer{port: 9001}
	return newCollector(d, "")
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

// --- queryAgent ---

func TestQueryAgent_OK(t *testing.T) {
	entries := []protocol.ServiceEntry{
		{Name: "svc1", Type: protocol.ServiceTypeStatic, Target: protocol.SDTarget{Targets: []string{"host:9100"}}},
	}
	body, _ := json.Marshal(entries)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
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

	svcList, targets, health, err := c.queryAgent(context.Background(), peer)
	if err != nil {
		t.Fatalf("queryAgent error: %v", err)
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

func TestQueryAgent_Unauthorized(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "denied", code)
		}))

		c := newTestCollector()
		peer := protocol.PeerInfo{AgentURL: srv.URL}
		_, _, health, _ := c.queryAgent(context.Background(), peer)
		srv.Close()

		if health != protocol.AgentHealthUnauthorized {
			t.Errorf("HTTP %d: health = %q, want unauthorized", code, health)
		}
	}
}

func TestQueryAgent_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	c := newTestCollector()
	peer := protocol.PeerInfo{AgentURL: srv.URL}
	_, _, health, err := c.queryAgent(context.Background(), peer)

	if health != protocol.AgentHealthTimeout {
		t.Errorf("health = %q, want timeout", health)
	}
	if err == nil {
		t.Error("expected error for bad JSON")
	}
}

func TestQueryAgent_BearerToken(t *testing.T) {
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
	c.queryAgent(context.Background(), peer)

	if capturedAuth != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want Bearer secret-token", capturedAuth)
	}
}

func TestQueryAgent_ConnectError(t *testing.T) {
	c := newTestCollector()
	peer := protocol.PeerInfo{AgentURL: "http://127.0.0.1:1"} // port 1 will be refused
	_, _, health, _ := c.queryAgent(context.Background(), peer)
	if health != protocol.AgentHealthTimeout {
		t.Errorf("health = %q, want timeout on connect error", health)
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
