package central

import (
	"context"
	"fmt"
	"log"
	"os/signal"
	"syscall"

	"github.com/lamgc/tailscale-service-discovery-agent/internal/config"
	"github.com/lamgc/tailscale-service-discovery-agent/internal/daemon"
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

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(ctx)
	}()

	select {
	case <-ctx.Done():
		log.Println("central: shutting down...")
		shutCtx, cancel := context.WithCancel(context.Background())
		cancel() // immediate shutdown
		srv.Shutdown(shutCtx)
		daemon.Cleanup(cfg.Management.Socket)
		return nil
	case err := <-errCh:
		return err
	}
}
