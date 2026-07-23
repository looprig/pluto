package pricing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// MaxSnapshotBytes bounds a models.dev price snapshot, whether it arrives
// over the network (FetchSnapshot) or is handed to ParseSnapshot directly
// (e.g. a caller-cached copy on disk). It rejects an absurd or hostile
// response before it is decoded, in either path.
const MaxSnapshotBytes = 8 << 20 // 8 MiB

// Snapshot is a frozen models.dev price table with provenance: where it came
// from, when it was fetched, and a digest of the exact bytes it was parsed
// from, so a caller can prove which price table priced a given run.
type Snapshot struct {
	SourceURL string
	FetchedAt time.Time
	Digest    string // sha256 hex of the raw snapshot bytes
	Rows      map[string]Rates
}

// Rates are USD per million tokens. A nil field means that dimension is not
// priced in the catalog (unknown), never zero; a dimension explicitly priced
// at zero is a non-nil pointer to 0.0. See Cost for how this distinction is
// used.
type Rates struct {
	Input, Output, Reasoning, CacheRead, CacheWrite *float64
}

// The models.dev API shape assumed here (verified only against the task's
// documented best-guess, not against a live fetch — see snapshotWire doc
// below): an object keyed by provider id, each provider carrying a "models"
// object keyed by model id, each model optionally carrying a "cost" object
// with numeric "input"/"output"/"reasoning"/"cache_read"/"cache_write"
// fields in USD per million tokens. THIS SHAPE MUST BE VERIFIED AGAINST THE
// LIVE https://models.dev/api.json RESPONSE BEFORE FetchSnapshot IS USED
// AGAINST REAL PRICES.

// snapshotWire is the top-level models.dev document: provider id -> provider.
type snapshotWire map[string]providerWire

// providerWire is one provider's entry: model id -> model.
type providerWire struct {
	Models map[string]modelWire `json:"models"`
}

// modelWire is one model's entry. Cost is a pointer so a model with no cost
// object at all (nothing priced, nothing known) is distinguishable from one
// whose cost object is present but omits individual dimensions.
type modelWire struct {
	Cost *costWire `json:"cost"`
}

// costWire mirrors the models.dev cost object. Every field is a pointer so
// encoding/json leaves it nil when the source omits the key, distinct from a
// key explicitly present with value 0 — exactly the distinction Rates needs.
type costWire struct {
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	Reasoning  *float64 `json:"reasoning"`
	CacheRead  *float64 `json:"cache_read"`
	CacheWrite *float64 `json:"cache_write"`
}

// ParseSnapshot decodes raw as a models.dev price document and returns the
// resulting Snapshot, stamped with sourceURL and fetchedAt for provenance and
// a sha256 digest of raw itself (not a re-encoding — the digest proves which
// exact bytes were parsed). raw is bounded to MaxSnapshotBytes before
// decoding: an oversized document is rejected outright, whether it reached
// here from the network or a local cache.
//
// Decoding tolerates unknown fields (the live document carries many fields
// this package does not need, such as ids, names, and context limits) and
// extracts only the cost object of each model. A model with no cost object
// at all is omitted from Rows entirely: nothing is known about it, so no row
// is worth reporting. A model with a cost object is always included, even if
// every individual dimension is absent (nil) — that is itself informative
// (the catalog knows about the model but prices none of its dimensions).
func ParseSnapshot(raw []byte, sourceURL string, fetchedAt time.Time) (Snapshot, error) {
	if len(raw) == 0 {
		return Snapshot{}, errors.New("pricing: ParseSnapshot: raw must not be empty")
	}
	if len(raw) > MaxSnapshotBytes {
		return Snapshot{}, fmt.Errorf("pricing: ParseSnapshot: raw exceeds %d bytes", MaxSnapshotBytes)
	}
	if sourceURL == "" {
		return Snapshot{}, errors.New("pricing: ParseSnapshot: sourceURL must not be empty")
	}

	var wire snapshotWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return Snapshot{}, fmt.Errorf("pricing: ParseSnapshot: decode: %w", err)
	}

	rows := make(map[string]Rates)
	for providerID, provider := range wire {
		for modelID, m := range provider.Models {
			if m.Cost == nil {
				continue
			}
			rows[providerID+"/"+modelID] = Rates{
				Input:      m.Cost.Input,
				Output:     m.Cost.Output,
				Reasoning:  m.Cost.Reasoning,
				CacheRead:  m.Cost.CacheRead,
				CacheWrite: m.Cost.CacheWrite,
			}
		}
	}

	sum := sha256.Sum256(raw)
	return Snapshot{
		SourceURL: sourceURL,
		FetchedAt: fetchedAt,
		Digest:    hex.EncodeToString(sum[:]),
		Rows:      rows,
	}, nil
}

