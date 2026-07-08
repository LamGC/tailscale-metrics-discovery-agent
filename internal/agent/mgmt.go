package agent

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/config"
	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/protocol"
)

// newMgmtServer returns an *http.Server that handles management API calls for
// the Agent. It is intended to be served over a Unix domain socket.
func newMgmtServer(s *Server) *http.Server {
	mux := http.NewServeMux()

	// GET /status — basic liveness / info
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		ts := agentTailscaleStatus(r.Context())
		writeJSON(w, protocol.StatusResponse{
			Running:   true,
			Info:      "agent",
			Tailscale: ts,
			Clients:   s.clientAccessList(),
		})
	})

	// GET /services — list all registered services
	mux.HandleFunc("/services", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.reg.list())
	})

	// POST /reload — re-read config file and apply changes
	mux.HandleFunc("/reload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := s.Reload(); err != nil {
			log.Printf("agent: reload warning: %v", err)
		}
		writeJSON(w, map[string]string{"status": "ok"})
	})

	// --- dynamic management endpoints called by CLI ---

	// POST /mgmt/service/add
	mux.HandleFunc("/mgmt/service/add", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Name           string            `json:"name"`
			Targets        []string          `json:"targets"`
			Labels         map[string]string `json:"labels"`
			HealthcheckURL string            `json:"healthcheck_url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var hcCfg *config.HealthcheckConfig
		if req.HealthcheckURL != "" {
			hcCfg = &config.HealthcheckConfig{URL: req.HealthcheckURL}
		}

		s.mu.Lock()
		oldCfg := cloneAgentConfig(s.cfg)
		nextCfg := cloneAgentConfig(s.cfg)
		s.mu.Unlock()
		nextCfg.Statics = append(nextCfg.Statics, config.StaticService{
			Name:        req.Name,
			Targets:     req.Targets,
			Labels:      req.Labels,
			Healthcheck: hcCfg,
		})
		if err := s.saveConfig(nextCfg); err != nil {
			http.Error(w, "saving config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if err := s.addStatic(req.Name, req.Targets, req.Labels, hcCfg); err != nil {
			if rollbackErr := s.saveConfig(oldCfg); rollbackErr != nil {
				http.Error(w, "saving config after rollback: "+rollbackErr.Error(), http.StatusInternalServerError)
				return
			}
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		s.mu.Lock()
		s.cfg = nextCfg
		s.mu.Unlock()
		writeJSON(w, map[string]string{"status": "ok"})
	})

	// POST /mgmt/service/remove
	mux.HandleFunc("/mgmt/service/remove", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		oldCfg := cloneAgentConfig(s.cfg)
		nextCfg := cloneAgentConfig(s.cfg)
		s.mu.Unlock()
		nextCfg.Statics = filterSlice(nextCfg.Statics, func(v config.StaticService) bool { return v.Name != req.Name })
		if err := s.saveConfig(nextCfg); err != nil {
			http.Error(w, "saving config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if err := s.removeStatic(req.Name); err != nil {
			if rollbackErr := s.saveConfig(oldCfg); rollbackErr != nil {
				http.Error(w, "saving config after rollback: "+rollbackErr.Error(), http.StatusInternalServerError)
				return
			}
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		s.mu.Lock()
		s.cfg = nextCfg
		s.mu.Unlock()
		writeJSON(w, map[string]string{"status": "ok"})
	})

	// POST /mgmt/bucket/add
	mux.HandleFunc("/mgmt/bucket/add", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Name           string            `json:"name"`
			Labels         map[string]string `json:"labels"`
			HealthcheckURL string            `json:"healthcheck_url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var hcCfg *config.HealthcheckConfig
		if req.HealthcheckURL != "" {
			hcCfg = &config.HealthcheckConfig{URL: req.HealthcheckURL}
		}
		s.mu.Lock()
		oldCfg := cloneAgentConfig(s.cfg)
		nextCfg := cloneAgentConfig(s.cfg)
		s.mu.Unlock()
		nextCfg.Buckets = append(nextCfg.Buckets, config.BucketService{
			Name:        req.Name,
			Labels:      req.Labels,
			Healthcheck: hcCfg,
		})
		if err := s.saveConfig(nextCfg); err != nil {
			http.Error(w, "saving config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if err := s.addBucket(req.Name, req.Labels, hcCfg); err != nil {
			if rollbackErr := s.saveConfig(oldCfg); rollbackErr != nil {
				http.Error(w, "saving config after rollback: "+rollbackErr.Error(), http.StatusInternalServerError)
				return
			}
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		s.mu.Lock()
		s.cfg = nextCfg
		s.mu.Unlock()
		writeJSON(w, map[string]string{"status": "ok"})
	})

	// POST /mgmt/bucket/remove
	mux.HandleFunc("/mgmt/bucket/remove", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		oldCfg := cloneAgentConfig(s.cfg)
		nextCfg := cloneAgentConfig(s.cfg)
		s.mu.Unlock()
		nextCfg.Buckets = filterSlice(nextCfg.Buckets, func(v config.BucketService) bool { return v.Name != req.Name })
		if err := s.saveConfig(nextCfg); err != nil {
			http.Error(w, "saving config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if err := s.removeBucket(req.Name); err != nil {
			if rollbackErr := s.saveConfig(oldCfg); rollbackErr != nil {
				http.Error(w, "saving config after rollback: "+rollbackErr.Error(), http.StatusInternalServerError)
				return
			}
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		s.mu.Lock()
		s.cfg = nextCfg
		s.mu.Unlock()
		writeJSON(w, map[string]string{"status": "ok"})
	})

	// POST /mgmt/bucket/clear
	mux.HandleFunc("/mgmt/bucket/clear", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		b, ok := s.buckets.get(req.Name)
		if !ok {
			http.Error(w, "bucket not found", http.StatusNotFound)
			return
		}
		b.clear()
		writeJSON(w, map[string]string{"status": "ok"})
	})

	// POST /mgmt/proxy/add
	mux.HandleFunc("/mgmt/proxy/add", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Name           string            `json:"name"`
			Target         string            `json:"target"`
			AuthType       string            `json:"auth_type"`
			Token          string            `json:"token"`
			Username       string            `json:"username"`
			Password       string            `json:"password"`
			Labels         map[string]string `json:"labels"`
			HealthcheckURL string            `json:"healthcheck_url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Auto-detect auth type from provided credentials.
		if req.AuthType == "" || req.AuthType == "none" {
			hasToken := req.Token != ""
			hasBasic := req.Username != "" || req.Password != ""
			if hasToken && hasBasic {
				http.Error(w, "conflicting auth: token and username/password cannot be used together", http.StatusBadRequest)
				return
			}
			if hasToken {
				req.AuthType = "bearer"
			} else if hasBasic {
				req.AuthType = "basic"
			}
		}
		auth := proxyAuth{
			authType: req.AuthType,
			token:    req.Token,
			username: req.Username,
			password: req.Password,
		}
		var hcCfg *config.HealthcheckConfig
		if req.HealthcheckURL != "" {
			hcCfg = &config.HealthcheckConfig{URL: req.HealthcheckURL}
		}
		s.mu.Lock()
		oldCfg := cloneAgentConfig(s.cfg)
		nextCfg := cloneAgentConfig(s.cfg)
		s.mu.Unlock()
		nextCfg.Proxies = append(nextCfg.Proxies, config.ProxyService{
			Name:        req.Name,
			Target:      req.Target,
			Auth:        config.ProxyAuth{Type: req.AuthType, Token: req.Token, Username: req.Username, Password: req.Password},
			Labels:      req.Labels,
			Healthcheck: hcCfg,
		})
		if err := s.saveConfig(nextCfg); err != nil {
			http.Error(w, "saving config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if err := s.addProxy(req.Name, req.Target, auth, req.Labels, hcCfg); err != nil {
			if rollbackErr := s.saveConfig(oldCfg); rollbackErr != nil {
				http.Error(w, "saving config after rollback: "+rollbackErr.Error(), http.StatusInternalServerError)
				return
			}
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		s.mu.Lock()
		s.cfg = nextCfg
		s.mu.Unlock()
		writeJSON(w, map[string]string{"status": "ok"})
	})

	// POST /mgmt/proxy/remove
	mux.HandleFunc("/mgmt/proxy/remove", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		oldCfg := cloneAgentConfig(s.cfg)
		nextCfg := cloneAgentConfig(s.cfg)
		s.mu.Unlock()
		nextCfg.Proxies = filterSlice(nextCfg.Proxies, func(v config.ProxyService) bool { return v.Name != req.Name })
		if err := s.saveConfig(nextCfg); err != nil {
			http.Error(w, "saving config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if err := s.removeProxy(req.Name); err != nil {
			if rollbackErr := s.saveConfig(oldCfg); rollbackErr != nil {
				http.Error(w, "saving config after rollback: "+rollbackErr.Error(), http.StatusInternalServerError)
				return
			}
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		s.mu.Lock()
		s.cfg = nextCfg
		s.mu.Unlock()
		writeJSON(w, map[string]string{"status": "ok"})
	})

	return &http.Server{Handler: mux}
}

func cloneAgentConfig(cfg config.AgentConfig) config.AgentConfig {
	out := config.AgentConfig{
		Server:      cfg.Server,
		Management:  cfg.Management,
		SelfMetrics: cloneSelfMetricsConfig(cfg.SelfMetrics),
		Statics:     make([]config.StaticService, len(cfg.Statics)),
		Buckets:     make([]config.BucketService, len(cfg.Buckets)),
		Proxies:     make([]config.ProxyService, len(cfg.Proxies)),
	}

	for i, st := range cfg.Statics {
		out.Statics[i] = config.StaticService{
			Name:        st.Name,
			Targets:     append([]string(nil), st.Targets...),
			Labels:      copyStringMap(st.Labels),
			Healthcheck: cloneHealthcheck(st.Healthcheck),
		}
	}
	for i, bc := range cfg.Buckets {
		out.Buckets[i] = config.BucketService{
			Name:        bc.Name,
			Labels:      copyStringMap(bc.Labels),
			Healthcheck: cloneHealthcheck(bc.Healthcheck),
		}
	}
	for i, pc := range cfg.Proxies {
		out.Proxies[i] = config.ProxyService{
			Name:        pc.Name,
			Target:      pc.Target,
			Auth:        pc.Auth,
			Labels:      copyStringMap(pc.Labels),
			Healthcheck: cloneHealthcheck(pc.Healthcheck),
		}
	}
	return out
}

func cloneSelfMetricsConfig(cfg config.SelfMetricsConfig) config.SelfMetricsConfig {
	return config.SelfMetricsConfig{
		Enabled:      cfg.Enabled,
		Path:         cfg.Path,
		Listen:       cfg.Listen,
		RegisterSelf: cfg.RegisterSelf,
		Labels:       copyStringMap(cfg.Labels),
	}
}

func cloneHealthcheck(cfg *config.HealthcheckConfig) *config.HealthcheckConfig {
	if cfg == nil {
		return nil
	}
	out := *cfg
	return &out
}

func copyStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON: %v", err)
	}
}
