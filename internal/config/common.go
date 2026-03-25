package config

// SelfMetricsConfig controls exposure of the daemon's own Prometheus metrics.
type SelfMetricsConfig struct {
	// Enabled controls whether the /metrics endpoint is active.
	// Defaults to true.
	Enabled bool `toml:"enabled"`
	// Path is the HTTP path for the metrics endpoint.
	// Defaults to "/metrics".
	Path string `toml:"path"`
	// Listen is an optional dedicated listen address (e.g. ":9102").
	// When empty, the endpoint is served on the main server port alongside
	// the other API endpoints.
	Listen string `toml:"listen"`
	// RegisterSelf, when true, adds this daemon's own metrics endpoint as
	// a static SDTarget so Prometheus discovers and scrapes it automatically.
	RegisterSelf bool `toml:"register_self"`
	// Labels are extra labels attached to the self-registration SDTarget.
	Labels map[string]string `toml:"labels"`
}

// DefaultSelfMetricsConfig returns sensible defaults.
func DefaultSelfMetricsConfig() SelfMetricsConfig {
	return SelfMetricsConfig{
		Enabled: true,
		Path:    "/metrics",
	}
}
