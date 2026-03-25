package central

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/lamgc/tailscale-service-discovery-agent/internal/config"
	"github.com/lamgc/tailscale-service-discovery-agent/internal/protocol"
)

// manualPeer is a peer configured explicitly by the operator.
type manualPeer struct {
	Name       string
	Address    string
	Port       int  // 0 = use discoverer.port
	fromConfig bool // true when loaded from config file (not via CLI)
}

// collector periodically queries each discovered Agent and aggregates the
// Prometheus SDTargets into a single list.
type collector struct {
	discoverer *discoverer
	agentToken string
	httpClient *http.Client

	mu      sync.RWMutex
	peers   []protocol.PeerInfo // full peer list with health status
	targets []protocol.SDTarget // aggregated from healthy peers only
	tsDown  bool                // true when the last Discover() call failed

	manualMu    sync.RWMutex
	manualPeers map[string]manualPeer // keyed by Tailscale IP / address
}

func newCollector(d *discoverer, agentToken string) *collector {
	return &collector{
		discoverer:  d,
		agentToken:  agentToken,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
		manualPeers: make(map[string]manualPeer),
	}
}

// Run starts the background refresh loop and the WatchIPNBus listener.
// It blocks until ctx is cancelled.
func (c *collector) Run(ctx context.Context, interval time.Duration) {
	triggerCh := make(chan struct{}, 1)

	// WatchIPNBus goroutine; auto-restarts on disconnect.
	go func() {
		lastFailed := false
		for ctx.Err() == nil {
			connected := c.discoverer.Watch(ctx,
				func() { // onConnect: called immediately after WatchIPNBus succeeds
					if lastFailed {
						log.Printf("central: reconnected to Tailscale IPN bus")
					}
				},
				func() { // onchange: called on netmap change
					select {
					case triggerCh <- struct{}{}:
					default: // refresh already queued
					}
				},
			)
			if ctx.Err() != nil {
				return
			}
			lastFailed = !connected
			// Brief pause before reconnecting after a Watch error.
			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
			}
		}
	}()

	c.refresh(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.refresh(ctx)
		case <-triggerCh:
			c.refresh(ctx)
		}
	}
}

// Targets returns the latest aggregated SDTarget list (healthy peers only).
func (c *collector) Targets() []protocol.SDTarget {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]protocol.SDTarget, len(c.targets))
	copy(out, c.targets)
	return out
}

// Peers returns the full peer list including health status.
func (c *collector) Peers() []protocol.PeerInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]protocol.PeerInfo, len(c.peers))
	copy(out, c.peers)
	return out
}

// UpdateAgentToken replaces the Bearer token used when querying Agents.
func (c *collector) UpdateAgentToken(token string) {
	c.mu.Lock()
	c.agentToken = token
	c.mu.Unlock()
}

// ReplaceConfigPeers atomically replaces all config-file-origin manual peers
// with the new list. CLI-added peers (fromConfig=false) are not affected.
func (c *collector) ReplaceConfigPeers(peers []config.ManualPeer) {
	c.manualMu.Lock()
	defer c.manualMu.Unlock()
	for addr, mp := range c.manualPeers {
		if mp.fromConfig {
			delete(c.manualPeers, addr)
		}
	}
	for _, p := range peers {
		c.manualPeers[p.Address] = manualPeer{
			Name:       p.Name,
			Address:    p.Address,
			Port:       p.Port,
			fromConfig: true,
		}
	}
}

// AddManualPeer adds or replaces a manually configured peer.
func (c *collector) AddManualPeer(mp manualPeer) {
	c.manualMu.Lock()
	c.manualPeers[mp.Address] = mp
	c.manualMu.Unlock()
}

// RemoveManualPeer removes a manual peer by address. Returns false if not found.
func (c *collector) RemoveManualPeer(address string) bool {
	c.manualMu.Lock()
	defer c.manualMu.Unlock()
	if _, ok := c.manualPeers[address]; !ok {
		return false
	}
	delete(c.manualPeers, address)
	return true
}

// ListManualPeers returns all manually configured peers.
func (c *collector) ListManualPeers() []manualPeer {
	c.manualMu.RLock()
	defer c.manualMu.RUnlock()
	out := make([]manualPeer, 0, len(c.manualPeers))
	for _, mp := range c.manualPeers {
		out = append(out, mp)
	}
	return out
}

// peerResult is the per-goroutine result from querying one Agent.
type peerResult struct {
	idx     int
	targets []protocol.SDTarget
	health  protocol.AgentHealth
}

