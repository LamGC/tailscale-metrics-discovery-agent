package agent

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/lamgc/tailscale-service-discovery-agent/internal/protocol"
)

// newMgmtServer returns an *http.Server that handles management API calls for
// the Agent. It is intended to be served over a Unix domain socket.
func newMgmtServer(s *Server) *http.Server {
	mux := http.NewServeMux()

	// GET /status — basic liveness / info
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, protocol.StatusResponse{
			Running: true,
			Info:    "agent",
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
			Name    string            `json:"name"`
			Targets []string          `json:"targets"`
			Labels  map[string]string `json:"labels"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.addStatic(req.Name, req.Targets, req.Labels); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
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
		writeJSON(w, map[string]string{"status": "ok"})
	})

	// POST /mgmt/bucket/add
	mux.HandleFunc("/mgmt/bucket/add", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Name   string            `json:"name"`
			Labels map[string]string `json:"labels"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.addBucket(req.Name, req.Labels); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
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
			Name     string            `json:"name"`
			Target   string            `json:"target"`
			AuthType string            `json:"auth_type"`
			Token    string            `json:"token"`
			Username string            `json:"username"`
			Password string            `json:"password"`
			Labels   map[string]string `json:"labels"`
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
		if err := s.addProxy(req.Name, req.Target, auth, req.Labels); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
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
		writeJSON(w, map[string]string{"status": "ok"})
	})

	return &http.Server{Handler: mux}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
