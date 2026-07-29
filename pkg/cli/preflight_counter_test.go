package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/looprig/inference"
	"github.com/looprig/inference/model"
	"github.com/looprig/mpqt/pkg/pricing"
)

// stubCounter is a no-op pricing.Counter used to assert the success path
// returns the constructed counter unchanged.
type stubCounter struct{}

func (stubCounter) Count(context.Context, inference.Request) (int, string, error) {
	return 0, "stub", nil
}

// TestCounterForPreflightDegradesOnError is the regression guard for the LM
// Studio failure: a provider whose exact token counter cannot be built must
// degrade the preflight estimate to the byte heuristic (a nil Counter) with a
// note, never abort the command.
func TestCounterForPreflightDegradesOnError(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	app := App{
		NewCounter: func(model.Model) (pricing.Counter, error) {
			return nil, errors.New("exact provider context counter unavailable")
		},
	}
	got := app.counterForPreflight(model.Model{Provider: "lmstudio"}, &out)
	if got != nil {
		t.Errorf("counter = %v, want nil (degrade to heuristic)", got)
	}
	if !strings.Contains(out.String(), "byte heuristic") {
		t.Errorf("expected a note about falling back to a byte heuristic, got: %q", out.String())
	}
	if !strings.Contains(out.String(), "lmstudio") {
		t.Errorf("expected the note to name the provider, got: %q", out.String())
	}
}

// TestCounterForPreflightSilentWhenUnconfigured proves a nil NewCounter (the
// heuristic-by-configuration path) returns nil without emitting a note — that
// is not an error, just the absence of a counter.
func TestCounterForPreflightSilentWhenUnconfigured(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	app := App{} // NewCounter nil
	if got := app.counterForPreflight(model.Model{Provider: "lmstudio"}, &out); got != nil {
		t.Errorf("counter = %v, want nil", got)
	}
	if out.Len() != 0 {
		t.Errorf("expected no note when NewCounter is unconfigured, got: %q", out.String())
	}
}

// TestCounterForPreflightReturnsCounterOnSuccess proves a successfully built
// counter is passed through unchanged with no note.
func TestCounterForPreflightReturnsCounterOnSuccess(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	want := stubCounter{}
	app := App{
		NewCounter: func(model.Model) (pricing.Counter, error) { return want, nil },
	}
	got := app.counterForPreflight(model.Model{Provider: "anthropic"}, &out)
	if got != pricing.Counter(want) {
		t.Errorf("counter = %v, want the constructed stubCounter", got)
	}
	if out.Len() != 0 {
		t.Errorf("expected no note on success, got: %q", out.String())
	}
}
