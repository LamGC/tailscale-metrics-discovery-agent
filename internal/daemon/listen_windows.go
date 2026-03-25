//go:build windows

package daemon

import (
	"fmt"
	"net"

	"github.com/tailscale/go-winio"
)

const (
	centralPipeName = `\\.\pipe\tsd-central`
	agentPipeName   = `\\.\pipe\tsd-agent`
)

// Listen creates a Windows Named Pipe listener at socketPath.
// socketPath should be in the form \\.\pipe\<name>.
func Listen(socketPath string) (net.Listener, error) {
	ln, err := winio.ListenPipe(socketPath, &winio.PipeConfig{
		// Allow only the current user and admins.
		SecurityDescriptor: "D:P(A;;GA;;;OW)(A;;GA;;;BA)",
		InputBufferSize:    65536,
		OutputBufferSize:   65536,
	})
	if err != nil {
		return nil, fmt.Errorf("listening on named pipe %s: %w", socketPath, err)
	}
	return ln, nil
}

// Dial connects to a Windows Named Pipe at socketPath.
func Dial(socketPath string) (net.Conn, error) {
	conn, err := winio.DialPipe(socketPath, nil)
	if err != nil {
		return nil, fmt.Errorf("connecting to pipe %s: %w", socketPath, err)
	}
	return conn, nil
}

// DefaultCentralSocket returns the Windows named pipe path for Central.
func DefaultCentralSocket() string {
	return centralPipeName
}

// DefaultAgentSocket returns the Windows named pipe path for Agent.
func DefaultAgentSocket() string {
	return agentPipeName
}

// Cleanup is a no-op on Windows; named pipes are removed automatically.
func Cleanup(socketPath string) {}
