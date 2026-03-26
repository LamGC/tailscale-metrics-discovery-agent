//go:build linux

package svcinstall

import (
	"os"
	"os/exec"
)

func platformDetect() InitSystem {
	// Check if /run/systemd/private exists
	if _, err := os.Stat("/run/systemd/private"); err == nil {
		return InitSystemd
	}

	// Check if systemctl is available and reports a running systemd
	if cmd := exec.Command("systemctl", "is-system-running"); cmd.Run() == nil {
		return InitSystemd
	}

	// Fallback to sysvinit
	return InitSysVInit
}
