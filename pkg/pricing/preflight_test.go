package pricing_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/looprig/core/content"
	"github.com/looprig/eval"
	"github.com/looprig/eval/exact"
	"github.com/looprig/eval/judge"
	"github.com/looprig/eval/rubric"
	"github.com/looprig/inference"
	"github.com/looprig/inference/model"
	"github.com/looprig/pluto/pkg/pricing"
	"github.com/looprig/pluto/pkg/qual"
)

func userMsg(text string) content.Conversation {
	return &content.UserMessage{Message: content.Message{
		Role:   content.RoleUser,
		Blocks: []content.Block{&content.TextBlock{Text: text}},
	}}
}

func scenario(id string) eval.Scenario {
	return eval.Scenario{
		ID:       id,
		Name:     "fixture",
		Revision: "r1",
		Input:    content.AgenticMessages{userMsg("hello")},
	}
}

// fakeCounter is a Counter that returns a canned token count and quality, or
// a canned error.
type fakeCounter struct {
	tokens  int
	quality string
	err     error
	calls   int
}

func (f *fakeCounter) Count(_ context.Context, _ inference.Request) (int, string, error) {
	f.calls++
	if f.err != nil {
		return 0, "", f.err
	}
	return f.tokens, f.quality, nil
}

// twoScenarioTablePlan builds a runnable TablePlan with 2 scenarios, one
// programmatic (non-judge) evaluator and exactly one real judge.New
// evaluator -- Descriptor().Method == eval.MethodModel is the signal
// Preflight uses to count judge calls, and this exercises it against the
// production judge type, not a hand-rolled stand-in.
func twoScenarioTablePlan(table eval.Name) qual.TablePlan {
	judgeEval := judge.New(rubric.AnswerRelevanceV1, nil, inference.Request{
		Model: model.CustomModel("test", "test", "", "judge-model", model.WithStructuredOutput()),
	})
	return qual.TablePlan{
		Pack:      "pack",
		Table:     table,
		Dimension: "capability",
		Runnable:  true,
		Suite: eval.Suite{
			Name:      table,
			Revision:  "r1",
			Scenarios: []eval.Scenario{scenario("s1"), scenario("s2")},
		},
		Evaluators: []eval.Evaluator{
			exact.RequiredText("hello"),
			judgeEval,
		},
	}
}

func TestPreflightCallCounts(t *testing.T) {
	t.Parallel()
	plan := twoScenarioTablePlan("greetings")
	cfg := eval.RunConfig{Trials: 3}

	got, err := pricing.Preflight(context.Background(), []qual.TablePlan{plan}, cfg, pricing.Rates{}, nil, nil)
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	// 2 scenarios x 3 trials = 6 target calls; 1 judge evaluator on the
	// table => 6 x 1 = 6 judge calls.
	if got.TargetCalls != 6 {
		t.Errorf("TargetCalls = %d, want 6", got.TargetCalls)
	}
	if got.JudgeCalls != 6 {
		t.Errorf("JudgeCalls = %d, want 6", got.JudgeCalls)
	}
}

func TestPreflightSkipsNonRunnablePlans(t *testing.T) {
	t.Parallel()
	runnable := twoScenarioTablePlan("greetings")
	skipped := qual.TablePlan{
		Pack: "pack", Table: "needs-tools", Dimension: "capability",
		Runnable: false, Missing: []qual.Capability{qual.CapabilityTools},
	}

	got, err := pricing.Preflight(context.Background(), []qual.TablePlan{runnable, skipped}, eval.RunConfig{}, pricing.Rates{}, nil, nil)
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	// RunConfig{} defaults Trials to 1: 2 scenarios x 1 trial = 2 target
	// calls, 2 x 1 judge = 2 judge calls. The skipped plan contributes
	// nothing and must not panic on its zero-value Suite/Evaluators.
	if got.TargetCalls != 2 {
		t.Errorf("TargetCalls = %d, want 2", got.TargetCalls)
	}
	if got.JudgeCalls != 2 {
		t.Errorf("JudgeCalls = %d, want 2", got.JudgeCalls)
	}
}

func TestPreflightNilCounterUsesHeuristic(t *testing.T) {
	t.Parallel()
	plan := twoScenarioTablePlan("greetings")
	templates := map[eval.Name]inference.Request{
		"greetings": {System: "be nice", Messages: content.AgenticMessages{userMsg("hello there")}},
	}

	got, err := pricing.Preflight(context.Background(), []qual.TablePlan{plan}, eval.RunConfig{}, pricing.Rates{}, nil, templates)
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if got.CounterQuality != "heuristic" {
		t.Errorf("CounterQuality = %q, want %q", got.CounterQuality, "heuristic")
	}
	if got.InputTokens[0] <= 0 {
		t.Errorf("InputTokens[expected] = %d, want > 0 from the heuristic estimate", got.InputTokens[0])
	}
}

