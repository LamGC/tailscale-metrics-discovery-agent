package agent

import (
	"fmt"
	"sync"
	"testing"
	"time"

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

func TestRegistry_SvcModifiedAt_OnAddRemove(t *testing.T) {
	r := newRegistry()
	t0 := r.SvcLastModified()

	time.Sleep(2 * time.Millisecond) // ensure clock advances
	_ = r.add(protocol.ServiceEntry{Name: "svc1", Type: protocol.ServiceTypeStatic})
	t1 := r.SvcLastModified()
	if !t1.After(t0) {
		t.Errorf("SvcLastModified did not advance after add: %v <= %v", t1, t0)
	}

	time.Sleep(2 * time.Millisecond)
	_ = r.remove("svc1")
	t2 := r.SvcLastModified()
	if !t2.After(t1) {
		t.Errorf("SvcLastModified did not advance after remove: %v <= %v", t2, t1)
	}
}

func TestRegistry_HealthModifiedAt_OnChange(t *testing.T) {
	r := newRegistry()
	_ = r.add(protocol.ServiceEntry{Name: "svc", Type: protocol.ServiceTypeStatic})

	t0 := r.HealthLastModified()

	// First health update: nil → healthy — should advance.
	time.Sleep(2 * time.Millisecond)
	r.updateHealth("svc", protocol.ServiceHealthStatus{
		Status:     protocol.ServiceHealthHealthy,
		StatusCode: 200,
	})
	t1 := r.HealthLastModified()
	if !t1.After(t0) {
		t.Errorf("HealthLastModified did not advance on first update: %v <= %v", t1, t0)
	}

	// Same Status/StatusCode/Message but different LastCheck — should NOT advance.
	time.Sleep(2 * time.Millisecond)
	now := time.Now()
	r.updateHealth("svc", protocol.ServiceHealthStatus{
		Status:     protocol.ServiceHealthHealthy,
		StatusCode: 200,
		LastCheck:  &now, // only LastCheck changed
	})
	t2 := r.HealthLastModified()
	if t2.After(t1) {
		t.Errorf("HealthLastModified advanced when only LastCheck changed: %v > %v", t2, t1)
	}

	// Status change: healthy → unhealthy — should advance.
	time.Sleep(2 * time.Millisecond)
	r.updateHealth("svc", protocol.ServiceHealthStatus{
		Status:  protocol.ServiceHealthUnhealthy,
		Message: "connection refused",
	})
	t3 := r.HealthLastModified()
	if !t3.After(t1) {
		t.Errorf("HealthLastModified did not advance on status change: %v <= %v", t3, t1)
	}
}

func TestRegistry_ListWithoutHealth(t *testing.T) {
	r := newRegistry()
	_ = r.add(protocol.ServiceEntry{Name: "svc1", Type: protocol.ServiceTypeStatic})
	r.updateHealth("svc1", protocol.ServiceHealthStatus{Status: protocol.ServiceHealthHealthy})

	// list() should include health.
	full := r.list()
	if full[0].Health == nil {
		t.Error("list() should include health")
	}

	// listWithoutHealth() should strip it.
	stripped := r.listWithoutHealth()
	if stripped[0].Health != nil {
		t.Error("listWithoutHealth() should have health=nil")
	}
}

func TestRegistry_ListHealth(t *testing.T) {
	r := newRegistry()
	_ = r.add(protocol.ServiceEntry{Name: "svc1", Type: protocol.ServiceTypeStatic})
	_ = r.add(protocol.ServiceEntry{Name: "svc2", Type: protocol.ServiceTypeStatic})

	// Only svc1 has health.
	r.updateHealth("svc1", protocol.ServiceHealthStatus{Status: protocol.ServiceHealthHealthy, StatusCode: 200})

	hm := r.listHealth()
	if len(hm) != 1 {
		t.Fatalf("listHealth len = %d, want 1", len(hm))
	}
	if hm["svc1"] == nil || hm["svc1"].Status != protocol.ServiceHealthHealthy {
		t.Errorf("svc1 health = %v, want healthy", hm["svc1"])
	}
	if hm["svc2"] != nil {
		t.Error("svc2 should not be in health map (no health check)")
	}
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
