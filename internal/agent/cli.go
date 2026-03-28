package agent

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

// ServiceCmd returns the "tsd agent service" subcommand.
func ServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Manage static services on the Agent",
	}
	cmd.AddCommand(serviceAddCmd(), serviceListCmd(), serviceRemoveCmd())
	return cmd
}

func serviceAddCmd() *cobra.Command {
	var (
		socket         string
		targets        []string
		labels         []string
		healthcheckURL string
	)
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a static service target",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			lbs, err := parseLabels(labels)
			if err != nil {
				return err
			}
			c := daemon.NewClient(resolveSocket(socket))
			return c.Post("/mgmt/service/add", map[string]any{
				"name":            args[0],
				"targets":         targets,
				"labels":          lbs,
				"healthcheck_url": healthcheckURL,
			}, nil)
		},
	}
	cmd.Flags().StringVar(&socket, "socket", "", "Management socket path")
	cmd.Flags().StringArrayVarP(&targets, "target", "t", nil, "Target address(es), e.g. host:9100 (repeatable)")
	cmd.Flags().StringArrayVarP(&labels, "label", "l", nil, "Label in key=value format (repeatable)")
	cmd.Flags().StringVar(&healthcheckURL, "healthcheck-url", "", "URL to GET periodically; 2xx = healthy")
	_ = cmd.MarkFlagRequired("target")
	return cmd
}

func serviceListCmd() *cobra.Command {
	var socket string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all registered services",
		RunE: func(cmd *cobra.Command, args []string) error {
			return listServices(resolveSocket(socket))
		},
	}
	cmd.Flags().StringVar(&socket, "socket", "", "Management socket path")
	return cmd
}

func serviceRemoveCmd() *cobra.Command {
	var socket string
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a static service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := daemon.NewClient(resolveSocket(socket))
			return c.Post("/mgmt/service/remove", map[string]any{"name": args[0]}, nil)
		},
	}
	cmd.Flags().StringVar(&socket, "socket", "", "Management socket path")
	return cmd
}

// BucketCmd returns the "tsd agent bucket" subcommand.
func BucketCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bucket",
		Short: "Manage Push Buckets on the Agent",
	}
	cmd.AddCommand(bucketAddCmd(), bucketListCmd(), bucketRemoveCmd(), bucketClearCmd())
	return cmd
}

func bucketAddCmd() *cobra.Command {
	var (
		socket         string
		labels         []string
		healthcheckURL string
	)
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Create a new push bucket",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			lbs, err := parseLabels(labels)
			if err != nil {
				return err
			}
			c := daemon.NewClient(resolveSocket(socket))
			return c.Post("/mgmt/bucket/add", map[string]any{
				"name":            args[0],
				"labels":          lbs,
				"healthcheck_url": healthcheckURL,
			}, nil)
		},
	}
	cmd.Flags().StringVar(&socket, "socket", "", "Management socket path")
	cmd.Flags().StringArrayVarP(&labels, "label", "l", nil, "Label in key=value format (repeatable)")
	cmd.Flags().StringVar(&healthcheckURL, "healthcheck-url", "", "URL to GET periodically; 2xx = healthy")
	return cmd
}

func bucketListCmd() *cobra.Command {
	var socket string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all services (shows bucket entries)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return listServices(resolveSocket(socket))
		},
	}
	cmd.Flags().StringVar(&socket, "socket", "", "Management socket path")
	return cmd
}

func bucketRemoveCmd() *cobra.Command {
	var socket string
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a push bucket",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := daemon.NewClient(resolveSocket(socket))
			return c.Post("/mgmt/bucket/remove", map[string]any{"name": args[0]}, nil)
		},
	}
	cmd.Flags().StringVar(&socket, "socket", "", "Management socket path")
	return cmd
}

func bucketClearCmd() *cobra.Command {
	var socket string
	cmd := &cobra.Command{
		Use:   "clear <name>",
		Short: "Clear all pushed metrics from a bucket",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := daemon.NewClient(resolveSocket(socket))
			return c.Post("/mgmt/bucket/clear", map[string]any{"name": args[0]}, nil)
		},
	}
	cmd.Flags().StringVar(&socket, "socket", "", "Management socket path")
	return cmd
}

// ProxyCmd returns the "tsd agent proxy" subcommand.
func ProxyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "proxy",
		Short: "Manage Proxy endpoints on the Agent",
	}
	cmd.AddCommand(proxyAddCmd(), proxyListCmd(), proxyRemoveCmd())
	return cmd
}

func proxyAddCmd() *cobra.Command {
	var (
		socket         string
		target         string
		authType       string
		token          string
		username       string
		password       string
		labels         []string
		healthcheckURL string
	)
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Create a proxy endpoint that Agent scrapes on behalf of Prometheus",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			lbs, err := parseLabels(labels)
			if err != nil {
				return err
			}
			c := daemon.NewClient(resolveSocket(socket))
			return c.Post("/mgmt/proxy/add", map[string]any{
				"name":            args[0],
				"target":          target,
				"auth_type":       authType,
				"token":           token,
				"username":        username,
				"password":        password,
				"labels":          lbs,
				"healthcheck_url": healthcheckURL,
			}, nil)
		},
	}
	cmd.Flags().StringVar(&socket, "socket", "", "Management socket path")
	cmd.Flags().StringVarP(&target, "target", "t", "", "Upstream metrics URL (e.g. http://localhost:9100/metrics)")
	cmd.Flags().StringVar(&authType, "auth-type", "none", "Auth type: none, bearer, basic")
	cmd.Flags().StringVar(&token, "token", "", "Bearer token (for --auth-type bearer)")
	cmd.Flags().StringVar(&username, "username", "", "Username (for --auth-type basic)")
	cmd.Flags().StringVar(&password, "password", "", "Password (for --auth-type basic)")
	cmd.Flags().StringArrayVarP(&labels, "label", "l", nil, "Label in key=value format (repeatable)")
	cmd.Flags().StringVar(&healthcheckURL, "healthcheck-url", "", "URL to GET periodically; 2xx = healthy")
	_ = cmd.MarkFlagRequired("target")
	return cmd
}

