package version

import "fmt"

// Version information injected at build time via ldflags.
// During development (plain go build), these retain their default values.
// When built via GoReleaser:
//   - Version: injected from git tag, with "+dev" suffix if working tree is dirty
//   - Commit: short git commit hash
var (
	// Version is the release version, e.g., "v1.0.0" or "v1.0.0+dev".
	// Defaults to "dev" when not injected.
	Version = "dev"

	// Commit is the short git commit hash.
	// Defaults to "unknown" when not injected.
	Commit = "unknown"
)

// Info returns a formatted version string suitable for display.
func Info() string {
	return fmt.Sprintf("Tailscale Metrics Discovery Agent %s (commit %s)", Version, Commit)
}
