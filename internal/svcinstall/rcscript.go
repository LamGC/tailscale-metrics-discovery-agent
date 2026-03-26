//go:build freebsd

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

//go:embed templates/rcscript.sh.tmpl
var rcScriptTemplate string

func platformInstall(cfg Config, init InitSystem) error {
	// Build template data (FreeBSD rc.d uses underscores in names)
	role := string(cfg.Role)
	rcName := "tsd_" + role
	data := templateData{
		BinaryPath:  cfg.BinaryPath,
		ConfigFile:  cfg.ConfigFile,
		ServiceName: "tsd-" + role,
		RcName:      rcName,
		Role:        role,
		Description: "Tailscale Metrics Discovery — " + role,
	}

	// Render template
	tmpl, err := template.New("rcscript").Parse(rcScriptTemplate)
	if err != nil {
		return fmt.Errorf("failed to parse rc.d template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("failed to render rc.d template: %w", err)
	}

	// Write to /usr/local/etc/rc.d/
	rcPath := filepath.Join("/usr/local/etc/rc.d", rcName)
	if err := os.WriteFile(rcPath, buf.Bytes(), 0755); err != nil {
		return fmt.Errorf("failed to write rc.d script: %w", err)
	}

	// Enable in rc.conf using sysrc
	if err := exec.Command("sysrc", rcName+"_enable=YES").Run(); err != nil {
		return fmt.Errorf("sysrc failed: %w", err)
	}

	fmt.Printf("Service %s installed and enabled at %s\n", rcName, rcPath)
	return nil
}

func platformUninstall(cfg Config, init InitSystem) error {
	role := string(cfg.Role)
	rcName := "tsd_" + role
	rcPath := filepath.Join("/usr/local/etc/rc.d", rcName)

	// Stop the service
	_ = exec.Command("service", rcName, "stop").Run()

	// Disable in rc.conf
	_ = exec.Command("sysrc", "-x", rcName+"_enable").Run()

	// Remove the rc.d script
	if err := os.Remove(rcPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove rc.d script: %w", err)
	}

	fmt.Printf("Service %s removed\n", rcName)
	return nil
}

func platformDetect() InitSystem {
	return InitRcD
}
