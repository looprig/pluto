// Package plutotest wires a Pluto pack execution into ordinary go test, and
// gates a test on the derived qualification disposition. It runs no trial
// loop of its own: each runnable table plan expands to exactly one
// eval.Suite executed once via eval.Run. Execution itself lives in
// pkg/run — this package is a thin go-test wrapper around run.Execute that
// adds t.Fatalf-on-error and t.Logf-on-skip test ergonomics.
package plutotest

import (
	"strings"
	"testing"

	"github.com/looprig/eval"
	"github.com/looprig/pluto/pkg/profile"
	"github.com/looprig/pluto/pkg/qual"
	"github.com/looprig/pluto/pkg/run"
)

// RunSpec is one full offline-or-live Pluto execution: a manifest, the packs
// to plan against it, the target to observe, and how many trials to run each
// scenario.
type RunSpec struct {
	Manifest qual.Manifest
	Packs    []qual.Pack
	Target   eval.Target
	Trials   int // passed straight to eval.RunConfig.Trials
}

// Run executes every pack in spec against spec.Target under spec.Manifest and
// returns the resulting Scorecard, by delegating to run.Execute (pkg/run):
// qual.Plan expands the manifest into per-table plans, a runnable plan is
// executed with eval.Run using spec.Target for every table, and a
// non-runnable plan (missing capability) becomes a Skipped TableResult
// instead of being executed. Run logs each skipped table's missing
// capabilities via t.Logf, and t.Fatalf's on any error run.Execute returns —
// a broken pack or manifest, or eval.Run's own preflight failure, is a bug in
// the test setup, not a verdict to record. Pluto implements no trial loop of
// its own — Trials passes straight through to eval.RunConfig.
func Run(t *testing.T, spec RunSpec) qual.Scorecard {
	t.Helper()
	ctx := t.Context()

	res, err := run.Execute(ctx, run.Spec{
		Manifest: spec.Manifest,
		Packs:    spec.Packs,
		Target:   spec.Target,
		Config:   eval.RunConfig{Trials: spec.Trials},
	})
	if err != nil {
		t.Fatalf("plutotest: run.Execute: %v", err)
	}
	for _, plan := range res.Skipped {
		missing := make([]string, 0, len(plan.Missing))
		for _, m := range plan.Missing {
			missing = append(missing, string(m))
		}
		t.Logf("plutotest: skipping %s/%s: missing capabilities [%s]", plan.Pack, plan.Table, strings.Join(missing, ", "))
	}
	return res.Scorecard
}

// RequireDisposition evaluates card against p (profile.Evaluate) and
// t.Fatalf's when the derived disposition is not one of allowed, printing the
// disposition and every requirement outcome. An empty allowed list is itself
// a t.Fatal: a gate that accepts no disposition is a configuration bug, not a
// legitimate all-reject gate.
func RequireDisposition(t *testing.T, card qual.Scorecard, p profile.Profile, allowed ...profile.Disposition) {
	t.Helper()
	if len(allowed) == 0 {
		t.Fatal("plutotest: RequireDisposition requires at least one allowed disposition")
		return
	}
	result, err := profile.Evaluate(card, p)
	if err != nil {
		t.Fatalf("plutotest: profile.Evaluate: %v", err)
	}
	for _, d := range allowed {
		if result.Disposition == d {
			return
		}
	}
	for _, rr := range result.Requirements {
		t.Logf("requirement %+v -> %s", rr.Requirement, rr.Outcome)
	}
	t.Fatalf("plutotest: disposition %s not in allowed %v", result.Disposition, allowed)
}
