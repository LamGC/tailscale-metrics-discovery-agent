package central

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"net"
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/config"
	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/daemon"
	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/protocol"
)

// Server is the Central HTTP server. It serves:
//   - GET /api/v1/sd   — Prometheus http_sd endpoint
type Server struct {
	mu          sync.RWMutex
	cfg         config.CentralConfig
	cfgFile     string
	col         *collector
	mux         *http.ServeMux
	httpSrv     *http.Server
	mgmtSrv     *http.Server
	metricsSrv  *http.Server        // optional dedicated metrics listener
	selfTargets []protocol.SDTarget // injected into /api/v1/sd when register_self=true
}

// NewServer creates a Central Server from the given config.
func NewServer(cfg config.CentralConfig) *Server {
	disc := newDiscoverer(
		cfg.Tailscale.Socket,
		cfg.Discovery.Tags,
		cfg.Discovery.AgentPort,
	)
	col := newCollector(disc, cfg.Discovery.AgentToken)
	col.ReplaceConfigPeers(cfg.ManualPeers) // load [[peer]] entries from config
	s := &Server{
		cfg: cfg,
		col: col,
		mux: http.NewServeMux(),
	}
	s.registerHandlers()
	return s
}

func (s *Server) registerHandlers() {
	s.mux.HandleFunc("/healthz", s.handleHealthz)
	s.mux.HandleFunc("/api/v1/sd", s.authMiddleware(s.handleSD))
}

