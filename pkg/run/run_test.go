package run_test

import (
	"context"
	"errors"
	"testing"

	"github.com/looprig/core/content"
	"github.com/looprig/eval"
	"github.com/looprig/inference"
	"github.com/looprig/inference/stream"
	"github.com/looprig/mpqt/pkg/codepacks/structuredoutput"
	"github.com/looprig/mpqt/pkg/packfile"
	"github.com/looprig/mpqt/pkg/qual"
	fixtarget "github.com/looprig/mpqt/pkg/qual/target"
	"github.com/looprig/mpqt/pkg/run"
)

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
// examples/qualification/qualification_test.go uses to fixture an offline
// run for this pack (pkg/codepacks/structuredoutput.V1, a real, validated,
// already-existing pack — this test exercises pkg/run.Execute's own
// behavior, not YAML pack loading, which Phase 2's packfile tests already
// cover).
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
