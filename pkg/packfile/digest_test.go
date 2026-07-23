package packfile

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"
)

// twoTableFS builds a two-table pack whose pack.yaml lists the tables in the
// given order, so callers can test order sensitivity directly.
func twoTableFS(order [2]string) fstest.MapFS {
	return fstest.MapFS{
		"pack/pack.yaml": {Data: []byte("pack: pack\nrevision: v1\ntables:\n  - " + order[0] + "\n  - " + order[1] + "\n")},
		"pack/a.yaml":    {Data: []byte("table: a\nrevision: v1\ndimension: d\n")},
		"pack/b.yaml":    {Data: []byte("table: b\nrevision: v1\ndimension: d\n")},
	}
}

func TestDigestDeterministicAndOrderSensitive(t *testing.T) {
	docAB1, err := Load(twoTableFS([2]string{"a.yaml", "b.yaml"}), "pack")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	docAB2, err := Load(twoTableFS([2]string{"a.yaml", "b.yaml"}), "pack")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	docBA, err := Load(twoTableFS([2]string{"b.yaml", "a.yaml"}), "pack")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	first := docAB1.Digest()
	second := docAB1.Digest()
	if first != second {
		t.Fatalf("Digest() not stable across repeated calls on the same Document: %s != %s", first, second)
	}
	if docAB1.Digest() != docAB2.Digest() {
		t.Fatalf("Digest() not deterministic: %s != %s", docAB1.Digest(), docAB2.Digest())
	}
	if docAB1.Digest() == docBA.Digest() {
		t.Fatalf("Digest() not order-sensitive: swapping table order produced the same hex %s", docAB1.Digest())
	}
}

func TestDigestIsLowercaseHex(t *testing.T) {
	doc, err := Load(toolUseFS(), "tool-use")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	d := doc.Digest()
	if len(d) != 64 {
		t.Fatalf("Digest() length = %d, want 64", len(d))
	}
	if strings.ToLower(d) != d {
		t.Fatalf("Digest() = %q, want lowercase hex", d)
	}
}

func TestVerifyDigest(t *testing.T) {
	doc, err := Load(toolUseFS(), "tool-use")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	t.Run("match", func(t *testing.T) {
		lock := DigestLockfile(doc)
		if err := VerifyDigest(doc, lock); err != nil {
			t.Fatalf("VerifyDigest() on freshly generated lockfile: %v", err)
		}
	})

	t.Run("hash differs revision same requires bump", func(t *testing.T) {
		bogusHash := strings.Repeat("0", 64)
		lock := []byte("packfile-digest/v1 " + string(doc.Pack.Revision) + " " + bogusHash + "\n")
		err := VerifyDigest(doc, lock)
		var pe *Error
		if !errors.As(err, &pe) {
			t.Fatalf("want *Error, got %T: %v", err, err)
		}
		if !strings.Contains(pe.Reason, "revision bump required") {
			t.Fatalf("Reason = %q, want it to mention revision bump required", pe.Reason)
		}
	})

	t.Run("hash differs revision differs bump acknowledged", func(t *testing.T) {
		bogusHash := strings.Repeat("0", 64)
		lock := []byte("packfile-digest/v1 v2-bumped " + bogusHash + "\n")
		if err := VerifyDigest(doc, lock); err != nil {
			t.Fatalf("VerifyDigest() with acknowledged revision bump: %v", err)
		}
	})

	t.Run("malformed lockfile", func(t *testing.T) {
		for _, lock := range [][]byte{
			[]byte("not a lockfile at all\n"),
			[]byte("packfile-digest/v2 v1 " + strings.Repeat("0", 64) + "\n"),
			[]byte("packfile-digest/v1 v1 not-hex\n"),
			[]byte(""),
		} {
			if err := VerifyDigest(doc, lock); err == nil {
				t.Fatalf("VerifyDigest(%q) accepted malformed lockfile", lock)
			}
		}
	})
}
