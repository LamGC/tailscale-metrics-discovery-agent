package agent

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/config"
)

func TestMgmtServiceAddRollsBackConfigOnRuntimeConflict(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "agent.toml")
	initialCfg := config.AgentConfig{
		Statics: []config.StaticService{
			{
				Name:    "svc",
				Targets: []string{"host:9100"},
			},
		},
	}
	if err := config.SaveAgentConfig(cfgPath, initialCfg); err != nil {
		t.Fatalf("save initial config: %v", err)
	}

	s := newTestServer(initialCfg)
	s.cfgFile = cfgPath
	if err := s.addStatic("svc", []string{"host:9100"}, nil, nil); err != nil {
		t.Fatalf("preload runtime state: %v", err)
	}

	mgmt := newMgmtServer(s)
	body := `{"name":"svc","targets":["dup:9100"]}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mgmt/service/add", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mgmt.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusConflict, w.Body.String())
	}

	got, err := config.LoadAgentConfig(cfgPath)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if len(got.Statics) != 1 {
		t.Fatalf("statics count = %d, want 1", len(got.Statics))
	}
	if got.Statics[0].Name != "svc" || got.Statics[0].Targets[0] != "host:9100" {
		t.Fatalf("unexpected config after rollback: %+v", got.Statics)
	}
}

func TestMgmtServiceRemoveRollsBackConfigWhenRuntimeNotFound(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "agent.toml")
	initialCfg := config.AgentConfig{
		Statics: []config.StaticService{
			{
				Name:    "svc",
				Targets: []string{"host:9100"},
			},
		},
	}
	if err := config.SaveAgentConfig(cfgPath, initialCfg); err != nil {
		t.Fatalf("save initial config: %v", err)
	}

	s := newTestServer(initialCfg)
	s.cfgFile = cfgPath
	mgmt := newMgmtServer(s)
	body := `{"name":"svc"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mgmt/service/remove", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mgmt.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusNotFound, w.Body.String())
	}
	got, err := config.LoadAgentConfig(cfgPath)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if len(got.Statics) != 1 {
		t.Fatalf("statics count = %d, want 1", len(got.Statics))
	}
}

func TestMgmtServiceAddSerializesConcurrentConfigUpdates(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "agent.toml")
	if err := config.SaveAgentConfig(cfgPath, config.AgentConfig{}); err != nil {
		t.Fatalf("save initial config: %v", err)
	}

	s := newTestServer(config.AgentConfig{})
	s.cfgFile = cfgPath
	mgmt := newMgmtServer(s)

	const count = 16
	var wg sync.WaitGroup
	errCh := make(chan string, count)
	for i := 0; i < count; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("svc-%02d", i)
			body := fmt.Sprintf(`{"name":%q,"targets":["host-%02d:9100"]}`, name, i)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/mgmt/service/add", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			mgmt.Handler.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				errCh <- fmt.Sprintf("%s status = %d; body: %s", name, w.Code, w.Body.String())
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Error(msg)
	}

	got, err := config.LoadAgentConfig(cfgPath)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if len(got.Statics) != count {
		t.Fatalf("statics count = %d, want %d; statics=%+v", len(got.Statics), count, got.Statics)
	}
}
