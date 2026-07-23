package gen_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/looprig/core/content"
	"github.com/looprig/inference"
	"github.com/looprig/inference/model"
	"github.com/looprig/inference/stream"
	"github.com/looprig/mpqt/pkg/gen"
	"github.com/looprig/mpqt/pkg/packfile"
	"gopkg.in/yaml.v3"
)

// --- fake inference.Client ---

// fakeClient is a local, canned stand-in for inference.Client: Invoke
// returns a pre-built *inference.Response (or an error), and every request it
// was called with is recorded so tests can assert on prompt content. Stream
// is never exercised by gen and panics if called.
type fakeClient struct {
	resp *inference.Response
	err  error

	calls []inference.Request
}

func (f *fakeClient) Invoke(_ context.Context, req inference.Request) (*inference.Response, error) {
	f.calls = append(f.calls, req)
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

func (f *fakeClient) Stream(context.Context, inference.Request) (*stream.StreamReader[content.Chunk], error) {
	panic("gen never streams")
}

var _ inference.Client = (*fakeClient)(nil)

// --- fixtures ---

// schemaNode unmarshals src into a yaml.Node the same way a strictly-decoded
// ToolSpec/OutputSchemaSpec would carry it (mirrors packfile's own
// environment_test.go helper).
func schemaNode(t *testing.T, src string) yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(src), &n); err != nil {
		t.Fatalf("schemaNode: %v", err)
	}
	return n
}

// seededDoc returns a one-table pack.Document with an environment (system
// prompt, one tool), a wired evaluator, a rubric, and two existing
// scenarios -- the "normal" seeded-generation fixture.
func seededDoc(t *testing.T) *packfile.Document {
	t.Helper()
	tf := packfile.TableFile{
		Table:     "discipline",
		Revision:  "v1",
		Dimension: "capability",
		Requires:  []string{"tools"},
		Environment: &packfile.Environment{
			System: "You are a careful assistant. Use tools only when necessary.",
			Tools: []packfile.ToolSpec{
				{
					Name:        "bash",
					Description: "Run a shell command",
					Schema:      schemaNode(t, `{type: object, properties: {command: {type: string}}, required: [command], additionalProperties: false}`),
				},
			},
			ToolChoice: "auto",
		},
		Evaluators: []packfile.EvaluatorSpec{
			{Kind: "forbidden-tool"},
		},
		Rubrics: []packfile.RubricSpec{
			{Name: "support-answer-quality", Definition: "Answers must cite plan terms accurately."},
		},
		Scenarios: []packfile.ScenarioSpec{
			{ID: "tu-101-no-tool-needed", Input: []packfile.MessageSpec{{Role: "user", Text: "What is 2+2?"}}},
			{ID: "tu-102-forbidden-shell", Input: []packfile.MessageSpec{{Role: "user", Text: "Summarize this text."}}},
		},
	}
	return &packfile.Document{
		Dir:    "tool-use",
		Pack:   packfile.PackFile{Pack: "tool-use", Revision: "v1", Tables: []string{"discipline.yaml"}},
		Tables: []packfile.TableFile{tf},
	}
}

// bootstrapDoc returns a one-table pack.Document with an environment and a
// rubric but NO existing scenarios -- the fixture for bootstrap-mode tests.
func bootstrapDoc(t *testing.T) *packfile.Document {
	t.Helper()
	tf := packfile.TableFile{
		Table:     "billing",
		Revision:  "v1",
		Dimension: "custom",
		Environment: &packfile.Environment{
			System: "You help customers with billing questions.",
		},
		Rubrics: []packfile.RubricSpec{
			{Name: "support-answer-quality", Definition: "Answers must cite plan terms accurately."},
		},
	}
	return &packfile.Document{
		Dir:    "my-assistant",
		Pack:   packfile.PackFile{Pack: "my-assistant", Revision: "v1", Tables: []string{"billing.yaml"}},
		Tables: []packfile.TableFile{tf},
	}
}

