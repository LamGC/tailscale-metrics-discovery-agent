package central

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/config"
	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/daemon"
	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/protocol"
	"github.com/LamGC/tailscale-metrics-discovery-agent/internal/svcinstall"
)

// CLIStatus queries the running Central daemon for its status.
func CLIStatus(socketPath string) error {
	if socketPath == "" {
		socketPath = daemon.DefaultCentralSocket()
	}
	c := daemon.NewClient(socketPath)
	var st protocol.StatusResponse
	if err := c.Get("/status", &st); err != nil {
		return fmt.Errorf("could not reach central daemon: %w", err)
	}
	fmt.Fprintln(os.Stdout, "Central daemon is running.")
	printTailscaleStatus(st.Tailscale)
	return nil
}

// CLIDiscover queries the running Central daemon for its discovered peers
// and prints a colour-coded table.
func CLIDiscover(socketPath string, useColor string) error {
	if socketPath == "" {
		socketPath = daemon.DefaultCentralSocket()
	}
	applyColorFlag(useColor)

	c := daemon.NewClient(socketPath)
	var resp protocol.PeersResponse
	if err := c.Get("/peers", &resp); err != nil {
		return fmt.Errorf("could not reach central daemon: %w", err)
	}
	if len(resp.Peers) == 0 {
		fmt.Println("No Agent peers discovered.")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "HOSTNAME\tTAILSCALE IP\tPORT\tSOURCE\tHEALTH\tSERVICES\tTAGS")
	for _, p := range resp.Peers {
		port := portFromURL(p.AgentURL)
		tags := strings.Join(p.Tags, ",")
		health := colorHealth(p.Health)
		source := colorSource(p.Source)
		svcCol := formatServiceCount(p)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			p.Hostname, p.TailscaleIP, port, source, health, svcCol, tags)
	}
	return tw.Flush()
}

// CLIHealth queries Central for service health status across all Agent peers.
func CLIHealth(socketPath, useColor string) error {
	if socketPath == "" {
		socketPath = daemon.DefaultCentralSocket()
	}
	applyColorFlag(useColor)

	c := daemon.NewClient(socketPath)
	type serviceHealth struct {
		Name   string                        `json:"name"`
		Type   protocol.ServiceType          `json:"type"`
		Health *protocol.ServiceHealthStatus `json:"health"`
	}
	type peerHealth struct {
		Hostname          string               `json:"hostname"`
		TailscaleIP       string               `json:"tailscale_ip"`
		AgentHealth       protocol.AgentHealth `json:"agent_health"`
		Services          []serviceHealth      `json:"services"`
		ServicesUpdatedAt *time.Time           `json:"services_updated_at,omitempty"`
	}
	var result []peerHealth
	if err := c.Get("/mgmt/health", &result); err != nil {
		return fmt.Errorf("could not reach central daemon: %w", err)
	}
	if len(result) == 0 {
		fmt.Println("No service health data available.")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "HOSTNAME\tTAILSCALE IP\tSERVICE\tTYPE\tSTATUS\tCODE\tMESSAGE")
	for _, p := range result {
		// Build hostname prefix, append stale note when agent is unreachable.
		hostPrefix := p.Hostname
		var staleNote string
		if p.AgentHealth != protocol.AgentHealthOK && p.ServicesUpdatedAt != nil {
			age := time.Since(*p.ServicesUpdatedAt).Round(time.Minute)
			staleNote = " " + color.YellowString("(stale %s ago)", formatDuration(age))
		}
		for _, svc := range p.Services {
			status := "-"
			code := "-"
			msg := "-"
			if h := svc.Health; h != nil {
				switch h.Status {
				case protocol.ServiceHealthHealthy:
					status = color.GreenString(string(h.Status))
				case protocol.ServiceHealthUnhealthy:
					status = color.RedString(string(h.Status))
				default:
					status = color.YellowString(string(h.Status))
				}
				if h.StatusCode > 0 {
					code = fmt.Sprintf("%d", h.StatusCode)
				}
				if h.Message != "" {
					msg = h.Message
				}
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				hostPrefix+staleNote, p.TailscaleIP, svc.Name, svc.Type, status, code, msg)
			hostPrefix = "" // only print hostname on first row per peer
			staleNote = ""
		}
	}
	return tw.Flush()
}

