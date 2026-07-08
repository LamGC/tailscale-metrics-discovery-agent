package central

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/config"
)

func TestMgmtPeerRemoveRollsBackConfigWhenRuntimeNotFound(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "central.toml")
	initialCfg := config.CentralConfig{
		ManualPeers: []config.ManualPeer{
			{
				Name:    "test-peer",
				Address: "100.64.0.1",
				Port:    9001,
			},
		},
	}
	if err := config.SaveCentralConfig(cfgPath, initialCfg); err != nil {
		t.Fatalf("save initial config: %v", err)
	}

	srv := NewServer(initialCfg)
	srv.cfgFile = cfgPath
	if !srv.col.RemoveManualPeer("100.64.0.1") {
		t.Fatal("preload runtime state: expected manual peer")
	}

	mgmt := newCentralMgmtServer(srv)
	body, _ := json.Marshal(map[string]any{
		"address": "100.64.0.1",
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mgmt/peer/remove", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	mgmt.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusNotFound, w.Body.String())
	}
	got, err := config.LoadCentralConfig(cfgPath)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if len(got.ManualPeers) != 1 {
		t.Fatalf("manual peers = %d, want 1", len(got.ManualPeers))
	}
}
