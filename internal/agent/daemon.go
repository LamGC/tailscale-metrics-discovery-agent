package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/lamgc/tailscale-service-discovery-agent/internal/config"
	"github.com/lamgc/tailscale-service-discovery-agent/internal/daemon"
)

// RunDaemon loads config from cfgFile and starts the Agent server.
// It blocks until SIGINT or SIGTERM is received.
func RunDaemon(cfgFile string) error {
	cfg, err := config.LoadAgentConfig(cfgFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Use platform-default socket if not configured.
	if cfg.Management.Socket == "" {
		cfg.Management.Socket = daemon.DefaultAgentSocket()
	}

	srv := NewServer(cfg)
	srv.cfgFile = cfgFile

	// Detect Tailscale IP for self-referential SDTargets (bucket/proxy endpoints).
	tsIP, err := detectSelfTailscaleIP()
	if err != nil {
		log.Printf("agent: could not detect Tailscale IP (%v); using listen addr for targets", err)
	} else {
		_, port, splitErr := splitHostPort(cfg.Server.Listen)
		if splitErr == nil && port != "" {
			srv.selfAddr = tsIP + ":" + port
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start()
	}()

	select {
	case <-ctx.Done():
		log.Println("agent: shutting down...")
		shutCtx, cancel := context.WithTimeout(context.Background(), 0)
		defer cancel()
		srv.Shutdown(shutCtx)
		daemon.Cleanup(cfg.Management.Socket)
		return nil
	case err := <-errCh:
		return err
	}
}

// CLIStatus queries the running Agent daemon for its status.
func CLIStatus(socketPath string) error {
	if socketPath == "" {
		socketPath = daemon.DefaultAgentSocket()
	}
	c := daemon.NewClient(socketPath)
	var st map[string]any
	if err := c.Get("/status", &st); err != nil {
		return fmt.Errorf("could not reach agent daemon: %w", err)
	}
	fmt.Fprintf(os.Stdout, "Agent daemon is running.\n")
	if info, ok := st["info"].(string); ok && info != "" {
		fmt.Fprintf(os.Stdout, "Info: %s\n", info)
	}
	return nil
}

// splitHostPort splits "host:port" or ":port".
func splitHostPort(addr string) (string, string, error) {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[:i], addr[i+1:], nil
		}
	}
	return "", "", fmt.Errorf("no port in address %q", addr)
}