// HealthCmd returns the "tsd central health" subcommand.
func HealthCmd() *cobra.Command {
	var (
		socket   string
		useColor string
	)
	cmd := &cobra.Command{
		Use:   "health",
		Short: "Show service health check status across all Agent peers",
		RunE: func(cmd *cobra.Command, args []string) error {
			return CLIHealth(socket, useColor)
		},
	}
	cmd.Flags().StringVar(&socket, "socket", "", "Management socket path")
	cmd.Flags().StringVar(&useColor, "color", "auto", "Color output: auto, true, false")
	return cmd
}

// PeerCmd returns the "tsd central peer" subcommand.
func PeerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "peer",
		Short: "Manage manually configured Agent peers on Central",
	}
	cmd.AddCommand(peerAddCmd(), peerListCmd(), peerRemoveCmd())
	return cmd
}

func peerAddCmd() *cobra.Command {
	var (
		socket string
		port   int
		name   string
	)
	cmd := &cobra.Command{
		Use:   "add <address>",
		Short: "Add a manually configured Agent peer",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := daemon.NewClient(resolveCentralSocket(socket))
			return c.Post("/mgmt/peer/add", map[string]any{
				"address": args[0],
				"port":    port,
				"name":    name,
			}, nil)
		},
	}
	cmd.Flags().StringVar(&socket, "socket", "", "Management socket path")
	cmd.Flags().IntVarP(&port, "port", "p", 0, fmt.Sprintf("Agent port (default: %d)", protocol.DefaultAgentPort))
	cmd.Flags().StringVar(&name, "name", "", "Optional friendly name")
	return cmd
}

