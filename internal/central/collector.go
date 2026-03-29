package central

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"golang.org/x/net/http2"

	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/config"
	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/protocol"
)

// manualPeer is a peer configured explicitly by the operator.
type manualPeer struct {
	Name       string
	Address    string
	Port       int  // 0 = use discoverer.port
	fromConfig bool // true when loaded from config file (not via CLI)
}

// serviceHistoryTTL is how long Central retains a peer's last-known Services
// after it becomes unreachable.
const serviceHistoryTTL = 72 * time.Hour

// cachedPeerServices holds a snapshot of a peer's services from the last
// successful query.
type cachedPeerServices struct {
	services  []protocol.ServiceEntry
	fetchedAt time.Time
}

// peerCacheEntry is the on-disk representation of one peer's cached data.
type peerCacheEntry struct {
	TailscaleIP string                  `json:"tailscale_ip"`
	Hostname    string                  `json:"hostname"`
	Tags        []string                `json:"tags"`
	AgentURL    string                  `json:"agent_url"`
	Source      protocol.PeerSource     `json:"source"`
	Services    []protocol.ServiceEntry `json:"services"`
	FetchedAt   time.Time               `json:"fetched_at"`
}

// collector periodically queries each discovered Agent and aggregates the
// Prometheus SDTargets into a single list.
type collector struct {
	discoverer *discoverer
	agentToken string
	httpClient *http.Client
	peersFile  string // path to the peer history cache JSON file; "" = disabled

	mu      sync.RWMutex
	peers   []protocol.PeerInfo // full peer list with health status
	targets []protocol.SDTarget // aggregated from healthy peers only
	tsDown  bool                // true when the last Discover() call failed

	cacheMu      sync.RWMutex
	serviceCache map[string]cachedPeerServices // keyed by TailscaleIP

	manualMu    sync.RWMutex
	manualPeers map[string]manualPeer // keyed by Tailscale IP / address

	saveMu sync.Mutex // serialises concurrent savePeerCache() calls

	// Per-peer Last-Modified tracking for conditional requests (If-Modified-Since).
	condMu             sync.RWMutex
	lastModifiedSvc    map[string]string // key=TailscaleIP, value=Last-Modified header
	lastModifiedHealth map[string]string
}

func newCollector(d *discoverer, agentToken string) *collector {
	h2t := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
	}
	return &collector{
		discoverer:         d,
		agentToken:         agentToken,
		httpClient:         &http.Client{Timeout: 10 * time.Second, Transport: h2t},
		serviceCache:       make(map[string]cachedPeerServices),
		manualPeers:        make(map[string]manualPeer),
		lastModifiedSvc:    make(map[string]string),
		lastModifiedHealth: make(map[string]string),
	}
}

// savePeerCache writes the current service cache to peersFile as JSON.
// It is a best-effort operation; errors are logged but not returned.
func (c *collector) savePeerCache() {
	if c.peersFile == "" {
		return
	}
	c.cacheMu.RLock()
	entries := make([]peerCacheEntry, 0, len(c.serviceCache))
	for ip, cached := range c.serviceCache {
		// Include basic peer info if available from the current peers list.
		var hostname string
		var tags []string
		var agentURL string
		var source protocol.PeerSource
		c.mu.RLock()
		for _, p := range c.peers {
			if p.TailscaleIP == ip {
				hostname = p.Hostname
				tags = p.Tags
				agentURL = p.AgentURL
				source = p.Source
				break
			}
		}
		c.mu.RUnlock()
		entries = append(entries, peerCacheEntry{
			TailscaleIP: ip,
			Hostname:    hostname,
			Tags:        tags,
			AgentURL:    agentURL,
			Source:      source,
			Services:    cached.services,
			FetchedAt:   cached.fetchedAt,
		})
	}
	c.cacheMu.RUnlock()

	c.saveMu.Lock()
	defer c.saveMu.Unlock()
	if err := config.AtomicWriteJSON(c.peersFile, entries); err != nil {
		log.Printf("central: failed to save peer cache: %v", err)
	}
}

// loadPeerCache reads the peer history cache from peersFile and pre-populates
// serviceCache with entries that are still within the TTL.
func (c *collector) loadPeerCache() {
	if c.peersFile == "" {
		return
	}
	data, err := os.ReadFile(c.peersFile)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("central: reading peer cache: %v", err)
		}
		return
	}
	var entries []peerCacheEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		log.Printf("central: parsing peer cache: %v", err)
		return
	}
	now := time.Now()
	loaded := 0
	c.cacheMu.Lock()
	for _, e := range entries {
		if now.Sub(e.FetchedAt) > serviceHistoryTTL {
			continue // expired
		}
		c.serviceCache[e.TailscaleIP] = cachedPeerServices{
			services:  e.Services,
			fetchedAt: e.FetchedAt,
		}
		loaded++
	}
	c.cacheMu.Unlock()
	if loaded > 0 {
		log.Printf("central: loaded %d peer(s) from cache (%s)", loaded, c.peersFile)
	}
}

