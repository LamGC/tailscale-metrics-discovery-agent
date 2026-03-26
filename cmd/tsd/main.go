package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/agent"
	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/central"
	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/config"
)

func main() {
	root := &cobra.Command{
		Use:   "tsd",
		Short: "Tailscale Service Discovery — Prometheus http_sd bridge",
	}

	root.AddCommand(centralCmd())
	root.AddCommand(agentCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func centralCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "central",
		Short: "Manage the Central service-discovery daemon",
	}

	var cfgFile string
	daemonCmd := &cobra.Command{
		Use:   "daemon",
		Short: "Start the Central daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return central.RunDaemon(cfgFile)
		},
	}
	daemonCmd.Flags().StringVarP(&cfgFile, "config", "c", config.DefaultConfigFile("central"), "Config file path")

	var statusSocket string
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show Central daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return central.CLIStatus(statusSocket)
		},
	}
	statusCmd.Flags().StringVar(&statusSocket, "socket", "", "Management socket path")

	var (
		discoverSocket string
		discoverColor  string
	)
	discoverCmd := &cobra.Command{
		Use:   "discover",
		Short: "List currently discovered Agent peers with health status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return central.CLIDiscover(discoverSocket, discoverColor)
		},
	}
	discoverCmd.Flags().StringVar(&discoverSocket, "socket", "", "Management socket path")
	discoverCmd.Flags().StringVar(&discoverColor, "color", "auto", "Color output: auto, true, false")

	cmd.AddCommand(daemonCmd, statusCmd, discoverCmd)
	cmd.AddCommand(central.PeerCmd())
	cmd.AddCommand(central.HealthCmd())
	cmd.AddCommand(central.InstallCmd())
	cmd.AddCommand(central.UninstallCmd())
	return cmd
}

func agentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Manage the Agent daemon",
	}

	var cfgFile string
	daemonCmd := &cobra.Command{
		Use:   "daemon",
		Short: "Start the Agent daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return agent.RunDaemon(cfgFile)
		},
	}
	daemonCmd.Flags().StringVarP(&cfgFile, "config", "c", config.DefaultConfigFile("agent"), "Config file path")

	var statusSocket string
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show Agent daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return agent.CLIStatus(statusSocket)
		},
	}
	statusCmd.Flags().StringVar(&statusSocket, "socket", "", "Management socket path")

	cmd.AddCommand(daemonCmd, statusCmd)
	cmd.AddCommand(agent.ServiceCmd())
	cmd.AddCommand(agent.BucketCmd())
	cmd.AddCommand(agent.ProxyCmd())
	cmd.AddCommand(agent.InstallCmd())
	cmd.AddCommand(agent.UninstallCmd())

	return cmd
}
