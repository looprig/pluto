// Package run executes Pluto packs against a target and rolls the results up
// into a qual.Scorecard. It is the shared execution core behind both
// pkg/plutotest (offline, go-test-driven runs) and the CLI (live runs against
// a real inference client): both re-express their own Spec in terms of this
// package's Execute and never re-implement the plan -> eval.Run -> scorecard
// pipeline themselves.
//
// pkg/run never constructs an inference.Client itself ("dependency
// confinement"): BuildTarget accepts a caller-supplied client and turns a
// table's environment template plus the run's manifest into a live
// eval.Target. Offline runs never call BuildTarget at all; they hand Execute
// a pre-built fixture target directly.
package run

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/looprig/eval"
	inferenceeval "github.com/looprig/eval/target/inference"
	"github.com/looprig/inference"
	"github.com/looprig/pluto/pkg/packfile"
	"github.com/looprig/pluto/pkg/qual"
)

// Spec is one qualification execution. Target may be any eval.Target
// (scripted fixture or live inference target); pkg/run never constructs
// clients (design: "Dependency confinement").
//
// Exactly one of Target and TargetForTable must be set. Target is used as-is
// for every table (the offline case: one fixture target answers every
// scenario in every pack). TargetForTable builds one target per table plan
// (the live case: the CLI constructs a fresh live target per table from that
// table's own environment via BuildTarget). Setting both, or neither, is a
// configuration error rejected by Execute before any pack is planned or any
// target is invoked.
type Spec struct {
	Manifest       qual.Manifest
	Packs          []qual.Pack
	Target         eval.Target
	TargetForTable func(qual.TablePlan) (eval.Target, error)
	Config         eval.RunConfig // zero value = eval defaults

	// Progress, if non-nil, is called once per table plan in pack/plan order,
	// just before that table is executed or recorded as skipped. It is a UI
	// hook for live per-table feedback during a long live run (a run of many
	// tables against a real model emits nothing else until the final report);
	// offline callers like plutotest leave it nil. It must not mutate the plan.
	Progress func(qual.TablePlan)

	// OnResult, if non-nil, is called once per RUNNABLE table immediately after
	// it executes, with that table's eval.Report — the companion to Progress
	// for a live UI that shows each table's pass/fail outcome as it completes.
	// It never fires for a skipped table (those have no report) and must not
	// mutate the report. Under table concurrency it is called from a worker
	// goroutine, possibly for several tables at once, so an implementation must
	// be safe for concurrent use.
	OnResult func(qual.TablePlan, eval.Report)

	// TableConcurrency is how many tables execute in parallel. 0 or 1 runs them
	// sequentially (the default, preserving strict pack/table order and
	// stop-at-first-error). A value >1 runs up to that many tables at once
	// through a worker pool — the throughput win for a corpus of many
	// single-scenario tables, where eval's own per-sample Config.Concurrency
	// cannot help. Per-provider request load is bounded independently by the
	// caller's rate-limited client (pluto's --max-concurrent-requests/--max-rpm).
	TableConcurrency int
}

// tableConcurrency returns the effective worker count (at least 1).
func (s Spec) tableConcurrency() int {
	if s.TableConcurrency > 1 {
		return s.TableConcurrency
	}
	return 1
}

// validate enforces the exactly-one-of Target/TargetForTable rule. It is
// checked before any pack is planned or any target constructed/invoked, so a
// misconfigured Spec never spends a paid call or partial work before failing.
func (s Spec) validate() error {
	switch {
	case s.Target != nil && s.TargetForTable != nil:
		return errors.New("run: Spec: exactly one of Target or TargetForTable must be set, got both")
	case s.Target == nil && s.TargetForTable == nil:
		return errors.New("run: Spec: exactly one of Target or TargetForTable must be set, got neither")
	default:
		return nil
	}
}

// targetFor resolves the target to use for one runnable table plan.
func (s Spec) targetFor(plan qual.TablePlan) (eval.Target, error) {
	if s.TargetForTable != nil {
		return s.TargetForTable(plan)
	}
	return s.Target, nil
}

