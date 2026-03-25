package central

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/lamgc/tailscale-service-discovery-agent/internal/protocol"
)

// newCentralMgmtServer returns an *http.Server for Central's management API.
// It is intended to be served over a platform-specific socket.
func newCentralMgmtServer(s *Server) *http.Server {
	mux := http.NewServeMux()

	// GET /status
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		ts := s.col.discoverer.TailscaleStatus(r.Context())
		writeJSON(w, protocol.StatusResponse{Running: true, Info: "central", Tailscale: ts})
	})

	// GET /peers — full peer list with health status
	mux.HandleFunc("/peers", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, protocol.PeersResponse{Peers: s.col.Peers()})
	})

	// GET /targets — current aggregated SD targets
	mux.HandleFunc("/targets", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.col.Targets())
	})

	// POST /reload — reload config file and trigger an immediate refresh
	mux.HandleFunc("/reload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := s.Reload(); err != nil {
			log.Printf("central: reload warning: %v", err)
		}
		go s.col.refresh(r.Context())
		writeJSON(w, map[string]string{"status": "ok"})
	})

	// POST /mgmt/peer/add
	mux.HandleFunc("/mgmt/peer/add", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Name    string `json:"name"`
			Address string `json:"address"`
			Port    int    `json:"port"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Address == "" {
			http.Error(w, "address is required", http.StatusBadRequest)
			return
		}
		s.col.AddManualPeer(manualPeer{
			Name:    req.Name,
			Address: req.Address,
			Port:    req.Port,
		})
		writeJSON(w, map[string]string{"status": "ok"})
	})

	// POST /mgmt/peer/remove
	mux.HandleFunc("/mgmt/peer/remove", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Address string `json:"address"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !s.col.RemoveManualPeer(req.Address) {
			http.Error(w, "peer not found", http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	})

	// GET /mgmt/peer/list — manual peers only
	mux.HandleFunc("/mgmt/peer/list", func(w http.ResponseWriter, r *http.Request) {
		type peerItem struct {
			Name    string `json:"name"`
			Address string `json:"address"`
			Port    int    `json:"port"`
		}
		manual := s.col.ListManualPeers()
		items := make([]peerItem, len(manual))
		for i, mp := range manual {
			items[i] = peerItem{Name: mp.Name, Address: mp.Address, Port: mp.Port}
		}
		writeJSON(w, items)
	})

	return &http.Server{Handler: mux}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
