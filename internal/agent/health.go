package agent

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/config"
	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/protocol"
)

const (
	defaultCheckInterval = 30 * time.Second
	defaultCheckTimeout  = 5 * time.Second
)

// healthChecker runs periodic health checks for services that have a check URL.
// It updates the corresponding registry entries in-place.
type healthChecker struct {
	reg        *registry
	httpClient *http.Client

	mu      sync.Mutex
	ctx     context.Context // server-lifetime context; set by Start
	cancels map[string]context.CancelFunc
}

func newHealthChecker(reg *registry) *healthChecker {
	return &healthChecker{
		reg:        reg,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		cancels:    make(map[string]context.CancelFunc),
	}
}

// Start sets the root context for all health-check goroutines.
// Must be called before Register.
func (h *healthChecker) Start(ctx context.Context) {
	h.mu.Lock()
	h.ctx = ctx
	h.mu.Unlock()
}

// Register starts a periodic health check for service name using cfg.
// If cfg is nil or cfg.URL is empty this is a no-op.
// Any previously registered check for name is cancelled first.
func (h *healthChecker) Register(name string, cfg *config.HealthcheckConfig) {
	if cfg == nil || cfg.URL == "" {
		return
	}
	interval := cfg.Interval.Duration
	if interval <= 0 {
		interval = defaultCheckInterval
	}
	timeout := cfg.Timeout.Duration
	if timeout <= 0 {
		timeout = defaultCheckTimeout
	}
	checkURL := cfg.URL

	h.mu.Lock()
	if cancel, ok := h.cancels[name]; ok {
		cancel()
	}
	if h.ctx == nil {
		h.mu.Unlock()
		return
	}
	childCtx, cancel := context.WithCancel(h.ctx)
	h.cancels[name] = cancel
	h.mu.Unlock()

	// Set initial "unknown" status immediately so the entry isn't nil.
	h.reg.updateHealth(name, protocol.ServiceHealthStatus{
		Status:   protocol.ServiceHealthUnknown,
		CheckURL: checkURL,
	})

	go h.runCheck(childCtx, name, checkURL, interval, timeout)
}

// Unregister stops the health check for service name.
func (h *healthChecker) Unregister(name string) {
	h.mu.Lock()
	if cancel, ok := h.cancels[name]; ok {
		cancel()
		delete(h.cancels, name)
	}
	h.mu.Unlock()
}

func (h *healthChecker) runCheck(ctx context.Context, name, checkURL string, interval, timeout time.Duration) {
	// Run immediately, then on each tick.
	h.doCheck(ctx, name, checkURL, timeout)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.doCheck(ctx, name, checkURL, timeout)
		}
	}
}

func (h *healthChecker) doCheck(ctx context.Context, name, checkURL string, timeout time.Duration) {
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	status := protocol.ServiceHealthStatus{
		CheckURL:  checkURL,
		LastCheck: new(time.Now()),
	}

	req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, checkURL, nil)
	if err != nil {
		status.Status = protocol.ServiceHealthUnhealthy
		status.Message = fmt.Sprintf("build request: %v", err)
		h.reg.updateHealth(name, status)
		return
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		status.Status = protocol.ServiceHealthUnhealthy
		status.Message = err.Error()
		h.reg.updateHealth(name, status)
		log.Printf("agent: healthcheck %q: %v", name, err)
		return
	}
	resp.Body.Close()

	status.StatusCode = resp.StatusCode
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		status.Status = protocol.ServiceHealthHealthy
	} else {
		status.Status = protocol.ServiceHealthUnhealthy
		status.Message = fmt.Sprintf("HTTP %d", resp.StatusCode)
		log.Printf("agent: healthcheck %q: HTTP %d", name, resp.StatusCode)
	}
	h.reg.updateHealth(name, status)
}
