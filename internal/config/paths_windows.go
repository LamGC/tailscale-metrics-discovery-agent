//go:build windows

package config

import (
	"os"
	"path/filepath"
)

// configRoot returns the tsd configuration root directory.
//   - system (ProgramData writable): %ProgramData%\tsd
//   - user mode:                     %APPDATA%\tsd
//
// Writability is tested by attempting os.MkdirAll; this is idempotent if the
// directory already exists and gracefully falls back on permission errors.
func configRoot() string {
	if pd := os.Getenv("ProgramData"); pd != "" {
		dir := filepath.Join(pd, "tsd")
		if err := os.MkdirAll(dir, 0o755); err == nil {
			return dir
		}
	}
	if ad := os.Getenv("APPDATA"); ad != "" {
		return filepath.Join(ad, "tsd")
	}
	return "tsd"
}

// DefaultSocketPath returns the Windows Named Pipe path for the given role.
// Named pipes are not filesystem-scoped, so system/user mode is irrelevant.
func DefaultSocketPath(role string) string {
	return `\\.\pipe\tsd-` + role
}