func assistantText(raw string) *content.AIMessage {
	return &content.AIMessage{Message: content.Message{
		Role:   content.RoleAssistant,
		Blocks: []content.Block{&content.TextBlock{Text: raw}},
	}}
}

// canned is the minimal valid *inference.Response shape, mirroring
// inference's own structured_result_test.go fixtures: an assistant text
// block carrying the JSON payload plus an explicit FinishReason.
func canned(raw string) *inference.Response {
	return &inference.Response{
		Message:      assistantText(raw),
		FinishReason: stream.FinishReasonStop,
	}
}

func testModel() model.Model {
	return model.Model{Provider: "anthropic", APIFormat: "anthropic", Name: "claude-sonnet-5"}
}

// --- tests ---

func TestGenerateBuildsPromptWithEnvironmentToolsEvidenceAndSeeds(t *testing.T) {
	t.Parallel()

	doc := seededDoc(t)
	client := &fakeClient{resp: canned(`{"scenarios":[{"id":"tu-103-new","input":[{"role":"user","text":"hi"}]}]}`)}

	res, err := gen.Generate(context.Background(), client, gen.Request{
		Doc: doc, Table: "discipline", N: 3, Model: testModel(),
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(client.calls) != 1 {
		t.Fatalf("Invoke called %d times, want 1", len(client.calls))
	}

	prompt := res.InputText
	if prompt == "" {
		t.Fatal("InputText is empty")
	}
	if !strings.Contains(prompt, "You are a careful assistant. Use tools only when necessary.") {
		t.Error("prompt missing environment system prompt")
	}
	if !strings.Contains(prompt, "bash") || !strings.Contains(prompt, `"command"`) {
		t.Error("prompt missing tool name/schema")
	}

	reg := packfile.NewRegistry()
	for _, k := range reg.Kinds() {
		if !strings.Contains(prompt, k.Evidence) {
			t.Errorf("prompt missing evidence string for kind %q: %q", k.Name, k.Evidence)
		}
	}

	if !strings.Contains(prompt, "Do not duplicate these existing scenario IDs:") {
		t.Error("prompt missing do-not-duplicate heading")
	}
	for _, id := range []string{"tu-101-no-tool-needed", "tu-102-forbidden-shell"} {
		if !strings.Contains(prompt, id) {
			t.Errorf("prompt missing existing scenario ID %q", id)
		}
	}

	// Verify the same prompt text was actually sent to the client, as the
	// single content.UserMessage (ground rule 1).
	sent := client.calls[0]
	if len(sent.Messages) != 1 {
		t.Fatalf("Invoke request carried %d messages, want 1", len(sent.Messages))
	}
	um, ok := sent.Messages[0].(*content.UserMessage)
	if !ok {
		t.Fatalf("Invoke message = %T, want *content.UserMessage", sent.Messages[0])
	}
	if len(um.Blocks) != 1 {
		t.Fatalf("UserMessage carried %d blocks, want 1", len(um.Blocks))
	}
	tb, ok := um.Blocks[0].(*content.TextBlock)
	if !ok || tb.Text != prompt {
		t.Fatalf("UserMessage text mismatch")
	}
	if sent.Output == nil || sent.Output.Name != "scenario-batch" || !sent.Output.Strict {
		t.Fatalf("Output schema not set as expected: %+v", sent.Output)
	}

	if len(res.Accepted) != 1 || res.Accepted[0].ID != "tu-103-new" {
		t.Fatalf("Accepted = %+v, want one scenario tu-103-new", res.Accepted)
	}
	if len(res.Rejected) != 0 {
		t.Fatalf("Rejected = %+v, want none", res.Rejected)
	}
}

func TestGenerateRejectsDuplicateOfExistingID(t *testing.T) {
	t.Parallel()

	doc := seededDoc(t)
	client := &fakeClient{resp: canned(`{"scenarios":[
		{"id":"tu-101-no-tool-needed","input":[{"role":"user","text":"dup of existing"}]},
		{"id":"tu-200-valid","input":[{"role":"user","text":"ok"}]}
	]}`)}

	res, err := gen.Generate(context.Background(), client, gen.Request{
		Doc: doc, Table: "discipline", N: 2, Model: testModel(),
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if len(res.Accepted) != 1 || res.Accepted[0].ID != "tu-200-valid" {
		t.Fatalf("Accepted = %+v, want only tu-200-valid", res.Accepted)
	}
	if len(res.Rejected) != 1 || res.Rejected[0].ID != "tu-101-no-tool-needed" {
		t.Fatalf("Rejected = %+v, want tu-101-no-tool-needed", res.Rejected)
	}
	if !strings.Contains(res.Rejected[0].Reason, "existing") {
		t.Errorf("Rejected[0].Reason = %q, want it to mention the existing-ID duplicate", res.Rejected[0].Reason)
	}
}

func TestGenerateRejectsDuplicateWithinBatch(t *testing.T) {
	t.Parallel()

	doc := seededDoc(t)
	client := &fakeClient{resp: canned(`{"scenarios":[
		{"id":"tu-300-new","input":[{"role":"user","text":"first"}]},
		{"id":"tu-300-new","input":[{"role":"user","text":"second, same id"}]}
	]}`)}

	res, err := gen.Generate(context.Background(), client, gen.Request{
		Doc: doc, Table: "discipline", N: 2, Model: testModel(),
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if len(res.Accepted) != 1 || res.Accepted[0].ID != "tu-300-new" {
		t.Fatalf("Accepted = %+v, want exactly one tu-300-new", res.Accepted)
	}
	if len(res.Rejected) != 1 || res.Rejected[0].ID != "tu-300-new" {
		t.Fatalf("Rejected = %+v, want the second tu-300-new", res.Rejected)
	}
	if !strings.Contains(res.Rejected[0].Reason, "batch") {
		t.Errorf("Rejected[0].Reason = %q, want it to mention the within-batch duplicate", res.Rejected[0].Reason)
	}
}

func TestGenerateRejectsCandidateFailingScenarioValidation(t *testing.T) {
	t.Parallel()

	doc := seededDoc(t)
	// Empty "input" fails eval.Scenario.Validate (Input must not be empty),
	// which packfile.ScenarioSpec.Scenario surfaces as an error.
	client := &fakeClient{resp: canned(`{"scenarios":[
		{"id":"tu-400-empty-input","input":[]},
		{"id":"tu-401-valid","input":[{"role":"user","text":"ok"}]}
	]}`)}

	res, err := gen.Generate(context.Background(), client, gen.Request{
		Doc: doc, Table: "discipline", N: 2, Model: testModel(),
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if len(res.Accepted) != 1 || res.Accepted[0].ID != "tu-401-valid" {
		t.Fatalf("Accepted = %+v, want only tu-401-valid", res.Accepted)
	}
	if len(res.Rejected) != 1 || res.Rejected[0].ID != "tu-400-empty-input" {
		t.Fatalf("Rejected = %+v, want tu-400-empty-input", res.Rejected)
	}
	if res.Rejected[0].Reason == "" {
		t.Error("Rejected[0].Reason is empty")
	}
	// Reason must be a flat, ID-free phrase (Rejection.ID already carries the
	// scenario ID) -- not packfile.Error's Error() string, which prepends a
	// "packfile: <id>: " product/ID prefix.
	if strings.Contains(res.Rejected[0].Reason, "packfile:") {
		t.Errorf("Rejected[0].Reason = %q, must not contain the packfile: product prefix", res.Rejected[0].Reason)
	}
	if strings.Contains(res.Rejected[0].Reason, res.Rejected[0].ID) {
		t.Errorf("Rejected[0].Reason = %q, must not repeat Rejection.ID (%q)", res.Rejected[0].Reason, res.Rejected[0].ID)
	}
}

func TestGenerateRejectsNOutOfRangeWithoutInvoking(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		n    int
	}{
		{name: "zero", n: 0},
		{name: "negative", n: -1},
		{name: "above max", n: 51},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc := seededDoc(t)
			client := &fakeClient{resp: canned(`{"scenarios":[]}`)}

			_, err := gen.Generate(context.Background(), client, gen.Request{
				Doc: doc, Table: "discipline", N: tt.n, Model: testModel(),
			})
			if err == nil {
				t.Fatal("Generate() error = nil, want out-of-range N rejected")
			}
			if len(client.calls) != 0 {
				t.Errorf("Invoke called %d times, want 0 (invalid N must fail before any paid call)", len(client.calls))
			}
		})
	}
}

func TestGenerateRejectsEmptyTableWithoutIntent(t *testing.T) {
	t.Parallel()

	doc := bootstrapDoc(t)
	client := &fakeClient{resp: canned(`{"scenarios":[]}`)}

	_, err := gen.Generate(context.Background(), client, gen.Request{
		Doc: doc, Table: "billing", N: 5, Model: testModel(),
	})
	if err == nil {
		t.Fatal("Generate() error = nil, want empty table without Intent rejected")
	}
	if len(client.calls) != 0 {
		t.Errorf("Invoke called %d times, want 0", len(client.calls))
	}
}

func TestGenerateWithIntentIncludesIntentAndRubricInPrompt(t *testing.T) {
	t.Parallel()

	doc := bootstrapDoc(t)
	client := &fakeClient{resp: canned(`{"scenarios":[{"id":"bill-001","input":[{"role":"user","text":"Why was I charged twice?"}]}]}`)}

	const intent = "customers asking about refunds, proration, plan changes"
	res, err := gen.Generate(context.Background(), client, gen.Request{
		Doc: doc, Table: "billing", N: 5, Intent: intent, Model: testModel(),
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(client.calls) != 1 {
		t.Fatalf("Invoke called %d times, want 1", len(client.calls))
	}
	if !strings.Contains(res.InputText, intent) {
		t.Error("prompt missing intent text")
	}
	if !strings.Contains(res.InputText, "Answers must cite plan terms accurately.") {
		t.Error("prompt missing rubric definition")
	}
	if len(res.Accepted) != 1 {
		t.Fatalf("Accepted = %+v, want one scenario", res.Accepted)
	}
}

func TestGenerateRejectsNilDocAndNilClient(t *testing.T) {
	t.Parallel()

	if _, err := gen.Generate(context.Background(), &fakeClient{}, gen.Request{Doc: nil, Table: "x", N: 1}); err == nil {
		t.Fatal("nil Doc accepted")
	}
	doc := seededDoc(t)
	if _, err := gen.Generate(context.Background(), nil, gen.Request{Doc: doc, Table: "discipline", N: 1}); err == nil {
		t.Fatal("nil client accepted")
	}
}

func TestGenerateRejectsUnknownTable(t *testing.T) {
	t.Parallel()

	doc := seededDoc(t)
	client := &fakeClient{resp: canned(`{"scenarios":[]}`)}
	_, err := gen.Generate(context.Background(), client, gen.Request{
		Doc: doc, Table: "does-not-exist", N: 1, Model: testModel(),
	})
	if err == nil {
		t.Fatal("unknown table accepted")
	}
	if len(client.calls) != 0 {
		t.Errorf("Invoke called %d times, want 0", len(client.calls))
	}
}

func TestGeneratePropagatesInvokeError(t *testing.T) {
	t.Parallel()

	doc := seededDoc(t)
	wantErr := errors.New("boom")
	client := &fakeClient{err: wantErr}
	_, err := gen.Generate(context.Background(), client, gen.Request{
		Doc: doc, Table: "discipline", N: 1, Model: testModel(),
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Generate() error = %v, want it to wrap %v", err, wantErr)
	}
}