// Run starts the background refresh loop and the WatchIPNBus listener.
// It blocks until ctx is cancelled.
// If nodeAttrs is true, RefreshSelfAttrs is called on connect and netmap changes.
func (c *collector) Run(ctx context.Context, interval time.Duration, nodeAttrs bool) {
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
					if nodeAttrs {
						c.discoverer.RefreshSelfAttrs(ctx)
					}
				},
				func() { // onchange: called on netmap change
					if nodeAttrs {
						c.discoverer.RefreshSelfAttrs(ctx)
					}
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
	idx       int
	services  []protocol.ServiceEntry                 // nil on 304
	targets   []protocol.SDTarget                     // nil on 304
	healthMap map[string]*protocol.ServiceHealthStatus // nil on 304 or old agent
	health    protocol.AgentHealth                     // worst of svc + health queries
	svc304    bool
	health304 bool
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
	now := time.Now()

	// For offline peers (skipped below), restore cached services immediately.
	c.cacheMu.RLock()
	for i, peer := range peers {
		if peer.Health != protocol.AgentHealthOffline {
			continue
		}
		if cached, ok := c.serviceCache[peer.TailscaleIP]; ok && now.Sub(cached.fetchedAt) <= serviceHistoryTTL {
			peers[i].Services = cached.services
			t := cached.fetchedAt
			peers[i].ServicesUpdatedAt = &t
		}
	}
	c.cacheMu.RUnlock()

	// Query each peer that Tailscale reports as online (or manually added).
	// For each peer, two requests are made in parallel via H2 multiplexing:
	//   GET /api/v1/services        — service list (without health)
	//   GET /api/v1/services/health — health status map
	// Both support If-Modified-Since / 304 Not Modified.
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
			var pr peerResult
			pr.idx = i

			var swg sync.WaitGroup
			swg.Add(2)

			// Query services.
			var svcHealth protocol.AgentHealth
			go func() {
				defer swg.Done()
				svcs, tgts, h, notMod, qErr := c.queryAgentServices(ctx, peer)
				if qErr != nil {
					log.Printf("central: agent %s (%s) services: %v", peer.Hostname, peer.TailscaleIP, qErr)
				}
				pr.services = svcs
				pr.targets = tgts
				pr.svc304 = notMod
				svcHealth = h
			}()

			// Query health.
			var hlthHealth protocol.AgentHealth
			go func() {
				defer swg.Done()
				hm, h, notMod, qErr := c.queryAgentHealth(ctx, peer)
				if qErr != nil {
					log.Printf("central: agent %s (%s) health: %v", peer.Hostname, peer.TailscaleIP, qErr)
				}
				pr.healthMap = hm
				pr.health304 = notMod
				hlthHealth = h
			}()

			swg.Wait()
			pr.health = worseHealth(svcHealth, hlthHealth)
			resultCh <- pr
		}()
	}
	wg.Wait()
	close(resultCh)

	// Apply results: update health, services, and collect targets.
	// On success update the service cache; on failure restore from cache if within TTL.
	var allTargets []protocol.SDTarget
	for r := range resultCh {
		peers[r.idx].Health = r.health
		ip := peers[r.idx].TailscaleIP
		if r.health == protocol.AgentHealthOK {
			// 1. Get base services: from response or from cache on 304.
			var services []protocol.ServiceEntry
			if r.svc304 {
				c.cacheMu.RLock()
				if cached, ok := c.serviceCache[ip]; ok {
					services = cached.services
				}
				c.cacheMu.RUnlock()
			} else {
				services = r.services
			}

			// 2. Merge health: on 200, overlay fresh health onto services.
			//    On 304, cached services already have the previously merged health.
			if !r.health304 && r.healthMap != nil {
				for i := range services {
					if h, ok := r.healthMap[services[i].Name]; ok {
						services[i].Health = h
					}
				}
			}

			// 3. Update cache with merged result.
			c.cacheMu.Lock()
			c.serviceCache[ip] = cachedPeerServices{services: services, fetchedAt: now}
			c.cacheMu.Unlock()

			t := now
			peers[r.idx].Services = services
			peers[r.idx].ServicesUpdatedAt = &t
			for _, svc := range services {
				allTargets = append(allTargets, svc.Target)
			}
		} else {
			// Restore from cache if still within TTL.
			c.cacheMu.RLock()
			cached, ok := c.serviceCache[ip]
			c.cacheMu.RUnlock()
			if ok && now.Sub(cached.fetchedAt) <= serviceHistoryTTL {
				peers[r.idx].Services = cached.services
				peers[r.idx].ServicesUpdatedAt = &cached.fetchedAt
			}
		}
	}

	// Evict cache entries whose TTL has expired (peers not in current list keep
	// the entry alive as long as they keep showing up; peers removed from
	// Tailscale entirely will simply age out here).
	c.cacheMu.Lock()
	for ip, cached := range c.serviceCache {
		if now.Sub(cached.fetchedAt) > serviceHistoryTTL {
			delete(c.serviceCache, ip)
		}
	}
	c.cacheMu.Unlock()
	if allTargets == nil {
		allTargets = []protocol.SDTarget{}
	}

	c.mu.Lock()
	c.peers = peers
	c.targets = allTargets
	c.mu.Unlock()

	// Persist the updated service cache to disk for fast startup next time.
	go c.savePeerCache()
}

