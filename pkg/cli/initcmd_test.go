package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/looprig/mpqt/pkg/cli"
)

func TestInitWritesExpectedFiles(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	dir := t.TempDir()

	code := cli.Main([]string{"init", "my-assistant", dir}, app.App)
	if code != cli.ExitOK {
		t.Fatalf("init: code = %d, stderr = %s", code, app.Err.String())
	}

	base := filepath.Join(dir, "my-assistant")
	for _, name := range []string{"pack.yaml", "example.yaml", "schema.json"} {
		if _, err := os.Stat(filepath.Join(base, name)); err != nil {
			t.Errorf("init: %s not written: %v", name, err)
		}
	}

	packYAML, err := os.ReadFile(filepath.Join(base, "pack.yaml"))
	if err != nil {
		t.Fatalf("read pack.yaml: %v", err)
	}
	if !strings.Contains(string(packYAML), "yaml-language-server: $schema=schema.json") {
		t.Errorf("pack.yaml missing $schema header: %s", packYAML)
	}
	if !strings.Contains(string(packYAML), `"my-assistant"`) {
		t.Errorf("pack.yaml missing pack name: %s", packYAML)
	}
}

// TestInitThenValidateRoundTrips proves the scaffolded pack itself passes
// `mpqt validate` cleanly: exit 0, no errors.
func TestInitThenValidateRoundTrips(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	initApp := newTestApp()
	if code := cli.Main([]string{"init", "roundtrip-pack", dir}, initApp.App); code != cli.ExitOK {
		t.Fatalf("init: code = %d, stderr = %s", code, initApp.Err.String())
	}

	validateApp := newTestApp()
	base := filepath.Join(dir, "roundtrip-pack")
	code := cli.Main([]string{"validate", base}, validateApp.App)
	if code != cli.ExitOK {
		t.Fatalf("validate after init: code = %d, stdout = %s stderr = %s",
			code, validateApp.Out.String(), validateApp.Err.String())
	}
	if strings.Contains(validateApp.Out.String(), "error:") {
		t.Errorf("validate after init reported an error: %s", validateApp.Out.String())
	}
}

func TestInitRejectsBadArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
	}{
		{"no args", []string{"init"}},
		{"too many args", []string{"init", "a", "b", "c"}},
		{"name contains path separator", []string{"init", "a/b", t.TempDir()}},
		{"name is dot-dot", []string{"init", "..", t.TempDir()}},
		{"empty name", []string{"init", ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			app := newTestApp()
			if code := cli.Main(tt.args, app.App); code != cli.ExitUsage {
				t.Fatalf("code = %d, want %d; stderr=%s", code, cli.ExitUsage, app.Err.String())
			}
		})
	}
}

func TestInitHelpPrintsUsageToStdout(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	code := cli.Main([]string{"init", "-h"}, app.App)
	if code != cli.ExitOK {
		t.Fatalf("init -h: code = %d, want %d", code, cli.ExitOK)
	}
	if !strings.Contains(app.Out.String(), "usage: mpqt init") {
		t.Errorf("init -h: Stdout = %q, want usage text", app.Out.String())
	}
	if app.Err.Len() != 0 {
		t.Errorf("init -h: Stderr = %q, want empty", app.Err.String())
	}
}
