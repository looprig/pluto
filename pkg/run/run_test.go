package run_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/looprig/core/content"
	"github.com/looprig/eval"
	"github.com/looprig/inference"
	"github.com/looprig/inference/stream"
	"github.com/looprig/pluto/pkg/codepacks/capability"
	"github.com/looprig/pluto/pkg/codepacks/structuredoutput"
	"github.com/looprig/pluto/pkg/packfile"
	"github.com/looprig/pluto/pkg/qual"
	fixtarget "github.com/looprig/pluto/pkg/qual/target"
	"github.com/looprig/pluto/pkg/run"
)

// countingTarget wraps a target, recording the maximum number of concurrent
// Observe calls so a test can assert the table worker pool honors its bound.
type countingTarget struct {
	inner    eval.Target
	inFlight atomic.Int32
	maxSeen  atomic.Int32
	hold     time.Duration
}

func (c *countingTarget) Name() string { return c.inner.Name() }

func (c *countingTarget) Observe(ctx context.Context, sc eval.Scenario) (eval.Observation, error) {
	n := c.inFlight.Add(1)
	for {
		m := c.maxSeen.Load()
		if n <= m || c.maxSeen.CompareAndSwap(m, n) {
			break
		}
	}
	if c.hold > 0 {
		time.Sleep(c.hold)
	}
	c.inFlight.Add(-1)
	return c.inner.Observe(ctx, sc)
}

// scriptedTextTarget scripts every scenario in pack with a plain text reply
// (no structured output), wrapped in a countingTarget — suitable for the
// text-evaluator capability pack, whose scenarios carry no structured-output
// expectation for scriptedFromPack to read.
func scriptedTextTarget(name string, pack qual.Pack, hold time.Duration) *countingTarget {
	scripts := map[string]fixtarget.Script{}
	for _, tbl := range pack.Tables {
		for _, sc := range tbl.Scenarios {
			scripts[sc.ID] = fixtarget.Script{Reply: "ok"}
		}
	}
	return &countingTarget{inner: fixtarget.NewScripted(name, scripts), hold: hold}
}

// conformingManifest declares structured_output, the only capability the
// structured-output pack's single table requires.
func conformingManifest() qual.Manifest {
	return qual.Manifest{
		TargetID:      "run-test",
		Role:          qual.RoleCandidate,
		Provider:      "test",
		Model:         "fake",
		APIFormat:     "openai",
		BaseURL:       "https://example.invalid/v1",
		Revision:      "r-fake",
		EndpointClass: qual.EndpointRemote,
		Capabilities:  []qual.Capability{qual.CapabilityStructuredOutput},
	}
}

// scriptedFromPack builds a Scripted eval.Target that answers every scenario
// in pack with a conforming structured-output reply, the same pattern
// pkg/plutotest's own tests use to fixture an offline run for this pack
// (pkg/codepacks/structuredoutput.V1, a real, validated, already-existing
// pack — this test exercises pkg/run.Execute's own behavior, not YAML pack
// loading, which the packfile tests already cover).
func scriptedFromPack(name string, pack qual.Pack) *fixtarget.Scripted {
	scripts := map[string]fixtarget.Script{}
	for _, tbl := range pack.Tables {
		for _, sc := range tbl.Scenarios {
			scripts[sc.ID] = fixtarget.Script{
				Reply: "ok",
				Structured: &fixtarget.Structured{
					SchemaName:     "output",
					SchemaRevision: sc.Expectation.StructuredOutput.Schema,
				},
			}
		}
	}
	return fixtarget.NewScripted(name, scripts)
}

// noCapManifest declares no capabilities, so the core-capability pack's tables
// (which require none) are all runnable.
func noCapManifest() qual.Manifest {
	m := conformingManifest()
	m.Capabilities = nil
	return m
}

