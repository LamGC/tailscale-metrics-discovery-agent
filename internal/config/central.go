package config

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/BurntSushi/toml"
)

// CentralConfig is the top-level Central configuration.
type CentralConfig struct {
	Server      CentralServer     `toml:"server"`
	Tailscale   TailscaleConfig   `toml:"tailscale"`
	Discovery   DiscoveryConfig   `toml:"discovery"`
	Management  CentralManagement `toml:"management"`
	ManualPeers []ManualPeer      `toml:"peer"`
	SelfMetrics SelfMetricsConfig `toml:"self_metrics"`
}

// CentralServer holds HTTP server settings for the Central.
type CentralServer struct {
	Listen string `toml:"listen"`
	// Token is an optional Bearer token that Prometheus must present when
	// querying /api/v1/sd. Leave empty to disable auth.
	Token string `toml:"token"`
}

// TailscaleConfig holds Tailscale daemon connection settings.
type TailscaleConfig struct {
	// Socket is the path to tailscaled's Unix socket.
	// Leave empty to use the platform default.
	Socket string `toml:"socket"`
}

// DiscoveryConfig controls how Central discovers Agent peers.
type DiscoveryConfig struct {
	// Tags is the list of Tailscale ACL tags to match when discovering peers.
	Tags []string `toml:"tags"`
	// AgentPort is the TCP port where Agents listen.
	AgentPort int `toml:"agent_port"`
	// RefreshInterval is how often Central re-queries Tailscale and Agents.
	RefreshInterval Duration `toml:"refresh_interval"`
	// AgentToken is the optional Bearer token sent when querying Agents.
	AgentToken string `toml:"agent_token"`
	// NodeAttrs enables automatic configuration via Tailscale ACL nodeAttrs.
	// When true (default), Central reads custom:tsd-agent-tag and
	// custom:tsd-agent-port from its own node attributes to override
	// Tags and AgentPort. Set to false to ignore nodeAttrs entirely.
	NodeAttrs bool `toml:"node_attrs"`
}

// CentralManagement configures the Unix-socket management API.
type CentralManagement struct {
	Socket string `toml:"socket"`
}

// ManualPeer is a peer configured explicitly by the operator, bypassing
// Tailscale tag-based auto-discovery. Useful for nodes on non-standard ports
// or nodes whose tags are not managed by the operator.
type ManualPeer struct {
	// Name is an optional human-readable label shown in CLI output.
	Name string `toml:"name"`
	// Address is the Tailscale IP (or MagicDNS hostname) of the Agent node.
	Address string `toml:"address"`
	// Port overrides the discovery.agent_port for this specific peer.
	// 0 means "use discovery.agent_port".
	Port int `toml:"port"`
}

// Duration wraps time.Duration for TOML (de)serialization as a string.
type Duration struct {
	time.Duration
}

func (d Duration) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

// DefaultCentralConfig returns a CentralConfig with sensible defaults.
func DefaultCentralConfig() CentralConfig {
	return CentralConfig{
		Server: CentralServer{
			Listen: ":9000",
		},
		Discovery: DiscoveryConfig{
			Tags:            []string{"tag:prometheus-agent"},
			AgentPort:       9001,
			RefreshInterval: Duration{5 * time.Second},
			NodeAttrs:       true,
		},
		Management: CentralManagement{
			Socket: DefaultSocketPath("central"),
		},
		SelfMetrics: DefaultSelfMetricsConfig(),
	}
}

// SaveCentralConfig writes cfg to path in TOML format atomically.
func SaveCentralConfig(path string, cfg CentralConfig) error {
	return atomicWriteTOML(path, cfg)
}

// LoadCentralConfig reads and parses a TOML config file.
// If path is empty, defaults are returned.
// If the file does not exist, it is created with defaults and defaults are returned.
func LoadCentralConfig(path string) (CentralConfig, error) {
	cfg := DefaultCentralConfig()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if initErr := initConfigFile(path, cfg); initErr != nil {
				log.Printf("tsd: could not initialise central config at %s: %v", path, initErr)
			} else {
				log.Printf("tsd: initialised default central config at %s", path)
			}
			return cfg, nil
		}
		return cfg, fmt.Errorf("reading central config: %w", err)
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing central config: %w", err)
	}
	return cfg, nil
}
