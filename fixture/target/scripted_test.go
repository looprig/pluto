package target

import (
	"context"
	"testing"
	"time"

	"github.com/looprig/core/content"
	"github.com/looprig/eval"
)

func scenario(id string) eval.Scenario {
	return eval.Scenario{
		ID: id, Name: "t", Revision: "r1",
		Input: content.AgenticMessages{&content.UserMessage{Message: content.Message{
			Role:   content.RoleUser,
			Blocks: []content.Block{&content.TextBlock{Text: "go"}},
		}}},
	}
}

func TestScriptedObserve(t *testing.T) {
	t.Parallel()
	st := NewScripted("fake-model", map[string]Script{
		"a": {
			Reply:    "done",
			Duration: 250 * time.Millisecond,
			ToolCalls: []ToolCall{
				{Name: "search", ID: "tu_1"},
				{Name: "bash", ID: "tu_2", IsError: true},
			},
			Structured: &Structured{SchemaName: "answer", SchemaRevision: "v1"},
		},
	})

	obs, err := st.Observe(context.Background(), scenario("a"))
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if err := obs.Validate(); err != nil {
		t.Fatalf("observation invalid: %v", err)
	}
	if obs.Subject.Revision != "r1" {
		t.Errorf("Subject.Revision = %q, want scenario revision r1", obs.Subject.Revision)
	}
	sample := eval.Sample{Scenario: &eval.Scenario{}, Observation: obs}
	sc := scenario("a")
	sample.Scenario = &sc
	if err := sample.Validate(); err != nil {
		t.Fatalf("sample invalid (revision mismatch?): %v", err)
	}
	kinds := map[eval.EvidenceKind]int{}
	for _, ev := range obs.Trace.Evidence {
		kinds[ev.Kind]++
	}
	if kinds[eval.EvidenceToolOperation] != 2 {
		t.Errorf("tool_operation evidence = %d, want 2", kinds[eval.EvidenceToolOperation])
	}
	if kinds[eval.EvidenceTiming] != 1 {
		t.Errorf("timing evidence = %d, want 1", kinds[eval.EvidenceTiming])
	}
	if kinds[eval.EvidenceStructuredOutput] != 1 {
		t.Errorf("structured_output evidence = %d, want 1", kinds[eval.EvidenceStructuredOutput])
	}

	if _, err := st.Observe(context.Background(), scenario("missing")); err == nil {
		t.Error("Observe() for unscripted scenario should error")
	}
}

func TestScriptedStructuredError(t *testing.T) {
	t.Parallel()
	st := NewScripted("fake-model", map[string]Script{
		"bad": {Reply: "{", StructuredErr: &StructuredErr{
			Schema: "v1", Reason: eval.StructuredErrorInvalidJSON,
		}},
	})
	obs, err := st.Observe(context.Background(), scenario("bad"))
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if err := obs.Validate(); err != nil {
		t.Fatalf("observation invalid: %v", err)
	}
	var found bool
	for _, ev := range obs.Trace.Evidence {
		if ev.Kind == eval.EvidenceStructuredError {
			found = true
		}
	}
	if !found {
		t.Error("expected structured_output_error evidence")
	}
}
