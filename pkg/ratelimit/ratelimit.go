// Package ratelimit decorates an inference.Client with client-side rate
// limiting: a requests-per-minute pacing limiter, an in-flight concurrency
// cap, and automatic retry with exponential backoff on rate-limit (HTTP 429)
// and transient server (5xx) / network failures.
//
// It is llm-free by design — it depends only on the inference interfaces the
// root module already carries (via eval), so it sits in pkg/ and is wrapped
// around whatever concrete client cmd/mpqt constructs, without pulling
// github.com/looprig/llm into the root module graph. Both paid commands
// (`mpqt run`, `mpqt gen`) route their target and judge clients through it.
//
// Limitation: providers signal a precise wait via the HTTP Retry-After
// response header, but the inference transport does not surface response
// headers on an error (failure.APIError carries only Status/Message/Body), so
// this package cannot honor Retry-After. It falls back to exponential backoff
// with full jitter, which is the standard behavior when Retry-After is
// unavailable. Threading Retry-After through would require an inference-module
// change to capture the header on APIError.
package ratelimit

import (
	"context"
	"errors"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/looprig/core/content"
	"github.com/looprig/inference"
	"github.com/looprig/inference/failure"
	"github.com/looprig/inference/stream"
)

// Default backoff bounds applied when retries are enabled but the caller left
// the corresponding Config field at zero.
const (
	defaultBaseBackoff = 500 * time.Millisecond
	defaultMaxBackoff  = 30 * time.Second
)

// Config configures the decorator. The zero value enables nothing, and New
// returns the wrapped client unchanged — no pacing, no cap, no retries.
type Config struct {
	// MaxRPM caps request starts to this many per minute, spacing them evenly
	// (a request may wait up to 60s/MaxRPM before it is issued). 0 = unlimited.
	MaxRPM int
	// MaxConcurrent caps simultaneous in-flight requests. 0 = unlimited.
	MaxConcurrent int
	// MaxRetries is how many times a rate-limited / transient failure is
	// retried after the first attempt (so total attempts = MaxRetries+1).
	// 0 = no retries.
	MaxRetries int
	// BaseBackoff is the first retry's backoff base; each subsequent retry
	// doubles it, capped by MaxBackoff, with full jitter applied. Zero uses
	// defaultBaseBackoff when retries are enabled.
	BaseBackoff time.Duration
	// MaxBackoff caps any single backoff wait. Zero uses defaultMaxBackoff.
	MaxBackoff time.Duration
}

// enabled reports whether any behavior is turned on. A fully-zero Config is a
// no-op the decorator can skip entirely.
func (c Config) enabled() bool {
	return c.MaxRPM > 0 || c.MaxConcurrent > 0 || c.MaxRetries > 0
}

// New wraps inner with the behavior configured by cfg. If cfg enables nothing,
// inner is returned unchanged so the decorator adds no overhead on the common
// unconfigured path.
func New(inner inference.Client, cfg Config) inference.Client {
	if !cfg.enabled() {
		return inner
	}
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = defaultBaseBackoff
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = defaultMaxBackoff
	}
	c := &client{
		inner:     inner,
		cfg:       cfg,
		now:       time.Now,
		sleep:     sleepCtx,
		randFloat: rand.Float64,
	}
	if cfg.MaxRPM > 0 {
		c.pacer = &pacer{interval: time.Minute / time.Duration(cfg.MaxRPM), now: c.now}
	}
	if cfg.MaxConcurrent > 0 {
		c.sem = make(chan struct{}, cfg.MaxConcurrent)
	}
	return c
}

// client is the decorating inference.Client.
type client struct {
	inner inference.Client
	cfg   Config
	pacer *pacer        // nil ⇒ no RPM limit
	sem   chan struct{} // nil ⇒ no concurrency cap

	// Injected for tests; production defaults are set by New.
	now       func() time.Time
	sleep     func(context.Context, time.Duration) error
	randFloat func() float64
}

