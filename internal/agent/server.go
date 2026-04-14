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
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"tailscale.com/client/local"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/config"
	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/daemon"
	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/protocol"
	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/tsutil"
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
	selfAddr     string              // host:port announced in SDTargets for dynamic services (v4 preferred)
	tsIPv4       string              // Tailscale IPv4 (e.g. "100.64.0.1")
	tsIPv6       string              // Tailscale IPv6 (e.g. "fd7a:115c:a1e0::1")
	extraTargets []protocol.SDTarget // appended to /api/v1/services when register_self=true

	// ACL Tag-based auth via Tailscale nodeAttrs.
	lc           local.Client
	allowedCTags []string // authorized Central ACL tags from nodeAttrs; empty = disabled

	// Client access tracking (in-memory only).
	lastClients map[string]*clientAccess // keyed by IP
}

// clientAccess records the last time a client accessed the Agent.
type clientAccess struct {
	nodeName string
	ip       string
	lastSeen time.Time
}

// NewServer creates a new Agent Server from the given config.
func NewServer(cfg config.AgentConfig) *Server {
	s := &Server{
		cfg:         cfg,
		reg:         newRegistry(),
		buckets:     newBucketStore(),
		proxies:     newProxyStore(),
		mux:         http.NewServeMux(),
		lastClients: make(map[string]*clientAccess),
	}
	s.hc = newHealthChecker(s.reg)
	s.registerHandlers()
	return s
}

func (s *Server) registerHandlers() {
	s.mux.HandleFunc("/healthz", s.handleHealthz)
	s.mux.HandleFunc("/api/v1/services", s.authMiddleware(s.handleServices))
	s.mux.HandleFunc("/api/v1/services/health", s.authMiddleware(s.handleServicesHealth))
	s.mux.HandleFunc("/push/", s.authMiddleware(s.handlePush))
	s.mux.HandleFunc("/bucket/", s.handleBucketMetrics)
	s.mux.HandleFunc("/proxy/", s.handleProxyMetrics)
}

// handleHealthz returns the health status including Tailscale connectivity.
// Returns 200 when healthy, 503 when unhealthy.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	ts := tsutil.QueryStatus(r.Context(), &s.lc)
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

// authMiddleware enforces authentication. Checks (in order):
//  1. ACL Tag verification via Tailscale WhoIs (if nodeAttrs configured)
//  2. Bearer token (if configured)
//
// When ACL Tag auth is active (allowedCTags non-empty):
//   - ACL Tag match → allow
//   - ACL Tag mismatch/WhoIs fail → check Bearer token
//     - Token configured: token must match → allow, else 401
//     - Token not configured: decide via allow_anonymous flag
//       - allow_anonymous=true → allow (open access)
//       - allow_anonymous=false (default) → 401
//
// When ACL Tag auth is inactive (allowedCTags empty):
//   - allow_anonymous has no effect
//   - Token configured: token must match → allow, else 401
//   - Token not configured: allow (open access, backward compatible)
func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		token := s.cfg.Server.Token
		allowedTags := s.allowedCTags
		allowAnon := s.cfg.Server.AllowAnonymous
		s.mu.RUnlock()

		// ACL Tag verification (when nodeAttrs auto-config is active).
		if len(allowedTags) > 0 {
			resp, err := s.lc.WhoIs(r.Context(), r.RemoteAddr)
			if err == nil && resp.Node != nil {
				if tagsIntersect(resp.Node.Tags, allowedTags) {
					s.recordClientAccess(r)
					next(w, r) // ACL Tag match → allow
					return
				}
			}
			// ACL Tag mismatch or WhoIs failed → check token as fallback.
			// If no token: allow only if allow_anonymous=true.
			if token == "" {
				if allowAnon {
					s.recordClientAccess(r)
					next(w, r) // allow anonymous
					return
				}
				// ACL Tag required, no token configured → reject.
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			// Token configured; fall through to token check.
		}

		// Bearer token check (standalone or fallback auth).
		if token != "" && r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Token matches or no token + no ACL Tag configured → allow.
		s.recordClientAccess(r)
		next(w, r)
	}
}