// queryAgentServices fetches the service list (without health) from a single Agent.
// Returns (entries, targets, health, notModified, error).
func (c *collector) queryAgentServices(ctx context.Context, peer protocol.PeerInfo) ([]protocol.ServiceEntry, []protocol.SDTarget, protocol.AgentHealth, bool, error) {
	c.mu.RLock()
	token := c.agentToken
	c.mu.RUnlock()

	url := peer.AgentURL + "/api/v1/services"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, protocol.AgentHealthTimeout, false, fmt.Errorf("build request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	// Conditional request: send If-Modified-Since if we have a stored value.
	c.condMu.RLock()
	if lm, ok := c.lastModifiedSvc[peer.TailscaleIP]; ok {
		req.Header.Set("If-Modified-Since", lm)
	}
	c.condMu.RUnlock()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || isTimeoutError(err) {
			return nil, nil, protocol.AgentHealthTimeout, false, fmt.Errorf("timeout reaching agent: %w", err)
		}
		return nil, nil, protocol.AgentHealthTimeout, false, fmt.Errorf("connect to agent: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotModified:
		return nil, nil, protocol.AgentHealthOK, true, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, nil, protocol.AgentHealthUnauthorized, false, fmt.Errorf("agent returned HTTP %d (token mismatch?)", resp.StatusCode)
	case http.StatusOK:
		// fall through
	default:
		return nil, nil, protocol.AgentHealthTimeout, false, fmt.Errorf("agent returned HTTP %d", resp.StatusCode)
	}

	// Store Last-Modified for future conditional requests.
	if lm := resp.Header.Get("Last-Modified"); lm != "" {
		c.condMu.Lock()
		c.lastModifiedSvc[peer.TailscaleIP] = lm
		c.condMu.Unlock()
	}

	var entries []protocol.ServiceEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, nil, protocol.AgentHealthTimeout, false, fmt.Errorf("decode agent response: %w", err)
	}
	targets := make([]protocol.SDTarget, 0, len(entries))
	for _, e := range entries {
		targets = append(targets, e.Target)
	}
	return entries, targets, protocol.AgentHealthOK, false, nil
}

// queryAgentHealth fetches the health map from a single Agent.
// Returns (healthMap, health, notModified, error).
func (c *collector) queryAgentHealth(ctx context.Context, peer protocol.PeerInfo) (map[string]*protocol.ServiceHealthStatus, protocol.AgentHealth, bool, error) {
	c.mu.RLock()
	token := c.agentToken
	c.mu.RUnlock()

	url := peer.AgentURL + "/api/v1/services/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, protocol.AgentHealthTimeout, false, fmt.Errorf("build request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	// Conditional request.
	c.condMu.RLock()
	if lm, ok := c.lastModifiedHealth[peer.TailscaleIP]; ok {
		req.Header.Set("If-Modified-Since", lm)
	}
	c.condMu.RUnlock()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || isTimeoutError(err) {
			return nil, protocol.AgentHealthTimeout, false, fmt.Errorf("timeout reaching agent: %w", err)
		}
		return nil, protocol.AgentHealthTimeout, false, fmt.Errorf("connect to agent: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotModified:
		return nil, protocol.AgentHealthOK, true, nil
	case http.StatusNotFound:
		// Old Agent without /services/health endpoint — not an error.
		return nil, protocol.AgentHealthOK, false, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, protocol.AgentHealthUnauthorized, false, fmt.Errorf("agent returned HTTP %d (token mismatch?)", resp.StatusCode)
	case http.StatusOK:
		// fall through
	default:
		return nil, protocol.AgentHealthTimeout, false, fmt.Errorf("agent returned HTTP %d", resp.StatusCode)
	}

	// Store Last-Modified for future conditional requests.
	if lm := resp.Header.Get("Last-Modified"); lm != "" {
		c.condMu.Lock()
		c.lastModifiedHealth[peer.TailscaleIP] = lm
		c.condMu.Unlock()
	}

	var healthMap map[string]*protocol.ServiceHealthStatus
	if err := json.NewDecoder(resp.Body).Decode(&healthMap); err != nil {
		return nil, protocol.AgentHealthTimeout, false, fmt.Errorf("decode agent health response: %w", err)
	}
	return healthMap, protocol.AgentHealthOK, false, nil
}

// worseHealth returns the worse of two AgentHealth values.
// Ordering: OK < Unknown < Timeout < Unauthorized < Offline.
func worseHealth(a, b protocol.AgentHealth) protocol.AgentHealth {
	rank := map[protocol.AgentHealth]int{
		protocol.AgentHealthOK:           0,
		protocol.AgentHealthUnknown:      1,
		protocol.AgentHealthTimeout:      2,
		protocol.AgentHealthUnauthorized: 3,
		protocol.AgentHealthOffline:      4,
	}
	if rank[b] > rank[a] {
		return b
	}
	return a
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
			port = c.discoverer.Port()
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
