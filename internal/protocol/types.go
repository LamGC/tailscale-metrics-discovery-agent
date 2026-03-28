package protocol

import "time"

// DefaultAgentPort is the well-known port that Agent listens on by default.
// Both Central (for auto-discovery) and Agent (for auto-binding) use this
// constant so the two sides agree without explicit configuration.
const DefaultAgentPort = 9001

// SDTarget is the Prometheus HTTP Service Discovery target format.
// https://prometheus.io/docs/prometheus/latest/http_sd/
type SDTarget struct {
	Targets []string          `json:"targets"`
	Labels  map[string]string `json:"labels,omitempty"`
}

// ServiceType identifies how a service entry is provided by the Agent.
type ServiceType string

const (
	ServiceTypeStatic ServiceType = "static"
	ServiceTypeBucket ServiceType = "bucket"
	ServiceTypeProxy  ServiceType = "proxy"
)

// ServiceHealth is the health status of a service's health check.
type ServiceHealth string

const (
	ServiceHealthUnknown   ServiceHealth = "unknown"   // check configured, not yet run
	ServiceHealthHealthy   ServiceHealth = "healthy"
	ServiceHealthUnhealthy ServiceHealth = "unhealthy"
)

// ServiceHealthStatus is the current result of a service health check.
type ServiceHealthStatus struct {
	Status     ServiceHealth `json:"status"`
	CheckURL   string        `json:"check_url"`
	LastCheck  *time.Time    `json:"last_check,omitempty"` // nil until first check completes
	StatusCode int           `json:"status_code,omitempty"`
	Message    string        `json:"message,omitempty"` // error message or HTTP status reason
}

// ServiceEntry is an entry in the Agent's service registry.
type ServiceEntry struct {
	Name   string               `json:"name"`
	Type   ServiceType          `json:"type"`
	Target SDTarget             `json:"target"`
	Health *ServiceHealthStatus `json:"health,omitempty"` // nil if no healthcheck configured
}

// TailscaleStatus summarizes the local Tailscale daemon state.
type TailscaleStatus struct {
	Connected    bool     `json:"connected"`
	BackendState string   `json:"backend_state"`
	Account      string   `json:"account,omitempty"`       // login name / email
	Hostname     string   `json:"hostname,omitempty"`      // this node's hostname
	TailscaleIPs []string `json:"tailscale_ips,omitempty"` // assigned Tailscale IPs
	Tags         []string `json:"tags,omitempty"`          // ACL tags (tagged nodes)
	Error        string   `json:"error,omitempty"`         // set when Tailscale is unreachable
}

// ClientAccessInfo records the last time a client (typically Central)
// accessed this Agent.
type ClientAccessInfo struct {
	NodeName string    `json:"node_name,omitempty"` // Tailscale node name, if resolvable
	IP       string    `json:"ip"`
	LastSeen time.Time `json:"last_seen"`
}

// StatusResponse is the common management API status payload.
type StatusResponse struct {
	Running   bool               `json:"running"`
	Version   string             `json:"version,omitempty"`
	Info      string             `json:"info,omitempty"`
	Tailscale *TailscaleStatus   `json:"tailscale,omitempty"`
	Clients   []ClientAccessInfo `json:"clients,omitempty"` // Agent only: recent client accesses
}

// PeerSource indicates how a peer was added to Central's peer set.
type PeerSource string

const (
	PeerSourceAuto   PeerSource = "auto"   // discovered via Tailscale ACL tag
	PeerSourceManual PeerSource = "manual" // explicitly configured by operator
)

// AgentHealth describes the reachability state of a peer's Agent.
type AgentHealth string

const (
	// AgentHealthOffline means the Tailscale node itself is not online.
	// Central cannot even attempt to reach the Agent.
	AgentHealthOffline AgentHealth = "offline"
	// AgentHealthTimeout means the Tailscale node is online but the Agent
	// HTTP endpoint did not respond within the deadline.
	AgentHealthTimeout AgentHealth = "timeout"
	// AgentHealthUnauthorized means the Agent responded with HTTP 401/403,
	// indicating a token mismatch.
	AgentHealthUnauthorized AgentHealth = "unauthorized"
	// AgentHealthOK means the Agent was successfully queried.
	AgentHealthOK AgentHealth = "ok"
	// AgentHealthUnknown is the initial state before the first query.
	AgentHealthUnknown AgentHealth = "unknown"
)

// PeerInfo describes a peer running an Agent.
type PeerInfo struct {
	Hostname          string         `json:"hostname"`
	TailscaleIP       string         `json:"tailscale_ip"`
	Tags              []string       `json:"tags"`
	AgentURL          string         `json:"agent_url"`
	Source            PeerSource     `json:"source"`
	Health            AgentHealth    `json:"health"`
	Services          []ServiceEntry `json:"services,omitempty"`           // from last successful query
	ServicesUpdatedAt *time.Time     `json:"services_updated_at,omitempty"` // when Services was last fetched
}

// PeersResponse is returned by Central's /peers management endpoint.
type PeersResponse struct {
	Peers []PeerInfo `json:"peers"`
}
