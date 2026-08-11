package cli_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/looprig/pluto/pkg/cli"
	"github.com/looprig/pluto/pkg/packfile"
)

func TestValidateCleanPackExitsOK(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	dir := writeTestPack(t, t.TempDir())

	code := cli.Main([]string{"validate", dir}, app.App)
	if code != cli.ExitOK {
		t.Fatalf("validate: code = %d, stdout = %s stderr = %s", code, app.Out.String(), app.Err.String())
	}
	if !strings.Contains(app.Out.String(), "no pack.digest lockfile") {
		t.Errorf("validate: expected a note about the missing pack.digest, got: %s", app.Out.String())
	}
}

func TestValidateDetectsDigestMismatch(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	dir := writeTestPack(t, t.TempDir())

	doc, err := packfile.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	lock := packfile.DigestLockfile(doc)
	// Corrupt the lockfile's hash so it no longer matches the pack's actual
	// digest, without changing pack.yaml's revision -- the "changed but not
	// bumped" case VerifyDigest must reject.
	corrupted := strings.Replace(string(lock), doc.Digest(), strings.Repeat("0", 64), 1)
	if corrupted == string(lock) {
		t.Fatal("test bug: corruption did not change the lockfile")
	}
	writeFile(t, filepath.Join(dir, "pack.digest"), corrupted)

	code := cli.Main([]string{"validate", dir}, app.App)
	if code != cli.ExitCommandFailure {
		t.Fatalf("validate with mismatched digest: code = %d, want %d; stdout=%s", code, cli.ExitCommandFailure, app.Out.String())
	}
	if !strings.Contains(app.Out.String(), "revision bump required") {
		t.Errorf("validate: expected a digest-mismatch error naming the revision-bump rule, got: %s", app.Out.String())
	}
}

func TestValidateAcceptsMatchingDigest(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	dir := writeTestPack(t, t.TempDir())

	doc, err := packfile.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	writeFile(t, filepath.Join(dir, "pack.digest"), string(packfile.DigestLockfile(doc)))

	if code := cli.Main([]string{"validate", dir}, app.App); code != cli.ExitOK {
		t.Fatalf("validate with matching digest: code = %d, stdout = %s stderr = %s", code, app.Out.String(), app.Err.String())
	}
}

func TestValidateReportsLoadErrorForMissingDir(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	code := cli.Main([]string{"validate", filepath.Join(t.TempDir(), "does-not-exist")}, app.App)
	if code != cli.ExitCommandFailure {
		t.Fatalf("validate on missing dir: code = %d, want %d", code, cli.ExitCommandFailure)
	}
}

// TestValidateExecuteRunsScriptedTable proves --execute actually drives the
// script-backed table through pkg/run.Execute rather than being a no-op.
func TestValidateExecuteRunsScriptedTable(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pack.yaml"), "pack: scripted\nrevision: v1\ntables:\n  - t1.yaml\n")
	writeFile(t, filepath.Join(dir, "t1.yaml"), `table: t1
revision: v1
dimension: capability
requires: []
evaluators:
  - kind: required-text
    substrings: ["ok"]
scenarios:
  - id: s1
    input:
      - role: user
        text: Say ok.
script:
  s1:
    reply: "ok, done"
`)

	code := cli.Main([]string{"validate", "--execute", dir}, app.App)
	if code != cli.ExitOK {
		t.Fatalf("validate --execute: code = %d, stdout = %s stderr = %s", code, app.Out.String(), app.Err.String())
	}
	if !strings.Contains(app.Out.String(), "1 table(s) executed") {
		t.Errorf("validate --execute: expected exactly 1 table executed, got: %s", app.Out.String())
	}
}

// TestValidateExecuteSurfacesTargetFailure proves an unscripted scenario is
// visible in the --execute summary rather than silently passing (a target
// failure never aborts eval.Run itself, but Execute must still complete and
// the CLI must still report the table as executed so a reader can look at
// the underlying report).
func TestValidateExecuteReportsEvenWithoutFullScriptCoverage(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pack.yaml"), "pack: partial\nrevision: v1\ntables:\n  - t1.yaml\n")
	writeFile(t, filepath.Join(dir, "t1.yaml"), `table: t1
revision: v1
dimension: capability
requires: []
evaluators:
  - kind: required-text
    substrings: ["ok"]
scenarios:
  - id: s1
    input:
      - role: user
        text: Say ok.
`)

	code := cli.Main([]string{"validate", "--execute", dir}, app.App)
	if code != cli.ExitOK {
		t.Fatalf("validate --execute (no script section): code = %d, stdout=%s stderr=%s", code, app.Out.String(), app.Err.String())
	}
	if !strings.Contains(app.Out.String(), "1 table(s) executed") {
		t.Errorf("validate --execute: expected the table to still be executed (target errors are per-sample, not fatal), got: %s", app.Out.String())
	}
}

func TestValidateAPIFormatFlagIsANoOpNote(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	dir := writeTestPack(t, t.TempDir())

	code := cli.Main([]string{"validate", "--api-format", "gemini", dir}, app.App)
	if code != cli.ExitOK {
		t.Fatalf("validate --api-format gemini: code = %d, stdout=%s stderr=%s", code, app.Out.String(), app.Err.String())
	}
	if !strings.Contains(app.Out.String(), "not yet implemented") {
		t.Errorf("validate --api-format gemini: expected a not-yet-implemented note, got: %s", app.Out.String())
	}
}

func TestValidateNoDirsFoundIsCommandFailure(t *testing.T) {
	// Not t.Parallel(): t.Chdir forbids it.
	app := newTestApp()
	emptyDir := t.TempDir()
	t.Chdir(emptyDir)

	if code := cli.Main([]string{"validate"}, app.App); code != cli.ExitCommandFailure {
		t.Fatalf("validate with no pack dirs anywhere under .: code = %d, want %d; stderr=%s", code, cli.ExitCommandFailure, app.Err.String())
	}
}
