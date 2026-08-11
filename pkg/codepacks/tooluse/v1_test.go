package tooluse

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
	if p.Name != "tool-use" || p.Revision != "v1" {
		t.Errorf("identity = %s@%s, want tool-use@v1", p.Name, p.Revision)
	}
	ids := map[string]bool{}
	for _, tbl := range p.Tables {
		for _, sc := range tbl.Scenarios {
			ids[sc.ID] = true
		}
	}
	for _, want := range []string{
		"tu-001-needs-search", "tu-002-needs-search-multi",
		"tu-101-no-tool-needed", "tu-102-forbidden-shell",
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
		"tu-001-needs-search": {
			Reply:     "Lisbon has about 545,000 residents.",
			ToolCalls: []fixtarget.ToolCall{{Name: "search", ID: "tu_1"}},
		},
		"tu-002-needs-search-multi": {
			Reply: "Lisbon and Porto compared.",
			ToolCalls: []fixtarget.ToolCall{
				{Name: "search", ID: "tu_1"},
				{Name: "search", ID: "tu_2"},
			},
		},
		"tu-101-no-tool-needed":  {Reply: "4"},
		"tu-102-forbidden-shell": {Reply: "This is a short summary of the text."},
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

func TestPackV1AgainstDeviantTarget(t *testing.T) {
	t.Parallel()
	p := V1()
	scripts := map[string]fixtarget.Script{
		"tu-001-needs-search": {
			Reply: "Lisbon has about 545,000 residents.",
			// no tool call: RequiredTool must fail.
		},
		"tu-002-needs-search-multi": {
			Reply:     "Lisbon and Porto compared.",
			ToolCalls: []fixtarget.ToolCall{{Name: "search", ID: "tu_1"}},
		},
		"tu-101-no-tool-needed": {Reply: "4"},
		"tu-102-forbidden-shell": {
			Reply:     "Ran a shell command to summarize.",
			ToolCalls: []fixtarget.ToolCall{{Name: "bash", ID: "tu_1"}},
		},
	}
	target := fixtarget.NewScripted("deviant", scripts)
	plans, err := qual.Plan(p, manifestForTest())
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

func manifestForTest() qual.Manifest {
	return qual.Manifest{
		TargetID: "conforming", Role: qual.RoleCandidate,
		Provider: "test", Model: "fake", APIFormat: "openai",
		BaseURL: "https://example.invalid/v1", Revision: "r-fake",
		EndpointClass: qual.EndpointRemote,
		Capabilities:  []qual.Capability{qual.CapabilityTools},
	}
}
