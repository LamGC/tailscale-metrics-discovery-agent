package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/config"
	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/protocol"
)

// newTestServer returns a Server without a running HTTP listener, suitable for
// handler-level unit tests using httptest.NewRecorder.
func newTestServer(cfg config.AgentConfig) *Server {
	s := NewServer(cfg)
	return s
}

func TestHandleHealthz(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	handleHealthz(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var got map[string]bool
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if !got["ok"] {
		t.Errorf("body = %v, want {ok:true}", got)
	}
}

func TestAuthMiddleware_NoToken(t *testing.T) {
	s := newTestServer(config.AgentConfig{}) // no token
	called := false
	handler := s.authMiddleware(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	w := httptest.NewRecorder()
	handler(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if !called {
		t.Fatal("handler not called when no token configured")
	}
}

func TestAuthMiddleware_Correct(t *testing.T) {
	s := newTestServer(config.AgentConfig{Server: config.AgentServer{Token: "tok"}})
	called := false
	handler := s.authMiddleware(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer tok")
	handler(w, r)

	if !called {
		t.Fatal("handler not called with correct token")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestAuthMiddleware_Wrong(t *testing.T) {
	s := newTestServer(config.AgentConfig{Server: config.AgentServer{Token: "tok"}})
	handler := s.authMiddleware(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called with wrong token")
	})

	cases := []string{"", "Bearer wrong", "wrong"}
	for _, auth := range cases {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		handler(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("auth=%q: status = %d, want 401", auth, w.Code)
		}
	}
}

func TestHandleServices_Empty(t *testing.T) {
	s := newTestServer(config.AgentConfig{})
	w := httptest.NewRecorder()
	s.handleServices(w, httptest.NewRequest(http.MethodGet, "/api/v1/services", nil))

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var entries []protocol.ServiceEntry
	if err := json.Unmarshal(w.Body.Bytes(), &entries); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestHandleServices_WithStatic(t *testing.T) {
	s := newTestServer(config.AgentConfig{})
	if err := s.addStatic("svc1", []string{"host:9100"}, map[string]string{"job": "test"}, nil); err != nil {
		t.Fatalf("addStatic: %v", err)
	}

	w := httptest.NewRecorder()
	s.handleServices(w, httptest.NewRequest(http.MethodGet, "/api/v1/services", nil))

	var entries []protocol.ServiceEntry
	if err := json.Unmarshal(w.Body.Bytes(), &entries); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Name != "svc1" {
		t.Errorf("name = %q, want svc1", entries[0].Name)
	}
}

func TestHandleServices_MethodNotAllowed(t *testing.T) {
	s := newTestServer(config.AgentConfig{})
	w := httptest.NewRecorder()
	s.handleServices(w, httptest.NewRequest(http.MethodPost, "/api/v1/services", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

func TestHandlePush_BucketNotFound(t *testing.T) {
	s := newTestServer(config.AgentConfig{})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/push/nonexistent/job/app", nil)
	s.mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHandlePush_Success(t *testing.T) {
	s := newTestServer(config.AgentConfig{})
	if err := s.addBucket("mybucket", nil, nil); err != nil {
		t.Fatalf("addBucket: %v", err)
	}

	body := "pushed_metric 99\n"
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/push/mybucket/job/test",
		strings.NewReader(body))
	s.mux.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", w.Code)
	}
}

func TestHandleBucketMetrics_NotFound(t *testing.T) {
	s := newTestServer(config.AgentConfig{})
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/bucket/nope/metrics", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHandleBucketMetrics_OK(t *testing.T) {
	s := newTestServer(config.AgentConfig{})
	if err := s.addBucket("bkt", nil, nil); err != nil {
		t.Fatalf("addBucket: %v", err)
	}

	// Push a metric.
	wPush := httptest.NewRecorder()
	reqPush := httptest.NewRequest(http.MethodPut, "/push/bkt/job/test",
		strings.NewReader("pushed_value 7\n"))
	s.mux.ServeHTTP(wPush, reqPush)

	// Read it back.
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/bucket/bkt/metrics", nil))

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "pushed_value") {
		t.Errorf("expected pushed_value in response body, got: %s", w.Body.String())
	}
}

func TestHandleProxyMetrics_NotFound(t *testing.T) {
	s := newTestServer(config.AgentConfig{})
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/proxy/nope/metrics", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHandleProxyMetrics_OK(t *testing.T) {
	// Start a real upstream metrics server.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("upstream_metric 42\n"))
	}))
	defer upstream.Close()

	s := newTestServer(config.AgentConfig{})
	if err := s.addProxy("prx", upstream.URL, proxyAuth{authType: "none"}, nil, nil); err != nil {
		t.Fatalf("addProxy: %v", err)
	}

	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/proxy/prx/metrics", nil))

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "upstream_metric") {
		t.Errorf("expected upstream_metric in body, got: %s", w.Body.String())
	}
}

func TestFilterSlice(t *testing.T) {
	in := []int{1, 2, 3, 4, 5}
	out := filterSlice(in, func(v int) bool { return v%2 == 0 })
	if len(out) != 2 || out[0] != 2 || out[1] != 4 {
		t.Errorf("filterSlice = %v, want [2 4]", out)
	}
}
