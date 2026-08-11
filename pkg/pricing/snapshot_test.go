package pricing_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/looprig/pluto/pkg/pricing"
)

// realisticFixture is a models.dev-shaped fragment: an object keyed by
// provider id, each provider carrying a "models" object keyed by model id,
// each model carrying a "cost" object with USD-per-million-token fields.
// This shape is a documented best-guess (see snapshot.go) that must be
// verified against the live https://models.dev/api.json before FetchSnapshot
// prices a real run.
const realisticFixture = `{
  "anthropic": {
    "models": {
      "claude-priced": {
        "cost": {"input": 3, "output": 15, "cache_read": 0.3, "cache_write": 3.75}
      },
      "claude-reasoning": {
        "cost": {"input": 3, "output": 15, "reasoning": 60}
      },
      "claude-unpriced-model": {
        "name": "no cost object at all"
      },
      "claude-zero-priced": {
        "cost": {"input": 0, "output": 0}
      }
    }
  },
  "openai": {
    "models": {
      "gpt-priced": {
        "cost": {"input": 2.5, "output": 10}
      }
    }
  }
}`

func TestParseSnapshot(t *testing.T) {
	t.Parallel()
	fetchedAt := time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC)

	snap, err := pricing.ParseSnapshot([]byte(realisticFixture), "https://models.dev/api.json", fetchedAt)
	if err != nil {
		t.Fatalf("ParseSnapshot() error = %v", err)
	}

	if snap.SourceURL != "https://models.dev/api.json" {
		t.Errorf("SourceURL = %q", snap.SourceURL)
	}
	if !snap.FetchedAt.Equal(fetchedAt) {
		t.Errorf("FetchedAt = %v, want %v", snap.FetchedAt, fetchedAt)
	}
	sum := sha256.Sum256([]byte(realisticFixture))
	wantDigest := hex.EncodeToString(sum[:])
	if snap.Digest != wantDigest {
		t.Errorf("Digest = %q, want %q", snap.Digest, wantDigest)
	}

	// A model with no cost object at all is omitted entirely.
	if _, ok := snap.Rows["anthropic/claude-unpriced-model"]; ok {
		t.Error("model with no cost object should not produce a row")
	}

	priced, ok := snap.Rows["anthropic/claude-priced"]
	if !ok {
		t.Fatal("anthropic/claude-priced row missing")
	}
	if priced.Input == nil || *priced.Input != 3 {
		t.Errorf("claude-priced Input = %v, want 3", priced.Input)
	}
	if priced.Output == nil || *priced.Output != 15 {
		t.Errorf("claude-priced Output = %v, want 15", priced.Output)
	}
	if priced.CacheRead == nil || *priced.CacheRead != 0.3 {
		t.Errorf("claude-priced CacheRead = %v, want 0.3", priced.CacheRead)
	}
	if priced.CacheWrite == nil || *priced.CacheWrite != 3.75 {
		t.Errorf("claude-priced CacheWrite = %v, want 3.75", priced.CacheWrite)
	}
	// reasoning is absent from the fixture's cost object for this model: nil,
	// never zero.
	if priced.Reasoning != nil {
		t.Errorf("claude-priced Reasoning = %v, want nil (unpriced, not free)", priced.Reasoning)
	}

	reasoning, ok := snap.Rows["anthropic/claude-reasoning"]
	if !ok {
		t.Fatal("anthropic/claude-reasoning row missing")
	}
	if reasoning.Reasoning == nil || *reasoning.Reasoning != 60 {
		t.Errorf("claude-reasoning Reasoning = %v, want 60", reasoning.Reasoning)
	}

	// An explicit zero in the source must decode as a non-nil pointer to
	// 0.0 -- a real free price, distinct from "not priced".
	zero, ok := snap.Rows["anthropic/claude-zero-priced"]
	if !ok {
		t.Fatal("anthropic/claude-zero-priced row missing")
	}
	if zero.Input == nil || *zero.Input != 0 {
		t.Errorf("claude-zero-priced Input = %v, want non-nil 0", zero.Input)
	}
	if zero.CacheRead != nil {
		t.Errorf("claude-zero-priced CacheRead = %v, want nil (omitted from source)", zero.CacheRead)
	}

	if _, ok := snap.Rows["openai/gpt-priced"]; !ok {
		t.Error("openai/gpt-priced row missing")
	}
	if len(snap.Rows) != 4 {
		t.Errorf("len(Rows) = %d, want 4 (unpriced model excluded)", len(snap.Rows))
	}
}

func TestParseSnapshotErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		raw       []byte
		sourceURL string
	}{
		{name: "empty body", raw: []byte{}, sourceURL: "https://models.dev/api.json"},
		{name: "malformed json", raw: []byte("{not json"), sourceURL: "https://models.dev/api.json"},
		{name: "empty source url", raw: []byte(`{}`), sourceURL: ""},
		{name: "oversized body", raw: bytes.Repeat([]byte("a"), pricing.MaxSnapshotBytes+1), sourceURL: "https://models.dev/api.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := pricing.ParseSnapshot(tt.raw, tt.sourceURL, time.Now()); err == nil {
				t.Error("ParseSnapshot() error = nil, want error")
			}
		})
	}
}

func TestFetchSnapshot(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(realisticFixture))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	snap, err := pricing.FetchSnapshot(ctx, srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("FetchSnapshot() error = %v", err)
	}
	if len(snap.Rows) != 4 {
		t.Errorf("len(Rows) = %d, want 4", len(snap.Rows))
	}
	if snap.SourceURL != srv.URL {
		t.Errorf("SourceURL = %q, want %q", snap.SourceURL, srv.URL)
	}
}

func TestFetchSnapshotBoundsBody(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(bytes.Repeat([]byte("a"), pricing.MaxSnapshotBytes+1))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := pricing.FetchSnapshot(ctx, srv.Client(), srv.URL)
	if err == nil {
		t.Fatal("FetchSnapshot() error = nil, want error for an oversized body")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("error = %v, want an 'exceeds' bound error", err)
	}
}

func TestFetchSnapshotRespectsContextDeadline(t *testing.T) {
	t.Parallel()
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	// Registered before the close(block) cleanup below, so LIFO order runs
	// close(block) first (unblocking the handler) and srv.Close second (which
	// would otherwise hang forever waiting for that outstanding request).
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(block) })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := pricing.FetchSnapshot(ctx, srv.Client(), srv.URL)
	if err == nil {
		t.Fatal("FetchSnapshot() error = nil, want a context-deadline error")
	}
}

func TestFetchSnapshotDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()

	var unsafeHit atomic.Bool
	unsafe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		unsafeHit.Store(true)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(realisticFixture))
	}))
	t.Cleanup(unsafe.Close)

	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, unsafe.URL, http.StatusFound)
	}))
	t.Cleanup(redirecting.Close)

	run := func(t *testing.T, client *http.Client) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := pricing.FetchSnapshot(ctx, client, redirecting.URL)
		if err == nil {
			t.Fatal("FetchSnapshot() error = nil, want an error for an unfollowed redirect (unexpected status 302)")
		}
		if !strings.Contains(err.Error(), "302") {
			t.Errorf("error = %v, want it to mention the unfollowed 302 status", err)
		}
		if unsafeHit.Load() {
			t.Error("FetchSnapshot followed the redirect to the unsafe target; it must not")
		}
	}

	t.Run("nil client", func(t *testing.T) {
		run(t, nil)
	})
	t.Run("caller-supplied client with no CheckRedirect of its own", func(t *testing.T) {
		unsafeHit.Store(false)
		// redirecting.Client() is a plain *http.Client with CheckRedirect
		// unset (Go's default 10-redirect-following behavior), exactly the
		// caller-supplied-client case FetchSnapshot must also harden.
		run(t, redirecting.Client())
	})
}

func TestFetchSnapshotRejectsUnsafeURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		url  string
	}{
		{name: "empty", url: ""},
		{name: "non-https non-loopback", url: "http://example.com/api.json"},
		{name: "unsupported scheme", url: "ftp://models.dev/api.json"},
		{name: "userinfo embedded", url: "https://user:pass@models.dev/api.json"},
		{name: "no host", url: "https:///api.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := pricing.FetchSnapshot(ctx, http.DefaultClient, tt.url); err == nil {
				t.Errorf("FetchSnapshot(%q) error = nil, want a rejected-url error", tt.url)
			}
		})
	}
}
