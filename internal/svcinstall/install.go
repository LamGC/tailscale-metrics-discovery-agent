package svcinstall

import (
	"fmt"
	"html"
	"os"
	"strconv"
	"strings"
)

// Role represents the service role (agent or central).
type Role string

const (
	RoleAgent   Role = "agent"
	RoleCentral Role = "central"
)

// InitSystem represents the system initialization manager.
type InitSystem string

const (
	InitAuto     InitSystem = "auto"
	InitSystemd  InitSystem = "systemd"
	InitSysVInit InitSystem = "sysvinit"
	InitLaunchd  InitSystem = "launchd"
	InitRcD      InitSystem = "rc.d"
)

// Config holds installation parameters.
type Config struct {
	Role       Role
	BinaryPath string     // absolute path to tsd binary
	ConfigFile string     // absolute path to role config file
	Init       InitSystem // init system; "auto" triggers detection
}

// templateData holds variables for service template rendering.
type templateData struct {
	BinaryPath        string
	ConfigFile        string
	SystemdExecStart  string
	ShellBinaryPath   string
	ShellConfigFile   string
	LaunchdBinaryPath string
	LaunchdConfigFile string
	ServiceName       string // "tsd-agent" or "tsd-central"
	RcName            string // "tsd_agent" or "tsd_central"
	Label             string // "net.lamgc.tsd-agent" etc (macOS)
	Description       string
	Role              string
}

func enrichTemplateData(data templateData, cfg Config) templateData {
	role := string(cfg.Role)
	data.SystemdExecStart = systemdCommandLine(cfg.BinaryPath, role, "daemon", "--config", cfg.ConfigFile)
	data.ShellBinaryPath = shellQuote(cfg.BinaryPath)
	data.ShellConfigFile = shellQuote(cfg.ConfigFile)
	data.LaunchdBinaryPath = html.EscapeString(cfg.BinaryPath)
	data.LaunchdConfigFile = html.EscapeString(cfg.ConfigFile)
	return data
}

func systemdCommandLine(args ...string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, systemdQuoteArg(arg))
	}
	return strings.Join(quoted, " ")
}

func systemdQuoteArg(arg string) string {
	return strings.ReplaceAll(strconv.Quote(arg), "%", "%%")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// Install registers the tsd daemon as a system service.
// Requires root/admin privileges.
func Install(cfg Config) error {
	if os.Getuid() != 0 {
		return fmt.Errorf("install requires root privileges; re-run with sudo")
	}

	init := cfg.Init
	if init == InitAuto {
		init = Detect()
	}

	return platformInstall(cfg, init)
}

// Uninstall removes the tsd daemon from system services.
// Requires root/admin privileges.
func Uninstall(cfg Config) error {
	if os.Getuid() != 0 {
		return fmt.Errorf("uninstall requires root privileges; re-run with sudo")
	}

	init := cfg.Init
	if init == InitAuto {
		init = Detect()
	}

	return platformUninstall(cfg, init)
}

// Detect returns the detected init system on the current platform.
// On Linux, performs actual detection; on other platforms returns a fixed value.
func Detect() InitSystem {
	return platformDetect()
}

// platform-specific implementations defined in platform-specific files
// platformInstall, platformUninstall, platformDetect are defined in:
//   - systemd.go, sysvinit.go, detect_linux.go (with //go:build linux)
//   - launchd.go (with //go:build darwin)
//   - rcscript.go (with //go:build freebsd)
//   - unsupported.go (with //go:build for other platforms)
