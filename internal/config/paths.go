package config

import (
	"bytes"
	"encoding/json"
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

// atomicWriteTOML serialises v as TOML and atomically replaces path.
func atomicWriteTOML(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(v); err != nil {
		return fmt.Errorf("encoding TOML: %w", err)
	}
	return atomicWrite(path, buf.Bytes(), 0o600)
}

// AtomicWriteJSON serialises v as indented JSON and atomically replaces path.
func AtomicWriteJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating dir: %w", err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding JSON: %w", err)
	}
	return atomicWrite(path, data, 0o600)
}

// atomicWrite writes data to path by writing to a temp file and renaming.
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// initConfigFile creates path with cfg serialised as TOML.
// Uses O_EXCL so it is a no-op if another process already created the file
// between the caller's os.IsNotExist check and this call.
func initConfigFile(path string, cfg any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}
