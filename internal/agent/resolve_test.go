package agent

import (
	"testing"

	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/protocol"
)

func TestResolveTarget_NoVars(t *testing.T) {
	rc := &resolveContext{tsIPv4: "100.64.0.1"}
	got, ok := resolveTarget("host:9100", rc)
	if !ok || got != "host:9100" {
		t.Errorf("resolveTarget(plain) = %q, %v; want %q, true", got, ok, "host:9100")
	}
}

func TestResolveTarget_TsIP(t *testing.T) {
	rc := &resolveContext{tsIPv4: "100.64.0.1", tsIPv6: "fd7a::1"}

	got, ok := resolveTarget("{ts.ip}:9100", rc)
	if !ok || got != "100.64.0.1:9100" {
		t.Errorf("resolveTarget({ts.ip}) = %q, %v; want %q, true", got, ok, "100.64.0.1:9100")
	}

	got, ok = resolveTarget("[{ts.ipv6}]:9100", rc)
	if !ok || got != "[fd7a::1]:9100" {
		t.Errorf("resolveTarget({ts.ipv6}) = %q, %v; want %q, true", got, ok, "[fd7a::1]:9100")
	}
}

func TestResolveTarget_TsIP_Missing(t *testing.T) {
	rc := &resolveContext{} // no IPs

	_, ok := resolveTarget("{ts.ip}:9100", rc)
	if ok {
		t.Error("resolveTarget({ts.ip}) should fail when no IPv4")
	}

	_, ok = resolveTarget("{ts.ipv6}:9100", rc)
	if ok {
		t.Error("resolveTarget({ts.ipv6}) should fail when no IPv6")
	}
}

func TestResolveTarget_Self(t *testing.T) {
	rc := &resolveContext{selfAddr: "100.64.0.1:9001"}

	got, ok := resolveTarget("{self}/bucket/mybucket/metrics", rc)
	if !ok || got != "100.64.0.1:9001/bucket/mybucket/metrics" {
		t.Errorf("resolveTarget({self}) = %q, %v; want %q, true", got, ok, "100.64.0.1:9001/bucket/mybucket/metrics")
	}
}

func TestResolveTarget_Self_Missing(t *testing.T) {
	rc := &resolveContext{} // no selfAddr
	_, ok := resolveTarget("{self}/bucket/x/metrics", rc)
	if ok {
		t.Error("resolveTarget({self}) should fail when selfAddr is empty")
	}
}

func TestResolveTarget_InterfaceNotFound(t *testing.T) {
	rc := &resolveContext{}
	_, ok := resolveTarget("{if.link.ip:nonexistent_iface_12345}:9100", rc)
	if ok {
		t.Error("resolveTarget with nonexistent interface should fail")
	}
}

func TestResolveTarget_LoopbackInterface(t *testing.T) {
	rc := &resolveContext{}
	got, ok := resolveTarget("{if.link.ip:lo}:9100", rc)
	if !ok {
		t.Skip("lo interface not available on this platform")
	}
	if got != "127.0.0.1:9100" {
		t.Errorf("resolveTarget({if.link.ip:lo}) = %q, want %q", got, "127.0.0.1:9100")
	}
}

func TestResolveTarget_UnknownVar(t *testing.T) {
	rc := &resolveContext{tsIPv4: "100.64.0.1"}
	_, ok := resolveTarget("{unknown.var}:9100", rc)
	if ok {
		t.Error("resolveTarget with unknown variable should fail")
	}
}

func TestResolveTarget_MultipleVars(t *testing.T) {
	rc := &resolveContext{tsIPv4: "100.64.0.1", tsIPv6: "fd7a::1"}
	got, ok := resolveTarget("{ts.ip}:{ts.ip}", rc)
	if !ok || got != "100.64.0.1:100.64.0.1" {
		t.Errorf("resolveTarget(multi) = %q, %v; want %q, true", got, ok, "100.64.0.1:100.64.0.1")
	}
}

func TestResolveTarget_MixedVarAndLiteral(t *testing.T) {
	rc := &resolveContext{tsIPv4: "100.64.0.1"}
	got, ok := resolveTarget("http://{ts.ip}:9100/metrics", rc)
	if !ok || got != "http://100.64.0.1:9100/metrics" {
		t.Errorf("resolveTarget(mixed) = %q, %v; want %q, true", got, ok, "http://100.64.0.1:9100/metrics")
	}
}

