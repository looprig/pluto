// Package run executes MPQT packs against a target and rolls the results up
// into a qual.Scorecard. It is the shared execution core behind both
// pkg/mpqttest (offline, go-test-driven runs) and the CLI (live runs against
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

	"github.com/looprig/eval"
	inferenceeval "github.com/looprig/eval/target/inference"
	"github.com/looprig/inference"
	"github.com/looprig/mpqt/pkg/packfile"
	"github.com/looprig/mpqt/pkg/qual"
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
// from eval.Run's own preflight check — in every case the caller receives no
// partial Result, since a failure at any of these stages means the run as a
// whole did not produce trustworthy coverage.
func Execute(ctx context.Context, s Spec) (Result, error) {
	if err := s.validate(); err != nil {
		return Result{}, err
	}

	var results []qual.TableResult
	var reports []eval.Report
	var skipped []qual.TablePlan

	for _, pack := range s.Packs {
		plans, err := qual.Plan(pack, s.Manifest)
		if err != nil {
			return Result{}, fmt.Errorf("run: Plan(%s): %w", pack.Name, err)
		}
		for _, plan := range plans {
			if !plan.Runnable {
				skipped = append(skipped, plan)
				results = append(results, qual.TableResult{
					Pack: plan.Pack, Table: plan.Table, Dimension: plan.Dimension,
					Skipped: true, Missing: plan.Missing,
				})
				continue
			}

			target, err := s.targetFor(plan)
			if err != nil {
				return Result{}, fmt.Errorf("run: TargetForTable(%s/%s): %w", plan.Pack, plan.Table, err)
			}

			report, err := eval.Run(ctx, s.Config, plan.Suite, target, plan.Evaluators...)
			if err != nil {
				return Result{}, fmt.Errorf("run: eval.Run(%s/%s): %w", plan.Pack, plan.Table, err)
			}
			reports = append(reports, report)
			results = append(results, qual.TableResult{
				Pack: plan.Pack, Table: plan.Table, Dimension: plan.Dimension,
				Report: report,
			})
		}
	}

	return Result{
		Scorecard: qual.Scorecard{Manifest: s.Manifest, Results: results},
		Reports:   reports,
		Skipped:   skipped,
	}, nil
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