// Result binds the rolled-up scorecard to its per-table eval reports and the
// skipped-table plans (visible coverage, never silent).
type Result struct {
	Scorecard qual.Scorecard
	Reports   []eval.Report
	Skipped   []qual.TablePlan
}

// Execute plans every pack in s against s.Manifest and runs each runnable
// table plan through eval.Run using the plan's own Suite and Evaluators. A
// non-runnable plan (missing capability) contributes a Skipped TableResult
// and is retained verbatim in Result.Skipped; it is never executed and never
// silently dropped. Execute returns an error from Spec validation, from
// qual.Plan (an ill-formed pack or manifest), from Spec.TargetForTable, or
// from eval.Run's own preflight check. Only Spec validation failure (before
// any pack is planned or any target invoked) yields a zero-value Result: once
// any table has actually been planned or run, a later failure returns the
// Result accumulated from every pack/table processed successfully so far
// ALONGSIDE the error, rather than discarding it. This extends the same
// "visible coverage, never silent" philosophy behind Result.Skipped to the
// error path — an operator running many tables against a live target should
// not lose every already-executed (and potentially paid) table's report just
// because a later table hit a transient error. Callers must check the error
// to know whether the run as a whole completed; a non-nil error always means
// something failed, but the accompanying Result may still carry partial,
// trustworthy coverage worth reporting.
func Execute(ctx context.Context, s Spec) (Result, error) {
	if err := s.validate(); err != nil {
		return Result{}, err
	}
	if s.tableConcurrency() > 1 {
		return s.executeParallel(ctx)
	}
	return s.executeSequential(ctx)
}

// executeSequential runs every table in strict pack/table order, one at a time,
// returning the partial result accumulated so far alongside the first error.
func (s Spec) executeSequential(ctx context.Context) (Result, error) {
	var results []qual.TableResult
	var reports []eval.Report
	var skipped []qual.TablePlan
	partial := func() Result {
		return Result{
			Scorecard: qual.Scorecard{Manifest: s.Manifest, Results: results},
			Reports:   reports,
			Skipped:   skipped,
		}
	}

	for _, pack := range s.Packs {
		plans, err := qual.Plan(pack, s.Manifest)
		if err != nil {
			return partial(), fmt.Errorf("run: Plan(%s): %w", pack.Name, err)
		}
		for _, plan := range plans {
			if s.Progress != nil {
				s.Progress(plan)
			}
			if !plan.Runnable {
				skipped = append(skipped, plan)
				results = append(results, skippedResult(plan))
				continue
			}

			target, err := s.targetFor(plan)
			if err != nil {
				return partial(), fmt.Errorf("run: TargetForTable(%s/%s): %w", plan.Pack, plan.Table, err)
			}

			report, err := eval.Run(ctx, s.Config, plan.Suite, target, plan.Evaluators...)
			if err != nil {
				return partial(), fmt.Errorf("run: eval.Run(%s/%s): %w", plan.Pack, plan.Table, err)
			}
			if s.OnResult != nil {
				s.OnResult(plan, report)
			}
			reports = append(reports, report)
			results = append(results, runnableResult(plan, report))
		}
	}

	return partial(), nil
}

