package agent

import (
	"context"
	"io"
	"net/http"
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

func TestDoCheck_Healthy(t *testing.T) {
	reg := newRegistry()
	_ = reg.add(protocol.ServiceEntry{Name: "svc", Type: protocol.ServiceTypeStatic})

	hc := newHealthChecker(reg)
	hc.httpClient = &http.Client{
		Transport: &mockRoundTripper{
			fn: func(r *http.Request) (*http.Response, error) {
				return mockResponse(http.StatusOK, ""), nil
			},
		},
	}

	ctx := context.Background()
	hc.doCheck(ctx, "svc", "http://example.com/healthz", 5*time.Second)

	entry, _ := reg.get("svc")
	if entry.Health == nil {
		t.Fatal("health is nil")
	}
	if entry.Health.Status != protocol.ServiceHealthHealthy {
		t.Errorf("status = %q, want healthy", entry.Health.Status)
	}
	if entry.Health.StatusCode != http.StatusOK {
		t.Errorf("status code = %d, want 200", entry.Health.StatusCode)
	}
}

func TestDoCheck_Unhealthy(t *testing.T) {
	reg := newRegistry()
	_ = reg.add(protocol.ServiceEntry{Name: "svc", Type: protocol.ServiceTypeStatic})

	hc := newHealthChecker(reg)
	hc.httpClient = &http.Client{
		Transport: &mockRoundTripper{
			fn: func(r *http.Request) (*http.Response, error) {
				return mockResponse(http.StatusServiceUnavailable, ""), nil
			},
		},
	}

	ctx := context.Background()
	hc.doCheck(ctx, "svc", "http://example.com/healthz", 5*time.Second)

	entry, _ := reg.get("svc")
	if entry.Health == nil {
		t.Fatal("health is nil")
	}
	if entry.Health.Status != protocol.ServiceHealthUnhealthy {
		t.Errorf("status = %q, want unhealthy", entry.Health.Status)
	}
	if entry.Health.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status code = %d, want 503", entry.Health.StatusCode)
	}
	if !strings.Contains(entry.Health.Message, "503") {
		t.Errorf("message %q should contain status code", entry.Health.Message)
	}
}

func TestDoCheck_NetworkError(t *testing.T) {
	reg := newRegistry()
	_ = reg.add(protocol.ServiceEntry{Name: "svc", Type: protocol.ServiceTypeStatic})

	hc := newHealthChecker(reg)
	hc.httpClient = &http.Client{
		Transport: &mockRoundTripper{
			fn: func(r *http.Request) (*http.Response, error) {
				return nil, &netError{msg: "connection refused"}
			},
		},
	}

	ctx := context.Background()
	hc.doCheck(ctx, "svc", "http://example.com/healthz", 5*time.Second)

	entry, _ := reg.get("svc")
	if entry.Health == nil {
		t.Fatal("health is nil")
	}
	if entry.Health.Status != protocol.ServiceHealthUnhealthy {
		t.Errorf("status = %q, want unhealthy", entry.Health.Status)
	}
}

func TestRegister_NilCfg(t *testing.T) {
	reg := newRegistry()
	_ = reg.add(protocol.ServiceEntry{Name: "svc", Type: protocol.ServiceTypeStatic})
	hc := newHealthChecker(reg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hc.Start(ctx)

	hc.Register("svc", nil) // should be a no-op
	entry, _ := reg.get("svc")
	if entry.Health != nil {
		t.Error("health should remain nil after Register(nil)")
	}
}

func TestRegister_EmptyURL(t *testing.T) {
	reg := newRegistry()
	_ = reg.add(protocol.ServiceEntry{Name: "svc", Type: protocol.ServiceTypeStatic})
	hc := newHealthChecker(reg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hc.Start(ctx)

	hc.Register("svc", &config.HealthcheckConfig{URL: ""}) // no-op
	entry, _ := reg.get("svc")
	if entry.Health != nil {
		t.Error("health should remain nil for empty URL")
	}
}

func TestUnregister_StopsCheck(t *testing.T) {
	reg := newRegistry()
	_ = reg.add(protocol.ServiceEntry{Name: "svc", Type: protocol.ServiceTypeStatic})

	checkCount := 0
	hc := newHealthChecker(reg)
	hc.httpClient = &http.Client{
		Transport: &mockRoundTripper{
			fn: func(r *http.Request) (*http.Response, error) {
				checkCount++
				return mockResponse(http.StatusOK, ""), nil
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hc.Start(ctx)

	hc.Register("svc", &config.HealthcheckConfig{
		URL: "http://example.com/healthz",
	})

	// Allow one check to run.
	time.Sleep(50 * time.Millisecond)
	hc.Unregister("svc")

	countAfterUnregister := checkCount

	// Wait a bit and confirm no more checks.
	time.Sleep(50 * time.Millisecond)
	if checkCount != countAfterUnregister {
		t.Errorf("check ran after Unregister: count went from %d to %d",
			countAfterUnregister, checkCount)
	}
}

// netError is a minimal error implementing the Timeout() interface.
type netError struct {
	msg string
}

func (e *netError) Error() string   { return e.msg }
func (e *netError) Timeout() bool   { return false }
func (e *netError) Temporary() bool { return false }
