package agent

import (
	"fmt"
	"slices"
	"sync"

	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/protocol"
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
	slices.SortFunc(out, func(a, b protocol.ServiceEntry) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	return out
}

// updateHealth updates the Health field of a registry entry in-place.
// Does nothing if name is not found.
func (r *registry) updateHealth(name string, h protocol.ServiceHealthStatus) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[name]; ok {
		e.Health = &h
	}
}
