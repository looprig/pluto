package ratelimit

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/looprig/core/content"
	"github.com/looprig/inference"
	"github.com/looprig/inference/failure"
	"github.com/looprig/inference/stream"
)

// fakeClient is a scriptable inference.Client. errs[i] is returned on call i
// (Invoke or Stream); once calls run past len(errs), it returns nil (success).
type fakeClient struct {
	mu    sync.Mutex
	calls int
	errs  []error

	// concurrency instrumentation
	inFlight atomic.Int32
	maxSeen  atomic.Int32
	hold     time.Duration // how long each call blocks in flight
}

func (f *fakeClient) next() error {
	f.mu.Lock()
	i := f.calls
	f.calls++
	f.mu.Unlock()
	if i < len(f.errs) {
		return f.errs[i]
	}
	return nil
}

func (f *fakeClient) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeClient) Invoke(_ context.Context, _ inference.Request) (*inference.Response, error) {
	n := f.inFlight.Add(1)
	for {
		m := f.maxSeen.Load()
		if n <= m || f.maxSeen.CompareAndSwap(m, n) {
			break
		}
	}
	if f.hold > 0 {
		time.Sleep(f.hold)
	}
	f.inFlight.Add(-1)
	if err := f.next(); err != nil {
		return nil, err
	}
	return &inference.Response{}, nil
}

func (f *fakeClient) Stream(_ context.Context, _ inference.Request) (*stream.StreamReader[content.Chunk], error) {
	if err := f.next(); err != nil {
		return nil, err
	}
	return nil, nil
}

// newTestClient builds a decorator with the injected clock/sleep/jitter a test
// needs. recordSleep captures every backoff/pace duration the policy waits.
func newTestClient(inner inference.Client, cfg Config, recorded *[]time.Duration) *client {
	c := New(inner, cfg).(*client)
	var mu sync.Mutex
	c.sleep = func(ctx context.Context, d time.Duration) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if recorded != nil {
			mu.Lock()
			*recorded = append(*recorded, d)
			mu.Unlock()
		}
		return nil
	}
	c.randFloat = func() float64 { return 1.0 } // full jitter multiplier ⇒ deterministic max
	return c
}

func apiErr(status int) error {
	return &failure.APIError{Status: status, Message: "x"}
}

func TestNewPassthroughWhenDisabled(t *testing.T) {
	t.Parallel()
	inner := &fakeClient{}
	got := New(inner, Config{})
	if got != inference.Client(inner) {
		t.Fatalf("New with zero Config must return inner unchanged, got %T", got)
	}
}

func TestInvokeRetriesRateLimitThenSucceeds(t *testing.T) {
	t.Parallel()
	inner := &fakeClient{errs: []error{apiErr(429), apiErr(429)}}
	var slept []time.Duration
	c := newTestClient(inner, Config{MaxRetries: 4, BaseBackoff: 100 * time.Millisecond, MaxBackoff: time.Second}, &slept)

	resp, err := c.Invoke(context.Background(), inference.Request{})
	if err != nil {
		t.Fatalf("Invoke: want success after retries, got %v", err)
	}
	if resp == nil {
		t.Fatal("Invoke: want non-nil response on success")
	}
	if inner.count() != 3 {
		t.Errorf("attempts = %d, want 3 (two 429s then success)", inner.count())
	}
	if len(slept) != 2 {
		t.Fatalf("backoff sleeps = %d, want 2", len(slept))
	}
	if slept[0] != 100*time.Millisecond || slept[1] != 200*time.Millisecond {
		t.Errorf("backoff growth = %v, want [100ms 200ms]", slept)
	}
}

func TestInvokeDoesNotRetryClientError(t *testing.T) {
	t.Parallel()
	inner := &fakeClient{errs: []error{apiErr(400)}}
	c := newTestClient(inner, Config{MaxRetries: 5, BaseBackoff: time.Millisecond, MaxBackoff: time.Second}, nil)

	_, err := c.Invoke(context.Background(), inference.Request{})
	if err == nil {
		t.Fatal("Invoke: want the 400 error, got nil")
	}
	if inner.count() != 1 {
		t.Errorf("attempts = %d, want 1 (400 is not retryable)", inner.count())
	}
}

