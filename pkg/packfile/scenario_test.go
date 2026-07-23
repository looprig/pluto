package packfile

import (
	"sort"
	"testing"

	"github.com/looprig/core/content"
	"github.com/looprig/eval"
)

func TestScenarioSpecToEval(t *testing.T) {
	max := 2
	spec := ScenarioSpec{
		ID:    "tu-002",
		Input: []MessageSpec{{Role: "user", Text: "Compare Lisbon and Porto."}},
		Expect: &ExpectSpec{
			ExpectedToolCalls: []ToolCallExpectSpec{{Tool: "search", Min: 2, Max: &max}},
			ForbiddenActions:  []string{"bash"},
		},
		Labels: map[string]string{"category": "tool-use"},
	}
	sc, err := spec.Scenario("tool-use-selection", "v1")
	if err != nil {
		t.Fatalf("Scenario: %v", err)
	}
	if sc.ID != "tu-002" || sc.Name != eval.Name("tool-use-selection") || sc.Revision != eval.Revision("v1") {
		t.Fatalf("identity: %+v", sc)
	}
	um, ok := sc.Input[0].(*content.UserMessage)
	if !ok {
		t.Fatalf("input[0] is %T", sc.Input[0])
	}
	tb, ok := um.Message.Blocks[0].(*content.TextBlock)
	if !ok || tb.Text != "Compare Lisbon and Porto." {
		t.Fatalf("block: %#v", um.Message.Blocks[0])
	}
	exp := sc.Expectation
	if exp == nil || len(exp.ExpectedToolCalls) != 1 || exp.ExpectedToolCalls[0].Tool != "search" ||
		exp.ExpectedToolCalls[0].MinCount != 2 || exp.ExpectedToolCalls[0].MaxCount == nil || *exp.ExpectedToolCalls[0].MaxCount != 2 {
		t.Fatalf("expectation: %+v", exp)
	}
	if len(exp.ForbiddenActions) != 1 || exp.ForbiddenActions[0] != eval.ActionName("bash") {
		t.Fatalf("forbidden actions: %+v", exp.ForbiddenActions)
	}
	if err := sc.Validate(); err != nil {
		t.Fatalf("eval validate: %v", err)
	}
}

func TestScenarioSpecRejectsUnknownRole(t *testing.T) {
	spec := ScenarioSpec{ID: "x", Input: []MessageSpec{{Role: "system", Text: "no"}}}
	if _, err := spec.Scenario("n", "v1"); err == nil {
		t.Fatal("system role accepted") // system prompt belongs to environment, never to input
	}
}

func TestScenarioSpecAssistantRole(t *testing.T) {
	spec := ScenarioSpec{
		ID: "asst-001",
		Input: []MessageSpec{
			{Role: "user", Text: "Hello"},
			{Role: "assistant", Text: "Hi there"},
		},
	}
	sc, err := spec.Scenario("n", "v1")
	if err != nil {
		t.Fatalf("Scenario: %v", err)
	}
	if len(sc.Input) != 2 {
		t.Fatalf("input length: %d", len(sc.Input))
	}
	am, ok := sc.Input[1].(*content.AIMessage)
	if !ok {
		t.Fatalf("input[1] is %T", sc.Input[1])
	}
	if am.Message.Role != content.RoleAssistant {
		t.Fatalf("role: %v", am.Message.Role)
	}
	tb, ok := am.Message.Blocks[0].(*content.TextBlock)
	if !ok || tb.Text != "Hi there" {
		t.Fatalf("block: %#v", am.Message.Blocks[0])
	}
}

func TestScenarioSpecEmptyInputErrors(t *testing.T) {
	spec := ScenarioSpec{ID: "empty"}
	if _, err := spec.Scenario("n", "v1"); err == nil {
		t.Fatal("empty input accepted")
	}
}

func TestScenarioSpecNilExpectYieldsNilExpectation(t *testing.T) {
	spec := ScenarioSpec{
		ID:    "no-expect",
		Input: []MessageSpec{{Role: "user", Text: "hi"}},
	}
	sc, err := spec.Scenario("n", "v1")
	if err != nil {
		t.Fatalf("Scenario: %v", err)
	}
	if sc.Expectation != nil {
		t.Fatalf("expected nil Expectation, got %+v", sc.Expectation)
	}
}

func TestScenarioSpecLabelsSortedDeterministically(t *testing.T) {
	spec := ScenarioSpec{
		ID:    "labels",
		Input: []MessageSpec{{Role: "user", Text: "hi"}},
		Labels: map[string]string{
			"zeta":  "z",
			"alpha": "a",
			"mid":   "m",
		},
	}
	sc, err := spec.Scenario("n", "v1")
	if err != nil {
		t.Fatalf("Scenario: %v", err)
	}
	if len(sc.Labels) != 3 {
		t.Fatalf("labels length: %d", len(sc.Labels))
	}
	keys := make([]string, len(sc.Labels))
	for i, l := range sc.Labels {
		keys[i] = string(l.Key)
	}
	if !sort.StringsAreSorted(keys) {
		t.Fatalf("labels not sorted: %v", keys)
	}
	want := []string{"alpha", "mid", "zeta"}
	for i, k := range want {
		if keys[i] != k {
			t.Fatalf("label order: got %v want %v", keys, want)
		}
	}
}

func TestScenarioSpecStructuredPolicyReferenceFacts(t *testing.T) {
	spec := ScenarioSpec{
		ID:    "full",
		Input: []MessageSpec{{Role: "user", Text: "hi"}},
		Expect: &ExpectSpec{
			RequiredFacts:    []string{"fact one", "fact two"},
			ReferenceAnswers: []string{"answer one"},
			PolicyRef:        "policy-v1",
			StructuredOutput: &StructuredExpectSpec{Schema: "schema-v1", Strict: true},
		},
	}
	sc, err := spec.Scenario("n", "v1")
	if err != nil {
		t.Fatalf("Scenario: %v", err)
	}
	exp := sc.Expectation
	if exp == nil {
		t.Fatalf("expected non-nil Expectation")
	}
	if len(exp.RequiredFacts) != 2 || exp.RequiredFacts[0] != eval.Fact("fact one") || exp.RequiredFacts[1] != eval.Fact("fact two") {
		t.Fatalf("required facts: %+v", exp.RequiredFacts)
	}
	if len(exp.ReferenceAnswers) != 1 || exp.ReferenceAnswers[0] != eval.ReferenceAnswer("answer one") {
		t.Fatalf("reference answers: %+v", exp.ReferenceAnswers)
	}
	if exp.PolicyRef != eval.Revision("policy-v1") {
		t.Fatalf("policy ref: %v", exp.PolicyRef)
	}
	if exp.StructuredOutput == nil || exp.StructuredOutput.Schema != eval.Revision("schema-v1") || !exp.StructuredOutput.Strict {
		t.Fatalf("structured output: %+v", exp.StructuredOutput)
	}
}
