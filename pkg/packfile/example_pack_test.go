package packfile

import (
	"os"
	"path/filepath"
	"testing"
)

// exampleDir locates the shipped packs/example reference pack relative to
// this package (pkg/packfile), two directories up from the repo root.
func exampleDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join("..", "..", "packs", "example")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("packs/example not found at %s: %v", dir, err)
	}
	return dir
}

// TestShippedExamplePack asserts the repo's own packs/example reference pack
// (the pack README's Quick start walks a reader through) loads, builds, and
// validates cleanly with no Lint findings, and that its committed
// pack.digest lockfile matches -- so drift in pkg/packfile that would break
// the shipped quickstart example fails here rather than silently rotting.
func TestShippedExamplePack(t *testing.T) {
	dir := exampleDir(t)

	doc, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir(%s): %v", dir, err)
	}

	if findings := doc.Lint(); len(findings) != 0 {
		t.Fatalf("Lint(%s): expected no findings, got %v", dir, findings)
	}

	pack, err := doc.Build(NewRegistry(), BuildContext{})
	if err != nil {
		t.Fatalf("Build(%s): %v", dir, err)
	}
	if err := pack.Validate(); err != nil {
		t.Fatalf("Validate(%s): %v", dir, err)
	}

	for _, tf := range doc.Tables {
		if _, err := tf.Environment.Template(); err != nil {
			t.Fatalf("table %s: environment.Template: %v", tf.Table, err)
		}
	}

	lock, err := os.ReadFile(filepath.Join(dir, "pack.digest"))
	if err != nil {
		t.Fatalf("read pack.digest: %v", err)
	}
	if err := VerifyDigest(doc, lock); err != nil {
		t.Fatalf("VerifyDigest: %v (regenerate packs/example/pack.digest with packfile.DigestLockfile)", err)
	}
}
