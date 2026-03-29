package central

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/config"
)

func newTestCentralServer() *Server {
	cfg := config.DefaultCentralConfig()
	return NewServer(cfg)
}

func TestMgmtPeerAdd_PortValidation(t *testing.T) {
	srv := newTestCentralServer()
	mgmt := newCentralMgmtServer(srv)

	tests := []struct {
		name       string
		port       int
		wantStatus int
	}{
		{"valid port 0", 0, http.StatusOK},
		{"valid port 9001", 9001, http.StatusOK},
		{"valid port 65535", 65535, http.StatusOK},
		{"negative port", -1, http.StatusBadRequest},
		{"port too large", 65536, http.StatusBadRequest},
		{"port way too large", 100000, http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{
				"name":    "test-peer",
				"address": "100.64.0.1",
				"port":    tt.port,
			})
			req := httptest.NewRequest(http.MethodPost, "/mgmt/peer/add", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			mgmt.Handler.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("port=%d: got status %d, want %d; body: %s",
					tt.port, w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
}

func TestMgmtPeerAdd_MissingAddress(t *testing.T) {
	srv := newTestCentralServer()
	mgmt := newCentralMgmtServer(srv)

	body, _ := json.Marshal(map[string]any{
		"name": "test-peer",
		"port": 9001,
	})
	req := httptest.NewRequest(http.MethodPost, "/mgmt/peer/add", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mgmt.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("missing address: got status %d, want %d", w.Code, http.StatusBadRequest)
	}
}