// handleHealthz returns the health status including Tailscale connectivity.
// Returns 200 when healthy, 503 when unhealthy.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	ts := s.col.discoverer.TailscaleStatus(r.Context())
	healthy := ts.Connected
	resp := struct {
		OK                 bool   `json:"ok"`
		TailscaleConnected bool   `json:"tailscale_connected"`
		TailscaleNetwork   bool   `json:"tailscale_network"`
		BackendState       string `json:"backend_state,omitempty"`
	}{
		OK:                 healthy,
		TailscaleConnected: ts.BackendState != "unreachable",
		TailscaleNetwork:   ts.Connected,
		BackendState:       ts.BackendState,
	}
	w.Header().Set("Content-Type", "application/json")
	if healthy {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// authMiddleware enforces Bearer token auth when a token is configured.
func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		token := s.cfg.Server.Token
		s.mu.RUnlock()
		if token != "" && r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// Reload re-reads the config file and applies changes that are safe to apply
// without restarting. On parse error the existing config is kept and an error
// is returned (callers should log a warning but not crash).
func (s *Server) Reload() error {
	if s.cfgFile == "" {
		return nil
	}
	cfg, err := config.LoadCentralConfig(s.cfgFile)
	if err != nil {
		return fmt.Errorf("reload central config: %w", err)
	}
	s.mu.Lock()
	s.cfg.Server.Token = cfg.Server.Token
	s.mu.Unlock()

	s.col.UpdateAgentToken(cfg.Discovery.AgentToken)
	s.col.discoverer.UpdateConfig(cfg.Discovery.Tags, cfg.Discovery.AgentPort)
	s.col.ReplaceConfigPeers(cfg.ManualPeers)

	// Handle node_attrs toggle on reload.
	if cfg.Discovery.NodeAttrs {
		s.col.discoverer.RefreshSelfAttrs(context.Background())
	} else {
		s.col.discoverer.ClearSelfAttrs()
	}

	log.Printf("central: config reloaded from %s", s.cfgFile)
	return nil
}

// handleSD returns the aggregated Prometheus http_sd target list.
func (s *Server) handleSD(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	targets := s.col.Targets()
	if targets == nil {
		targets = []protocol.SDTarget{}
	}
	if len(s.selfTargets) > 0 {
		targets = append(targets, s.selfTargets...)
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(targets); err != nil {
		log.Printf("central: encode SD targets: %v", err)
	}
}

// setupMetrics configures the self-metrics endpoint based on SelfMetrics config.
// Must be called after selfTargets is ready to be populated (i.e., in Start()).
func (s *Server) setupMetrics() {
	sm := s.cfg.SelfMetrics
	if !sm.Enabled {
		return
	}

	path := sm.Path
	if path == "" {
		path = "/metrics"
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		newCentralCollector(s.col),
	)
	handler := promhttp.HandlerFor(reg, promhttp.HandlerOpts{})

	if sm.Listen != "" {
		mx := http.NewServeMux()
		mx.Handle(path, handler)
		s.metricsSrv = &http.Server{Addr: sm.Listen, Handler: mx}
	} else {
		s.mux.Handle(path, handler)
	}

	if sm.RegisterSelf {
		listenAddr := sm.Listen
		if listenAddr == "" {
			listenAddr = s.cfg.Server.Listen
		}
		labels := map[string]string{
			"__tsd_service_name": "tsd-central",
			"__tsd_service_type": "static",
			"__metrics_path__":   path,
		}
		maps.Copy(labels, sm.Labels)
		s.selfTargets = []protocol.SDTarget{{
			Targets: []string{resolveListenToHost(listenAddr)},
			Labels:  labels,
		}}
	}
}

// resolveListenToHost converts a listen address (e.g. ":9000") to a
// host:port string suitable for Prometheus targets.
// Unspecified or wildcard hosts are replaced with "localhost".
func resolveListenToHost(listenAddr string) string {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return listenAddr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return host + ":" + port
}

// Start begins the collector refresh loop, then starts the HTTP and management
// servers. It blocks until one of the servers returns an error.
func (s *Server) Start(ctx context.Context) error {
	s.setupMetrics()
	go s.col.Run(ctx, s.cfg.Discovery.RefreshInterval.Duration, s.cfg.Discovery.NodeAttrs)

	s.httpSrv = &http.Server{
		Addr:    s.cfg.Server.Listen,
		Handler: s.mux,
	}

	errCh := make(chan error, 3)

	go func() {
		log.Printf("central: HTTP server listening on %s", s.cfg.Server.Listen)
		if err := s.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("central HTTP server: %w", err)
		}
	}()

	if s.metricsSrv != nil {
		go func() {
			log.Printf("central: self-metrics listening on %s", s.cfg.SelfMetrics.Listen)
			if err := s.metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				errCh <- fmt.Errorf("central metrics server: %w", err)
			}
		}()
	}

	if s.cfg.Management.Socket != "" {
		mgmt := newCentralMgmtServer(s)
		s.mgmtSrv = mgmt
		go func() {
			ln, err := daemon.Listen(s.cfg.Management.Socket)
			if err != nil {
				errCh <- fmt.Errorf("central mgmt socket: %w", err)
				return
			}
			log.Printf("central: management socket at %s", s.cfg.Management.Socket)
			if err := mgmt.Serve(ln); err != nil && err != http.ErrServerClosed {
				errCh <- fmt.Errorf("central mgmt server: %w", err)
			}
		}()
	}

	return <-errCh
}

// saveCentralConfig atomically writes cfg to the configured config file.
func (s *Server) saveCentralConfig(cfg config.CentralConfig) {
	if s.cfgFile == "" {
		return
	}
	if err := config.SaveCentralConfig(s.cfgFile, cfg); err != nil {
		log.Printf("central: failed to save config: %v", err)
	}
}

// filterManualPeers returns a new slice with the peer at address removed.
func filterManualPeers(peers []config.ManualPeer, address string) []config.ManualPeer {
	out := make([]config.ManualPeer, 0, len(peers))
	for _, p := range peers {
		if p.Address != address {
			out = append(out, p)
		}
	}
	return out
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) {
	if s.httpSrv != nil {
		_ = s.httpSrv.Shutdown(ctx)
	}
	if s.metricsSrv != nil {
		_ = s.metricsSrv.Shutdown(ctx)
	}
	if s.mgmtSrv != nil {
		_ = s.mgmtSrv.Shutdown(ctx)
	}
}
