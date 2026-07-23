package packfile

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/looprig/core/content"
	"github.com/looprig/eval"
	"github.com/looprig/eval/exact"
	"github.com/looprig/eval/rubric"
	"github.com/looprig/inference"
	"github.com/looprig/inference/stream"
	"gopkg.in/yaml.v3"
)

// evaluatorSpec decodes a YAML fragment into an EvaluatorSpec through its
// existing custom UnmarshalYAML (Task 2), the same path a table file's
// evaluators list goes through.
func evaluatorSpec(t *testing.T, yamlSrc string) EvaluatorSpec {
	t.Helper()
	var spec EvaluatorSpec
	if err := yaml.Unmarshal([]byte(yamlSrc), &spec); err != nil {
		t.Fatalf("evaluatorSpec: decode: %v", err)
	}
	return spec
}

// testRubric builds a minimal, valid rubric.Rubric -- good enough for
// judge.New and rubric.Rubric.Validate to accept -- named to match the
// "support-answer-quality" rubric name used across the registry tests.
func testRubric() rubric.Rubric {
	return rubric.Rubric{
		Name:       "support-answer-quality",
		Revision:   "v1",
		Scope:      eval.ScopeCase,
		Definition: "Measures whether the assistant's answer is accurate and helpful.",
		Criteria: []rubric.Criterion{
			{ID: "accuracy", Description: "The answer is factually accurate.", MinScore: 0, MaxScore: 1},
		},
	}
}

// fakeJudgeClient is a minimal inference.Client stub. The registry's judge
// kind never calls Invoke/Stream at Build time -- it only needs a non-nil
// client -- so the stub's bodies are never exercised here.
type fakeJudgeClient struct{}

func (fakeJudgeClient) Invoke(context.Context, inference.Request) (*inference.Response, error) {
	return nil, nil
}

func (fakeJudgeClient) Stream(context.Context, inference.Request) (*stream.StreamReader[content.Chunk], error) {
	return nil, nil
}

