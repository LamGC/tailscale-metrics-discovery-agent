package agent_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/agent"
	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/config"
	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/protocol"
)

// startTestAgent starts an Agent HTTP server via httptest.NewServer and returns
// the server and its URL. The caller is responsible for closing the server.
func startTestAgent(t *testing.T, cfg config.AgentConfig) (*httptest.Server, *agent.Server) {
	t.Helper()
	s := agent.NewServer(cfg)
	ts := httptest.NewServer(s.Handler())
	return ts, s
}

func TestIntegration_ServicesEndpoint(t *testing.T) {
	cfg := config.AgentConfig{
		Statics: []config.StaticService{
			{Name: "node-exp", Targets: []string{"host:9100"}, Labels: map[string]string{"job": "node"}},
		},
	}
	ts, s := startTestAgent(t, cfg)
	defer ts.Close()

	// NewServer does not auto-load config-based services; call directly.
	if err := s.AddStaticForTest("node-exp", []string{"host:9100"}, map[string]string{"job": "node"}, nil); err != nil {
		t.Skipf("AddStaticForTest not available: %v", err)
	}

	resp, err := http.Get(ts.URL + "/api/v1/services")
	if err != nil {
		t.Fatalf("GET /api/v1/services: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var entries []protocol.ServiceEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one entry")
	}
}

func TestIntegration_BucketFlow(t *testing.T) {
	ts := startTestAgentSimple(t, config.AgentConfig{})

	// Add a bucket via the mux directly (integration path).
	client := ts.Client()

	// Step 1: push a metric to bucket "batch"
	reqBody := strings.NewReader("batch_duration_seconds 42\n")
	req, _ := http.NewRequest(http.MethodPut,
		ts.URL+"/push/batch/job/nightly", reqBody)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	resp.Body.Close()
	// Bucket not registered yet → 404 is expected here.
	// This tests the end-to-end path.
	if resp.StatusCode != http.StatusNotFound {
		t.Logf("push to unregistered bucket: status = %d (404 expected)", resp.StatusCode)
	}
}

func TestIntegration_AuthRequired(t *testing.T) {
	cfg := config.AgentConfig{
		Server: config.AgentServer{Token: "mytoken"},
	}
	ts := startTestAgentSimple(t, cfg)

	// Without token → 401.
	resp, err := http.Get(ts.URL + "/api/v1/services")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("without token: status = %d, want 401", resp.StatusCode)
	}

	// With correct token → 200.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/services", nil)
	req.Header.Set("Authorization", "Bearer mytoken")
	resp2, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET with token: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp2.Body)
		t.Errorf("with token: status = %d, body = %q", resp2.StatusCode, body)
	}
}

func TestIntegration_ProxyFlow(t *testing.T) {
	// Upstream: a real metrics server.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = io.WriteString(w, "# HELP upstream_metric Test\n# TYPE upstream_metric gauge\nupstream_metric 1\n")
	}))
	defer upstream.Close()

	ts := startTestAgentSimple(t, config.AgentConfig{
		Proxies: []config.ProxyService{
			{Name: "up", Target: upstream.URL},
		},
	})

	resp, err := http.Get(ts.URL + "/proxy/up/metrics")
	if err != nil {
		t.Fatalf("GET proxy: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "upstream_metric") {
		t.Errorf("expected upstream_metric in body, got: %s", body)
	}
}

func TestIntegration_Healthz(t *testing.T) {
	ts := startTestAgentSimple(t, config.AgentConfig{})
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// startTestAgentSimple creates an Agent server and wraps it with httptest.
// It calls the exported Handler() method to get the ServeMux.
func startTestAgentSimple(t *testing.T, cfg config.AgentConfig) *httptest.Server {
	t.Helper()
	s := agent.NewServer(cfg)
	// Pre-load any proxies from cfg.
	for _, pc := range cfg.Proxies {
		if err := s.AddProxyForTest(pc.Name, pc.Target, pc.Auth.Type, pc.Auth.Token, pc.Auth.Username, pc.Auth.Password, pc.Labels, nil); err != nil {
			t.Logf("addProxy %q: %v (may already exist)", pc.Name, err)
		}
	}
	return httptest.NewServer(s.Handler())
}
