package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/looprig/pluto/pkg/cli"
)

const genCannedResponse = `{"scenarios":[{"id":"gen-001","input":[{"role":"user","text":"hi"}]}]}`

func TestGenNoWritePrintsYAMLWithGeneratedIDs(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	withClient(app, &fakeClient{resp: assistantText(genCannedResponse)})

	dir := writeTestPack(t, t.TempDir())
	cfgPath := filepath.Join(t.TempDir(), "gen.yaml")
	writeFile(t, cfgPath, testLLMConfigYAML)

	code := cli.Main([]string{
		"gen", "--pack", dir, "--table", "t1", "-n", "1", "--config", cfgPath,
		"--skip-cost-estimate", "--no-write",
	}, app.App)
	if code != cli.ExitOK {
		t.Fatalf("gen --no-write: code = %d, stdout=%s stderr=%s", code, app.Out.String(), app.Err.String())
	}
	if !strings.Contains(app.Out.String(), "gen-001") {
		t.Errorf("gen --no-write: Stdout missing generated ID gen-001:\n%s", app.Out.String())
	}
	// --no-write must never touch the table file on disk.
	data, err := os.ReadFile(filepath.Join(dir, "t1.yaml"))
	if err != nil {
		t.Fatalf("read t1.yaml: %v", err)
	}
	if strings.Contains(string(data), "gen-001") {
		t.Error("gen --no-write wrote to the table file on disk")
	}
}

func TestGenWritesAppendedScenarioToTableFile(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	withClient(app, &fakeClient{resp: assistantText(genCannedResponse)})

	dir := writeTestPack(t, t.TempDir())
	cfgPath := filepath.Join(t.TempDir(), "gen.yaml")
	writeFile(t, cfgPath, testLLMConfigYAML)

	code := cli.Main([]string{
		"gen", "--pack", dir, "--table", "t1", "-n", "1", "--config", cfgPath,
		"--skip-cost-estimate",
	}, app.App)
	if code != cli.ExitOK {
		t.Fatalf("gen: code = %d, stdout=%s stderr=%s", code, app.Out.String(), app.Err.String())
	}

	data, err := os.ReadFile(filepath.Join(dir, "t1.yaml"))
	if err != nil {
		t.Fatalf("read t1.yaml: %v", err)
	}
	if !strings.Contains(string(data), "gen-001") {
		t.Errorf("gen: table file was not appended with gen-001:\n%s", data)
	}
	if !strings.Contains(string(data), "generated-by") {
		t.Errorf("gen: appended scenario missing generated-by provenance label:\n%s", data)
	}
}

func TestGenRawPrintsJSONL(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	withClient(app, &fakeClient{resp: assistantText(genCannedResponse)})

	dir := writeTestPack(t, t.TempDir())
	cfgPath := filepath.Join(t.TempDir(), "gen.yaml")
	writeFile(t, cfgPath, testLLMConfigYAML)

	code := cli.Main([]string{
		"gen", "--pack", dir, "--table", "t1", "-n", "1", "--config", cfgPath,
		"--skip-cost-estimate", "--no-write", "--raw",
	}, app.App)
	if code != cli.ExitOK {
		t.Fatalf("gen --raw: code = %d, stderr=%s", code, app.Err.String())
	}
	if !strings.Contains(app.Out.String(), `"id":"gen-001"`) {
		t.Errorf("gen --raw: Stdout missing JSONL row for gen-001:\n%s", app.Out.String())
	}
}

func TestGenDryRunStopsBeforePaidCall(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	client := &fakeClient{resp: assistantText(genCannedResponse)}
	withClient(app, client)

	dir := writeTestPack(t, t.TempDir())
	cfgPath := filepath.Join(t.TempDir(), "gen.yaml")
	writeFile(t, cfgPath, testLLMConfigYAML)

	code := cli.Main([]string{
		"gen", "--pack", dir, "--table", "t1", "-n", "1", "--config", cfgPath, "--dry-run",
	}, app.App)
	if code != cli.ExitOK {
		t.Fatalf("gen --dry-run: code = %d, stdout=%s stderr=%s", code, app.Out.String(), app.Err.String())
	}
	if len(client.calls) != 0 {
		t.Errorf("gen --dry-run invoked the client %d times, want 0", len(client.calls))
	}
}

// TestGenRequirePricedWithoutSnapshotFailsClosed proves --require-priced
// gates the call BEFORE any paid call when the estimate is incomplete
// (no pricing snapshot was supplied, so every rate is unknown).
func TestGenRequirePricedWithoutSnapshotFailsClosed(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	client := &fakeClient{resp: assistantText(genCannedResponse)}
	withClient(app, client)

	dir := writeTestPack(t, t.TempDir())
	cfgPath := filepath.Join(t.TempDir(), "gen.yaml")
	writeFile(t, cfgPath, testLLMConfigYAML)

	code := cli.Main([]string{
		"gen", "--pack", dir, "--table", "t1", "-n", "1", "--config", cfgPath, "--require-priced",
	}, app.App)
	if code != cli.ExitPricing {
		t.Fatalf("gen --require-priced (no snapshot): code = %d, want %d; stdout=%s", code, cli.ExitPricing, app.Out.String())
	}
	if len(client.calls) != 0 {
		t.Errorf("gen --require-priced gate failure invoked the client %d times, want 0", len(client.calls))
	}
}

func TestGenMissingRequiredFlagsIsUsageError(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	code := cli.Main([]string{"gen"}, app.App)
	if code != cli.ExitUsage {
		t.Fatalf("gen with no flags: code = %d, want %d", code, cli.ExitUsage)
	}
}

func TestGenNOutOfRangeIsUsageError(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	dir := writeTestPack(t, t.TempDir())
	cfgPath := filepath.Join(t.TempDir(), "gen.yaml")
	writeFile(t, cfgPath, testLLMConfigYAML)

	code := cli.Main([]string{"gen", "--pack", dir, "--table", "t1", "-n", "0", "--config", cfgPath}, app.App)
	if code != cli.ExitUsage {
		t.Fatalf("gen -n 0: code = %d, want %d", code, cli.ExitUsage)
	}
}

func TestGenUnknownTableIsCommandFailure(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	withClient(app, &fakeClient{resp: assistantText(genCannedResponse)})

	dir := writeTestPack(t, t.TempDir())
	cfgPath := filepath.Join(t.TempDir(), "gen.yaml")
	writeFile(t, cfgPath, testLLMConfigYAML)

	code := cli.Main([]string{
		"gen", "--pack", dir, "--table", "does-not-exist", "-n", "1", "--config", cfgPath,
		"--skip-cost-estimate",
	}, app.App)
	if code != cli.ExitCommandFailure {
		t.Fatalf("gen unknown table: code = %d, want %d", code, cli.ExitCommandFailure)
	}
}