// FetchSnapshot fetches a models.dev price document over HTTP and parses it
// with ParseSnapshot. It builds the request with ctx (every I/O call here
// carries the caller's deadline — there is no unbounded blocking) and bounds
// the response body to MaxSnapshotBytes with io.LimitReader, reading one
// byte past the bound so an oversized body is detected and rejected rather
// than silently truncated. client may be nil, in which case a default client
// is used.
//
// url is validated before use: it must be a syntactically safe https URL
// (or http restricted to a loopback host, for local testing), with a host
// present and no embedded userinfo. This is the same rule the inference
// module applies to a Model's BaseURL, applied here because url is
// caller-supplied and this function makes it the target of a real network
// request.
//
// That validation only covers the URL actually passed in: without more, the
// real HTTP request would still follow Go's default redirect behavior (up to
// 10 redirects, none of them re-validated), so a validated
// https://models.dev/api.json that later 302s to an internal or unsafe host
// would sail through unchecked. To close that gap, FetchSnapshot disables
// redirect-following — mirroring the same CheckRedirect: http.ErrUseLastResponse
// policy the inference module's own transport client uses (see
// inference/transport/client.go's newHTTPClient) — on whichever client ends
// up making the request: when client is nil, the default client it builds
// never follows redirects; when the caller supplies their own client, that
// same policy is applied to a copy of it (the caller's original client is
// never mutated) unless the caller has already set their own CheckRedirect,
// in which case that explicit choice is respected as-is. Either way, a 3xx
// response is surfaced as its own (non-200) status and rejected below,
// rather than silently chased.
func FetchSnapshot(ctx context.Context, client *http.Client, rawURL string) (Snapshot, error) {
	if err := validateFetchURL(rawURL); err != nil {
		return Snapshot{}, err
	}
	client = noRedirectClient(client)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Snapshot{}, fmt.Errorf("pricing: FetchSnapshot: build request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return Snapshot{}, fmt.Errorf("pricing: FetchSnapshot: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Snapshot{}, fmt.Errorf("pricing: FetchSnapshot: unexpected status %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxSnapshotBytes+1))
	if err != nil {
		return Snapshot{}, fmt.Errorf("pricing: FetchSnapshot: read body: %w", err)
	}
	if len(body) > MaxSnapshotBytes {
		return Snapshot{}, fmt.Errorf("pricing: FetchSnapshot: response exceeds %d bytes", MaxSnapshotBytes)
	}

	return ParseSnapshot(body, rawURL, time.Now())
}

// noRedirectClient resolves the *http.Client FetchSnapshot actually uses:
// client itself if it already declares a CheckRedirect policy (the caller's
// explicit choice is respected); otherwise a client that behaves like client
// (or, if client is nil, like http.DefaultClient) in every other respect but
// never follows a redirect. It never mutates the client passed in — a
// non-nil client is copied by value before CheckRedirect is set on the copy.
func noRedirectClient(client *http.Client) *http.Client {
	if client == nil {
		return &http.Client{CheckRedirect: refuseRedirect}
	}
	if client.CheckRedirect != nil {
		return client
	}
	cp := *client
	cp.CheckRedirect = refuseRedirect
	return &cp
}

// refuseRedirect is an http.Client.CheckRedirect that never follows a
// redirect: it mirrors the inference module's own transport client (see
// inference/transport/client.go's newHTTPClient), which disables
// redirect-following for the same reason FetchSnapshot needs to here — a
// fixed API endpoint has no legitimate need to redirect, and following one
// would retarget an already-validated URL at an unvalidated host with no
// re-validation. Returning http.ErrUseLastResponse makes http.Client return
// the 3xx response itself instead of chasing Location.
func refuseRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

// validateFetchURL rejects a URL that is empty, unparsable, carries
// userinfo, has no host, or uses a scheme other than https (http is
// permitted only against a loopback host, so local test servers work).
func validateFetchURL(raw string) error {
	if raw == "" {
		return errors.New("pricing: FetchSnapshot: url must not be empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("pricing: FetchSnapshot: invalid url: %w", err)
	}
	if u.User != nil {
		return errors.New("pricing: FetchSnapshot: url must not contain userinfo credentials")
	}
	if u.Host == "" {
		return errors.New("pricing: FetchSnapshot: url must include a host")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		switch strings.ToLower(u.Hostname()) {
		case "127.0.0.1", "::1", "localhost":
			return nil
		}
		return errors.New("pricing: FetchSnapshot: http scheme is permitted only for a loopback host (127.0.0.1, ::1, or localhost)")
	default:
		return errors.New("pricing: FetchSnapshot: scheme must be https (http allowed only for a loopback host)")
	}
}
