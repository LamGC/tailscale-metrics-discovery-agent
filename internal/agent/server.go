package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/config"
	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/daemon"
	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/protocol"
)

// Server is the Agent HTTP server. It serves:
//   - GET  /api/v1/services          — service list for Central
//   - PUT/POST /push/<bucket>/job/<job>[/instance/<inst>]  — Pushgateway push
//   - DELETE   /push/<bucket>/job/<job>[/instance/<inst>]  — remove group
//   - GET  /bucket/<name>/metrics    — expose bucket metrics
//   - GET  /proxy/<name>/metrics     — proxy-scrape local target
type Server struct {
	mu           sync.RWMutex
	cfg          config.AgentConfig
	cfgFile      string
	reg          *registry
	hc           *healthChecker
	hcCancel     context.CancelFunc
	buckets      *bucketStore
	proxies      *proxyStore
	mux          *http.ServeMux
	httpSrv      *http.Server
	mgmtSrv      *http.Server
	metricsSrv   *http.Server        // optional dedicated metrics listener
	selfAddr     string              // host:port announced in SDTargets for dynamic services
	extraTargets []protocol.SDTarget // appended to /api/v1/services when register_self=true
}

// NewServer creates a new Agent Server from the given config.
func NewServer(cfg config.AgentConfig) *Server {
	s := &Server{
		cfg:     cfg,
		reg:     newRegistry(),
		buckets: newBucketStore(),
		proxies: newProxyStore(),
		mux:     http.NewServeMux(),
	}
	s.hc = newHealthChecker(s.reg)
	s.registerHandlers()
	return s
}

