//go:build !windows

package config

import (
	"os"
	"path/filepath"
)

// configRoot returns the tsd configuration root directory.
//   - root (uid 0): /etc/tsd
//   - normal user:  ~/.tsd
func configRoot() string {
	if os.Getuid() == 0 {
		return "/etc/tsd"
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "/etc/tsd"
	}
	return filepath.Join(home, ".tsd")
}

// DefaultSocketPath returns the Unix domain socket path for the given role.
// Sockets live alongside the config files under ConfigDir(role).
func DefaultSocketPath(role string) string {
	return filepath.Join(ConfigDir(role), role+".sock")
}