// TestExecuteParallelRunsAllTablesBounded proves table concurrency runs every
// runnable table, caps in-flight tables at TableConcurrency, and reassembles
// results in the same pack/table order as a sequential run (determinism).
func TestExecuteParallelRunsAllTablesBounded(t *testing.T) {
	t.Parallel()
	pack := capability.V1() // many single-scenario tables, none needing a capability
	const workers = 4

	target := scriptedTextTarget("parallel", pack, time.Millisecond)
	res, err := run.Execute(context.Background(), run.Spec{
		Manifest:         noCapManifest(),
		Packs:            []qual.Pack{pack},
		Target:           target,
		TableConcurrency: workers,
	})
	if err != nil {
		t.Fatalf("Execute (parallel): %v", err)
	}
	if len(res.Reports) != len(pack.Tables) {
		t.Fatalf("reports = %d, want %d (every table runs)", len(res.Reports), len(pack.Tables))
	}
	if max := target.maxSeen.Load(); max > workers {
		t.Errorf("max concurrent Observe = %d, want <= %d", max, workers)
	}

	// Same table order as a sequential run.
	seqTarget := scriptedTextTarget("seq", pack, 0)
	seq, err := run.Execute(context.Background(), run.Spec{
		Manifest: noCapManifest(), Packs: []qual.Pack{pack}, Target: seqTarget,
	})
	if err != nil {
		t.Fatalf("Execute (sequential): %v", err)
	}
	if len(seq.Scorecard.Results) != len(res.Scorecard.Results) {
		t.Fatalf("result counts differ: parallel %d, sequential %d", len(res.Scorecard.Results), len(seq.Scorecard.Results))
	}
	for i := range seq.Scorecard.Results {
		if res.Scorecard.Results[i].Table != seq.Scorecard.Results[i].Table {
			t.Errorf("result[%d] table = %q (parallel) vs %q (sequential): order not preserved",
				i, res.Scorecard.Results[i].Table, seq.Scorecard.Results[i].Table)
		}
	}
}

