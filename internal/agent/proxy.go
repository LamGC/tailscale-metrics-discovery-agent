package agent

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

// proxyAuth holds authentication credentials used when Agent fetches the
// real target on behalf of Prometheus.
type proxyAuth struct {
	// authType is one of: "none", "bearer", "basic". Defaults to "none".
	authType string
	token    string
	username string
	password string
}

// proxy represents a single virtual scrape endpoint. When Prometheus hits
// /proxy/<name>/metrics, the Agent fetches the configured target and
// streams the response back.
type proxy struct {
	target string
	auth   proxyAuth
	client *http.Client
}

func newProxy(target string, auth proxyAuth) *proxy {
	return &proxy{
		target: target,
		auth:   auth,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// serveMetrics fetches the upstream target and proxies the response.
func (p *proxy) serveMetrics(w http.ResponseWriter, r *http.Request) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, p.target, nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("build upstream request: %v", err), http.StatusInternalServerError)
		return
	}

	switch p.auth.authType {
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+p.auth.token)
	case "basic":
		req.SetBasicAuth(p.auth.username, p.auth.password)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf("fetching upstream: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Forward Content-Type from upstream.
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		log.Printf("proxy: copy error for %s: %v", p.target, err)
	}
}

// --- proxy store ---

type proxyStore struct {
	mu      sync.RWMutex
	proxies map[string]*proxy
}

func newProxyStore() *proxyStore {
	return &proxyStore{proxies: make(map[string]*proxy)}
}

func (ps *proxyStore) add(name string, p *proxy) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if _, ok := ps.proxies[name]; ok {
		return fmt.Errorf("proxy %q already exists", name)
	}
	ps.proxies[name] = p
	return nil
}

func (ps *proxyStore) remove(name string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if _, ok := ps.proxies[name]; !ok {
		return fmt.Errorf("proxy %q not found", name)
	}
	delete(ps.proxies, name)
	return nil
}

func (ps *proxyStore) get(name string) (*proxy, bool) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	p, ok := ps.proxies[name]
	return p, ok
}