// refresh re-discovers peers, applies manual overrides, queries each online
// Agent, and atomically updates the cached peer list and SD targets.
func (c *collector) refresh(ctx context.Context) {
	autoPeers, err := c.discoverer.Discover(ctx)
	if err != nil {
		c.mu.Lock()
		c.tsDown = true
		c.mu.Unlock()
		log.Printf("central: discovery error: %v", err)
		return
	}
	c.mu.Lock()
	wasDown := c.tsDown
	c.tsDown = false
	c.mu.Unlock()
	if wasDown {
		log.Printf("central: reconnected to Tailscale, resuming discovery")
	}

	peers := c.mergePeers(autoPeers)

	// Query each peer that Tailscale reports as online (or manually added).
	resultCh := make(chan peerResult, len(peers))
	var wg sync.WaitGroup
	for i, peer := range peers {
		if peer.Health == protocol.AgentHealthOffline {
			continue // Tailscale says node is down; skip
		}
		i, peer := i, peer
		wg.Add(1)
		go func() {
			defer wg.Done()
			targets, health, qErr := c.queryAgent(ctx, peer)
			if qErr != nil {
				log.Printf("central: agent %s (%s): %v", peer.Hostname, peer.TailscaleIP, qErr)
			}
			resultCh <- peerResult{idx: i, targets: targets, health: health}
		}()
	}
	wg.Wait()
	close(resultCh)

	// Apply results: update health and collect targets.
	var allTargets []protocol.SDTarget
	for r := range resultCh {
		peers[r.idx].Health = r.health
		if r.health == protocol.AgentHealthOK {
			allTargets = append(allTargets, r.targets...)
		}
	}
	if allTargets == nil {
		allTargets = []protocol.SDTarget{}
	}

	c.mu.Lock()
	c.peers = peers
	c.targets = allTargets
	c.mu.Unlock()
}

// queryAgent fetches the service list from a single Agent.
// Returns the SD targets, the resulting AgentHealth, and any error.
func (c *collector) queryAgent(ctx context.Context, peer protocol.PeerInfo) ([]protocol.SDTarget, protocol.AgentHealth, error) {
	c.mu.RLock()
	token := c.agentToken
	c.mu.RUnlock()

	url := peer.AgentURL + "/api/v1/services"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, protocol.AgentHealthTimeout, fmt.Errorf("build request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || isTimeoutError(err) {
			return nil, protocol.AgentHealthTimeout, fmt.Errorf("timeout reaching agent: %w", err)
		}
		return nil, protocol.AgentHealthTimeout, fmt.Errorf("connect to agent: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, protocol.AgentHealthUnauthorized, fmt.Errorf("agent returned HTTP %d (token mismatch?)", resp.StatusCode)
	case http.StatusOK:
		// fall through
	default:
		return nil, protocol.AgentHealthTimeout, fmt.Errorf("agent returned HTTP %d", resp.StatusCode)
	}

	var targets []protocol.SDTarget
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return nil, protocol.AgentHealthTimeout, fmt.Errorf("decode agent response: %w", err)
	}
	return targets, protocol.AgentHealthOK, nil
}

// mergePeers combines auto-discovered peers with manually configured peers.
// If the same address appears in both, the manual entry's port takes precedence.
func (c *collector) mergePeers(auto []protocol.PeerInfo) []protocol.PeerInfo {
	c.manualMu.RLock()
	defer c.manualMu.RUnlock()

	result := make([]protocol.PeerInfo, len(auto))
	copy(result, auto)
	autoIdx := make(map[string]int, len(auto))
	for i, p := range result {
		autoIdx[p.TailscaleIP] = i
	}

	for _, mp := range c.manualPeers {
		port := mp.Port
		if port == 0 {
			port = c.discoverer.port
		}
		agentURL := fmt.Sprintf("http://%s:%d", mp.Address, port)

		if idx, ok := autoIdx[mp.Address]; ok {
			// Override the port for an auto-discovered peer.
			result[idx].AgentURL = agentURL
		} else {
			// Peer not in Tailscale status; add as manual-only.
			name := mp.Name
			if name == "" {
				name = mp.Address
			}
			result = append(result, protocol.PeerInfo{
				Hostname:    name,
				TailscaleIP: mp.Address,
				Tags:        []string{},
				AgentURL:    agentURL,
				Source:      protocol.PeerSourceManual,
				Health:      protocol.AgentHealthUnknown,
			})
		}
	}
	return result
}

func isTimeoutError(err error) bool {
	var netErr interface{ Timeout() bool }
	return errors.As(err, &netErr) && netErr.Timeout()
}
