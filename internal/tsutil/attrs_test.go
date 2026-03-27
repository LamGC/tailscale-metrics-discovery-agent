package tsutil

import (
	"testing"

	"tailscale.com/tailcfg"
)

func TestParseCapMap_CentralValid(t *testing.T) {
	cm := tailcfg.NodeCapMap{
		"custom:tsd-agent-tag=tag:prometheus-agent": nil,
		"custom:tsd-agent-tag=tag:metrics-agent":    nil,
		"custom:tsd-agent-port=9001":                nil,
	}
	attrs := ParseCapMap(cm)
	if attrs == nil {
		t.Fatal("expected non-nil attrs for valid Central config")
	}
	if attrs.AgentPort != 9001 {
		t.Errorf("AgentPort = %d, want 9001", attrs.AgentPort)
	}
	if len(attrs.AgentTags) != 2 {
		t.Errorf("AgentTags len = %d, want 2", len(attrs.AgentTags))
	}
}

func TestParseCapMap_AgentValid(t *testing.T) {
	cm := tailcfg.NodeCapMap{
		"custom:tsd-central-tag=tag:prometheus-central": nil,
		"custom:tsd-agent-port=9001":                    nil,
	}
	attrs := ParseCapMap(cm)
	if attrs == nil {
		t.Fatal("expected non-nil attrs for valid Agent config")
	}
	if len(attrs.CentralTags) != 1 || attrs.CentralTags[0] != "tag:prometheus-central" {
		t.Errorf("CentralTags = %v", attrs.CentralTags)
	}
	if attrs.AgentPort != 9001 {
		t.Errorf("AgentPort = %d, want 9001", attrs.AgentPort)
	}
}

func TestParseCapMap_BothValid(t *testing.T) {
	// A node with both Central and Agent attrs (unusual but allowed).
	cm := tailcfg.NodeCapMap{
		"custom:tsd-agent-tag=tag:prometheus-agent":     nil,
		"custom:tsd-central-tag=tag:prometheus-central": nil,
		"custom:tsd-agent-port=9001":                    nil,
	}
	attrs := ParseCapMap(cm)
	if attrs == nil {
		t.Fatal("expected non-nil attrs")
	}
	if len(attrs.AgentTags) != 1 {
		t.Errorf("AgentTags len = %d, want 1", len(attrs.AgentTags))
	}
	if len(attrs.CentralTags) != 1 {
		t.Errorf("CentralTags len = %d, want 1", len(attrs.CentralTags))
	}
}

func TestParseCapMap_MissingPort(t *testing.T) {
	// Has agent-tag but no port → invalid.
	cm := tailcfg.NodeCapMap{
		"custom:tsd-agent-tag=tag:prometheus-agent": nil,
	}
	attrs := ParseCapMap(cm)
	if attrs != nil {
		t.Error("expected nil when port is missing")
	}
}

func TestParseCapMap_MissingTags(t *testing.T) {
	// Has port but no tags → invalid.
	cm := tailcfg.NodeCapMap{
		"custom:tsd-agent-port=9001": nil,
	}
	attrs := ParseCapMap(cm)
	if attrs != nil {
		t.Error("expected nil when no tags are present")
	}
}

func TestParseCapMap_NilMap(t *testing.T) {
	attrs := ParseCapMap(nil)
	if attrs != nil {
		t.Error("expected nil for nil CapMap")
	}
}

func TestParseCapMap_EmptyMap(t *testing.T) {
	attrs := ParseCapMap(tailcfg.NodeCapMap{})
	if attrs != nil {
		t.Error("expected nil for empty CapMap")
	}
}

func TestParseCapMap_InvalidPort(t *testing.T) {
	cm := tailcfg.NodeCapMap{
		"custom:tsd-agent-tag=tag:test":  nil,
		"custom:tsd-agent-port=notaport": nil,
	}
	attrs := ParseCapMap(cm)
	if attrs != nil {
		t.Error("expected nil for invalid port")
	}
}

func TestParseCapMap_PortOutOfRange(t *testing.T) {
	cm := tailcfg.NodeCapMap{
		"custom:tsd-agent-tag=tag:test":  nil,
		"custom:tsd-agent-port=99999":    nil,
	}
	attrs := ParseCapMap(cm)
	if attrs != nil {
		t.Error("expected nil for port out of range")
	}
}

func TestParseCapMap_EmptyTagValue(t *testing.T) {
	// "custom:tsd-agent-tag=" (empty value after =) should be ignored.
	cm := tailcfg.NodeCapMap{
		"custom:tsd-agent-tag=":          nil,
		"custom:tsd-agent-port=9001":     nil,
	}
	attrs := ParseCapMap(cm)
	if attrs != nil {
		t.Error("expected nil when tag value is empty")
	}
}

func TestParseCapMap_UnrelatedCaps(t *testing.T) {
	// Non-tsd capabilities should be ignored.
	cm := tailcfg.NodeCapMap{
		"custom:tsd-agent-tag=tag:test": nil,
		"custom:tsd-agent-port=9001":    nil,
		"funnel":                         nil,
		"https://tailscale.com/cap/ssh":  nil,
	}
	attrs := ParseCapMap(cm)
	if attrs == nil {
		t.Fatal("expected non-nil attrs")
	}
	if len(attrs.AgentTags) != 1 {
		t.Errorf("AgentTags len = %d, want 1", len(attrs.AgentTags))
	}
}