// tagsIntersect returns true if any of the node's tags is in the allowed set.
func tagsIntersect(nodeTags []string, allowed []string) bool {
	set := make(map[string]struct{}, len(allowed))
	for _, t := range allowed {
		set[t] = struct{}{}
	}
	for _, t := range nodeTags {
		if _, ok := set[t]; ok {
			return true
		}
	}
	return false
}

// recordClientAccess records a successful client access. It resolves the
// Tailscale node name via WhoIs when possible. The data is kept in memory only.
func (s *Server) recordClientAccess(r *http.Request) {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip == "" {
		ip = r.RemoteAddr
	}

	var nodeName string
	resp, err := s.lc.WhoIs(r.Context(), r.RemoteAddr)
	if err == nil && resp.Node != nil {
		nodeName = resp.Node.ComputedName
	}

	s.mu.Lock()
	s.lastClients[ip] = &clientAccess{
		nodeName: nodeName,
		ip:       ip,
		lastSeen: time.Now(),
	}
	s.mu.Unlock()
}

// clientAccessList returns a snapshot of recent client accesses for the status API.
func (s *Server) clientAccessList() []protocol.ClientAccessInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]protocol.ClientAccessInfo, 0, len(s.lastClients))
	for _, ca := range s.lastClients {
		result = append(result, protocol.ClientAccessInfo{
			NodeName: ca.nodeName,
			IP:       ca.ip,
			LastSeen: ca.lastSeen,
		})
	}
	return result
}

