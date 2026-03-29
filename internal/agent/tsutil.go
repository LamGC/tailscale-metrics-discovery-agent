package agent

import (
	"context"
	"fmt"

	"tailscale.com/client/local"

	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/protocol"
	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/tsutil"
)

// detectSelfTailscaleIPs returns the IPv4 and IPv6 addresses assigned to this
// node by Tailscale. Either may be empty if not assigned.
// Returns an error if Tailscale is not running or the node has no addresses.
func detectSelfTailscaleIPs() (ipv4, ipv6 string, err error) {
	var lc local.Client
	st, err := lc.Status(context.Background())
	if err != nil {
		return "", "", fmt.Errorf("tailscale status: %w", err)
	}
	for _, addr := range st.TailscaleIPs {
		if addr.Is4() && ipv4 == "" {
			ipv4 = addr.String()
		}
		if addr.Is6() && ipv6 == "" {
			ipv6 = addr.String()
		}
	}
	if ipv4 == "" && ipv6 == "" {
		return "", "", fmt.Errorf("no Tailscale IPs assigned")
	}
	return ipv4, ipv6, nil
}

// agentTailscaleStatus returns the current Tailscale daemon state for the agent node.
func agentTailscaleStatus(ctx context.Context) *protocol.TailscaleStatus {
	var lc local.Client
	return tsutil.QueryStatus(ctx, &lc)
}
