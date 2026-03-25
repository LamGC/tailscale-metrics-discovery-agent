package agent

import (
	"fmt"
	"sync"

	"github.com/lamgc/tailscale-service-discovery-agent/internal/protocol"
)

// registry holds all registered services (static, bucket, proxy).
// It is safe for concurrent use.
type registry struct {
	mu      sync.RWMutex
	entries map[string]*protocol.ServiceEntry // keyed by name
}

func newRegistry() *registry {
	return &registry{
		entries: make(map[string]*protocol.ServiceEntry),
	}
}

func (r *registry) add(e protocol.ServiceEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[e.Name]; ok {
		return fmt.Errorf("service %q already exists", e.Name)
	}
	e2 := e
	r.entries[e.Name] = &e2
	return nil
}

func (r *registry) remove(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[name]; !ok {
		return fmt.Errorf("service %q not found", name)
	}
	delete(r.entries, name)
	return nil
}

func (r *registry) get(name string) (protocol.ServiceEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[name]
	if !ok {
		return protocol.ServiceEntry{}, false
	}
	return *e, true
}

func (r *registry) list() []protocol.ServiceEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]protocol.ServiceEntry, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, *e)
	}
	return out
}

// sdTargets converts all registry entries to Prometheus SDTarget slice.
func (r *registry) sdTargets() []protocol.SDTarget {
	entries := r.list()
	out := make([]protocol.SDTarget, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Target)
	}
	return out
}
