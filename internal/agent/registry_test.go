package agent

import (
	"fmt"
	"sync"
	"testing"

	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/protocol"
)

func TestRegistry_AddRemoveGet(t *testing.T) {
	r := newRegistry()

	entry := protocol.ServiceEntry{
		Name: "svc1",
		Type: protocol.ServiceTypeStatic,
		Target: protocol.SDTarget{
			Targets: []string{"host:9100"},
		},
	}
	if err := r.add(entry); err != nil {
		t.Fatalf("add: %v", err)
	}

	got, ok := r.get("svc1")
	if !ok {
		t.Fatal("get: not found")
	}
	if got.Name != "svc1" {
		t.Errorf("get: name = %q, want %q", got.Name, "svc1")
	}

	if err := r.remove("svc1"); err != nil {
		t.Fatalf("remove: %v", err)
	}

	_, ok = r.get("svc1")
	if ok {
		t.Fatal("get after remove: still found")
	}

	if err := r.remove("svc1"); err == nil {
		t.Fatal("remove non-existent: expected error, got nil")
	}
}

func TestRegistry_Add_Duplicate(t *testing.T) {
	r := newRegistry()
	entry := protocol.ServiceEntry{Name: "dup", Type: protocol.ServiceTypeStatic}
	if err := r.add(entry); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if err := r.add(entry); err == nil {
		t.Fatal("second add: expected error, got nil")
	}
}

func TestRegistry_List_Sorted(t *testing.T) {
	r := newRegistry()
	for _, name := range []string{"charlie", "alpha", "bravo"} {
		_ = r.add(protocol.ServiceEntry{Name: name, Type: protocol.ServiceTypeStatic})
	}
	list := r.list()
	if len(list) != 3 {
		t.Fatalf("list len = %d, want 3", len(list))
	}
	want := []string{"alpha", "bravo", "charlie"}
	for i, e := range list {
		if e.Name != want[i] {
			t.Errorf("list[%d].Name = %q, want %q", i, e.Name, want[i])
		}
	}
}

func TestRegistry_UpdateHealth(t *testing.T) {
	r := newRegistry()
	_ = r.add(protocol.ServiceEntry{Name: "svc", Type: protocol.ServiceTypeStatic})

	h := protocol.ServiceHealthStatus{Status: protocol.ServiceHealthHealthy, StatusCode: 200}
	r.updateHealth("svc", h)

	got, _ := r.get("svc")
	if got.Health == nil {
		t.Fatal("health is nil after updateHealth")
	}
	if got.Health.Status != protocol.ServiceHealthHealthy {
		t.Errorf("status = %q, want healthy", got.Health.Status)
	}

	// Updating unknown name is a no-op (should not panic).
	r.updateHealth("does-not-exist", h)
}

func TestRegistry_Concurrent(t *testing.T) {
	r := newRegistry()
	const n = 50
	var wg sync.WaitGroup

	// Add n services concurrently.
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.add(protocol.ServiceEntry{
				Name: fmt.Sprintf("svc%d", i),
				Type: protocol.ServiceTypeStatic,
			})
		}()
	}
	wg.Wait()

	// Concurrent list + remove.
	for i := 0; i < n; i++ {
		i := i
		wg.Add(2)
		go func() {
			defer wg.Done()
			r.list()
		}()
		go func() {
			defer wg.Done()
			_ = r.remove(fmt.Sprintf("svc%d", i))
		}()
	}
	wg.Wait()
}