func proxyListCmd() *cobra.Command {
	var socket string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all services (shows proxy entries)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return listServices(resolveSocket(socket))
		},
	}
	cmd.Flags().StringVar(&socket, "socket", "", "Management socket path")
	return cmd
}

func proxyRemoveCmd() *cobra.Command {
	var socket string
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a proxy endpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := daemon.NewClient(resolveSocket(socket))
			return c.Post("/mgmt/proxy/remove", map[string]any{"name": args[0]}, nil)
		},
	}
	cmd.Flags().StringVar(&socket, "socket", "", "Management socket path")
	return cmd
}

// InstallCmd returns the "tsd agent install" command.
func InstallCmd() *cobra.Command {
	var (
		configFile string
		initSystem string
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install tsd agent as a system service",
		RunE: func(cmd *cobra.Command, args []string) error {
			binary, err := os.Executable()
			if err != nil {
				return fmt.Errorf("could not determine binary path: %w", err)
			}

			cfgPath := configFile
			if cfgPath == "" {
				cfgPath = config.DefaultConfigFile("agent")
			}

			cfg := svcinstall.Config{
				Role:       svcinstall.RoleAgent,
				BinaryPath: binary,
				ConfigFile: cfgPath,
				Init:       svcinstall.InitSystem(initSystem),
			}
			return svcinstall.Install(cfg)
		},
	}
	cmd.Flags().StringVar(&configFile, "config", "", "Config file path (default: /etc/tsd/agent.toml for root, ~/.tsd/agent.toml otherwise)")
	cmd.Flags().StringVar(&initSystem, "init", "auto", "Init system: auto, systemd, sysvinit, launchd, rc.d")
	return cmd
}

// UninstallCmd returns the "tsd agent uninstall" command.
func UninstallCmd() *cobra.Command {
	var initSystem string
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove tsd agent from system services",
		RunE: func(cmd *cobra.Command, args []string) error {
			binary, err := os.Executable()
			if err != nil {
				return fmt.Errorf("could not determine binary path: %w", err)
			}

			cfg := svcinstall.Config{
				Role:       svcinstall.RoleAgent,
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

// printAgentTailscaleStatus prints a human-readable Tailscale status block.
func printAgentTailscaleStatus(ts *protocol.TailscaleStatus) {
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

// printClientAccess prints client access information from the Agent status.
func printClientAccess(clients []protocol.ClientAccessInfo) {
	if len(clients) == 0 {
		return
	}
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "Recent clients:")
	for _, c := range clients {
		age := time.Since(c.LastSeen).Round(time.Second)
		if c.NodeName != "" {
			fmt.Fprintf(os.Stdout, "  %s (%s)  last seen %s ago\n", c.NodeName, c.IP, formatAge(age))
		} else {
			fmt.Fprintf(os.Stdout, "  %s  last seen %s ago\n", c.IP, formatAge(age))
		}
	}
}

// formatAge formats a duration as a human-friendly short string.
func formatAge(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	hours := int(d.Hours())
	days := hours / 24
	if days > 0 {
		h := hours % 24
		if h > 0 {
			return fmt.Sprintf("%dd%dh", days, h)
		}
		return fmt.Sprintf("%dd", days)
	}
	m := int(d.Minutes()) % 60
	if m > 0 {
		return fmt.Sprintf("%dh%dm", hours, m)
	}
	return fmt.Sprintf("%dh", hours)
}

// --- helpers ---

func resolveSocket(override string) string {
	if override != "" {
		return override
	}
	return daemon.DefaultAgentSocket()
}

func parseLabels(raw []string) (map[string]string, error) {
	lbs := make(map[string]string, len(raw))
	for _, kv := range raw {
		idx := strings.IndexByte(kv, '=')
		if idx < 0 {
			return nil, fmt.Errorf("invalid label %q: expected key=value", kv)
		}
		lbs[kv[:idx]] = kv[idx+1:]
	}
	return lbs, nil
}

func listServices(socketPath string) error {
	c := daemon.NewClient(socketPath)
	var entries []protocol.ServiceEntry
	if err := c.Get("/services", &entries); err != nil {
		return fmt.Errorf("could not reach agent daemon: %w", err)
	}
	if len(entries) == 0 {
		fmt.Println("No services registered.")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tTYPE\tHEALTH\tTARGETS")
	for _, e := range entries {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			e.Name, e.Type, colorServiceHealth(e.Health),
			strings.Join(e.Target.Targets, ", "))
	}
	return tw.Flush()
}

// colorServiceHealth returns a coloured health status string.
func colorServiceHealth(h *protocol.ServiceHealthStatus) string {
	if h == nil {
		return "-"
	}
	switch h.Status {
	case protocol.ServiceHealthHealthy:
		if h.StatusCode > 0 {
			return color.GreenString("healthy (%d)", h.StatusCode)
		}
		return color.GreenString("healthy")
	case protocol.ServiceHealthUnhealthy:
		if h.StatusCode > 0 {
			return color.RedString("unhealthy (%d)", h.StatusCode)
		}
		if h.Message != "" {
			return color.RedString("unhealthy: %s", h.Message)
		}
		return color.RedString("unhealthy")
	default:
		return color.YellowString("unknown")
	}
}
