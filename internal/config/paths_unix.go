//go:build !windows

package config

import (
	"fmt"
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

// DefaultSocketPath returns the management socket path for the given role.
//
// System (uid 0):
//
//	/var/run/tsd/{role}.sock
//
// User, tried in order (first whose parent directory is accessible):
//
//  1. $XDG_RUNTIME_DIR/tsd/{role}.sock
//  2. /run/user/{uid}/tsd/{role}.sock
//  3. /tmp/tsd_{uid}/{role}.sock
//  4. {ConfigDir(role)}/tsd.sock  (ultimate fallback)
//
// The chosen directory is created by daemon.Listen when the socket is opened,
// not here — DefaultSocketPath has no side effects.
func DefaultSocketPath(role string) string {
	if os.Getuid() == 0 {
		return fmt.Sprintf("/var/run/tsd/%s.sock", role)
	}

	uid := fmt.Sprintf("%d", os.Getuid())

	// 1. XDG_RUNTIME_DIR — set by systemd-logind / PAM on modern distros.
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" && isDirAccessible(xdg) {
		return filepath.Join(xdg, "tsd", role+".sock")
	}

	// 2. /run/user/{uid} — fallback when XDG_RUNTIME_DIR is not exported.
	runUser := "/run/user/" + uid
	if isDirAccessible(runUser) {
		return filepath.Join(runUser, "tsd", role+".sock")
	}

	// 3. /tmp/tsd_{uid} — always writable.
	if isDirAccessible("/tmp") {
		return fmt.Sprintf("/tmp/tsd_%s/%s.sock", uid, role)
	}

	// 4. Role config dir — ultimate fallback.
	return filepath.Join(ConfigDir(role), "tsd.sock")
}

// isDirAccessible reports whether path is an existing, accessible directory.
// It has no side effects.
func isDirAccessible(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
