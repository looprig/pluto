package cli

import (
	"context"
	"flag"
	"testing"

	"github.com/looprig/core/content"
	"github.com/looprig/inference"
	"github.com/looprig/inference/model"
	"github.com/looprig/inference/stream"
	"github.com/looprig/mpqt/pkg/ratelimit"
)

// wiringFakeClient is a bare inference.Client used only to assert identity
// (whether App.client returned it unchanged or wrapped it).
type wiringFakeClient struct{}

func (wiringFakeClient) Invoke(context.Context, inference.Request) (*inference.Response, error) {
	return &inference.Response{}, nil
}
func (wiringFakeClient) Stream(context.Context, inference.Request) (*stream.StreamReader[content.Chunk], error) {
	return nil, nil
}

func TestRateLimitFlagDefaults(t *testing.T) {
	t.Parallel()
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	rf := registerRateLimitFlags(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := rf.config()
	want := ratelimit.Config{MaxRetries: defaultMaxRetries}
	if got != want {
		t.Errorf("default config = %+v, want %+v (retries on by default, no RPM/concurrency cap)", got, want)
	}
}

func TestRateLimitFlagsParseAndClamp(t *testing.T) {
	t.Parallel()
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	rf := registerRateLimitFlags(fs)
	if err := fs.Parse([]string{"--max-rpm", "120", "--max-concurrent-requests", "4", "--max-retries", "-3"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := rf.config()
	want := ratelimit.Config{MaxRPM: 120, MaxConcurrent: 4, MaxRetries: 0} // negative retries clamped to 0
	if got != want {
		t.Errorf("config = %+v, want %+v", got, want)
	}
}

func TestAppClientWrapsWhenRateLimitEnabled(t *testing.T) {
	t.Parallel()
	inner := wiringFakeClient{}
	app := App{
		NewClient: func(model.Model) (inference.Client, error) { return inner, nil },
		RateLimit: ratelimit.Config{MaxRetries: 3},
	}
	got, err := app.client(model.Model{})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	if got == inference.Client(inner) {
		t.Error("App.client returned the raw client; expected it wrapped in the rate limiter")
	}
}

func TestAppClientPassthroughWhenRateLimitDisabled(t *testing.T) {
	t.Parallel()
	inner := wiringFakeClient{}
	app := App{
		NewClient: func(model.Model) (inference.Client, error) { return inner, nil },
		// RateLimit left zero ⇒ passthrough.
	}
	got, err := app.client(model.Model{})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	if got != inference.Client(inner) {
		t.Error("App.client wrapped the client despite an unconfigured RateLimit; expected passthrough")
	}
}