func TestPreflightNilCounterNoTemplatesStillHeuristic(t *testing.T) {
	t.Parallel()
	plan := twoScenarioTablePlan("greetings")

	got, err := pricing.Preflight(context.Background(), []qual.TablePlan{plan}, eval.RunConfig{}, pricing.Rates{}, nil, nil)
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if got.CounterQuality != "heuristic" {
		t.Errorf("CounterQuality = %q, want %q even with no templates supplied", got.CounterQuality, "heuristic")
	}
	if len(got.Unknowns) == 0 {
		t.Error("Unknowns should record the missing template for both the table and its judge")
	}
}

func TestPreflightUsesCounterWhenProvided(t *testing.T) {
	t.Parallel()
	plan := twoScenarioTablePlan("greetings")
	templates := map[eval.Name]inference.Request{
		"greetings":                   {},
		rubric.AnswerRelevanceV1.Name: {},
	}
	counter := &fakeCounter{tokens: 100, quality: "exact-tokenizer-v1"}

	got, err := pricing.Preflight(context.Background(), []qual.TablePlan{plan}, eval.RunConfig{Trials: 2}, pricing.Rates{}, counter, templates)
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if got.CounterQuality != "exact-tokenizer-v1" {
		t.Errorf("CounterQuality = %q, want %q", got.CounterQuality, "exact-tokenizer-v1")
	}
	// 2 scenarios x 2 trials = 4 calls per template; two templates (table +
	// judge) each contribute 100 tokens/call => (4*100) + (4*100) = 800.
	if got.InputTokens[0] != 800 {
		t.Errorf("InputTokens[expected] = %d, want 800", got.InputTokens[0])
	}
	if counter.calls == 0 {
		t.Error("counter.Count was never called")
	}
}

func TestPreflightCounterErrorAborts(t *testing.T) {
	t.Parallel()
	plan := twoScenarioTablePlan("greetings")
	templates := map[eval.Name]inference.Request{"greetings": {}}
	boom := errors.New("counter unavailable")
	counter := &fakeCounter{err: boom}

	_, err := pricing.Preflight(context.Background(), []qual.TablePlan{plan}, eval.RunConfig{}, pricing.Rates{}, counter, templates)
	if err == nil {
		t.Fatal("Preflight() error = nil, want the counter's error to abort the plan")
	}
	if !errors.Is(err, boom) {
		t.Errorf("error = %v, want it to wrap %v", err, boom)
	}
}

func TestPreflightMissingTemplateRecordsUnknown(t *testing.T) {
	t.Parallel()
	plan := twoScenarioTablePlan("greetings")

	got, err := pricing.Preflight(context.Background(), []qual.TablePlan{plan}, eval.RunConfig{}, pricing.Rates{}, nil, nil)
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if got.InputTokens[0] != 0 || got.InputTokens[1] != 0 {
		t.Errorf("InputTokens = %v, want [0,0] when no template is available", got.InputTokens)
	}
	var sawTable, sawJudge bool
	for _, u := range got.Unknowns {
		if strings.Contains(u, "table greetings") {
			sawTable = true
		}
		if strings.Contains(u, string(rubric.AnswerRelevanceV1.Name)) {
			sawJudge = true
		}
	}
	if !sawTable {
		t.Errorf("Unknowns = %v, want an entry for the missing table template", got.Unknowns)
	}
	if !sawJudge {
		t.Errorf("Unknowns = %v, want an entry for the missing judge template", got.Unknowns)
	}
}

func TestPreflightInvalidRunConfigErrors(t *testing.T) {
	t.Parallel()
	plan := twoScenarioTablePlan("greetings")
	cfg := eval.RunConfig{Trials: -1}

	if _, err := pricing.Preflight(context.Background(), []qual.TablePlan{plan}, cfg, pricing.Rates{}, nil, nil); err == nil {
		t.Error("Preflight() error = nil, want a RunConfig validation error")
	}
}

func TestPreflightOutputCapFromSampling(t *testing.T) {
	t.Parallel()
	plan := twoScenarioTablePlan("greetings")
	maxTokens := 256
	templates := map[eval.Name]inference.Request{
		"greetings":                   {Model: model.Model{Sampling: model.Sampling{MaxTokens: &maxTokens}}},
		rubric.AnswerRelevanceV1.Name: {Model: model.Model{Sampling: model.Sampling{MaxTokens: &maxTokens}}},
	}

	got, err := pricing.Preflight(context.Background(), []qual.TablePlan{plan}, eval.RunConfig{}, pricing.Rates{}, nil, templates)
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	// 2 scenarios x 1 trial = 2 target calls and 2 judge calls, each side
	// capped at 256 output tokens => (2 + 2) * 256 = 1024, expected == max
	// since no generation has actually happened yet.
	if got.OutputTokens[0] != 1024 || got.OutputTokens[1] != 1024 {
		t.Errorf("OutputTokens = %v, want [1024, 1024]", got.OutputTokens)
	}
}
