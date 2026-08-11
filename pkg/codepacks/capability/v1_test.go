package capability

import (
	"context"
	"testing"

	"github.com/looprig/eval"
	"github.com/looprig/eval/evaltest"
	"github.com/looprig/pluto/pkg/qual"
	fixtarget "github.com/looprig/pluto/pkg/qual/target"
)

var wantTables = []string{
	"if-001-exact-phrase", "if-002-forbidden-word", "if-003-format-constraint",
	"if-004-priority-conflict", "ka-001-capital", "ka-002-arithmetic",
	"ka-003-unit-conversion",
}

func TestPackV1Valid(t *testing.T) {
	t.Parallel()
	p := V1()
	if err := p.Validate(); err != nil {
		t.Fatalf("V1() pack invalid: %v", err)
	}
	if p.Name != "core-capability" || p.Revision != "v1" {
		t.Errorf("identity = %s@%s, want core-capability@v1", p.Name, p.Revision)
	}
	if len(p.Tables) != len(wantTables) {
		t.Fatalf("len(Tables) = %d, want %d", len(p.Tables), len(wantTables))
	}
	names := map[string]bool{}
	for _, tbl := range p.Tables {
		names[string(tbl.Name)] = true
		if len(tbl.Scenarios) != 1 {
			t.Errorf("table %s has %d scenarios, want 1", tbl.Name, len(tbl.Scenarios))
		}
		if len(tbl.Evaluators) == 0 {
			t.Errorf("table %s has no evaluators", tbl.Name)
		}
		if tbl.Dimension != "capability" {
			t.Errorf("table %s dimension = %s, want capability", tbl.Name, tbl.Dimension)
		}
	}
	for _, want := range wantTables {
		if !names[want] {
			t.Errorf("missing table %s", want)
		}
	}
}

func TestPackV1AgainstConformingTarget(t *testing.T) {
	t.Parallel()
	p := V1()
	scripts := map[string]fixtarget.Script{
		"if-001-exact-phrase":      {Reply: "the beacon is lit"},
		"if-002-forbidden-word":    {Reply: "The sky turned warm shades of amber and rose."},
		"if-003-format-constraint": {Reply: "- apple\n- banana\n- cherry"},
		"if-004-priority-conflict": {Reply: "I will answer in English as originally instructed."},
		"ka-001-capital":           {Reply: "Canberra"},
		"ka-002-arithmetic":        {Reply: "391"},
		"ka-003-unit-conversion":   {Reply: "2500"},
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
		"if-001-exact-phrase":      {Reply: "the beacon is out"},
		"if-002-forbidden-word":    {Reply: "The sky turned a deep shade of orange."},
		"if-003-format-constraint": {Reply: "apple, banana, cherry"},
		"if-004-priority-conflict": {Reply: "Je vais répondre en français."},
		"ka-001-capital":           {Reply: "Sydney"},
		"ka-002-arithmetic":        {Reply: "392"},
		"ka-003-unit-conversion":   {Reply: "250"},
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
	}
}