func TestResolveTarget_UnclosedBrace(t *testing.T) {
	rc := &resolveContext{tsIPv4: "100.64.0.1"}
	// Unclosed brace should be treated as literal.
	got, ok := resolveTarget("host{9100", rc)
	if !ok || got != "host{9100" {
		t.Errorf("resolveTarget(unclosed) = %q, %v; want %q, true", got, ok, "host{9100")
	}
}

func TestResolveTarget_PartialFailure(t *testing.T) {
	// One var succeeds, the other fails → whole target fails.
	rc := &resolveContext{tsIPv4: "100.64.0.1"} // no tsIPv6
	_, ok := resolveTarget("{ts.ip}:{ts.ipv6}", rc)
	if ok {
		t.Error("resolveTarget should fail when any variable fails")
	}
}

func TestSelfAddrForRequest_IPv4(t *testing.T) {
	got := selfAddrForRequest("100.64.0.5:12345", "100.64.0.1", "fd7a::1", "9001")
	if got != "100.64.0.1:9001" {
		t.Errorf("selfAddrForRequest(v4) = %q, want %q", got, "100.64.0.1:9001")
	}
}

func TestSelfAddrForRequest_IPv6(t *testing.T) {
	got := selfAddrForRequest("[fd7a::5]:12345", "100.64.0.1", "fd7a::1", "9001")
	if got != "[fd7a::1]:9001" {
		t.Errorf("selfAddrForRequest(v6) = %q, want %q", got, "[fd7a::1]:9001")
	}
}

func TestSelfAddrForRequest_IPv6Only(t *testing.T) {
	got := selfAddrForRequest("[fd7a::5]:12345", "", "fd7a::1", "9001")
	if got != "[fd7a::1]:9001" {
		t.Errorf("selfAddrForRequest(v6only) = %q, want %q", got, "[fd7a::1]:9001")
	}
}

func TestSelfAddrForRequest_IPv4Fallback(t *testing.T) {
	// Requester is v6 but we only have v4.
	got := selfAddrForRequest("[fd7a::5]:12345", "100.64.0.1", "", "9001")
	if got != "100.64.0.1:9001" {
		t.Errorf("selfAddrForRequest(v4fallback) = %q, want %q", got, "100.64.0.1:9001")
	}
}

func TestSelfAddrForRequest_NoIPs(t *testing.T) {
	got := selfAddrForRequest("1.2.3.4:5", "", "", "9001")
	if got != "" {
		t.Errorf("selfAddrForRequest(noIPs) = %q, want empty", got)
	}
}

func TestResolveEntry_AllTargetsResolved(t *testing.T) {
	rc := &resolveContext{tsIPv4: "100.64.0.1"}
	e := protocol.ServiceEntry{
		Name: "svc",
		Target: protocol.SDTarget{
			Targets: []string{"{ts.ip}:9100", "literal:9200"},
		},
	}
	got, ok := resolveEntry(e, rc)
	if !ok {
		t.Fatal("resolveEntry should succeed")
	}
	if len(got.Target.Targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(got.Target.Targets))
	}
	if got.Target.Targets[0] != "100.64.0.1:9100" {
		t.Errorf("target[0] = %q, want %q", got.Target.Targets[0], "100.64.0.1:9100")
	}
}

func TestResolveEntry_PartialTargetsResolved(t *testing.T) {
	rc := &resolveContext{tsIPv4: "100.64.0.1"} // no tsIPv6
	e := protocol.ServiceEntry{
		Name: "svc",
		Target: protocol.SDTarget{
			Targets: []string{"{ts.ip}:9100", "{ts.ipv6}:9200"},
		},
	}
	got, ok := resolveEntry(e, rc)
	if !ok {
		t.Fatal("resolveEntry should succeed when at least one target resolves")
	}
	if len(got.Target.Targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(got.Target.Targets))
	}
	if got.Target.Targets[0] != "100.64.0.1:9100" {
		t.Errorf("target[0] = %q, want %q", got.Target.Targets[0], "100.64.0.1:9100")
	}
}

func TestResolveEntry_AllTargetsFail(t *testing.T) {
	rc := &resolveContext{} // no IPs at all
	e := protocol.ServiceEntry{
		Name: "svc",
		Target: protocol.SDTarget{
			Targets: []string{"{ts.ip}:9100"},
		},
	}
	_, ok := resolveEntry(e, rc)
	if ok {
		t.Error("resolveEntry should fail when all targets fail to resolve")
	}
}