// executeParallel runs up to s.tableConcurrency() tables at once through a
// worker pool. All packs are planned first (a planning error aborts before any
// paid call); runnable tables are dispatched to workers, and the first table
// error cancels the shared context so in-flight work stops rather than burning
// more paid calls. Results are reassembled in the original pack/table order so
// the scorecard is deterministic regardless of completion order.
func (s Spec) executeParallel(ctx context.Context) (Result, error) {
	var plans []qual.TablePlan
	for _, pack := range s.Packs {
		pp, err := qual.Plan(pack, s.Manifest)
		if err != nil {
			return Result{Scorecard: qual.Scorecard{Manifest: s.Manifest}}, fmt.Errorf("run: Plan(%s): %w", pack.Name, err)
		}
		plans = append(plans, pp...)
	}

	type outcome struct {
		result    qual.TableResult
		report    eval.Report
		hasReport bool
	}
	outcomes := make([]outcome, len(plans))

	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		sem      = make(chan struct{}, s.tableConcurrency())
		wg       sync.WaitGroup
		errMu    sync.Mutex
		firstErr error
	)
	recordErr := func(err error) {
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		errMu.Unlock()
		cancel() // stop dispatching / running further tables
	}

	for i := range plans {
		plan := plans[i]
		if !plan.Runnable {
			if s.Progress != nil {
				s.Progress(plan)
			}
			outcomes[i] = outcome{result: skippedResult(plan)}
			continue
		}
		if cctx.Err() != nil {
			continue // a prior table failed; do not start more
		}
		select {
		case sem <- struct{}{}:
		case <-cctx.Done():
			continue
		}
		wg.Add(1)
		go func(i int, plan qual.TablePlan) {
			defer wg.Done()
			defer func() { <-sem }()
			if cctx.Err() != nil {
				return
			}
			if s.Progress != nil {
				s.Progress(plan)
			}
			target, err := s.targetFor(plan)
			if err != nil {
				recordErr(fmt.Errorf("run: TargetForTable(%s/%s): %w", plan.Pack, plan.Table, err))
				return
			}
			report, err := eval.Run(cctx, s.Config, plan.Suite, target, plan.Evaluators...)
			if err != nil {
				recordErr(fmt.Errorf("run: eval.Run(%s/%s): %w", plan.Pack, plan.Table, err))
				return
			}
			if s.OnResult != nil {
				s.OnResult(plan, report)
			}
			outcomes[i] = outcome{result: runnableResult(plan, report), report: report, hasReport: true}
		}(i, plan)
	}
	wg.Wait()

	var results []qual.TableResult
	var reports []eval.Report
	var skipped []qual.TablePlan
	for i, plan := range plans {
		o := outcomes[i]
		switch {
		case !plan.Runnable:
			skipped = append(skipped, plan)
			results = append(results, o.result)
		case o.hasReport:
			reports = append(reports, o.report)
			results = append(results, o.result)
		}
	}
	return Result{
		Scorecard: qual.Scorecard{Manifest: s.Manifest, Results: results},
		Reports:   reports,
		Skipped:   skipped,
	}, firstErr
}

// skippedResult builds the TableResult recorded for a capability-skipped table.
func skippedResult(plan qual.TablePlan) qual.TableResult {
	return qual.TableResult{
		Pack: plan.Pack, Table: plan.Table, Dimension: plan.Dimension,
		Skipped: true, Missing: plan.Missing,
	}
}

// runnableResult builds the TableResult recorded for an executed table.
func runnableResult(plan qual.TablePlan, report eval.Report) qual.TableResult {
	return qual.TableResult{
		Pack: plan.Pack, Table: plan.Table, Dimension: plan.Dimension,
		Report: report,
	}
}

// BuildTarget constructs the live inference target for one table: it expands
// env into the provider-neutral request template (Environment.Template,
// pkg/packfile), stamps the manifest's model onto that template, and wraps
// both in an eval.Target revisioned to tableRevision so Sample.Validate
// accepts observations against that table's suite (ground rule 2). client is
// supplied by the caller (the CLI module) — BuildTarget never constructs one
// itself.
//
// A nil env is legal (a table without an environment yields a zero
// request template, per Environment.Template). m is validated before use, so
// an unknown or malformed manifest field — including an unknown capability —
// is rejected here rather than silently producing an under- or
// over-permissioned model descriptor.
func BuildTarget(client inference.Client, m qual.Manifest, env *packfile.Environment, tableRevision eval.Revision) (eval.Target, error) {
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("run: BuildTarget: invalid manifest: %w", err)
	}

	req, err := env.Template()
	if err != nil {
		return nil, fmt.Errorf("run: BuildTarget: environment template: %w", err)
	}
	req.Model = ManifestModel(m)

	return inferenceeval.NewTarget(client, req, inferenceeval.WithRevision(tableRevision)), nil
}
