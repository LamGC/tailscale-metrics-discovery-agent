package central

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/lamgc/tailscale-service-discovery-agent/internal/daemon"
	"github.com/lamgc/tailscale-service-discovery-agent/internal/protocol"
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
	fmt.Fprintln(tw, "HOSTNAME\tTAILSCALE IP\tPORT\tSOURCE\tHEALTH\tTAGS")
	for _, p := range resp.Peers {
		port := portFromURL(p.AgentURL)
		tags := strings.Join(p.Tags, ",")
		health := colorHealth(p.Health)
		source := colorSource(p.Source)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			p.Hostname, p.TailscaleIP, port, source, health, tags)
	}
	return tw.Flush()
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
