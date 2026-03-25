//go:build !windows

package daemon

import (
	"fmt"
	"net"
	"os"

	"github.com/lamgc/tailscale-service-discovery-agent/internal/config"
)

// Listen creates a listener on the given socket path.duiyu
// On Unix-like systems this is a Unix domain socket.
func Listen(socketPath string) (net.Listener, error) {
	// Remove stale socket file.
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("removing stale socket %s: %w", socketPath, err)
	}
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listening on unix socket %s: %w", socketPath, err)
	}
	// Restrict permissions: only owner can connect.
	if err := os.Chmod(socketPath, 0600); err != nil {
		_ = ln.Close()
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
