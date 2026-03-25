package daemon

import (
	"context"
	"net"
	"net/http"
)

// newSocketTransport creates an *http.Transport that connects all requests
// through the management socket at socketPath, ignoring the host in the URL.
func newSocketTransport(socketPath string) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return Dial(socketPath)
		},
		DisableKeepAlives: true,
	}
}
