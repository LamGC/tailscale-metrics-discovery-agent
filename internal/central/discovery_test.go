package central

import (
	"sync"
	"testing"
)

func TestDiscoverer_PortConcurrent(t *testing.T) {
	d := newDiscoverer("", []string{"tag:test"}, 9001)

	var wg sync.WaitGroup
	wg.Add(2)

	// Writer goroutine.
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			d.UpdateConfig([]string{"tag:test"}, 9000+i)
		}
	}()

	// Reader goroutine.
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			p := d.Port()
			if p < 0 {
				t.Errorf("Port() returned negative: %d", p)
			}
		}
	}()

	wg.Wait()
}

func TestTailscaleNodeNamePrefersDNSName(t *testing.T) {
	got := tailscaleNodeName("agent.tailnet.ts.net", "machine-host")
	if got != "agent.tailnet.ts.net" {
		t.Fatalf("node name = %q, want DNSName", got)
	}
	got = tailscaleNodeName("", "machine-host")
	if got != "machine-host" {
		t.Fatalf("fallback node name = %q, want machine hostname", got)
	}
}
