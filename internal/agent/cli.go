package agent

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/lamgc/tailscale-service-discovery-agent/internal/daemon"
	"github.com/lamgc/tailscale-service-discovery-agent/internal/protocol"
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
		socket  string
		targets []string
		labels  []string
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
				"name":    args[0],
				"targets": targets,
				"labels":  lbs,
			}, nil)
		},
	}
	cmd.Flags().StringVar(&socket, "socket", "", "Management socket path")
	cmd.Flags().StringArrayVarP(&targets, "target", "t", nil, "Target address(es), e.g. host:9100 (repeatable)")
	cmd.Flags().StringArrayVarP(&labels, "label", "l", nil, "Label in key=value format (repeatable)")
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
		socket string
		labels []string
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
				"name":   args[0],
				"labels": lbs,
			}, nil)
		},
	}
	cmd.Flags().StringVar(&socket, "socket", "", "Management socket path")
	cmd.Flags().StringArrayVarP(&labels, "label", "l", nil, "Label in key=value format (repeatable)")
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
		socket   string
		target   string
		authType string
		token    string
		username string
		password string
		labels   []string
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
				"name":      args[0],
				"target":    target,
				"auth_type": authType,
				"token":     token,
				"username":  username,
				"password":  password,
				"labels":    lbs,
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
	fmt.Fprintln(tw, "NAME\tTYPE\tTARGETS")
	for _, e := range entries {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", e.Name, e.Type, strings.Join(e.Target.Targets, ", "))
	}
	return tw.Flush()
}
