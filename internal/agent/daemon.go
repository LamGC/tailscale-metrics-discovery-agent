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
	"tailscale.com/ipn"

	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/config"
	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/daemon"
	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/protocol"
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

	// Start a persistent Tailscale watcher that handles:
	// 1. Detecting/updating self IP for SD targets (replaces watchTailscaleConnect)
	// 2. Loading nodeAttrs for ACL-based auth (when node_attrs is enabled)
	go watchTailscale(ctx, srv, listenPort, cfg.Server.NodeAttrs)

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

// watchTailscale listens on the Tailscale IPN bus to:
//  1. Detect and update the self Tailscale IP for SD targets
//  2. Reload nodeAttrs for ACL Tag-based auth on connect and ACL policy changes
//
// It auto-reconnects on errors with a brief pause. Blocks until ctx is cancelled.
func watchTailscale(ctx context.Context, srv *Server, listenPort string, nodeAttrs bool) {
	var lc local.Client

	lastFailed := false
	for ctx.Err() == nil {
		const mask = ipn.NotifyInitialNetMap | ipn.NotifyRateLimit
		watcher, err := lc.WatchIPNBus(ctx, mask)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if !lastFailed {
				log.Printf("agent: WatchIPNBus unavailable (%v); will retry", err)
			}
			lastFailed = true
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		// Connected.
		if lastFailed {
			log.Printf("agent: reconnected to Tailscale IPN bus")
		}
		lastFailed = false

		// On connect: detect IP and load nodeAttrs.
		detectAndSetSelfIP(ctx, &lc, srv, listenPort)
		if nodeAttrs {
			srv.LoadNodeAttrs(ctx)
		}

		// Watch for netmap changes.
		for {
			n, err := watcher.Next()
			if err != nil {
				watcher.Close()
				if ctx.Err() != nil {
					return
				}
				log.Printf("agent: WatchIPNBus error: %v; reconnecting", err)
				break
			}
			if n.NetMap != nil {
				detectAndSetSelfIP(ctx, &lc, srv, listenPort)
				if nodeAttrs {
					srv.LoadNodeAttrs(ctx)
				}
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

// detectAndSetSelfIP queries Tailscale status and updates srv.selfAddr.
func detectAndSetSelfIP(ctx context.Context, lc *local.Client, srv *Server, listenPort string) {
	if listenPort == "" {
		return
	}
	st, err := lc.Status(ctx)
	if err != nil {
		return
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
		return
	}
	newAddr := tsIP + ":" + listenPort
	srv.mu.Lock()
	old := srv.selfAddr
	srv.selfAddr = newAddr
	srv.mu.Unlock()
	if old != newAddr {
		log.Printf("agent: Tailscale self address: %s", newAddr)
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