func (s *Server) registerHandlers() {
	s.mux.HandleFunc("/healthz", handleHealthz)
	s.mux.HandleFunc("/api/v1/services", s.authMiddleware(s.handleServices))
	s.mux.HandleFunc("/push/", s.authMiddleware(s.handlePush))
	s.mux.HandleFunc("/bucket/", s.handleBucketMetrics)
	s.mux.HandleFunc("/proxy/", s.handleProxyMetrics)
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
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

// Reload re-reads the config file and applies safe changes without restarting.
// On parse error a warning is logged and the existing config is kept.
func (s *Server) Reload() error {
	if s.cfgFile == "" {
		return nil
	}
	cfg, err := config.LoadAgentConfig(s.cfgFile)
	if err != nil {
		return fmt.Errorf("reload agent config: %w", err)
	}
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()

	s.reloadConfigServices(cfg)
	log.Printf("agent: config reloaded from %s", s.cfgFile)
	return nil
}

// reloadConfigServices removes all registered services and re-adds them from cfg.
// Since CLI adds are persisted to the config file, cfg is the single source of truth.
func (s *Server) reloadConfigServices(cfg config.AgentConfig) {
	for _, e := range s.reg.list() {
		switch e.Type {
		case protocol.ServiceTypeStatic:
			_ = s.removeStatic(e.Name)
		case protocol.ServiceTypeBucket:
			_ = s.removeBucket(e.Name)
		case protocol.ServiceTypeProxy:
			_ = s.removeProxy(e.Name)
		}
	}
	for _, st := range cfg.Statics {
		if err := s.addStatic(st.Name, st.Targets, st.Labels, st.Healthcheck); err != nil {
			log.Printf("agent: reload static %q: %v", st.Name, err)
		}
	}
	for _, bc := range cfg.Buckets {
		if err := s.addBucket(bc.Name, bc.Labels, bc.Healthcheck); err != nil {
			log.Printf("agent: reload bucket %q: %v", bc.Name, err)
		}
	}
	for _, pc := range cfg.Proxies {
		auth := proxyAuth{
			authType: pc.Auth.Type,
			token:    pc.Auth.Token,
			username: pc.Auth.Username,
			password: pc.Auth.Password,
		}
		if err := s.addProxy(pc.Name, pc.Target, auth, pc.Labels, pc.Healthcheck); err != nil {
			log.Printf("agent: reload proxy %q: %v", pc.Name, err)
		}
	}
}

// Start loads static services from config, then starts the HTTP and
// management servers.
func (s *Server) Start() error {
	hcCtx, hcCancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.hcCancel = hcCancel
	s.mu.Unlock()
	s.hc.Start(hcCtx)

	if err := s.loadStaticServices(); err != nil {
		return err
	}
	if err := s.loadBuckets(); err != nil {
		return err
	}
	if err := s.loadProxies(); err != nil {
		return err
	}
	s.setupMetrics()

	s.httpSrv = &http.Server{
		Addr:    s.cfg.Server.Listen,
		Handler: s.mux,
	}

	errCh := make(chan error, 3)

	go func() {
		log.Printf("agent: HTTP server listening on %s", s.cfg.Server.Listen)
		if err := s.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("agent HTTP server: %w", err)
		}
	}()

	if s.metricsSrv != nil {
		go func() {
			log.Printf("agent: self-metrics listening on %s", s.cfg.SelfMetrics.Listen)
			if err := s.metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				errCh <- fmt.Errorf("agent metrics server: %w", err)
			}
		}()
	}

	if s.cfg.Management.Socket != "" {
		mgmt := newMgmtServer(s)
		s.mgmtSrv = mgmt
		go func() {
			ln, err := daemon.Listen(s.cfg.Management.Socket)
			if err != nil {
				errCh <- fmt.Errorf("agent mgmt socket: %w", err)
				return
			}
			log.Printf("agent: management socket at %s", s.cfg.Management.Socket)
			if err := mgmt.Serve(ln); err != nil && err != http.ErrServerClosed {
				errCh <- fmt.Errorf("agent mgmt server: %w", err)
			}
		}()
	}

	return <-errCh
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) {
	s.mu.Lock()
	if s.hcCancel != nil {
		s.hcCancel()
	}
	s.mu.Unlock()
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

// loadStaticServices registers static services from config.
func (s *Server) loadStaticServices() error {
	for _, st := range s.cfg.Statics {
		if err := s.addStatic(st.Name, st.Targets, st.Labels, st.Healthcheck); err != nil {
			return fmt.Errorf("loading static service %q: %w", st.Name, err)
		}
	}
	return nil
}

// loadBuckets creates bucket entries from config.
func (s *Server) loadBuckets() error {
	for _, bc := range s.cfg.Buckets {
		if err := s.addBucket(bc.Name, bc.Labels, bc.Healthcheck); err != nil {
			return err
		}
	}
	return nil
}

// loadProxies creates proxy entries from config.
func (s *Server) loadProxies() error {
	for _, pc := range s.cfg.Proxies {
		auth := proxyAuth{
			authType: pc.Auth.Type,
			token:    pc.Auth.Token,
			username: pc.Auth.Username,
			password: pc.Auth.Password,
		}
		if err := s.addProxy(pc.Name, pc.Target, auth, pc.Labels, pc.Healthcheck); err != nil {
			return err
		}
	}
	return nil
}

// selfHost returns the host (without port) from the listen address.
// It is used to build SDTarget URLs for dynamic services.
func (s *Server) selfHost() string {
	if s.selfAddr != "" {
		return s.selfAddr
	}
	return s.cfg.Server.Listen
}

// --- /api/v1/services ---

func (s *Server) handleServices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	entries := s.reg.list()
	if entries == nil {
		entries = []protocol.ServiceEntry{}
	}
	// Append self-metrics targets as static entries (no health check).
	for _, t := range s.extraTargets {
		entries = append(entries, protocol.ServiceEntry{
			Name:   "tsd-agent-metrics",
			Type:   protocol.ServiceTypeStatic,
			Target: t,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(entries); err != nil {
		log.Printf("agent: failed to encode services: %v", err)
	}
}

// setupMetrics configures the self-metrics endpoint. Must be called in Start()
// after selfAddr is set so the correct SDTarget address can be computed.
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
		newAgentCollector(s.reg),
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
		var target string
		if sm.Listen != "" {
			// Dedicated listener: resolve wildcard host to Tailscale IP.
			host, port, err := net.SplitHostPort(sm.Listen)
			if err == nil && (host == "" || host == "0.0.0.0" || host == "::") {
				tsHost, _, _ := net.SplitHostPort(s.selfAddr)
				if tsHost != "" {
					host = tsHost
				} else {
					host = "localhost"
				}
			}
			target = host + ":" + port + path
		} else {
			// Serve on main port: use selfAddr (Tailscale IP:port).
			if s.selfAddr != "" {
				target = s.selfAddr + path
			} else {
				// Fallback: resolve main listen addr.
				h, port, err := net.SplitHostPort(s.cfg.Server.Listen)
				if err == nil {
					if h == "" || h == "0.0.0.0" || h == "::" {
						h = "localhost"
					}
					target = h + ":" + port + path
				} else {
					target = s.cfg.Server.Listen + path
				}
			}
		}
		labels := map[string]string{
			"__tsd_service_name": "tsd-agent",
			"__tsd_service_type": "static",
		}
		maps.Copy(labels, sm.Labels)
		s.extraTargets = append(s.extraTargets, protocol.SDTarget{
			Targets: []string{target},
			Labels:  labels,
		})
	}
}

// --- /push/<bucket>/... ---

func (s *Server) handlePush(w http.ResponseWriter, r *http.Request) {
	// Path: /push/<bucket>/job/<job>[/instance/<inst>]
	path := strings.TrimPrefix(r.URL.Path, "/push/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 {
		http.Error(w, "bad push path", http.StatusBadRequest)
		return
	}
	bucketName := parts[0]
	rest := parts[1] // job/<job>[/instance/<inst>]

	b, ok := s.buckets.get(bucketName)
	if !ok {
		http.Error(w, fmt.Sprintf("bucket %q not found", bucketName), http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodPut, http.MethodPost:
		b.push(w, r, rest)
	case http.MethodDelete:
		b.delete(w, r, rest)
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

// --- /bucket/<name>/metrics ---

func (s *Server) handleBucketMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/bucket/")
	name = strings.TrimSuffix(name, "/metrics")
	name = strings.Trim(name, "/")

	b, ok := s.buckets.get(name)
	if !ok {
		http.Error(w, fmt.Sprintf("bucket %q not found", name), http.StatusNotFound)
		return
	}
	b.serveMetrics(w, r)
}

// --- /proxy/<name>/metrics ---

func (s *Server) handleProxyMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/proxy/")
	name = strings.TrimSuffix(name, "/metrics")
	name = strings.Trim(name, "/")

	p, ok := s.proxies.get(name)
	if !ok {
		http.Error(w, fmt.Sprintf("proxy %q not found", name), http.StatusNotFound)
		return
	}
	p.serveMetrics(w, r)
}

// addBucket creates a new push bucket and registers it in the service registry.
func (s *Server) addBucket(name string, labels map[string]string, hcCfg *config.HealthcheckConfig) error {
	b := newBucket(name)
	if err := s.buckets.add(name, b); err != nil {
		return err
	}
	lbs := map[string]string{}
	maps.Copy(lbs, labels)
	lbs["__tsd_service_name"] = name
	lbs["__tsd_service_type"] = "bucket"
	entry := protocol.ServiceEntry{
		Name: name,
		Type: protocol.ServiceTypeBucket,
		Target: protocol.SDTarget{
			Targets: []string{s.selfHost() + "/bucket/" + name + "/metrics"},
			Labels:  lbs,
		},
	}
	if err := s.reg.add(entry); err != nil {
		_ = s.buckets.remove(name)
		return fmt.Errorf("registering bucket %q: %w", name, err)
	}
	s.hc.Register(name, hcCfg)
	return nil
}

// removeBucket removes a bucket and its registry entry.
func (s *Server) removeBucket(name string) error {
	s.hc.Unregister(name)
	if err := s.buckets.remove(name); err != nil {
		return err
	}
	return s.reg.remove(name)
}

// addProxy creates a proxy and registers it in the service registry.
func (s *Server) addProxy(name, target string, auth proxyAuth, labels map[string]string, hcCfg *config.HealthcheckConfig) error {
	p := newProxy(target, auth)
	if err := s.proxies.add(name, p); err != nil {
		return err
	}
	lbs := map[string]string{}
	maps.Copy(lbs, labels)
	lbs["__tsd_service_name"] = name
	lbs["__tsd_service_type"] = "proxy"
	entry := protocol.ServiceEntry{
		Name: name,
		Type: protocol.ServiceTypeProxy,
		Target: protocol.SDTarget{
			Targets: []string{s.selfHost() + "/proxy/" + name + "/metrics"},
			Labels:  lbs,
		},
	}
	if err := s.reg.add(entry); err != nil {
		_ = s.proxies.remove(name)
		return fmt.Errorf("registering proxy %q: %w", name, err)
	}
	s.hc.Register(name, hcCfg)
	return nil
}

// removeProxy removes a proxy and its registry entry.
func (s *Server) removeProxy(name string) error {
	s.hc.Unregister(name)
	if err := s.proxies.remove(name); err != nil {
		return err
	}
	return s.reg.remove(name)
}

// addStatic adds a static service entry.
func (s *Server) addStatic(name string, targets []string, labels map[string]string, hcCfg *config.HealthcheckConfig) error {
	lbs := map[string]string{}
	maps.Copy(lbs, labels)
	lbs["__tsd_service_name"] = name
	entry := protocol.ServiceEntry{
		Name: name,
		Type: protocol.ServiceTypeStatic,
		Target: protocol.SDTarget{
			Targets: targets,
			Labels:  lbs,
		},
	}
	if err := s.reg.add(entry); err != nil {
		return err
	}
	s.hc.Register(name, hcCfg)
	return nil
}

// saveConfig persists cfg to the configured config file.
// Errors are logged but not returned — a failed save should not break the operation.
func (s *Server) saveConfig(cfg config.AgentConfig) {
	if s.cfgFile == "" {
		return
	}
	if err := config.SaveAgentConfig(s.cfgFile, cfg); err != nil {
		log.Printf("agent: failed to save config: %v", err)
	}
}

// filterSlice returns a new slice containing only elements for which keep returns true.
func filterSlice[T any](s []T, keep func(T) bool) []T {
	out := make([]T, 0, len(s))
	for _, v := range s {
		if keep(v) {
			out = append(out, v)
		}
	}
	return out
}

// removeStatic removes a static service entry.
func (s *Server) removeStatic(name string) error {
	s.hc.Unregister(name)
	return s.reg.remove(name)
}