func TestBuiltinRegistryBuildsForbiddenTool(t *testing.T) {
	reg := NewRegistry()
	spec := evaluatorSpec(t, "kind: forbidden-tool\ntool: bash\n")
	ev, err := reg.Build(spec, BuildContext{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if ev.Descriptor().Name != exact.ForbiddenTool("bash").Descriptor().Name {
		t.Fatalf("descriptor mismatch")
	}
}

func TestRegistryRejectsUnknownKind(t *testing.T) {
	reg := NewRegistry()
	_, err := reg.Build(evaluatorSpec(t, "kind: nope\n"), BuildContext{})
	var pe *Error
	if !errors.As(err, &pe) || !strings.Contains(pe.Reason, "known kinds:") {
		t.Fatalf("want known-kind list in error, got %v", err)
	}
}

func TestRegistryRejectsUnknownOption(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.Build(evaluatorSpec(t, "kind: forbidden-tool\ntool: bash\nbogus: 1\n"), BuildContext{}); err == nil {
		t.Fatal("unknown option accepted")
	}
}

func TestJudgeKindRequiresClient(t *testing.T) {
	reg := NewRegistry()
	spec := evaluatorSpec(t, "kind: judge\nrubric: support-answer-quality\n")
	bc := BuildContext{Rubrics: map[string]rubric.Rubric{"support-answer-quality": testRubric()}}
	if _, err := reg.Build(spec, bc); !errors.Is(err, ErrJudgeUnconfigured) {
		t.Fatalf("want ErrJudgeUnconfigured, got %v", err)
	}
}

func TestBuildRequiredText(t *testing.T) {
	reg := NewRegistry()
	spec := evaluatorSpec(t, "kind: required-text\nsubstrings: [\"refund\", \"policy\"]\n")
	ev, err := reg.Build(spec, BuildContext{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := exact.RequiredText("refund", "policy").Descriptor().Name
	if ev.Descriptor().Name != want {
		t.Fatalf("descriptor mismatch: got %v want %v", ev.Descriptor().Name, want)
	}
}

func TestBuildForbiddenText(t *testing.T) {
	reg := NewRegistry()
	spec := evaluatorSpec(t, "kind: forbidden-text\nsubstrings: [\"guaranteed\"]\n")
	ev, err := reg.Build(spec, BuildContext{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := exact.ForbiddenText("guaranteed").Descriptor().Name
	if ev.Descriptor().Name != want {
		t.Fatalf("descriptor mismatch: got %v want %v", ev.Descriptor().Name, want)
	}
}

func TestBuildRequiredTool(t *testing.T) {
	reg := NewRegistry()
	spec := evaluatorSpec(t, "kind: required-tool\ntool: lookup_account\n")
	ev, err := reg.Build(spec, BuildContext{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := exact.RequiredTool("lookup_account").Descriptor().Name
	if ev.Descriptor().Name != want {
		t.Fatalf("descriptor mismatch: got %v want %v", ev.Descriptor().Name, want)
	}
}

func TestBuildToolErrorRate(t *testing.T) {
	reg := NewRegistry()
	spec := evaluatorSpec(t, "kind: tool-error-rate\nmax-error-rate: 0.34\n")
	ev, err := reg.Build(spec, BuildContext{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := exact.ToolErrorRate(exact.MaxErrorRate(0.34)).Descriptor().Name
	if ev.Descriptor().Name != want {
		t.Fatalf("descriptor mismatch: got %v want %v", ev.Descriptor().Name, want)
	}
}

func TestBuildToolErrorRateWithoutThreshold(t *testing.T) {
	reg := NewRegistry()
	spec := evaluatorSpec(t, "kind: tool-error-rate\n")
	ev, err := reg.Build(spec, BuildContext{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := exact.ToolErrorRate().Descriptor().Name
	if ev.Descriptor().Name != want {
		t.Fatalf("descriptor mismatch: got %v want %v", ev.Descriptor().Name, want)
	}
}

func TestBuildMaxDuration(t *testing.T) {
	reg := NewRegistry()
	spec := evaluatorSpec(t, "kind: max-duration\nlimit: 30s\n")
	ev, err := reg.Build(spec, BuildContext{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := exact.MaxDuration(30 * time.Second).Descriptor().Name
	if ev.Descriptor().Name != want {
		t.Fatalf("descriptor mismatch: got %v want %v", ev.Descriptor().Name, want)
	}
}

func TestBuildSchemaResult(t *testing.T) {
	reg := NewRegistry()
	spec := evaluatorSpec(t, "kind: schema-result\n")
	ev, err := reg.Build(spec, BuildContext{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := exact.SchemaResult().Descriptor().Name
	if ev.Descriptor().Name != want {
		t.Fatalf("descriptor mismatch: got %v want %v", ev.Descriptor().Name, want)
	}
}

func TestBuildJudge(t *testing.T) {
	reg := NewRegistry()
	spec := evaluatorSpec(t, "kind: judge\nrubric: support-answer-quality\n")
	bc := BuildContext{
		JudgeClient: fakeJudgeClient{},
		Rubrics:     map[string]rubric.Rubric{"support-answer-quality": testRubric()},
	}
	ev, err := reg.Build(spec, bc)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if ev.Descriptor().Name != eval.Name("support-answer-quality") {
		t.Fatalf("descriptor mismatch: got %v", ev.Descriptor().Name)
	}
}

func TestJudgeKindUnknownRubricIsNotUnconfiguredError(t *testing.T) {
	reg := NewRegistry()
	spec := evaluatorSpec(t, "kind: judge\nrubric: does-not-exist\n")
	bc := BuildContext{
		JudgeClient: fakeJudgeClient{},
		Rubrics:     map[string]rubric.Rubric{"support-answer-quality": testRubric()},
	}
	_, err := reg.Build(spec, bc)
	if err == nil {
		t.Fatal("unknown rubric accepted")
	}
	if errors.Is(err, ErrJudgeUnconfigured) {
		t.Fatalf("unknown-rubric error must be distinct from ErrJudgeUnconfigured, got %v", err)
	}
}

func TestRequiredTextRejectsEmptySubstrings(t *testing.T) {
	reg := NewRegistry()
	spec := evaluatorSpec(t, "kind: required-text\nsubstrings: []\n")
	if _, err := reg.Build(spec, BuildContext{}); err == nil {
		t.Fatal("empty substrings accepted")
	}
}

func TestForbiddenTextRejectsEmptySubstrings(t *testing.T) {
	reg := NewRegistry()
	spec := evaluatorSpec(t, "kind: forbidden-text\nsubstrings: []\n")
	if _, err := reg.Build(spec, BuildContext{}); err == nil {
		t.Fatal("empty substrings accepted")
	}
}

func TestRequiredToolRejectsEmptyName(t *testing.T) {
	reg := NewRegistry()
	spec := evaluatorSpec(t, "kind: required-tool\ntool: \"\"\n")
	if _, err := reg.Build(spec, BuildContext{}); err == nil {
		t.Fatal("empty tool name accepted")
	}
}

func TestForbiddenToolRejectsEmptyName(t *testing.T) {
	reg := NewRegistry()
	spec := evaluatorSpec(t, "kind: forbidden-tool\ntool: \"\"\n")
	if _, err := reg.Build(spec, BuildContext{}); err == nil {
		t.Fatal("empty tool name accepted")
	}
}

func TestToolErrorRateRejectsOutOfRangeAtBuild(t *testing.T) {
	tests := []struct {
		name    string
		yamlSrc string
	}{
		{"above one", "kind: tool-error-rate\nmax-error-rate: 1.5\n"},
		{"below zero", "kind: tool-error-rate\nmax-error-rate: -0.1\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := NewRegistry()
			spec := evaluatorSpec(t, tt.yamlSrc)
			if _, err := reg.Build(spec, BuildContext{}); err == nil {
				t.Fatalf("%s: out-of-range max-error-rate accepted at Build", tt.name)
			}
		})
	}
}

func TestMaxDurationRejectsNonPositiveOrInvalid(t *testing.T) {
	tests := []struct {
		name    string
		yamlSrc string
	}{
		{"zero", "kind: max-duration\nlimit: 0s\n"},
		{"negative", "kind: max-duration\nlimit: -5s\n"},
		{"unparseable", "kind: max-duration\nlimit: nope\n"},
		{"empty", "kind: max-duration\nlimit: \"\"\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := NewRegistry()
			spec := evaluatorSpec(t, tt.yamlSrc)
			if _, err := reg.Build(spec, BuildContext{}); err == nil {
				t.Fatalf("%s: accepted", tt.name)
			}
		})
	}
}

func TestAllKindsRejectUnknownOption(t *testing.T) {
	tests := []struct {
		name    string
		yamlSrc string
	}{
		{"required-text", "kind: required-text\nsubstrings: [a]\nbogus: 1\n"},
		{"forbidden-text", "kind: forbidden-text\nsubstrings: [a]\nbogus: 1\n"},
		{"required-tool", "kind: required-tool\ntool: bash\nbogus: 1\n"},
		{"forbidden-tool", "kind: forbidden-tool\ntool: bash\nbogus: 1\n"},
		{"tool-error-rate", "kind: tool-error-rate\nmax-error-rate: 0.5\nbogus: 1\n"},
		{"max-duration", "kind: max-duration\nlimit: 30s\nbogus: 1\n"},
		{"schema-result", "kind: schema-result\nbogus: 1\n"},
		{"judge", "kind: judge\nrubric: support-answer-quality\nbogus: 1\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := NewRegistry()
			spec := evaluatorSpec(t, tt.yamlSrc)
			bc := BuildContext{
				JudgeClient: fakeJudgeClient{},
				Rubrics:     map[string]rubric.Rubric{"support-answer-quality": testRubric()},
			}
			if _, err := reg.Build(spec, bc); err == nil {
				t.Fatalf("%s: unknown option accepted", tt.name)
			}
		})
	}
}

func TestRegisterRejectsDuplicate(t *testing.T) {
	reg := NewRegistry()
	err := reg.Register(Kind{
		Name:  "forbidden-tool",
		Build: func(*yaml.Node, BuildContext) (eval.Evaluator, error) { return nil, nil },
	})
	if err == nil {
		t.Fatal("duplicate kind accepted")
	}
}

func TestRegisterRejectsEmptyName(t *testing.T) {
	reg := NewRegistry()
	err := reg.Register(Kind{
		Name:  "",
		Build: func(*yaml.Node, BuildContext) (eval.Evaluator, error) { return nil, nil },
	})
	if err == nil {
		t.Fatal("empty kind name accepted")
	}
}

func TestKindsSorted(t *testing.T) {
	reg := NewRegistry()
	kinds := reg.Kinds()
	if len(kinds) == 0 {
		t.Fatal("no kinds registered")
	}
	names := make([]string, len(kinds))
	for i, k := range kinds {
		names[i] = k.Name
	}
	if !sort.StringsAreSorted(names) {
		t.Fatalf("kinds not sorted: %v", names)
	}
}

func TestRubricSpecRubric(t *testing.T) {
	rs := RubricSpec{
		Name:       "quality",
		Revision:   "v1",
		Scope:      "turn",
		Definition: "measures answer quality",
		Criteria: []CriterionSpec{
			{ID: "accuracy", Description: "accurate", MinScore: 0, MaxScore: 1},
		},
		Anchors: []AnchorSpec{
			{Score: 0.5, Label: "mid", Description: "middling"},
		},
	}
	rb, err := rs.Rubric()
	if err != nil {
		t.Fatalf("Rubric: %v", err)
	}
	if rb.Scope != eval.ScopeTurn {
		t.Fatalf("scope: got %v want ScopeTurn", rb.Scope)
	}
	if err := rb.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestRubricSpecRubricDefaultScope(t *testing.T) {
	rs := RubricSpec{
		Name:       "quality",
		Revision:   "v1",
		Definition: "measures answer quality",
		Criteria: []CriterionSpec{
			{ID: "accuracy", Description: "accurate", MinScore: 0, MaxScore: 1},
		},
	}
	rb, err := rs.Rubric()
	if err != nil {
		t.Fatalf("Rubric: %v", err)
	}
	if rb.Scope != eval.ScopeCase {
		t.Fatalf("default scope: got %v want ScopeCase", rb.Scope)
	}
}

func TestRubricSpecRubricUnknownScope(t *testing.T) {
	rs := RubricSpec{
		Name:       "quality",
		Revision:   "v1",
		Scope:      "nope",
		Definition: "measures answer quality",
		Criteria: []CriterionSpec{
			{ID: "accuracy", Description: "accurate", MinScore: 0, MaxScore: 1},
		},
	}
	if _, err := rs.Rubric(); err == nil {
		t.Fatal("unknown scope accepted")
	}
}
