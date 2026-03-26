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
		writeJSON(w, protocol.StatusResponse{Running: true, Info: "agent", Tailscale: ts})
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
		if err := s.addStatic(req.Name, req.Targets, req.Labels, hcCfg); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		s.mu.Lock()
		s.cfg.Statics = append(s.cfg.Statics, config.StaticService{
			Name:        req.Name,
			Targets:     req.Targets,
			Labels:      req.Labels,
			Healthcheck: hcCfg,
		})
		cfg := s.cfg
		s.mu.Unlock()
		s.saveConfig(cfg)
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
		if err := s.removeStatic(req.Name); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		s.mu.Lock()
		s.cfg.Statics = filterSlice(s.cfg.Statics, func(v config.StaticService) bool { return v.Name != req.Name })
		cfg := s.cfg
		s.mu.Unlock()
		s.saveConfig(cfg)
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
		if err := s.addBucket(req.Name, req.Labels, hcCfg); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		s.mu.Lock()
		s.cfg.Buckets = append(s.cfg.Buckets, config.BucketService{
			Name:        req.Name,
			Labels:      req.Labels,
			Healthcheck: hcCfg,
		})
		cfg := s.cfg
		s.mu.Unlock()
		s.saveConfig(cfg)
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
		if err := s.removeBucket(req.Name); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		s.mu.Lock()
		s.cfg.Buckets = filterSlice(s.cfg.Buckets, func(v config.BucketService) bool { return v.Name != req.Name })
		cfg := s.cfg
		s.mu.Unlock()
		s.saveConfig(cfg)
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
		if err := s.addProxy(req.Name, req.Target, auth, req.Labels, hcCfg); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		s.mu.Lock()
		s.cfg.Proxies = append(s.cfg.Proxies, config.ProxyService{
			Name:        req.Name,
			Target:      req.Target,
			Auth:        config.ProxyAuth{Type: req.AuthType, Token: req.Token, Username: req.Username, Password: req.Password},
			Labels:      req.Labels,
			Healthcheck: hcCfg,
		})
		cfg := s.cfg
		s.mu.Unlock()
		s.saveConfig(cfg)
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
		if err := s.removeProxy(req.Name); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		s.mu.Lock()
		s.cfg.Proxies = filterSlice(s.cfg.Proxies, func(v config.ProxyService) bool { return v.Name != req.Name })
		cfg := s.cfg
		s.mu.Unlock()
		s.saveConfig(cfg)
		writeJSON(w, map[string]string{"status": "ok"})
	})

	return &http.Server{Handler: mux}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