func peerListCmd() *cobra.Command {
	var (
		socket   string
		useColor string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List manually configured peers",
		RunE: func(cmd *cobra.Command, args []string) error {
			applyColorFlag(useColor)
			c := daemon.NewClient(resolveCentralSocket(socket))
			type peerItem struct {
				Name    string `json:"name"`
				Address string `json:"address"`
				Port    int    `json:"port"`
			}
			var items []peerItem
			if err := c.Get("/mgmt/peer/list", &items); err != nil {
				return fmt.Errorf("could not reach central daemon: %w", err)
			}
			if len(items) == 0 {
				fmt.Println("No manual peers configured.")
				return nil
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tADDRESS\tPORT")
			for _, it := range items {
				port := it.Port
				if port == 0 {
					port = protocol.DefaultAgentPort
				}
				fmt.Fprintf(tw, "%s\t%s\t%d\n", it.Name, it.Address, port)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&socket, "socket", "", "Management socket path")
	cmd.Flags().StringVar(&useColor, "color", "auto", "Color output: auto, true, false")
	return cmd
}

func peerRemoveCmd() *cobra.Command {
	var socket string
	cmd := &cobra.Command{
		Use:   "remove <address>",
		Short: "Remove a manually configured peer",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := daemon.NewClient(resolveCentralSocket(socket))
			return c.Post("/mgmt/peer/remove", map[string]any{"address": args[0]}, nil)
		},
	}
	cmd.Flags().StringVar(&socket, "socket", "", "Management socket path")
	return cmd
}

// --- helpers ---

func resolveCentralSocket(override string) string {
	if override != "" {
		return override
	}
	return daemon.DefaultCentralSocket()
}

// applyColorFlag applies the --color flag value to fatih/color.
func applyColorFlag(val string) {
	switch strings.ToLower(val) {
	case "false", "no", "0":
		color.NoColor = true
	case "true", "yes", "1":
		color.NoColor = false
		// "auto" (default): fatih/color detects TTY automatically; nothing to do.
	}
}

// colorHealth returns a coloured string for an AgentHealth value.
func colorHealth(h protocol.AgentHealth) string {
	switch h {
	case protocol.AgentHealthOK:
		return color.GreenString("ok")
	case protocol.AgentHealthOffline:
		return color.HiBlackString("offline")
	case protocol.AgentHealthTimeout:
		return color.YellowString("timeout")
	case protocol.AgentHealthUnauthorized:
		return color.RedString("unauthorized")
	default:
		return string(h)
	}
}

// colorSource returns a coloured string for a PeerSource value.
func colorSource(s protocol.PeerSource) string {
	switch s {
	case protocol.PeerSourceAuto:
		return color.CyanString("auto")
	case protocol.PeerSourceManual:
		return color.MagentaString("manual")
	default:
		return string(s)
	}
}

// printTailscaleStatus prints a human-readable Tailscale status block.
func printTailscaleStatus(ts *protocol.TailscaleStatus) {
	if ts == nil {
		return
	}
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "Tailscale:")
	if ts.Error != "" {
		fmt.Fprintf(os.Stdout, "  State:   %s (%s)\n", ts.BackendState, ts.Error)
		return
	}
	stateStr := ts.BackendState
	if ts.Connected {
		stateStr = color.GreenString(stateStr)
	} else {
		stateStr = color.YellowString(stateStr)
	}
	fmt.Fprintf(os.Stdout, "  State:   %s\n", stateStr)
	if ts.Account != "" {
		fmt.Fprintf(os.Stdout, "  Account: %s\n", ts.Account)
	}
	if ts.Hostname != "" {
		fmt.Fprintf(os.Stdout, "  Node:    %s\n", ts.Hostname)
	}
	for _, ip := range ts.TailscaleIPs {
		fmt.Fprintf(os.Stdout, "  IP:      %s\n", ip)
	}
	for _, tag := range ts.Tags {
		fmt.Fprintf(os.Stdout, "  Tag:     %s\n", tag)
	}
}

// formatServiceCount formats the Services count for the discover table.
// If the data is stale (peer unreachable, showing cached data) it appends
// a "(stale X ago)" annotation coloured yellow.
func formatServiceCount(p protocol.PeerInfo) string {
	if p.ServicesUpdatedAt == nil {
		return "-"
	}
	count := fmt.Sprintf("%d", len(p.Services))
	if p.Health != protocol.AgentHealthOK {
		age := time.Since(*p.ServicesUpdatedAt).Round(time.Minute)
		return count + " " + color.YellowString("(stale %s ago)", formatDuration(age))
	}
	return count
}

// formatDuration formats a duration as a human-friendly short string, e.g.
// "5m", "2h30m", "1d4h".
func formatDuration(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	if days > 0 && hours > 0 {
		return fmt.Sprintf("%dd%dh", days, hours)
	}
	if days > 0 {
		return fmt.Sprintf("%dd", days)
	}
	if mins > 0 {
		return fmt.Sprintf("%dh%dm", hours, mins)
	}
	return fmt.Sprintf("%dh", hours)
}

// InstallCmd returns the "tsd central install" command.
func InstallCmd() *cobra.Command {
	var (
		configFile string
		initSystem string
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install tsd central as a system service",
		RunE: func(cmd *cobra.Command, args []string) error {
			binary, err := os.Executable()
			if err != nil {
				return fmt.Errorf("could not determine binary path: %w", err)
			}

			cfgPath := configFile
			if cfgPath == "" {
				cfgPath = config.DefaultConfigFile("central")
			}

			cfg := svcinstall.Config{
				Role:       svcinstall.RoleCentral,
				BinaryPath: binary,
				ConfigFile: cfgPath,
				Init:       svcinstall.InitSystem(initSystem),
			}
			return svcinstall.Install(cfg)
		},
	}
	cmd.Flags().StringVar(&configFile, "config", "", "Config file path (default: /etc/tsd/central.toml for root, ~/.tsd/central.toml otherwise)")
	cmd.Flags().StringVar(&initSystem, "init", "auto", "Init system: auto, systemd, sysvinit, launchd, rc.d")
	return cmd
}

// UninstallCmd returns the "tsd central uninstall" command.
func UninstallCmd() *cobra.Command {
	var initSystem string
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove tsd central from system services",
		RunE: func(cmd *cobra.Command, args []string) error {
			binary, err := os.Executable()
			if err != nil {
				return fmt.Errorf("could not determine binary path: %w", err)
			}

			cfg := svcinstall.Config{
				Role:       svcinstall.RoleCentral,
				BinaryPath: binary,
				ConfigFile: "", // not needed for uninstall
				Init:       svcinstall.InitSystem(initSystem),
			}
			return svcinstall.Uninstall(cfg)
		},
	}
	cmd.Flags().StringVar(&initSystem, "init", "auto", "Init system: auto, systemd, sysvinit, launchd, rc.d")
	return cmd
}

// --- helpers ---

// portFromURL extracts the port from an "http://host:port" URL string.
func portFromURL(u string) string {
	// Find last colon.
	for i := len(u) - 1; i >= 0; i-- {
		if u[i] == ':' {
			return u[i+1:]
		}
	}
	return "?"
}
