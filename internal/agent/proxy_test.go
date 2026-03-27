package agent

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProxy_NoAuth(t *testing.T) {
	p := newProxy("http://upstream/metrics", proxyAuth{authType: "none"})
	p.client = &http.Client{
		Transport: &mockRoundTripper{
			fn: func(r *http.Request) (*http.Response, error) {
				resp := mockResponse(http.StatusOK, "upstream_metric 1")
				resp.Header.Set("Content-Type", "text/plain; version=0.0.4")
				return resp, nil
			},
		},
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/proxy/test/metrics", nil)
	p.serveMetrics(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "upstream_metric") {
		t.Errorf("expected upstream_metric in body, got: %s", w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
}

func TestProxy_BearerAuth(t *testing.T) {
	var capturedAuth string
	p := newProxy("http://upstream/metrics", proxyAuth{authType: "bearer", token: "secret"})
	p.client = &http.Client{
		Transport: &mockRoundTripper{
			fn: func(r *http.Request) (*http.Response, error) {
				capturedAuth = r.Header.Get("Authorization")
				return mockResponse(http.StatusOK, ""), nil
			},
		},
	}

	w := httptest.NewRecorder()
	p.serveMetrics(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if capturedAuth != "Bearer secret" {
		t.Errorf("Authorization = %q, want %q", capturedAuth, "Bearer secret")
	}
}

func TestProxy_BasicAuth(t *testing.T) {
	var capturedUser, capturedPass string
	p := newProxy("http://upstream/metrics", proxyAuth{authType: "basic", username: "user", password: "pass"})
	p.client = &http.Client{
		Transport: &mockRoundTripper{
			fn: func(r *http.Request) (*http.Response, error) {
				capturedUser, capturedPass, _ = r.BasicAuth()
				return mockResponse(http.StatusOK, ""), nil
			},
		},
	}

	w := httptest.NewRecorder()
	p.serveMetrics(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if capturedUser != "user" || capturedPass != "pass" {
		t.Errorf("BasicAuth = (%q, %q), want (user, pass)", capturedUser, capturedPass)
	}
}

func TestProxy_UpstreamError(t *testing.T) {
	p := newProxy("http://upstream/metrics", proxyAuth{authType: "none"})
	p.client = &http.Client{
		Transport: &mockRoundTripper{
			fn: func(r *http.Request) (*http.Response, error) {
				return nil, &netError{msg: "connection refused"}
			},
		},
	}

	w := httptest.NewRecorder()
	p.serveMetrics(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", w.Code)
	}
}

func TestProxy_UpstreamNonOK(t *testing.T) {
	p := newProxy("http://upstream/metrics", proxyAuth{authType: "none"})
	p.client = &http.Client{
		Transport: &mockRoundTripper{
			fn: func(r *http.Request) (*http.Response, error) {
				return mockResponse(http.StatusInternalServerError, "oops"), nil
			},
		},
	}

	w := httptest.NewRecorder()
	p.serveMetrics(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
	body, _ := io.ReadAll(w.Body)
	if !strings.Contains(string(body), "oops") {
		t.Errorf("expected oops in body, got: %s", body)
	}
}

func TestProxyStore_AddRemoveGet(t *testing.T) {
	ps := newProxyStore()
	p := newProxy("http://up/m", proxyAuth{})

	if err := ps.add("p1", p); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, ok := ps.get("p1"); !ok {
		t.Fatal("get: not found")
	}
	if err := ps.add("p1", p); err == nil {
		t.Fatal("duplicate add: expected error")
	}
	if err := ps.remove("p1"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, ok := ps.get("p1"); ok {
		t.Fatal("get after remove: still found")
	}
}
