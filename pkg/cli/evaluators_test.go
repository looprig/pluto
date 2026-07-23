package cli_test

import (
	"strings"
	"testing"

	"github.com/looprig/mpqt/pkg/cli"
	"github.com/looprig/mpqt/pkg/packfile"
)

func TestEvaluatorsListsEveryBuiltinKind(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	if code := cli.Main([]string{"evaluators"}, app.App); code != cli.ExitOK {
		t.Fatalf("evaluators: code = %d, stderr = %s", code, app.Err.String())
	}

	out := app.Out.String()
	for _, k := range packfile.NewRegistry().Kinds() {
		if !strings.Contains(out, k.Name) {
			t.Errorf("evaluators output missing kind %q:\n%s", k.Name, out)
		}
		if !strings.Contains(out, k.Evidence) {
			t.Errorf("evaluators output missing evidence for kind %q:\n%s", k.Name, out)
		}
	}
	// Header row plus at least one entry per kind.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < len(packfile.NewRegistry().Kinds())+1 {
		t.Errorf("evaluators output has %d lines, want at least %d", len(lines), len(packfile.NewRegistry().Kinds())+1)
	}
}

func TestEvaluatorsRejectsExtraArgs(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	if code := cli.Main([]string{"evaluators", "unexpected"}, app.App); code != cli.ExitUsage {
		t.Fatalf("evaluators with extra args: code = %d, want %d", code, cli.ExitUsage)
	}
}
