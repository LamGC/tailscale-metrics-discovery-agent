//go:build linux

package svcinstall

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"
)

//go:embed templates/sysvinit.sh.tmpl
var sysvInitTemplate string

func installSysVInit(cfg Config) error {
	// Build template data
	data := templateData{
		BinaryPath:  cfg.BinaryPath,
		ConfigFile:  cfg.ConfigFile,
		ServiceName: "tsd-" + string(cfg.Role),
		Role:        string(cfg.Role),
		Description: "Tailscale Metrics Discovery — " + string(cfg.Role),
	}

	// Render template
	tmpl, err := template.New("sysvinit").Parse(sysvInitTemplate)
	if err != nil {
		return fmt.Errorf("failed to parse sysvinit template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("failed to render sysvinit template: %w", err)
	}

	// Write to /etc/init.d/
	initPath := filepath.Join("/etc/init.d", data.ServiceName)
	if err := os.WriteFile(initPath, buf.Bytes(), 0755); err != nil {
		return fmt.Errorf("failed to write sysvinit script: %w", err)
	}

	// Try to register with update-rc.d first (Debian/Ubuntu)
	if err := exec.Command("update-rc.d", data.ServiceName, "defaults").Run(); err != nil {
		// Fall back to chkconfig (RHEL/CentOS) if update-rc.d is not available
		_ = exec.Command("chkconfig", "--add", data.ServiceName).Run()
	}

	fmt.Printf("Service %s installed at %s\n", data.ServiceName, initPath)
	return nil
}

func uninstallSysVInit(cfg Config) error {
	serviceName := "tsd-" + string(cfg.Role)
	initPath := filepath.Join("/etc/init.d", serviceName)

	// Stop the service
	_ = exec.Command(initPath, "stop").Run()

	// Try to unregister with update-rc.d (Debian/Ubuntu)
	if err := exec.Command("update-rc.d", serviceName, "remove").Run(); err != nil {
		// Fall back to chkconfig (RHEL/CentOS)
		_ = exec.Command("chkconfig", "--del", serviceName).Run()
	}

	// Remove the init script
	if err := os.Remove(initPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove sysvinit script: %w", err)
	}

	fmt.Printf("Service %s removed\n", serviceName)
	return nil
}