// TestExecuteParallelOnResultFiresPerTable proves OnResult is invoked once per
// runnable table on the parallel path (from worker goroutines).
func TestExecuteParallelOnResultFiresPerTable(t *testing.T) {
	t.Parallel()
	pack := capability.V1()
	var count atomic.Int32
	_, err := run.Execute(context.Background(), run.Spec{
		Manifest:         noCapManifest(),
		Packs:            []qual.Pack{pack},
		Target:           scriptedTextTarget("onresult", pack, 0),
		TableConcurrency: 3,
		OnResult:         func(qual.TablePlan, eval.Report) { count.Add(1) },
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if int(count.Load()) != len(pack.Tables) {
		t.Errorf("OnResult fired %d times, want %d", count.Load(), len(pack.Tables))
	}
}

func TestExecuteOfflinePack(t *testing.T) {
	t.Parallel()
	pack := structuredoutput.V1()
	res, err := run.Execute(context.Background(), run.Spec{
		Manifest: conformingManifest(),
		Packs:    []qual.Pack{pack},
		Target:   scriptedFromPack("offline-test", pack),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(res.Reports) == 0 {
		t.Fatal("no reports")
	}
	if len(res.Skipped) != 0 {
		t.Fatalf("Skipped = %+v, want none (manifest declares structured_output)", res.Skipped)
	}
	// qual.Scorecard has no exported Dimensions field: Dimensions() is a
	// method that rolls the scorecard's Results up per-dimension and errors
	// on an empty scorecard. A non-empty, non-erroring result is this test's
	// "the scorecard actually carries evidence" assertion.
	dims, err := res.Scorecard.Dimensions()
	if err != nil {
		t.Fatalf("Scorecard.Dimensions: %v", err)
	}
	if len(dims) == 0 {
		t.Fatal("empty scorecard: no dimensions")
	}
	for _, d := range dims {
		if d.Undecided {
			t.Errorf("dimension %s: Undecided = true, want a decided score from a conforming target", d.Dimension)
		}
	}
}

// TestExecuteInvokesProgressPerPlan proves the Progress hook fires once per
// table plan, in order, on both the runnable and skipped paths — the live
// per-table feedback the CLI relies on so a long run against a real model is
// not silent between preflight and the final report.
func TestExecuteInvokesProgressPerPlan(t *testing.T) {
	t.Parallel()
	pack := structuredoutput.V1() // one table, requires structured_output

	// Runnable path: conforming manifest, table executes.
	var runnable []qual.TablePlan
	if _, err := run.Execute(context.Background(), run.Spec{
		Manifest: conformingManifest(),
		Packs:    []qual.Pack{pack},
		Target:   scriptedFromPack("progress-run", pack),
		Progress: func(p qual.TablePlan) { runnable = append(runnable, p) },
	}); err != nil {
		t.Fatalf("Execute (runnable): %v", err)
	}
	if len(runnable) != len(pack.Tables) {
		t.Fatalf("Progress calls = %d, want %d (one per table)", len(runnable), len(pack.Tables))
	}
	if !runnable[0].Runnable {
		t.Error("Progress plan.Runnable = false on a conforming manifest, want true")
	}

	// Skipped path: manifest without the capability, table is skipped but
	// Progress still fires for it.
	manifest := conformingManifest()
	manifest.Capabilities = nil
	var skipped []qual.TablePlan
	if _, err := run.Execute(context.Background(), run.Spec{
		Manifest: manifest,
		Packs:    []qual.Pack{pack},
		Target:   fixtarget.NewScripted("progress-skip", nil),
		Progress: func(p qual.TablePlan) { skipped = append(skipped, p) },
	}); err != nil {
		t.Fatalf("Execute (skipped): %v", err)
	}
	if len(skipped) != len(pack.Tables) {
		t.Fatalf("Progress calls on skip path = %d, want %d", len(skipped), len(pack.Tables))
	}
	if skipped[0].Runnable {
		t.Error("Progress plan.Runnable = true for a table missing its capability, want false")
	}
}

func TestExecuteSkippedTableRecordsMissingCapability(t *testing.T) {
	t.Parallel()
	pack := structuredoutput.V1()
	manifest := conformingManifest()
	manifest.Capabilities = nil // declares no capabilities: the pack's only
	// table (requires structured_output) must be skipped, never run.

	res, err := run.Execute(context.Background(), run.Spec{
		Manifest: manifest,
		Packs:    []qual.Pack{pack},
		// An empty script map is fine: Observe must never be called on the
		// skipped path, and an unscripted call would itself fail this test
		// via fixtarget.UnscriptedScenarioError.
		Target: fixtarget.NewScripted("offline-test", nil),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(res.Reports) != 0 {
		t.Fatalf("Reports = %+v, want none (every table skipped)", res.Reports)
	}
	if len(res.Skipped) != 1 {
		t.Fatalf("Skipped = %+v, want exactly 1 table plan", res.Skipped)
	}
	if res.Skipped[0].Runnable {
		t.Error("Skipped[0].Runnable = true, want false")
	}
	want := []qual.Capability{qual.CapabilityStructuredOutput}
	if len(res.Skipped[0].Missing) != 1 || res.Skipped[0].Missing[0] != want[0] {
		t.Errorf("Skipped[0].Missing = %v, want %v", res.Skipped[0].Missing, want)
	}
	if len(res.Scorecard.Results) != 1 || !res.Scorecard.Results[0].Skipped {
		t.Fatalf("Scorecard.Results = %+v, want exactly one Skipped result", res.Scorecard.Results)
	}
}

func TestExecuteTargetForTableBuildsOnePerTable(t *testing.T) {
	t.Parallel()
	pack := structuredoutput.V1()
	scripted := scriptedFromPack("per-table", pack)
	var calls []qual.TablePlan

	res, err := run.Execute(context.Background(), run.Spec{
		Manifest: conformingManifest(),
		Packs:    []qual.Pack{pack},
		TargetForTable: func(plan qual.TablePlan) (eval.Target, error) {
			calls = append(calls, plan)
			return scripted, nil
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(res.Reports) == 0 {
		t.Fatal("no reports")
	}
	if len(calls) != 1 {
		t.Fatalf("TargetForTable calls = %d, want 1 (one runnable table)", len(calls))
	}
	if calls[0].Table != pack.Tables[0].Name {
		t.Errorf("TargetForTable plan.Table = %s, want %s", calls[0].Table, pack.Tables[0].Name)
	}
}

func TestExecuteTargetForTableError(t *testing.T) {
	t.Parallel()
	pack := structuredoutput.V1()
	wantErr := errors.New("boom")

	_, err := run.Execute(context.Background(), run.Spec{
		Manifest: conformingManifest(),
		Packs:    []qual.Pack{pack},
		TargetForTable: func(qual.TablePlan) (eval.Target, error) {
			return nil, wantErr
		},
	})
	if err == nil {
		t.Fatal("Execute: want error from TargetForTable, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("Execute error = %v, want wrapping %v", err, wantErr)
	}
}

// TestExecuteReturnsPartialResultOnLaterTableError proves Execute preserves
// every table processed successfully before a later table's error, rather
// than discarding it: pkg/codepacks/capability.V1 has several
// single-scenario, single-table cases with no special capability
// requirement, so a plain conforming manifest makes every one of them
// Runnable, and TargetForTable can be made to fail predictably for exactly
// one of those tables while the others succeed.
func TestExecuteReturnsPartialResultOnLaterTableError(t *testing.T) {
	t.Parallel()
	pack := capability.V1()
	if len(pack.Tables) < 2 {
		t.Fatalf("capability.V1() has %d tables, want at least 2 for this test", len(pack.Tables))
	}
	failingTable := pack.Tables[1].Name
	wantErr := errors.New("boom")

	var scriptsForTable = func(tbl qual.Table) *fixtarget.Scripted {
		scripts := map[string]fixtarget.Script{}
		for _, sc := range tbl.Scenarios {
			scripts[sc.ID] = fixtarget.Script{Reply: "ok"}
		}
		return fixtarget.NewScripted(string(tbl.Name), scripts)
	}

	var calls []qual.TablePlan
	res, err := run.Execute(context.Background(), run.Spec{
		Manifest: conformingManifest(),
		Packs:    []qual.Pack{pack},
		TargetForTable: func(plan qual.TablePlan) (eval.Target, error) {
			calls = append(calls, plan)
			if plan.Table == failingTable {
				return nil, wantErr
			}
			for _, tbl := range pack.Tables {
				if tbl.Name == plan.Table {
					return scriptsForTable(tbl), nil
				}
			}
			t.Fatalf("no table named %s in pack", plan.Table)
			return nil, nil
		},
	})

	if err == nil {
		t.Fatal("Execute: want error from the failing table, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("Execute error = %v, want wrapping %v", err, wantErr)
	}

	// The table before the failing one (pack.Tables[0]) was processed
	// successfully and must still be present in the returned Result, not
	// discarded because a later table failed.
	if len(res.Reports) != 1 {
		t.Fatalf("Reports = %+v, want exactly 1 report from the earlier successful table", res.Reports)
	}
	if len(res.Scorecard.Results) != 1 {
		t.Fatalf("Scorecard.Results = %+v, want exactly 1 result from the earlier successful table", res.Scorecard.Results)
	}
	if res.Scorecard.Results[0].Table != pack.Tables[0].Name {
		t.Errorf("Scorecard.Results[0].Table = %s, want %s", res.Scorecard.Results[0].Table, pack.Tables[0].Name)
	}
	if res.Scorecard.Results[0].Skipped {
		t.Error("Scorecard.Results[0].Skipped = true, want false (it succeeded)")
	}
	// Execute must have stopped at the failing table: TargetForTable is
	// called for tables[0] and tables[1] (the failure), never beyond.
	if len(calls) != 2 {
		t.Fatalf("TargetForTable calls = %d, want exactly 2 (stop at the failing table)", len(calls))
	}
}

func TestExecuteSpecValidation(t *testing.T) {
	t.Parallel()
	pack := structuredoutput.V1()
	scripted := scriptedFromPack("validation", pack)

	tests := []struct {
		name string
		spec run.Spec
	}{
		{
			name: "neither Target nor TargetForTable set",
			spec: run.Spec{Manifest: conformingManifest(), Packs: []qual.Pack{pack}},
		},
		{
			name: "both Target and TargetForTable set",
			spec: run.Spec{
				Manifest: conformingManifest(),
				Packs:    []qual.Pack{pack},
				Target:   scripted,
				TargetForTable: func(qual.TablePlan) (eval.Target, error) {
					return scripted, nil
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := run.Execute(context.Background(), tt.spec); err == nil {
				t.Fatal("Execute: want error, got nil")
			}
		})
	}
}

func TestExecutePlanErrorRejectsInvalidManifest(t *testing.T) {
	t.Parallel()
	pack := structuredoutput.V1()
	invalid := conformingManifest()
	invalid.Provider = "" // Manifest.Validate rejects an empty Provider.

	if _, err := run.Execute(context.Background(), run.Spec{
		Manifest: invalid,
		Packs:    []qual.Pack{pack},
		Target:   scriptedFromPack("invalid-manifest", pack),
	}); err == nil {
		t.Fatal("Execute: want error for an invalid manifest, got nil")
	}
}

// fakeClient is a minimal inference.Client fixture for BuildTarget: it
// always answers with a fixed assistant text reply and never streams.
type fakeClient struct{}

func (fakeClient) Invoke(_ context.Context, _ inference.Request) (*inference.Response, error) {
	return &inference.Response{
		Message: &content.AIMessage{Message: content.Message{
			Role:   content.RoleAssistant,
			Blocks: []content.Block{&content.TextBlock{Text: "ok"}},
		}},
	}, nil
}

func (fakeClient) Stream(_ context.Context, _ inference.Request) (*stream.StreamReader[content.Chunk], error) {
	return nil, errors.New("fakeClient: Stream not implemented")
}

func TestBuildTarget(t *testing.T) {
	t.Parallel()
	env := &packfile.Environment{System: "you are a qualification fixture"}
	m := conformingManifest()

	target, err := run.BuildTarget(fakeClient{}, m, env, eval.Revision("table-rev"))
	if err != nil {
		t.Fatalf("BuildTarget: %v", err)
	}

	sc := eval.Scenario{
		ID:       "s1",
		Name:     "case",
		Revision: "table-rev",
		Input: content.AgenticMessages{&content.UserMessage{Message: content.Message{
			Role:   content.RoleUser,
			Blocks: []content.Block{&content.TextBlock{Text: "hello"}},
		}}},
	}
	obs, err := target.Observe(context.Background(), sc)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if obs.Subject.Revision != "table-rev" {
		t.Errorf("Subject.Revision = %s, want table-rev (WithRevision)", obs.Subject.Revision)
	}
	if err := obs.Validate(); err != nil {
		t.Errorf("Observation.Validate: %v", err)
	}
}

func TestBuildTargetNilEnvironment(t *testing.T) {
	t.Parallel()
	m := conformingManifest()
	if _, err := run.BuildTarget(fakeClient{}, m, nil, eval.Revision("table-rev")); err != nil {
		t.Fatalf("BuildTarget(nil env): %v", err)
	}
}

func TestBuildTargetRejectsInvalidManifest(t *testing.T) {
	t.Parallel()
	m := conformingManifest()
	m.BaseURL = "not-a-url-with-userinfo://user:pass@host" // rejected by qual.Manifest.Validate
	if _, err := run.BuildTarget(fakeClient{}, m, nil, eval.Revision("table-rev")); err == nil {
		t.Fatal("BuildTarget: want error for an invalid manifest, got nil")
	}
}
