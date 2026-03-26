//go:build linux

package svcinstall

import "fmt"

func platformInstall(cfg Config, init InitSystem) error {
	switch init {
	case InitSystemd:
		return installSystemd(cfg)
	case InitSysVInit:
		return installSysVInit(cfg)
	default:
		return fmt.Errorf("unsupported init system: %s", init)
	}
}

func platformUninstall(cfg Config, init InitSystem) error {
	switch init {
	case InitSystemd:
		return uninstallSystemd(cfg)
	case InitSysVInit:
		return uninstallSysVInit(cfg)
	default:
		return fmt.Errorf("unsupported init system: %s", init)
	}
}
