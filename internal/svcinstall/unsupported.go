//go:build !linux && !darwin && !freebsd

package svcinstall

import "fmt"

func platformInstall(cfg Config, init InitSystem) error {
	return fmt.Errorf("service install not yet supported on this platform")
}

func platformUninstall(cfg Config, init InitSystem) error {
	return fmt.Errorf("service uninstall not yet supported on this platform")
}

func platformDetect() InitSystem {
	return ""
}