func TestInvokeRetryExhaustionReturnsLastError(t *testing.T) {
	t.Parallel()
	inner := &fakeClient{errs: []error{apiErr(503), apiErr(503), apiErr(503), apiErr(503)}}
	c := newTestClient(inner, Config{MaxRetries: 2, BaseBackoff: time.Millisecond, MaxBackoff: time.Second}, nil)

	_, err := c.Invoke(context.Background(), inference.Request{})
	var ae *failure.APIError
	if !errors.As(err, &ae) || ae.Status != 503 {
		t.Fatalf("want a 503 APIError after exhaustion, got %v", err)
	}
	if inner.count() != 3 {
		t.Errorf("attempts = %d, want 3 (initial + 2 retries)", inner.count())
	}
}

func TestBackoffGrowsAndCaps(t *testing.T) {
	t.Parallel()
	inner := &fakeClient{errs: []error{apiErr(429), apiErr(429), apiErr(429), apiErr(429)}}
	var slept []time.Duration
	c := newTestClient(inner, Config{MaxRetries: 4, BaseBackoff: 300 * time.Millisecond, MaxBackoff: time.Second}, &slept)

	_, _ = c.Invoke(context.Background(), inference.Request{})
	want := []time.Duration{300 * time.Millisecond, 600 * time.Millisecond, time.Second, time.Second}
	if len(slept) != len(want) {
		t.Fatalf("sleeps = %v, want %v", slept, want)
	}
	for i := range want {
		if slept[i] != want[i] {
			t.Errorf("sleep[%d] = %v, want %v (full sequence %v)", i, slept[i], want[i], slept)
		}
	}
}

func TestPacerReservesEvenlySpacedSlots(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p := &pacer{interval: time.Second, now: func() time.Time { return base }}
	// Clock frozen: successive reservations queue at 0s, 1s, 2s from now.
	for i, want := range []time.Duration{0, time.Second, 2 * time.Second} {
		if got := p.reserve(); got != want {
			t.Errorf("reserve #%d = %v, want %v", i, got, want)
		}
	}
}

func TestConcurrencyCapBoundsInFlight(t *testing.T) {
	t.Parallel()
	inner := &fakeClient{hold: 5 * time.Millisecond}
	c := New(inner, Config{MaxConcurrent: 3}).(*client)

	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.Invoke(context.Background(), inference.Request{})
		}()
	}
	wg.Wait()

	if max := inner.maxSeen.Load(); max > 3 {
		t.Errorf("max in-flight = %d, want <= 3 (MaxConcurrent)", max)
	}
	if inner.count() != 24 {
		t.Errorf("completed calls = %d, want 24", inner.count())
	}
}

func TestContextCancelStopsRetries(t *testing.T) {
	t.Parallel()
	inner := &fakeClient{errs: []error{apiErr(429), apiErr(429), apiErr(429)}}
	c := New(inner, Config{MaxRetries: 5, BaseBackoff: time.Hour, MaxBackoff: time.Hour}).(*client)
	// Real sleepCtx: a cancelled context must cut the (1h) backoff short.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.Invoke(ctx, inference.Request{})
	if err == nil {
		t.Fatal("Invoke: want a context error, got nil")
	}
	if inner.count() != 1 {
		t.Errorf("attempts = %d, want 1 (cancel stops before the first retry sleeps out)", inner.count())
	}
}

func TestStreamRetriesRateLimit(t *testing.T) {
	t.Parallel()
	inner := &fakeClient{errs: []error{apiErr(429)}}
	c := newTestClient(inner, Config{MaxRetries: 2, BaseBackoff: time.Millisecond, MaxBackoff: time.Second}, nil)

	if _, err := c.Stream(context.Background(), inference.Request{}); err != nil {
		t.Fatalf("Stream: want success after one retry, got %v", err)
	}
	if inner.count() != 2 {
		t.Errorf("attempts = %d, want 2", inner.count())
	}
}

func TestIsRetryable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"429 rate limit", apiErr(429), true},
		{"500 server", apiErr(500), true},
		{"503 unavailable", apiErr(503), true},
		{"400 bad request", apiErr(400), false},
		{"401 unauthorized", apiErr(401), false},
		{"404 not found", apiErr(404), false},
		{"network error", &failure.NetworkError{Err: errors.New("dial")}, true},
		{"plain error", errors.New("boom"), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isRetryable(tt.err); got != tt.want {
				t.Errorf("isRetryable(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
