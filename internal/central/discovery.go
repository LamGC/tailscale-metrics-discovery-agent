package central

import (
	"context"
	"fmt"
	"log"
	"net/netip"
	"sync"

	"tailscale.com/client/local"
	"tailscale.com/ipn"

	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/protocol"
	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/tsutil"
)

// discoverer queries the local Tailscale daemon and returns peers that carry
// at least one of the configured ACL tags.
type discoverer struct {
	lc local.Client

	mu   sync.RWMutex
	tags map[string]struct{}
	port int

	// selfAttrs holds nodeAttrs read from this node's CapMap.
	// nil means nodeAttrs are not configured or not valid.
	selfAttrsMu sync.RWMutex
	selfAttrs   *tsutil.TSDNodeAttrs
}

func newDiscoverer(socketPath string, tags []string, agentPort int) *discoverer {
	d := &discoverer{port: agentPort}
	d.tags = toTagSet(tags)
	if socketPath != "" {
		d.lc.Socket = socketPath
	}
	return d
}

// UpdateConfig replaces the tag filter and agent port used for future
// Discover calls. Safe for concurrent use.
func (d *discoverer) UpdateConfig(tags []string, port int) {
	d.mu.Lock()
	d.tags = toTagSet(tags)
	d.port = port
	d.mu.Unlock()
}

// Port returns the currently configured agent port. Safe for concurrent use.
func (d *discoverer) Port() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.port
}

func toTagSet(tags []string) map[string]struct{} {
	s := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		s[t] = struct{}{}
	}
	return s
}

// RefreshSelfAttrs reads this node's Tailscale nodeAttrs and caches the result.
// On error, the previous cached value is retained (not cleared).
func (d *discoverer) RefreshSelfAttrs(ctx context.Context) {
	attrs, err := tsutil.ReadSelfNodeAttrs(ctx, &d.lc)
	if err != nil {
		log.Printf("central: failed to read nodeAttrs: %v (retaining previous)", err)
		return
	}
	d.selfAttrsMu.Lock()
	d.selfAttrs = attrs
	d.selfAttrsMu.Unlock()
	if attrs != nil {
		log.Printf("central: nodeAttrs: agent tags=%v, port=%d", attrs.AgentTags, attrs.AgentPort)
	}
}

// ClearSelfAttrs removes cached nodeAttrs (used when node_attrs is disabled).
func (d *discoverer) ClearSelfAttrs() {
	d.selfAttrsMu.Lock()
	d.selfAttrs = nil
	d.selfAttrsMu.Unlock()
}

// TailscaleStatus returns the current Tailscale daemon state for this node.
func (d *discoverer) TailscaleStatus(ctx context.Context) *protocol.TailscaleStatus {
	return tsutil.QueryStatus(ctx, &d.lc)
}

// Discover returns all online peers that have at least one matching tag.
// Offline peers (Tailscale node not online) are returned with
// AgentHealth = AgentHealthOffline and no AgentURL.
//
// If valid nodeAttrs are cached, they override the config-based tags and port.
// PeerInfo.Tags contains only the matched tags (intersection of node tags and
// the discovery tag set), not all of the node's tags.
func (d *discoverer) Discover(ctx context.Context) ([]protocol.PeerInfo, error) {
	// Snapshot config under read-lock to avoid holding the lock during I/O.
	d.mu.RLock()
	tags := d.tags
	port := d.port
	d.mu.RUnlock()

	// Override with nodeAttrs if available.
	d.selfAttrsMu.RLock()
	attrs := d.selfAttrs
	d.selfAttrsMu.RUnlock()
	if attrs != nil {
		if len(attrs.AgentTags) > 0 {
			tags = toTagSet(attrs.AgentTags)
		}
		if attrs.AgentPort > 0 {
			port = attrs.AgentPort
		}
	}

	st, err := d.lc.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("tailscale status: %w", err)
	}

	var peers []protocol.PeerInfo
	for _, peer := range st.Peer {
		matched := matchedTagSlice(peer.Tags, tags)
		if len(matched) == 0 {
			continue
		}
		tsIP := pickIP(peer.TailscaleIPs)
		if tsIP == "" {
			continue
		}
		info := protocol.PeerInfo{
			Hostname:    peer.HostName,
			TailscaleIP: tsIP,
			Tags:        matched,
			Source:      protocol.PeerSourceAuto,
		}
		if peer.Online {
			info.AgentURL = fmt.Sprintf("http://%s:%d", tsIP, port)
			info.Health = protocol.AgentHealthUnknown
		} else {
			info.Health = protocol.AgentHealthOffline
		}
		peers = append(peers, info)
	}
	return peers, nil
}

// Watch listens on the Tailscale IPN bus and calls onchange whenever the
// network map changes (peer comes online, goes offline, etc.).
// onConnect is called once immediately after WatchIPNBus connects (may be nil).
// It blocks until ctx is cancelled or a fatal error occurs, then returns
// so the caller can retry.
// Returns true if WatchIPNBus connected successfully (even if it later
// disconnected), false if it could not connect at all.
func (d *discoverer) Watch(ctx context.Context, onConnect func(), onchange func()) bool {
	// NotifyInitialNetMap: receive current netmap on connect.
	// NotifyRateLimit: avoid flooding on rapid sequential changes.
	const mask = ipn.NotifyInitialNetMap | ipn.NotifyRateLimit
	watcher, err := d.lc.WatchIPNBus(ctx, mask)
	if err != nil {
		log.Printf("central: WatchIPNBus unavailable (%v); relying on periodic polling", err)
		return false
	}
	defer watcher.Close()
	if onConnect != nil {
		onConnect()
	}

	for {
		n, err := watcher.Next()
		if err != nil {
			if ctx.Err() != nil {
				return true // normal shutdown
			}
			log.Printf("central: WatchIPNBus error: %v", err)
			return true
		}
		// NetMap is populated on initial connect and whenever peers change.
		if n.NetMap != nil {
			onchange()
		}
	}
}

func matchesTags(tags interface{ AsSlice() []string }, set map[string]struct{}) bool {
	if tags == nil {
		return false
	}
	for _, t := range tags.AsSlice() {
		if _, ok := set[t]; ok {
			return true
		}
	}
	return false
}

// matchedTagSlice returns the intersection of a node's tags and the discovery
// tag set. Returns nil if there are no matches.
func matchedTagSlice(tags interface{ AsSlice() []string }, set map[string]struct{}) []string {
	if tags == nil {
		return nil
	}
	var matched []string
	for _, t := range tags.AsSlice() {
		if _, ok := set[t]; ok {
			matched = append(matched, t)
		}
	}
	return matched
}

// pickIP returns the first IPv4 address, or the first address of any family.
func pickIP(addrs []netip.Addr) string {
	for _, a := range addrs {
		if a.Is4() {
			return a.String()
		}
	}
	if len(addrs) > 0 {
		return addrs[0].String()
	}
	return ""
}
