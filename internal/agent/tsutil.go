package agent

import (
	"context"
	"fmt"

	"tailscale.com/client/local"

	"github.com/lamgc/tailscale-service-discovery-agent/internal/protocol"
	"github.com/lamgc/tailscale-service-discovery-agent/internal/tsutil"
)

// detectSelfTailscaleIP returns the first IPv4 address assigned to this node
// by Tailscale. Returns an error if Tailscale is not running or the node has
// no addresses yet.
func detectSelfTailscaleIP() (string, error) {
	var lc local.Client
	st, err := lc.Status(context.Background())
	if err != nil {
		return "", fmt.Errorf("tailscale status: %w", err)
	}
	for _, addr := range st.TailscaleIPs {
		if addr.Is4() {
			return addr.String(), nil
		}
	}
	if len(st.TailscaleIPs) > 0 {
		return st.TailscaleIPs[0].String(), nil
	}
	return "", fmt.Errorf("no Tailscale IPs assigned")
}

// agentTailscaleStatus returns the current Tailscale daemon state for the agent node.
func agentTailscaleStatus(ctx context.Context) *protocol.TailscaleStatus {
	var lc local.Client
	return tsutil.QueryStatus(ctx, &lc)
}
