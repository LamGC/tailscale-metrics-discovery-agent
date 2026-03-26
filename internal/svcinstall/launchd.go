//go:build darwin

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

//go:embed templates/launchd.plist.tmpl
var launchdTemplate string

func platformInstall(cfg Config, init InitSystem) error {
	// Build template data
	role := string(cfg.Role)
	data := templateData{
		BinaryPath:  cfg.BinaryPath,
		ConfigFile:  cfg.ConfigFile,
		ServiceName: "tsd-" + role,
		Role:        role,
		Label:       "net.lamgc.tsd-" + role,
		Description: "Tailscale Metrics Discovery — " + role,
	}

	// Render template
	tmpl, err := template.New("launchd").Parse(launchdTemplate)
	if err != nil {
		return fmt.Errorf("failed to parse launchd template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("failed to render launchd template: %w", err)
	}

	// Write to /Library/LaunchDaemons/
	plistPath := filepath.Join("/Library/LaunchDaemons", data.Label+".plist")
	if err := os.WriteFile(plistPath, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write launchd plist: %w", err)
	}

	// Load the plist with launchctl
	if err := exec.Command("launchctl", "load", "-w", plistPath).Run(); err != nil {
		return fmt.Errorf("launchctl load failed: %w", err)
	}

	fmt.Printf("Service %s installed and loaded at %s\n", data.Label, plistPath)
	return nil
}

func platformUninstall(cfg Config, init InitSystem) error {
	role := string(cfg.Role)
	label := "net.lamgc.tsd-" + role
	plistPath := filepath.Join("/Library/LaunchDaemons", label+".plist")

	// Unload the plist
	_ = exec.Command("launchctl", "unload", "-w", plistPath).Run()

	// Remove the plist file
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove launchd plist: %w", err)
	}

	fmt.Printf("Service %s removed\n", label)
	return nil
}

func platformDetect() InitSystem {
	return InitLaunchd
}
