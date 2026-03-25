package tsutil

import (
	"context"

	"tailscale.com/client/local"

	"github.com/lamgc/tailscale-service-discovery-agent/internal/protocol"
)

// QueryStatus returns the Tailscale daemon state by querying the local API.
// If Tailscale is unreachable the returned status has Connected=false and
// an Error field set; the error itself is not propagated so callers do not
// need special-case handling.
func QueryStatus(ctx context.Context, lc *local.Client) *protocol.TailscaleStatus {
	st, err := lc.Status(ctx)
	if err != nil {
		return &protocol.TailscaleStatus{
			Connected:    false,
			BackendState: "unreachable",
			Error:        err.Error(),
		}
	}
	ts := &protocol.TailscaleStatus{
		Connected:    st.BackendState == "Running",
		BackendState: st.BackendState,
	}
	if st.Self != nil {
		ts.Hostname = st.Self.HostName
		for _, ip := range st.Self.TailscaleIPs {
			ts.TailscaleIPs = append(ts.TailscaleIPs, ip.String())
		}
		if st.Self.Tags != nil {
			ts.Tags = st.Self.Tags.AsSlice()
		}
		if profile, ok := st.User[st.Self.UserID]; ok {
			ts.Account = profile.LoginName
		}
	}
	return ts
}
