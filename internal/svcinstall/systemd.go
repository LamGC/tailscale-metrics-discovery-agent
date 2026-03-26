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

//go:embed templates/systemd.service.tmpl
var systemdTemplate string

func installSystemd(cfg Config) error {
	// Build template data
	data := templateData{
		BinaryPath:  cfg.BinaryPath,
		ConfigFile:  cfg.ConfigFile,
		ServiceName: "tsd-" + string(cfg.Role),
		Role:        string(cfg.Role),
		Description: "Tailscale Metrics Discovery — " + string(cfg.Role),
	}

	// Render template
	tmpl, err := template.New("systemd").Parse(systemdTemplate)
	if err != nil {
		return fmt.Errorf("failed to parse systemd template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("failed to render systemd template: %w", err)
	}

	// Write to /etc/systemd/system/
	unitPath := filepath.Join("/etc/systemd/system", data.ServiceName+".service")
	if err := os.WriteFile(unitPath, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write systemd unit file: %w", err)
	}

	// Reload systemd and enable service
	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("systemctl daemon-reload failed: %w", err)
	}

	if err := exec.Command("systemctl", "enable", data.ServiceName+".service").Run(); err != nil {
		return fmt.Errorf("systemctl enable failed: %w", err)
	}

	fmt.Printf("Service %s installed and enabled at %s\n", data.ServiceName, unitPath)
	return nil
}

func uninstallSystemd(cfg Config) error {
	serviceName := "tsd-" + string(cfg.Role)
	unitPath := filepath.Join("/etc/systemd/system", serviceName+".service")

	// Stop the service
	_ = exec.Command("systemctl", "stop", serviceName+".service").Run()

	// Disable the service
	_ = exec.Command("systemctl", "disable", serviceName+".service").Run()

	// Remove the unit file
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove systemd unit file: %w", err)
	}

	// Reload systemd
	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("systemctl daemon-reload failed: %w", err)
	}

	fmt.Printf("Service %s removed\n", serviceName)
	return nil
}
