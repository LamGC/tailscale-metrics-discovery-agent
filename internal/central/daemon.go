package central

import (
	"context"
	"fmt"
	"log"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/config"
	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/daemon"
)

// RunDaemon loads config from cfgFile and starts the Central server.
// It blocks until SIGINT or SIGTERM is received.
func RunDaemon(cfgFile string) error {
	cfg, err := config.LoadCentralConfig(cfgFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if cfg.Management.Socket == "" {
		cfg.Management.Socket = daemon.DefaultCentralSocket()
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv := NewServer(cfg)
	srv.cfgFile = cfgFile

	// Read nodeAttrs from Tailscale before starting the collector loop.
	if cfg.Discovery.NodeAttrs {
		srv.col.discoverer.RefreshSelfAttrs(ctx)
	}

	// Compute the peer cache file path (same directory as config file).
	peersFile := peerCacheFile(cfgFile)
	srv.col.peersFile = peersFile
	srv.col.loadPeerCache()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(ctx)
	}()

	select {
	case <-ctx.Done():
		log.Println("central: shutting down...")
		shutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx)
		daemon.Cleanup(cfg.Management.Socket)
		return nil
	case err := <-errCh:
		return err
	}
}

// peerCacheFile returns the peer history cache path for the given config file.
// It sits in the same directory as the config file.
func peerCacheFile(cfgFile string) string {
	if cfgFile == "" {
		return filepath.Join(config.ConfigDir("central"), "central-peers.json")
	}
	return filepath.Join(filepath.Dir(cfgFile), "central-peers.json")
}