// LoadNodeAttrs reads this node's Tailscale nodeAttrs and updates:
//   - ACL auth config (allowedCTags) from CentralTags
//   - Listen port from AgentPort (updates cfg.Server.Listen)
//
// On error, the previous values are retained. Safe for concurrent use.
func (s *Server) LoadNodeAttrs(ctx context.Context) {
	attrs, err := tsutil.ReadSelfNodeAttrs(ctx, &s.lc)
	if err != nil {
		log.Printf("agent: failed to read nodeAttrs: %v (retaining previous)", err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if attrs == nil {
		s.allowedCTags = nil
		return
	}
	if len(attrs.CentralTags) > 0 {
		s.allowedCTags = attrs.CentralTags
		log.Printf("agent: ACL-based auth enabled, allowed central tags: %v", attrs.CentralTags)
	} else {
		s.allowedCTags = nil
	}
	if attrs.AgentPort > 0 {
		newListen := fmt.Sprintf(":%d", attrs.AgentPort)
		if s.cfg.Server.Listen != newListen {
			log.Printf("agent: nodeAttrs overriding listen port to %s", newListen)
			s.cfg.Server.Listen = newListen
		}
	}
}

// ClearNodeAttrs removes ACL tag auth config (used when node_attrs is disabled).
func (s *Server) ClearNodeAttrs() {
	s.mu.Lock()
	s.allowedCTags = nil
	s.mu.Unlock()
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

	// Handle node_attrs toggle on reload.
	if cfg.Server.NodeAttrs {
		s.LoadNodeAttrs(context.Background())
	} else {
		s.ClearNodeAttrs()
	}

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

	h2s := &http2.Server{}
	s.httpSrv = &http.Server{
		Addr:    s.cfg.Server.Listen,
		Handler: h2c.NewHandler(s.mux, h2s),
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

// --- /api/v1/services ---

func (s *Server) handleServices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check conditional request before doing any work.
	modTime := s.reg.SvcLastModified()
	if checkNotModified(r, modTime) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	entries := s.reg.listWithoutHealth()

	// Build resolve context for this request.
	s.mu.RLock()
	rc := &resolveContext{
		tsIPv4: s.tsIPv4,
		tsIPv6: s.tsIPv6,
	}
	listenAddr := s.cfg.Server.Listen
	extras := s.extraTargets // snapshot slice header under lock
	s.mu.RUnlock()

	// Append self-metrics targets as static entries (no health check).
	for _, t := range extras {
		entries = append(entries, protocol.ServiceEntry{
			Name:   "tsd-agent-metrics",
			Type:   protocol.ServiceTypeStatic,
			Target: t,
		})
	}

	_, listenPort, _ := splitHostPort(listenAddr)
	rc.selfAddr = selfAddrForRequest(r.RemoteAddr, rc.tsIPv4, rc.tsIPv6, listenPort)

	// Resolve variables in all targets.
	resolved := make([]protocol.ServiceEntry, 0, len(entries))
	for _, e := range entries {
		re, ok := resolveEntry(e, rc)
		if ok {
			resolved = append(resolved, re)
		}
	}

	w.Header().Set("Last-Modified", modTime.UTC().Format(http.TimeFormat))
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resolved); err != nil {
		log.Printf("agent: failed to encode services: %v", err)
	}
}

func (s *Server) handleServicesHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	modTime := s.reg.HealthLastModified()
	if checkNotModified(r, modTime) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	health := s.reg.listHealth()
	w.Header().Set("Last-Modified", modTime.UTC().Format(http.TimeFormat))
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(health); err != nil {
		log.Printf("agent: failed to encode services health: %v", err)
	}
}

// checkNotModified returns true if the request's If-Modified-Since header
// indicates the client already has the latest data.
func checkNotModified(r *http.Request, modTime time.Time) bool {
	ims := r.Header.Get("If-Modified-Since")
	if ims == "" {
		return false
	}
	t, err := http.ParseTime(ims)
	if err != nil {
		return false
	}
	// HTTP dates have second precision; truncate for comparison.
	return !modTime.Truncate(time.Second).After(t.Truncate(time.Second))
}

// resolveEntry resolves all target variables in a ServiceEntry.
// Returns false if any target in the entry cannot be resolved.
func resolveEntry(e protocol.ServiceEntry, rc *resolveContext) (protocol.ServiceEntry, bool) {
	var resolvedTargets []string
	for _, t := range e.Target.Targets {
		rt, ok := resolveTarget(t, rc)
		if !ok {
			continue
		}
		resolvedTargets = append(resolvedTargets, rt)
	}
	if len(resolvedTargets) == 0 {
		return protocol.ServiceEntry{}, false
	}
	e.Target.Targets = resolvedTargets
	return e, true
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
			// Dedicated listener: resolve wildcard host to Tailscale IP variable.
			host, port, err := net.SplitHostPort(sm.Listen)
			if err == nil && (host == "" || host == "0.0.0.0" || host == "::") {
				target = "{ts.ip}:" + port + path
			} else {
				target = host + ":" + port + path
			}
		} else {
			// Serve on main port: use {self} (resolved per-request).
			target = "{self}" + path
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
			Targets: []string{"{self}/bucket/" + name + "/metrics"},
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
			Targets: []string{"{self}/proxy/" + name + "/metrics"},
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

// Handler returns the HTTP handler for use with httptest.NewServer in tests.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// AddStaticForTest is a test helper that exposes addStatic to external test packages.
func (s *Server) AddStaticForTest(name string, targets []string, labels map[string]string, hcCfg *config.HealthcheckConfig) error {
	return s.addStatic(name, targets, labels, hcCfg)
}

// AddProxyForTest is a test helper that exposes addProxy to external test packages.
func (s *Server) AddProxyForTest(name, target, authType, token, username, password string, labels map[string]string, hcCfg *config.HealthcheckConfig) error {
	return s.addProxy(name, target, proxyAuth{authType: authType, token: token, username: username, password: password}, labels, hcCfg)
}
