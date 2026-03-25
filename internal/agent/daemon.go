package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tailscale.com/client/local"

	"github.com/lamgc/tailscale-service-discovery-agent/internal/config"
	"github.com/lamgc/tailscale-service-discovery-agent/internal/daemon"
	"github.com/lamgc/tailscale-service-discovery-agent/internal/protocol"
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
	_, listenPort, _ := splitHostPort(cfg.Server.Listen)
	tsIP, err := detectSelfTailscaleIP()
	if err != nil {
		log.Printf("agent: could not detect Tailscale IP (%v); will retry in background", err)
	} else if listenPort != "" {
		srv.selfAddr = tsIP + ":" + listenPort
		log.Printf("agent: Tailscale IP detected, self address: %s", srv.selfAddr)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// If Tailscale was not available at startup, retry in background until we
	// get an IP so bucket/proxy endpoints appear in the SD targets list.
	if srv.selfAddr == "" && listenPort != "" {
		go watchTailscaleConnect(ctx, srv, listenPort)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start()
	}()

	select {
	case <-ctx.Done():
		log.Println("agent: shutting down...")
		shutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
	var st protocol.StatusResponse
	if err := c.Get("/status", &st); err != nil {
		return fmt.Errorf("could not reach agent daemon: %w", err)
	}
	fmt.Fprintln(os.Stdout, "Agent daemon is running.")
	printAgentTailscaleStatus(st.Tailscale)
	return nil
}

// watchTailscaleConnect polls Tailscale every 10 seconds until an IP is
// obtained, then updates srv.selfAddr and logs the connection.
func watchTailscaleConnect(ctx context.Context, srv *Server, listenPort string) {
	var lc local.Client
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Second):
		}
		st, err := lc.Status(ctx)
		if err != nil {
			continue
		}
		var tsIP string
		for _, addr := range st.TailscaleIPs {
			if addr.Is4() {
				tsIP = addr.String()
				break
			}
		}
		if tsIP == "" && len(st.TailscaleIPs) > 0 {
			tsIP = st.TailscaleIPs[0].String()
		}
		if tsIP == "" {
			continue
		}
		newAddr := tsIP + ":" + listenPort
		srv.mu.Lock()
		srv.selfAddr = newAddr
		srv.mu.Unlock()
		log.Printf("agent: connected to Tailscale, self address: %s", newAddr)
		return
	}
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
