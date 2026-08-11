package structuredoutput

import (
	"context"
	"testing"

	"github.com/looprig/eval"
	"github.com/looprig/eval/evaltest"
	"github.com/looprig/pluto/pkg/qual"
	fixtarget "github.com/looprig/pluto/pkg/qual/target"
)

func TestPackV1Valid(t *testing.T) {
	t.Parallel()
	p := V1()
	if err := p.Validate(); err != nil {
		t.Fatalf("V1() pack invalid: %v", err)
	}
	if p.Name != "structured-output" || p.Revision != "v1" {
		t.Errorf("identity = %s@%s, want structured-output@v1", p.Name, p.Revision)
	}
	ids := map[string]bool{}
	for _, tbl := range p.Tables {
		for _, sc := range tbl.Scenarios {
			ids[sc.ID] = true
			if sc.Expectation == nil || sc.Expectation.StructuredOutput == nil {
				t.Errorf("scenario %s lacks a structured-output expectation", sc.ID)
			}
		}
	}
	for _, want := range []string{
		"so-001-flat-object", "so-002-nested-object", "so-003-enum-selection",
		"so-004-required-fields", "so-005-unicode-values", "so-006-large-array",
	} {
		if !ids[want] {
			t.Errorf("missing scenario %s", want)
		}
	}
}

func TestPackV1AgainstConformingTarget(t *testing.T) {
	t.Parallel()
	p := V1()
	scripts := map[string]fixtarget.Script{}
	for _, tbl := range p.Tables {
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
	target := fixtarget.NewScripted("conforming", scripts)
	plans, err := qual.Plan(p, manifestForTest())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	for _, pl := range plans {
		if !pl.Runnable {
			t.Fatalf("table %s not runnable: missing %v", pl.Table, pl.Missing)
		}
		report, err := eval.Run(context.Background(), eval.RunConfig{}, pl.Suite,
			target, pl.Evaluators...)
		if err != nil {
			t.Fatalf("eval.Run error = %v", err)
		}
		evaltest.RequirePass(t, report)
	}
}

func TestPackV1AgainstMalformedTarget(t *testing.T) {
	t.Parallel()
	p := V1()
	scripts := map[string]fixtarget.Script{}
	for _, tbl := range p.Tables {
		for _, sc := range tbl.Scenarios {
			scripts[sc.ID] = fixtarget.Script{
				Reply: "{", StructuredErr: &fixtarget.StructuredErr{
					Schema: sc.Expectation.StructuredOutput.Schema,
					Reason: eval.StructuredErrorInvalidJSON,
				},
			}
		}
	}
	target := fixtarget.NewScripted("malformed", scripts)
	plans, err := qual.Plan(p, manifestForTest())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	failed := 0
	for _, pl := range plans {
		report, err := eval.Run(context.Background(), eval.RunConfig{}, pl.Suite,
			target, pl.Evaluators...)
		if err != nil {
			t.Fatalf("eval.Run error = %v", err)
		}
		failed += report.Summary.Assessments[eval.StatusFail]
	}
	if failed == 0 {
		t.Error("malformed structured output produced zero failing assessments")
	}
}

func manifestForTest() qual.Manifest {
	return qual.Manifest{
		TargetID: "conforming", Role: qual.RoleCandidate,
		Provider: "test", Model: "fake", APIFormat: "openai",
		BaseURL: "https://example.invalid/v1", Revision: "r-fake",
		EndpointClass: qual.EndpointRemote,
		Capabilities:  []qual.Capability{qual.CapabilityStructuredOutput},
	}
}
