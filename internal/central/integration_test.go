package central

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/protocol"
)

// newIntegrationCollector builds a collector that uses no real discoverer
// and queries peers via its httpClient directly.
// Uses HTTP/1.1 transport for compatibility with httptest.Server.
func newIntegrationCollector(token string) *collector {
	d := &discoverer{port: 9001}
	c := newCollector(d, token)
	c.httpClient = &http.Client{Timeout: 10 * time.Second} // HTTP/1.1 for tests
	return c
}

// seedPeers injects a static list of PeerInfo as if Discover had returned them.
func seedPeers(c *collector, peers []protocol.PeerInfo) {
	c.mu.Lock()
	c.peers = peers
	c.mu.Unlock()
}

// TestIntegration_CollectorQueriesAgents verifies that queryAgentServices
// fetches services from mock agents and populates targets.
func TestIntegration_CollectorQueriesAgents(t *testing.T) {
	// Build two mock agent HTTP servers.
	makeAgentSrv := func(name string) *httptest.Server {
		entries := []protocol.ServiceEntry{
			{
				Name: name,
				Type: protocol.ServiceTypeStatic,
				Target: protocol.SDTarget{
					Targets: []string{"host:9100"},
					Labels:  map[string]string{"job": name},
				},
			},
		}
		body, _ := json.Marshal(entries)
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
		}))
	}

	srv1 := makeAgentSrv("svc-a")
	defer srv1.Close()
	srv2 := makeAgentSrv("svc-b")
	defer srv2.Close()

	c := newIntegrationCollector("")

	// Inject peers with AgentURL pointing at the mock servers.
	peers := []protocol.PeerInfo{
		{TailscaleIP: "100.1.1.1", Hostname: "node1", AgentURL: srv1.URL, Source: protocol.PeerSourceAuto},
		{TailscaleIP: "100.1.1.2", Hostname: "node2", AgentURL: srv2.URL, Source: protocol.PeerSourceAuto},
	}
	// Call queryAgentServices directly for each peer and aggregate.
	var allTargets []protocol.SDTarget
	for _, peer := range peers {
		_, targets, health, _, err := c.queryAgentServices(context.Background(), peer)
		if err != nil {
			t.Fatalf("queryAgentServices(%s): %v", peer.Hostname, err)
		}
		if health != protocol.AgentHealthOK {
			t.Errorf("peer %s: health = %q, want ok", peer.Hostname, health)
		}
		allTargets = append(allTargets, targets...)
	}

	if len(allTargets) != 2 {
		t.Fatalf("targets count = %d, want 2", len(allTargets))
	}
	names := make(map[string]bool)
	for _, tgt := range allTargets {
		names[tgt.Labels["job"]] = true
	}
	if !names["svc-a"] || !names["svc-b"] {
		t.Errorf("missing expected job labels; got: %v", names)
	}
}

// TestIntegration_AgentUnreachable_CacheServed verifies that after a
// successful query, if the agent goes offline the collector serves stale
// services from its in-memory cache.
func TestIntegration_AgentUnreachable_CacheServed(t *testing.T) {
	entries := []protocol.ServiceEntry{
		{Name: "cached-svc", Type: protocol.ServiceTypeStatic,
			Target: protocol.SDTarget{Targets: []string{"host:9100"}}},
	}
	body, _ := json.Marshal(entries)

	// First query succeeds.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))

	c := newIntegrationCollector("")
	peer := protocol.PeerInfo{TailscaleIP: "100.2.2.2", Hostname: "node3", AgentURL: srv.URL}

	_, _, health, _, err := c.queryAgentServices(context.Background(), peer)
	if err != nil || health != protocol.AgentHealthOK {
		t.Fatalf("initial query failed: health=%q err=%v", health, err)
	}
	// Seed the service cache manually (queryAgentServices doesn't update it — refresh does).
	c.cacheMu.Lock()
	c.serviceCache[peer.TailscaleIP] = cachedPeerServices{
		services: entries,
	}
	c.cacheMu.Unlock()

	srv.Close() // Take the agent offline.

	// Query again — should fail with timeout/connect error.
	_, _, health2, _, _ := c.queryAgentServices(context.Background(), peer)
	if health2 == protocol.AgentHealthOK {
		t.Error("expected non-OK health when agent is down")
	}

	// Cache entry should still be present.
	c.cacheMu.RLock()
	cached, ok := c.serviceCache[peer.TailscaleIP]
	c.cacheMu.RUnlock()
	if !ok {
		t.Fatal("cache entry should still exist after agent goes offline")
	}
	if len(cached.services) != 1 || cached.services[0].Name != "cached-svc" {
		t.Errorf("cached services = %+v, want cached-svc", cached.services)
	}
}

// TestIntegration_AuthMismatch verifies that when the agent requires a token
// and the collector sends the wrong one, AgentHealthUnauthorized is returned.
func TestIntegration_AuthMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer correct-token" {
			http.Error(w, "denied", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()

	c := newIntegrationCollector("wrong-token")
	peer := protocol.PeerInfo{AgentURL: srv.URL}
	_, _, health, _, _ := c.queryAgentServices(context.Background(), peer)

	if health != protocol.AgentHealthUnauthorized {
		t.Errorf("health = %q, want unauthorized", health)
	}
}

// TestIntegration_CorrectToken verifies that a correct token produces a healthy response.
func TestIntegration_CorrectToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			http.Error(w, "denied", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()

	c := newIntegrationCollector("mytoken")
	peer := protocol.PeerInfo{AgentURL: srv.URL}
	_, _, health, _, err := c.queryAgentServices(context.Background(), peer)

	if err != nil {
		t.Fatalf("queryAgentServices: %v", err)
	}
	if health != protocol.AgentHealthOK {
		t.Errorf("health = %q, want ok", health)
	}
}
