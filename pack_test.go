package mpqt

import (
	"context"
	"testing"

	"github.com/looprig/core/content"
	"github.com/looprig/eval"
	"github.com/looprig/eval/exact"
)

func userMsg(text string) content.Conversation {
	return &content.UserMessage{Message: content.Message{
		Role:   content.RoleUser,
		Blocks: []content.Block{&content.TextBlock{Text: text}},
	}}
}

func validScenario(id string) eval.Scenario {
	return eval.Scenario{
		ID:       id,
		Name:     "fixture-target",
		Revision: "r1",
		Input:    content.AgenticMessages{userMsg("say hello")},
	}
}

func validTable(name string, scenarioIDs ...string) Table {
	scenarios := make([]eval.Scenario, 0, len(scenarioIDs))
	for _, id := range scenarioIDs {
		scenarios = append(scenarios, validScenario(id))
	}
	return Table{
		Name:       eval.Name(name),
		Revision:   "1",
		Dimension:  "capability",
		Scenarios:  scenarios,
		Evaluators: []eval.Evaluator{exact.RequiredText("hello")},
	}
}

func validPack() Pack {
	return Pack{
		Name:     "core-capability",
		Revision: "v1",
		Tables:   []Table{validTable("greetings", "greet-001", "greet-002")},
	}
}

func TestPackValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		mutate  func(*Pack)
		wantErr bool
	}{
		{name: "valid", mutate: func(p *Pack) {}},
		{name: "empty name", mutate: func(p *Pack) { p.Name = "" }, wantErr: true},
		{name: "empty revision", mutate: func(p *Pack) { p.Revision = "" }, wantErr: true},
		{name: "no tables", mutate: func(p *Pack) { p.Tables = nil }, wantErr: true},
		{name: "duplicate table names", mutate: func(p *Pack) {
			p.Tables = append(p.Tables, validTable("greetings", "greet-003"))
		}, wantErr: true},
		{name: "duplicate scenario id across tables", mutate: func(p *Pack) {
			p.Tables = append(p.Tables, validTable("other", "greet-001"))
		}, wantErr: true},
		{name: "table without scenarios", mutate: func(p *Pack) {
			p.Tables[0].Scenarios = nil
		}, wantErr: true},
		{name: "table without evaluators", mutate: func(p *Pack) {
			p.Tables[0].Evaluators = nil
		}, wantErr: true},
		{name: "nil evaluator", mutate: func(p *Pack) {
			p.Tables[0].Evaluators = []eval.Evaluator{nil}
		}, wantErr: true},
		{name: "invalid scenario", mutate: func(p *Pack) {
			p.Tables[0].Scenarios[0].Input = nil
		}, wantErr: true},
		{name: "empty dimension", mutate: func(p *Pack) {
			p.Tables[0].Dimension = ""
		}, wantErr: true},
		{name: "unknown required capability", mutate: func(p *Pack) {
			p.Tables[0].Requires = []Capability{"telepathy"}
		}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := validPack()
			tt.mutate(&p)
			err := p.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPlanCapabilityFiltering(t *testing.T) {
	t.Parallel()
	pack := validPack()
	tools := validTable("tool-table", "tool-001")
	tools.Requires = []Capability{CapabilityTools}
	pack.Tables = append(pack.Tables, tools)

	m := validManifest() // has CapabilityTools + CapabilityStructuredOutput
	plans, err := Plan(pack, m)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(plans) != 2 {
		t.Fatalf("Plan() returned %d entries, want 2", len(plans))
	}
	for _, pl := range plans {
		if !pl.Runnable {
			t.Errorf("table %q not runnable with satisfying manifest", pl.Table)
		}
	}

	bare := m
	bare.Capabilities = nil
	plans, err = Plan(pack, bare)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	var filtered *TablePlan
	for i := range plans {
		if plans[i].Table == "tool-table" {
			filtered = &plans[i]
		}
	}
	if filtered == nil {
		t.Fatal("filtered table missing from plan output")
	}
	if filtered.Runnable {
		t.Error("table requiring tools should not be runnable without the capability")
	}
	if len(filtered.Missing) != 1 || filtered.Missing[0] != CapabilityTools {
		t.Errorf("Missing = %v, want [tools]", filtered.Missing)
	}

	if _, err := Plan(Pack{}, m); err == nil {
		t.Error("Plan() with invalid pack should error")
	}
	invalid := m
	invalid.TargetID = ""
	if _, err := Plan(pack, invalid); err == nil {
		t.Error("Plan() with invalid manifest should error")
	}
}

func TestTableSuiteRunsUnderEval(t *testing.T) {
	t.Parallel()
	pack := validPack()
	plans, err := Plan(pack, validManifest())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	suite := plans[0].Suite
	if err := suite.Validate(); err != nil {
		t.Fatalf("expanded suite invalid: %v", err)
	}
	report, err := eval.Run(context.Background(), eval.RunConfig{}, suite,
		staticTarget{}, plans[0].Evaluators...)
	if err != nil {
		t.Fatalf("eval.Run error = %v", err)
	}
	if report.Summary.Samples != 2 {
		t.Errorf("Samples = %d, want 2", report.Summary.Samples)
	}
}

// staticTarget answers every scenario with "hello" and the mandatory matching
// subject revision.
type staticTarget struct{}

func (staticTarget) Name() string { return "static" }

func (staticTarget) Observe(_ context.Context, sc eval.Scenario) (eval.Observation, error) {
	conv := append(content.AgenticMessages{}, sc.Input...)
	conv = append(conv, &content.AIMessage{Message: content.Message{
		Role:   content.RoleAssistant,
		Blocks: []content.Block{&content.TextBlock{Text: "hello"}},
	}})
	return eval.Observation{
		Conversation: conv,
		Subject: eval.Subject{
			ID:       "static",
			Kind:     eval.SubjectModel,
			Name:     "static",
			Revision: sc.Revision,
		},
	}, nil
}
