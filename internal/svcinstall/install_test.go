package svcinstall

import "testing"

func TestTemplateEscapingHelpers(t *testing.T) {
	if got, want := shellQuote("/opt/tsd bin/tsd's"), "'/opt/tsd bin/tsd'\\''s'"; got != want {
		t.Fatalf("shellQuote = %q, want %q", got, want)
	}
	if got, want := systemdQuoteArg("/opt/tsd%bin/tsd"), "\"/opt/tsd%%bin/tsd\""; got != want {
		t.Fatalf("systemdQuoteArg = %q, want %q", got, want)
	}
	data := enrichTemplateData(templateData{}, Config{
		Role:       RoleAgent,
		BinaryPath: "/opt/tsd & bin/tsd",
		ConfigFile: "/etc/tsd/<agent>.toml",
	})
	if got, want := data.LaunchdBinaryPath, "/opt/tsd &amp; bin/tsd"; got != want {
		t.Fatalf("LaunchdBinaryPath = %q, want %q", got, want)
	}
	if got, want := data.LaunchdConfigFile, "/etc/tsd/&lt;agent&gt;.toml"; got != want {
		t.Fatalf("LaunchdConfigFile = %q, want %q", got, want)
	}
}