// Invoke implements inference.Client, applying pacing, the concurrency cap,
// and retry-with-backoff around the wrapped client's Invoke.
func (c *client) Invoke(ctx context.Context, req inference.Request) (*inference.Response, error) {
	var resp *inference.Response
	err := c.withPolicy(ctx, func() error {
		var e error
		resp, e = c.inner.Invoke(ctx, req)
		return e
	})
	return resp, err
}

// Stream implements inference.Client. Pacing and retry apply to establishing
// the stream (a non-2xx on setup surfaces as a retryable APIError before any
// chunk is read). The concurrency slot is held only around stream setup, not
// across the caller's subsequent reads — MaxConcurrent bounds request
// initiation, which is the Invoke-based path mpqt actually exercises.
func (c *client) Stream(ctx context.Context, req inference.Request) (*stream.StreamReader[content.Chunk], error) {
	var reader *stream.StreamReader[content.Chunk]
	err := c.withPolicy(ctx, func() error {
		var e error
		reader, e = c.inner.Stream(ctx, req)
		return e
	})
	return reader, err
}

// withPolicy runs op under the rate limiter and concurrency cap, retrying a
// retryable failure up to cfg.MaxRetries times with exponential backoff. It
// stops immediately on a non-retryable error, on exhausting retries, or on
// context cancellation.
func (c *client) withPolicy(ctx context.Context, op func() error) error {
	var lastErr error
	for attempt := 0; ; attempt++ {
		if err := c.pace(ctx); err != nil {
			return err
		}
		err := c.withSlot(ctx, op)
		if err == nil {
			return nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return err
		}
		if attempt >= c.cfg.MaxRetries || !isRetryable(err) {
			return err
		}
		if err := c.sleep(ctx, c.backoff(attempt)); err != nil {
			return errors.Join(lastErr, err)
		}
	}
}

// pace waits for the RPM limiter to admit one request start. It is a no-op
// when no RPM limit is configured.
func (c *client) pace(ctx context.Context) error {
	if c.pacer == nil {
		return nil
	}
	return c.sleep(ctx, c.pacer.reserve())
}

// withSlot runs op while holding one concurrency slot (a no-op acquire when no
// cap is configured). The slot is released as soon as op returns, so a
// goroutine sleeping between retries never occupies a slot.
func (c *client) withSlot(ctx context.Context, op func() error) error {
	if c.sem == nil {
		return op()
	}
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-c.sem }()
	return op()
}

// backoff returns the wait before the given retry attempt (0-indexed): an
// exponentially growing base capped by MaxBackoff, then full jitter in
// [0, capped].
func (c *client) backoff(attempt int) time.Duration {
	d := c.cfg.BaseBackoff
	for i := 0; i < attempt && d < c.cfg.MaxBackoff; i++ {
		d *= 2
	}
	if d > c.cfg.MaxBackoff {
		d = c.cfg.MaxBackoff
	}
	return time.Duration(c.randFloat() * float64(d))
}

// pacer is an evenly-spacing rate limiter: it hands out request-start slots at
// a fixed interval, reserving future slots so concurrent callers queue rather
// than burst. It bounds the average start rate without allowing a bucket-style
// burst, which is gentler on a provider's own limiter.
type pacer struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
	now      func() time.Time
}

// reserve claims the next start slot and returns how long the caller must wait
// before using it. Concurrent callers each get a distinct slot spaced by
// interval.
func (p *pacer) reserve() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	at := p.next
	if at.Before(now) {
		at = now
	}
	p.next = at.Add(p.interval)
	return at.Sub(now)
}

// isRetryable reports whether err is a transient failure worth retrying: a
// rate-limit (HTTP 429) or server (5xx) API error, or a network error. Client
// errors (other 4xx), validation errors, and context cancellation are not
// retried.
func isRetryable(err error) bool {
	var apiErr *failure.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Status == http.StatusTooManyRequests ||
			(apiErr.Status >= 500 && apiErr.Status <= 599)
	}
	var netErr *failure.NetworkError
	return errors.As(err, &netErr)
}

// sleepCtx sleeps for d or until ctx is cancelled, whichever comes first. A
// non-positive d returns immediately (still honoring an already-cancelled
// context).
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
