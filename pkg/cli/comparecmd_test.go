package cli_test

import (
	"path/filepath"
	"testing"

	"github.com/looprig/mpqt/pkg/cli"
)

const incumbentManifestYAML = `target-id: incumbent-1
role: incumbent
provider: test
model: fake-model
api-format: openai
base-url: https://example.invalid/v1
revision: r0
endpoint-class: remote
capabilities: []
`

// buildReport drives `mpqt run` end to end (via a fake client) to produce a
// real reportjson file at path, so compare tests exercise the actual wire
// format two independent `run` invocations would have produced, not a
// hand-built fixture.
func buildReport(t *testing.T, manifestYAML, reply, path string) {
	t.Helper()
	app := newTestApp()
	withClient(app, &fakeClient{resp: assistantText(reply)})

	dir := t.TempDir()
	packDir := writeTestPack(t, dir)
	manifestPath := filepath.Join(dir, "manifest.yaml")
	profilePath := filepath.Join(dir, "profile.yaml")
	writeFile(t, manifestPath, manifestYAML)
	writeFile(t, profilePath, testProfileYAML)

	code := cli.Main([]string{
		"run", "--manifest", manifestPath, "--profile", profilePath, "--packs", packDir,
		"--skip-cost-estimate", "--require", "unverified", "--out", path,
	}, app.App)
	if code != cli.ExitOK && code != cli.ExitGateFailed {
		t.Fatalf("buildReport: run: code = %d, stdout=%s stderr=%s", code, app.Out.String(), app.Err.String())
	}
}

func TestCompareNoRegressionExitsOK(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	candidatePath := filepath.Join(dir, "candidate.json")
	incumbentPath := filepath.Join(dir, "incumbent.json")
	buildReport(t, testManifestYAML, "ok, done", candidatePath)
	buildReport(t, incumbentManifestYAML, "ok, done", incumbentPath)

	app := newTestApp()
	code := cli.Main([]string{"compare", "--candidate", candidatePath, "--incumbent", incumbentPath}, app.App)
	if code != cli.ExitOK {
		t.Fatalf("compare (no regression): code = %d, stdout=%s stderr=%s", code, app.Out.String(), app.Err.String())
	}
}

func TestCompareRegressionExitsGateFailed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	candidatePath := filepath.Join(dir, "candidate.json")
	incumbentPath := filepath.Join(dir, "incumbent.json")
	// Incumbent passes (baseline StatusPass); candidate now fails: a
	// regression by pkg/compare's own definition.
	buildReport(t, incumbentManifestYAML, "ok, done", incumbentPath)
	buildReport(t, testManifestYAML, "nope, refused", candidatePath)

	app := newTestApp()
	code := cli.Main([]string{"compare", "--candidate", candidatePath, "--incumbent", incumbentPath}, app.App)
	if code != cli.ExitGateFailed {
		t.Fatalf("compare (regression): code = %d, want %d; stdout=%s", code, cli.ExitGateFailed, app.Out.String())
	}
}

func TestCompareMissingRequiredFlagsIsUsageError(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	if code := cli.Main([]string{"compare"}, app.App); code != cli.ExitUsage {
		t.Fatalf("compare with no flags: code = %d, want %d", code, cli.ExitUsage)
	}
}

func TestCompareUnreadableFileIsCommandFailure(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	code := cli.Main([]string{
		"compare",
		"--candidate", filepath.Join(t.TempDir(), "missing.json"),
		"--incumbent", filepath.Join(t.TempDir(), "also-missing.json"),
	}, app.App)
	if code != cli.ExitCommandFailure {
		t.Fatalf("compare with missing files: code = %d, want %d", code, cli.ExitCommandFailure)
	}
}
