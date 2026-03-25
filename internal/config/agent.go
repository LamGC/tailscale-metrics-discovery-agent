package config

import (
	"fmt"
	"log"
	"os"

	"github.com/BurntSushi/toml"
)

// AgentConfig is the top-level Agent configuration.
type AgentConfig struct {
	Server      AgentServer      `toml:"server"`
	Management  AgentManagement  `toml:"management"`
	SelfMetrics SelfMetricsConfig `toml:"self_metrics"`
	Statics     []StaticService  `toml:"static"`
	Buckets     []BucketService  `toml:"bucket"`
	Proxies     []ProxyService   `toml:"proxy"`
}

// AgentServer holds HTTP server settings for the Agent.
type AgentServer struct {
	Listen string `toml:"listen"`
	// Token is an optional Bearer token that Central must present when
	// querying /api/v1/services. Leave empty to disable auth.
	Token string `toml:"token"`
}

// AgentManagement configures the Unix-socket management API.
type AgentManagement struct {
	Socket string `toml:"socket"`
}

// StaticService is a pre-configured Prometheus target exposed as-is.
type StaticService struct {
	Name    string            `toml:"name"`
	Targets []string          `toml:"targets"`
	Labels  map[string]string `toml:"labels"`
}

// BucketService is a named Pushgateway-like container. Each bucket gets its
// own /bucket/<name>/metrics scrape endpoint and is auto-registered as a
// separate SDTarget pointing to the Agent itself.
type BucketService struct {
	Name   string            `toml:"name"`
	Labels map[string]string `toml:"labels"`
}

// ProxyService is a virtual endpoint: Agent proxies scrape requests to a
// local target, optionally injecting authentication.
type ProxyService struct {
	Name   string            `toml:"name"`
	Target string            `toml:"target"`
	Auth   ProxyAuth         `toml:"auth"`
	Labels map[string]string `toml:"labels"`
}

// ProxyAuth holds authentication credentials for a proxy target.
type ProxyAuth struct {
	// Type is one of: "none", "bearer", "basic". Defaults to "none".
	Type     string `toml:"type"`
	Token    string `toml:"token"`
	Username string `toml:"username"`
	Password string `toml:"password"`
}

// DefaultAgentConfig returns an AgentConfig with sensible defaults.
func DefaultAgentConfig() AgentConfig {
	return AgentConfig{
		Server: AgentServer{
			Listen: ":9001",
		},
		Management: AgentManagement{
			Socket: DefaultSocketPath("agent"),
		},
		SelfMetrics: DefaultSelfMetricsConfig(),
	}
}

// LoadAgentConfig reads and parses a TOML config file.
// If path is empty, defaults are returned.
// If the file does not exist, it is created with defaults and defaults are returned.
func LoadAgentConfig(path string) (AgentConfig, error) {
	cfg := DefaultAgentConfig()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if initErr := initConfigFile(path, cfg); initErr != nil {
				log.Printf("tsd: could not initialise agent config at %s: %v", path, initErr)
			} else {
				log.Printf("tsd: initialised default agent config at %s", path)
			}
			return cfg, nil
		}
		return cfg, fmt.Errorf("reading agent config: %w", err)
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing agent config: %w", err)
	}
	return cfg, nil
}

// SaveAgentConfig writes cfg to path in TOML format.
func SaveAgentConfig(path string, cfg AgentConfig) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating agent config file: %w", err)
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}
