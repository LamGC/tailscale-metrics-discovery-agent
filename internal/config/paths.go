package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// ConfigDir returns the role-specific config directory.
// role is "central" or "agent".
func ConfigDir(role string) string {
	return filepath.Join(configRoot(), role)
}

// DefaultConfigFile returns the default config file path for the given role.
func DefaultConfigFile(role string) string {
	return filepath.Join(ConfigDir(role), role+".toml")
}

// initConfigFile creates path with cfg serialised as TOML.
// Uses O_EXCL so it is a no-op if another process already created the file
// between the caller's os.IsNotExist check and this call.
func initConfigFile(path string, cfg any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}
