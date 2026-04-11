package agent

import (
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/protocol"
)

// registry holds all registered services (static, bucket, proxy).
// It is safe for concurrent use.
type registry struct {
	mu               sync.RWMutex
	entries          map[string]*protocol.ServiceEntry // keyed by name
	svcModifiedAt    time.Time                         // updated on add/remove
	healthModifiedAt time.Time                         // updated when health actually changes
}

func newRegistry() *registry {
	now := time.Now()
	return &registry{
		entries:          make(map[string]*protocol.ServiceEntry),
		svcModifiedAt:    now,
		healthModifiedAt: now,
	}
}

func (r *registry) add(e protocol.ServiceEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[e.Name]; ok {
		return fmt.Errorf("service %q already exists", e.Name)
	}
	r.entries[e.Name] = new(e)
	r.svcModifiedAt = time.Now()
	return nil
}

func (r *registry) remove(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[name]; !ok {
		return fmt.Errorf("service %q not found", name)
	}
	delete(r.entries, name)
	r.svcModifiedAt = time.Now()
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
	return r.sortedEntries(true)
}

// listWithoutHealth returns all entries with Health set to nil.
// Used by the /api/v1/services endpoint.
func (r *registry) listWithoutHealth() []protocol.ServiceEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sortedEntries(false)
}

// sortedEntries returns a sorted copy of entries. Must be called with r.mu held.
func (r *registry) sortedEntries(includeHealth bool) []protocol.ServiceEntry {
	out := make([]protocol.ServiceEntry, 0, len(r.entries))
	for _, e := range r.entries {
		entry := *e
		if !includeHealth {
			entry.Health = nil
		}
		out = append(out, entry)
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

// listHealth returns a map of service name to current health status.
// Services without a health check are omitted.
func (r *registry) listHealth() map[string]*protocol.ServiceHealthStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]*protocol.ServiceHealthStatus, len(r.entries))
	for name, e := range r.entries {
		if e.Health != nil {
			out[name] = new(*e.Health)
		}
	}
	return out
}

// SvcLastModified returns the time the service list was last structurally changed.
func (r *registry) SvcLastModified() time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.svcModifiedAt
}

// HealthLastModified returns the time a health status last actually changed.
func (r *registry) HealthLastModified() time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.healthModifiedAt
}

// updateHealth updates the Health field of a registry entry in-place.
// Only updates healthModifiedAt when the status actually changes
// (Status, StatusCode, or Message differs). Does nothing if name is not found.
func (r *registry) updateHealth(name string, h protocol.ServiceHealthStatus) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[name]
	if !ok {
		return
	}
	changed := e.Health == nil ||
		e.Health.Status != h.Status ||
		e.Health.StatusCode != h.StatusCode ||
		e.Health.Message != h.Message
	e.Health = &h
	if changed {
		r.healthModifiedAt = time.Now()
	}
}
