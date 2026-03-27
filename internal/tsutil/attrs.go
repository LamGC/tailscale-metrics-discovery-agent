package tsutil

import (
	"context"
	"strconv"
	"strings"

	"tailscale.com/client/local"
	"tailscale.com/tailcfg"
)

const (
	// CapPrefixAgentTag is the nodeAttrs capability prefix for agent ACL tags.
	// Central reads these from its own CapMap to know which agent tags to discover.
	CapPrefixAgentTag = "custom:tsd-agent-tag="

	// CapPrefixAgentPort is the nodeAttrs capability prefix for the agent port.
	CapPrefixAgentPort = "custom:tsd-agent-port="

	// CapPrefixCentralTag is the nodeAttrs capability prefix for central ACL tags.
	// Agent reads these from its own CapMap to know which central tags are authorized.
	CapPrefixCentralTag = "custom:tsd-central-tag="
)

// TSDNodeAttrs holds tsd-specific configuration parsed from Tailscale nodeAttrs.
// A nil value from ReadSelfNodeAttrs indicates no valid nodeAttrs were found.
type TSDNodeAttrs struct {
	AgentTags   []string // Central: which agent ACL tags to discover
	CentralTags []string // Agent: which central ACL tags are authorized
	AgentPort   int      // Agent listen port; 0 = not set
}

// ReadSelfNodeAttrs reads this node's Tailscale CapMap and parses tsd-specific
// capabilities. It returns nil (not an error) when the nodeAttrs are absent or
// incomplete — callers should fall back to local config in that case.
//
// Validity rules:
//   - For Central use: must have AgentPort AND ≥1 AgentTag
//   - For Agent use: must have AgentPort AND ≥1 CentralTag
//
// If neither Central nor Agent validity is satisfied, nil is returned.
func ReadSelfNodeAttrs(ctx context.Context, lc *local.Client) (*TSDNodeAttrs, error) {
	st, err := lc.Status(ctx)
	if err != nil {
		return nil, err
	}
	if st.Self == nil {
		return nil, nil
	}
	return ParseCapMap(st.Self.CapMap), nil
}

// ParseCapMap extracts TSDNodeAttrs from a NodeCapMap. Exported for testing.
// Returns nil if neither Central nor Agent validity rules are satisfied.
func ParseCapMap(cm tailcfg.NodeCapMap) *TSDNodeAttrs {
	if cm == nil {
		return nil
	}

	var attrs TSDNodeAttrs
	for cap := range cm {
		s := string(cap)
		if tag, ok := strings.CutPrefix(s, CapPrefixAgentTag); ok && tag != "" {
			attrs.AgentTags = append(attrs.AgentTags, tag)
		}
		if tag, ok := strings.CutPrefix(s, CapPrefixCentralTag); ok && tag != "" {
			attrs.CentralTags = append(attrs.CentralTags, tag)
		}
		if portStr, ok := strings.CutPrefix(s, CapPrefixAgentPort); ok {
			if p, err := strconv.Atoi(portStr); err == nil && p > 0 && p <= 65535 {
				attrs.AgentPort = p
			}
		}
	}

	// Validity: Central needs port + ≥1 agent tag, Agent needs port + ≥1 central tag.
	centralValid := attrs.AgentPort > 0 && len(attrs.AgentTags) > 0
	agentValid := attrs.AgentPort > 0 && len(attrs.CentralTags) > 0

	if !centralValid && !agentValid {
		return nil
	}
	return &attrs
}
