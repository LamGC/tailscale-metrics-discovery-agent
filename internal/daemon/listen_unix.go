//go:build !windows

package daemon

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/config"
)

// Listen creates a Unix domain socket listener at socketPath.
//
// Directory permissions:
//   - root (uid 0): 0o755 — consistent with /var/run conventions
//   - normal user:  0o700 — private to the owning user
//
// The socket file itself is always restricted to 0o600 (owner r/w only)
// so no other user can connect even if they discover the path.
func Listen(socketPath string) (net.Listener, error) {
	dirMode := os.FileMode(0o700)
	if os.Getuid() == 0 {
		dirMode = 0o755
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), dirMode); err != nil {
		return nil, fmt.Errorf("creating socket dir %s: %w", filepath.Dir(socketPath), err)
	}
	// Remove stale socket file.
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("removing stale socket %s: %w", socketPath, err)
	}
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listening on unix socket %s: %w", socketPath, err)
	}
	// Restrict to owner only — no other user may connect.
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = ln.Close()
		_ = os.Remove(socketPath)
		return nil, fmt.Errorf("chmod socket: %w", err)
	}
	return ln, nil
}

// Dial connects to the management socket at socketPath.
func Dial(socketPath string) (net.Conn, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("connecting to socket %s: %w", socketPath, err)
	}
	return conn, nil
}

// DefaultCentralSocket returns the platform-default management socket path for Central.
func DefaultCentralSocket() string {
	return config.DefaultSocketPath("central")
}

// DefaultAgentSocket returns the platform-default management socket path for Agent.
func DefaultAgentSocket() string {
	return config.DefaultSocketPath("agent")
}

// Cleanup removes the socket file on shutdown.
func Cleanup(socketPath string) {
	_ = os.Remove(socketPath)
}
