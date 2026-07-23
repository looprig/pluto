package cli_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/looprig/mpqt/pkg/cli"
	"github.com/looprig/mpqt/pkg/reportjson"
)

// runFixture writes a manifest, a profile requiring a perfect capability
// score, and a testTableYAML-shaped pack (one required-text evaluator
// expecting "ok") into a fresh temp dir, and returns (packDir, manifestPath,
// profilePath, reportPath).
func runFixture(t *testing.T) (packDir, manifestPath, profilePath, reportPath string) {
	t.Helper()
	dir := t.TempDir()
	packDir = writeTestPack(t, dir)
	manifestPath = filepath.Join(dir, "manifest.yaml")
	profilePath = filepath.Join(dir, "profile.yaml")
	reportPath = filepath.Join(dir, "report.json")
	writeFile(t, manifestPath, testManifestYAML)
	writeFile(t, profilePath, testProfileYAML)
	return packDir, manifestPath, profilePath, reportPath
}

func TestRunConformingTargetReachesExitOK(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	withClient(app, &fakeClient{resp: assistantText("ok, done")})

	packDir, manifestPath, profilePath, reportPath := runFixture(t)
	code := cli.Main([]string{
		"run", "--manifest", manifestPath, "--profile", profilePath, "--packs", packDir,
		"--skip-cost-estimate", "--out", reportPath,
	}, app.App)
	if code != cli.ExitOK {
		t.Fatalf("run (conforming target): code = %d, stdout=%s stderr=%s", code, app.Out.String(), app.Err.String())
	}

	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	dec, err := reportjson.Decode(data)
	if err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if dec.Profile == nil {
		t.Fatal("report carries no profile result")
	}
	if string(dec.Profile.Disposition) != "qualified" {
		t.Errorf("Disposition = %s, want qualified", dec.Profile.Disposition)
	}
}

func TestRunNonConformingTargetReachesExitGateFailed(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	withClient(app, &fakeClient{resp: assistantText("nope, refused")})

	packDir, manifestPath, profilePath, reportPath := runFixture(t)
	code := cli.Main([]string{
		"run", "--manifest", manifestPath, "--profile", profilePath, "--packs", packDir,
		"--skip-cost-estimate", "--out", reportPath,
	}, app.App)
	if code != cli.ExitGateFailed {
		t.Fatalf("run (non-conforming target): code = %d, want %d; stdout=%s", code, cli.ExitGateFailed, app.Out.String())
	}

	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	dec, err := reportjson.Decode(data)
	if err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if dec.Profile == nil || string(dec.Profile.Disposition) == "qualified" {
		t.Errorf("Disposition = %v, want a non-qualified disposition", dec.Profile)
	}
}

func TestRunMissingRequiredFlagsIsUsageError(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	if code := cli.Main([]string{"run"}, app.App); code != cli.ExitUsage {
		t.Fatalf("run with no flags: code = %d, want %d", code, cli.ExitUsage)
	}
}

func TestRunUnknownRequireValueIsUsageError(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	withClient(app, &fakeClient{resp: assistantText("ok")})
	packDir, manifestPath, profilePath, reportPath := runFixture(t)

	code := cli.Main([]string{
		"run", "--manifest", manifestPath, "--profile", profilePath, "--packs", packDir,
		"--require", "not-a-real-disposition", "--skip-cost-estimate", "--out", reportPath,
	}, app.App)
	if code != cli.ExitUsage {
		t.Fatalf("run --require bogus: code = %d, want %d", code, cli.ExitUsage)
	}
}

// TestRunRequirePricedWithoutSnapshotFailsClosed proves the cost-ceiling gate
// runs (and aborts) strictly before pkg/run.Execute is ever called.
func TestRunRequirePricedWithoutSnapshotFailsClosed(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	client := &fakeClient{resp: assistantText("ok")}
	withClient(app, client)
	packDir, manifestPath, profilePath, reportPath := runFixture(t)

	code := cli.Main([]string{
		"run", "--manifest", manifestPath, "--profile", profilePath, "--packs", packDir,
		"--require-priced", "--out", reportPath,
	}, app.App)
	if code != cli.ExitPricing {
		t.Fatalf("run --require-priced (no snapshot): code = %d, want %d; stdout=%s", code, cli.ExitPricing, app.Out.String())
	}
	if len(client.calls) != 0 {
		t.Errorf("run --require-priced gate failure invoked the client %d times, want 0", len(client.calls))
	}
	if _, err := os.Stat(reportPath); err == nil {
		t.Error("run --require-priced gate failure should not have written a report")
	}
}

// TestRunMaxCostCeilingExceeded proves --max-estimated-cost-usd gates before
// Execute when a pricing snapshot makes the estimate fully known but over
// budget.
func TestRunMaxCostCeilingExceeded(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	client := &fakeClient{resp: assistantText("ok")}
	withClient(app, client)
	withCounter(app, fakeCounter{tokens: 1_000_000, quality: "fake"})

	packDir, manifestPath, profilePath, reportPath := runFixture(t)
	dir := filepath.Dir(packDir)
	snapshotPath := filepath.Join(dir, "prices.json")
	// input rate of $5/M tokens * 1,000,000 tokens = $5.00, well over a
	// $0.01 ceiling; output has no declared cap so the run's own honesty
	// rule would otherwise make Known=false -- rates on both dimensions
	// keep the estimate fully known regardless.
	writeFile(t, snapshotPath, `{"test":{"models":{"fake-model":{"cost":{"input":5,"output":5}}}}}`)

	code := cli.Main([]string{
		"run", "--manifest", manifestPath, "--profile", profilePath, "--packs", packDir,
		"--pricing-snapshot", snapshotPath, "--max-estimated-cost-usd", "0.01", "--out", reportPath,
	}, app.App)
	if code != cli.ExitPricing {
		t.Fatalf("run over cost ceiling: code = %d, want %d; stdout=%s", code, cli.ExitPricing, app.Out.String())
	}
	if len(client.calls) != 0 {
		t.Errorf("run over cost ceiling invoked the client %d times, want 0", len(client.calls))
	}
}

func TestRunSkipCostEstimateBypassesPreflight(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	withClient(app, &fakeClient{resp: assistantText("ok")})
	packDir, manifestPath, profilePath, reportPath := runFixture(t)

	code := cli.Main([]string{
		"run", "--manifest", manifestPath, "--profile", profilePath, "--packs", packDir,
		"--skip-cost-estimate", "--require-priced", "--out", reportPath,
	}, app.App)
	// --skip-cost-estimate takes precedence over --require-priced: no
	// preflight plan is computed at all, so there is nothing to gate on.
	if code != cli.ExitOK {
		t.Fatalf("run --skip-cost-estimate --require-priced: code = %d, want %d; stdout=%s stderr=%s",
			code, cli.ExitOK, app.Out.String(), app.Err.String())
	}
}
