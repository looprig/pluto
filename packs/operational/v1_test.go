package operational

import (
	"context"
	"testing"
	"time"

	"github.com/looprig/eval"
	"github.com/looprig/eval/evaltest"
	"github.com/looprig/mpqt"
	fixtarget "github.com/looprig/mpqt/fixture/target"
)

func TestPackV1Valid(t *testing.T) {
	t.Parallel()
	p := V1()
	if err := p.Validate(); err != nil {
		t.Fatalf("V1() pack invalid: %v", err)
	}
	if p.Name != "operational-stability" || p.Revision != "v1" {
		t.Errorf("identity = %s@%s, want operational-stability@v1", p.Name, p.Revision)
	}
	ids := map[string]bool{}
	names := map[string]bool{}
	for _, tbl := range p.Tables {
		names[string(tbl.Name)] = true
		for _, sc := range tbl.Scenarios {
			ids[sc.ID] = true
		}
		if tbl.Dimension != dimension {
			t.Errorf("table %s dimension = %s, want operational", tbl.Name, tbl.Dimension)
		}
	}
	for _, want := range []string{"latency", "tool-errors"} {
		if !names[want] {
			t.Errorf("missing table %s", want)
		}
	}
	for _, want := range []string{
		"op-001-short-prompt", "op-002-medium-prompt", "op-003-long-prompt", "op-101-flaky-tools",
	} {
		if !ids[want] {
			t.Errorf("missing scenario %s", want)
		}
	}
}

func TestPackV1AgainstConformingTarget(t *testing.T) {
	t.Parallel()
	p := V1()
	scripts := map[string]fixtarget.Script{
		"op-001-short-prompt":  {Reply: "ok", Duration: 250 * time.Millisecond},
		"op-002-medium-prompt": {Reply: "ok", Duration: 250 * time.Millisecond},
		"op-003-long-prompt":   {Reply: "ok", Duration: 250 * time.Millisecond},
		"op-101-flaky-tools": {
			Reply: "done",
			ToolCalls: []fixtarget.ToolCall{
				{Name: "search", ID: "tu_1"},
				{Name: "search", ID: "tu_2"},
				{Name: "search", ID: "tu_3", IsError: true},
			},
		},
	}
	target := fixtarget.NewScripted("conforming", scripts)
	plans, err := mpqt.Plan(p, manifestForTest())
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

func TestPackV1AgainstDeviantTarget(t *testing.T) {
	t.Parallel()
	p := V1()
	scripts := map[string]fixtarget.Script{
		"op-001-short-prompt":  {Reply: "ok", Duration: 31 * time.Second},
		"op-002-medium-prompt": {Reply: "ok", Duration: 31 * time.Second},
		"op-003-long-prompt":   {Reply: "ok", Duration: 31 * time.Second},
		"op-101-flaky-tools": {
			Reply: "done",
			ToolCalls: []fixtarget.ToolCall{
				{Name: "search", ID: "tu_1", IsError: true},
				{Name: "search", ID: "tu_2", IsError: true},
				{Name: "search", ID: "tu_3"},
			},
		},
	}
	target := fixtarget.NewScripted("deviant", scripts)
	plans, err := mpqt.Plan(p, manifestForTest())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	for _, pl := range plans {
		report, err := eval.Run(context.Background(), eval.RunConfig{}, pl.Suite,
			target, pl.Evaluators...)
		if err != nil {
			t.Fatalf("eval.Run error = %v", err)
		}
		if report.Summary.Assessments[eval.StatusFail] == 0 {
			t.Errorf("table %s: deviant target produced zero failing assessments", pl.Table)
		}
	}
}

func manifestForTest() mpqt.Manifest {
	return mpqt.Manifest{
		TargetID: "conforming", Role: mpqt.RoleCandidate,
		Provider: "test", Model: "fake", APIFormat: "openai",
		BaseURL: "https://example.invalid/v1", Revision: "r-fake",
		EndpointClass: mpqt.EndpointRemote,
		Capabilities:  []mpqt.Capability{mpqt.CapabilityTools},
	}
}
