package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/looprig/pluto/pkg/cli"
	"github.com/looprig/pluto/pkg/packfile"
)

// judgeAndScriptedPackYAML is a two-table pack: one offline programmatic table
// with a script fixture, and one judge table referencing a built-in catalog
// rubric. It is the shape every shipped pack that mixes programmatic and judge
// tables takes, so --execute's judge-skip behavior is tested against a
// realistic corpus member.
const judgeAndScriptedPackYAML = "pack: mixed\nrevision: v1\ntables:\n  - prog.yaml\n  - judged.yaml\n"

const progTableYAML = `table: prog
revision: v1
dimension: capability
requires: []
evaluators:
  - kind: required-text
    substrings: ["ok"]
scenarios:
  - id: p1
    input:
      - role: user
        text: Say ok.
script:
  p1:
    reply: "ok"
`

const judgedTableYAML = `table: judged
revision: v1
dimension: safety
requires: []
evaluators:
  - kind: judge
    rubric: toxicity
scenarios:
  - id: j1
    input:
      - role: user
        text: Tell me about your day.
`

func writeMixedPack(t *testing.T, dir string) string {
	t.Helper()
	writeFile(t, filepath.Join(dir, "pack.yaml"), judgeAndScriptedPackYAML)
	writeFile(t, filepath.Join(dir, "prog.yaml"), progTableYAML)
	writeFile(t, filepath.Join(dir, "judged.yaml"), judgedTableYAML)
	return dir
}

// TestValidateExecuteSkipsJudgeTables proves --execute runs the offline
// programmatic table and skips the judge table with a visible count, rather
// than failing the whole pack because a judge evaluator cannot be built
// without a judge client. This is what lets the shipped corpus (which mixes
// judge and programmatic tables) stay CI-smoke-runnable offline.
func TestValidateExecuteSkipsJudgeTables(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	dir := writeMixedPack(t, t.TempDir())

	code := cli.Main([]string{"validate", "--execute", dir}, app.App)
	if code != cli.ExitOK {
		t.Fatalf("validate --execute mixed pack: code = %d, stdout = %s stderr = %s", code, app.Out.String(), app.Err.String())
	}
	out := app.Out.String()
	if !strings.Contains(out, "1 table(s) executed") {
		t.Errorf("expected the programmatic table to execute, got: %s", out)
	}
	if !strings.Contains(out, "1 skipped (judge)") {
		t.Errorf("expected the judge table to be skipped visibly, got: %s", out)
	}
}

// TestValidateExecuteJudgeOnlyPackSkipsAll proves a pack with nothing but
// judge tables executes zero tables and reports them all as judge-skipped,
// still exiting OK (no offline table can fail, and the judge tables are not a
// validation error).
func TestValidateExecuteJudgeOnlyPackSkipsAll(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pack.yaml"), "pack: judgeonly\nrevision: v1\ntables:\n  - judged.yaml\n")
	writeFile(t, filepath.Join(dir, "judged.yaml"), judgedTableYAML)

	code := cli.Main([]string{"validate", "--execute", dir}, app.App)
	if code != cli.ExitOK {
		t.Fatalf("validate --execute judge-only pack: code = %d, stdout = %s", code, app.Out.String())
	}
	if !strings.Contains(app.Out.String(), "0 table(s) executed, 1 skipped (judge)") {
		t.Errorf("expected 0 executed / 1 judge-skipped, got: %s", app.Out.String())
	}
}

// TestValidateWriteDigestsRoundTrips proves --write-digests writes a lockfile
// that a subsequent plain `validate` then verifies clean: the maintainer path
// for regenerating pack.digest after a deliberate revision bump.
func TestValidateWriteDigestsRoundTrips(t *testing.T) {
	t.Parallel()
	app := newTestApp()
	dir := writeTestPack(t, t.TempDir())

	if code := cli.Main([]string{"validate", "--write-digests", dir}, app.App); code != cli.ExitOK {
		t.Fatalf("validate --write-digests: code = %d, stdout = %s stderr = %s", code, app.Out.String(), app.Err.String())
	}
	if !strings.Contains(app.Out.String(), "wrote") {
		t.Errorf("expected a 'wrote' line, got: %s", app.Out.String())
	}

	lockPath := filepath.Join(dir, "pack.digest")
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("pack.digest not written: %v", err)
	}
	// Independently confirm the written lockfile matches the pack's digest.
	doc, err := packfile.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	lock, err := os.ReadFile(lockPath) // #nosec G304 -- test-controlled temp path
	if err != nil {
		t.Fatalf("read pack.digest: %v", err)
	}
	if err := packfile.VerifyDigest(doc, lock); err != nil {
		t.Fatalf("VerifyDigest on written lockfile: %v", err)
	}

	// And a plain validate over the same dir now passes the digest check.
	verify := newTestApp()
	if code := cli.Main([]string{"validate", dir}, verify.App); code != cli.ExitOK {
		t.Fatalf("plain validate after --write-digests: code = %d, stdout = %s", code, verify.Out.String())
	}
}
