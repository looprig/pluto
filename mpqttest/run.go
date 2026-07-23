// Package mpqttest wires an MPQT pack execution into ordinary go test, and
// gates a test on the derived qualification disposition. It runs no trial
// loop of its own: each runnable table plan expands to exactly one
// eval.Suite executed once via eval.Run.
package mpqttest

import (
	"fmt"
	"strings"
	"testing"

	"github.com/looprig/eval"
	"github.com/looprig/mpqt"
	"github.com/looprig/mpqt/profile"
)

// RunSpec is one full offline-or-live MPQT execution: a manifest, the packs
// to plan against it, the target to observe, and how many trials to run each
// scenario.
type RunSpec struct {
	Manifest mpqt.Manifest
	Packs    []mpqt.Pack
	Target   eval.Target
	Trials   int // passed straight to eval.RunConfig.Trials
}

// Run executes every pack in spec against spec.Target under spec.Manifest and
// returns the resulting Scorecard. For each pack, mpqt.Plan expands the
// manifest into per-table plans; a runnable plan is executed with eval.Run
// inside its own t.Run(pack/table) subtest, and a non-runnable plan (missing
// capability) becomes a Skipped TableResult, logging the missing
// capabilities rather than attempting execution. eval.Run's own preflight
// error is a t.Fatalf: a broken pack or manifest is a bug in the test setup,
// not a verdict to record. MPQT implements no trial loop of its own — Trials
// passes straight through to eval.RunConfig.
func Run(t *testing.T, spec RunSpec) mpqt.Scorecard {
	t.Helper()
	ctx := t.Context()

	var results []mpqt.TableResult
	for _, pack := range spec.Packs {
		plans, err := mpqt.Plan(pack, spec.Manifest)
		if err != nil {
			t.Fatalf("mpqttest: Plan(%s): %v", pack.Name, err)
		}
		for _, plan := range plans {
			name := fmt.Sprintf("%s/%s", plan.Pack, plan.Table)
			t.Run(name, func(t *testing.T) {
				if !plan.Runnable {
					missing := make([]string, 0, len(plan.Missing))
					for _, m := range plan.Missing {
						missing = append(missing, string(m))
					}
					t.Logf("mpqttest: skipping %s: missing capabilities [%s]", name, strings.Join(missing, ", "))
					results = append(results, mpqt.TableResult{
						Pack: plan.Pack, Table: plan.Table, Dimension: plan.Dimension,
						Skipped: true, Missing: plan.Missing,
					})
					return
				}
				report, err := eval.Run(ctx, eval.RunConfig{Trials: spec.Trials}, plan.Suite, spec.Target, plan.Evaluators...)
				if err != nil {
					t.Fatalf("mpqttest: eval.Run(%s): %v", name, err)
				}
				results = append(results, mpqt.TableResult{
					Pack: plan.Pack, Table: plan.Table, Dimension: plan.Dimension,
					Report: report,
				})
			})
		}
	}
	return mpqt.Scorecard{Manifest: spec.Manifest, Results: results}
}

// RequireDisposition evaluates card against p (profile.Evaluate) and
// t.Fatalf's when the derived disposition is not one of allowed, printing the
// disposition and every requirement outcome. An empty allowed list is itself
// a t.Fatal: a gate that accepts no disposition is a configuration bug, not a
// legitimate all-reject gate.
func RequireDisposition(t *testing.T, card mpqt.Scorecard, p profile.Profile, allowed ...profile.Disposition) {
	t.Helper()
	if len(allowed) == 0 {
		t.Fatal("mpqttest: RequireDisposition requires at least one allowed disposition")
		return
	}
	result, err := profile.Evaluate(card, p)
	if err != nil {
		t.Fatalf("mpqttest: profile.Evaluate: %v", err)
	}
	for _, d := range allowed {
		if result.Disposition == d {
			return
		}
	}
	for _, rr := range result.Requirements {
		t.Logf("requirement %+v -> %s", rr.Requirement, rr.Outcome)
	}
	t.Fatalf("mpqttest: disposition %s not in allowed %v", result.Disposition, allowed)
}
