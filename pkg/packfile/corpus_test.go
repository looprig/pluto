package packfile

import (
	"os"
	"path/filepath"
	"testing"
)

// corpusDir locates the repo's shipped YAML pack corpus relative to this
// package (pkg/packfile), two directories up from the repo root.
func corpusDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join("..", "..", "packs")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("packs/ corpus not found at %s: %v", dir, err)
	}
	return dir
}

// shippedPackDirs returns every immediate subdirectory of packs/ that holds a
// pack.yaml -- the shipped corpus the CLI ships and `mpqt validate` checks.
func shippedPackDirs(t *testing.T) []string {
	t.Helper()
	root := corpusDir(t)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read packs/: %v", err)
	}
	var dirs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "pack.yaml")); err == nil {
			dirs = append(dirs, dir)
		}
	}
	if len(dirs) == 0 {
		t.Fatalf("no packs found under %s", root)
	}
	return dirs
}

// TestShippedCorpusLoadsBuildsAndDigestsMatch is the compiled guard over the
// entire shipped YAML pack corpus: every pack must load strictly, lint with no
// findings, template each table's environment, build every table (judge tables
// too, via a fake judge client), pass qual validation, and match its committed
// pack.digest lockfile. Any drift in pkg/packfile that would break a shipped
// pack -- or an uncommitted edit to a pack that was not followed by
// `mpqt validate --write-digests` and a revision bump -- fails here rather than
// silently rotting until a live run. It replaces the single-example test the
// deleted packs/example pack used to anchor.
func TestShippedCorpusLoadsBuildsAndDigestsMatch(t *testing.T) {
	reg := NewRegistry()
	// A fake judge client lets judge-backed tables build offline; the client is
	// never invoked (Build only needs a non-nil client to construct the judge
	// evaluator), so no network or model call happens.
	bc := BuildContext{JudgeClient: fakeJudgeClient{}}

	for _, dir := range shippedPackDirs(t) {
		dir := dir
		t.Run(filepath.Base(dir), func(t *testing.T) {
			t.Parallel()

			doc, err := LoadDir(dir)
			if err != nil {
				t.Fatalf("LoadDir(%s): %v", dir, err)
			}

			if findings := doc.Lint(); len(findings) != 0 {
				t.Fatalf("Lint(%s): expected no findings, got %v", dir, findings)
			}

			for _, tf := range doc.Tables {
				if _, err := tf.Environment.Template(); err != nil {
					t.Fatalf("%s table %s: environment.Template: %v", dir, tf.Table, err)
				}
			}

			pack, err := doc.Build(reg, bc)
			if err != nil {
				t.Fatalf("Build(%s): %v", dir, err)
			}
			if err := pack.Validate(); err != nil {
				t.Fatalf("Validate(%s): %v", dir, err)
			}

			lock, err := os.ReadFile(filepath.Join(dir, "pack.digest")) // #nosec G304 -- test-controlled corpus path
			if err != nil {
				t.Fatalf("read %s pack.digest: %v", dir, err)
			}
			if err := VerifyDigest(doc, lock); err != nil {
				t.Fatalf("VerifyDigest(%s): %v (run `mpqt validate --write-digests %s` after a deliberate change + revision bump)", dir, err, dir)
			}
		})
	}
}
