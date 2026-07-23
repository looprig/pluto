package packfile

import (
	"sort"

	"github.com/looprig/core/content"
	"github.com/looprig/eval"
)

// Scenario maps a strictly-decoded ScenarioSpec to the eval.Scenario the
// evaluation framework actually runs. defaultName is used when the spec's own
// Name is empty. The result is validated with sc.Validate() before it is
// returned, so packfile never emits a scenario eval would reject; a
// validation failure is wrapped in a *Error naming the scenario ID.
func (s ScenarioSpec) Scenario(defaultName, revision string) (eval.Scenario, error) {
	name := s.Name
	if name == "" {
		name = defaultName
	}

	input, err := s.input()
	if err != nil {
		return eval.Scenario{}, wrapPathErr(s.ID, err)
	}

	sc := eval.Scenario{
		ID:          s.ID,
		Name:        eval.Name(name),
		Revision:    eval.Revision(revision),
		Input:       input,
		Expectation: s.expectation(),
		Labels:      s.labels(),
	}

	if err := sc.Validate(); err != nil {
		return eval.Scenario{}, wrapPathErr(s.ID, err)
	}
	return sc, nil
}

// input converts each MessageSpec into the corresponding sealed
// content.Conversation message. Only "user" and "assistant" roles are
// supported in v1; anything else is rejected here rather than defaulting.
func (s ScenarioSpec) input() (content.AgenticMessages, error) {
	msgs := make(content.AgenticMessages, 0, len(s.Input))
	for _, m := range s.Input {
		blocks := []content.Block{&content.TextBlock{Text: m.Text}}
		switch m.Role {
		case "user":
			msgs = append(msgs, &content.UserMessage{Message: content.Message{
				Role: content.RoleUser, Blocks: blocks,
			}})
		case "assistant":
			msgs = append(msgs, &content.AIMessage{Message: content.Message{
				Role: content.RoleAssistant, Blocks: blocks,
			}})
		default:
			return nil, &Error{Path: s.ID, Reason: "unknown message role: " + m.Role}
		}
	}
	return msgs, nil
}

// expectation converts the optional ExpectSpec into *eval.Expectation
// field-for-field. A nil Expect yields a nil Expectation.
func (s ScenarioSpec) expectation() *eval.Expectation {
	if s.Expect == nil {
		return nil
	}
	e := s.Expect

	var facts []eval.Fact
	for _, f := range e.RequiredFacts {
		facts = append(facts, eval.Fact(f))
	}

	var actions []eval.ActionName
	for _, a := range e.ForbiddenActions {
		actions = append(actions, eval.ActionName(a))
	}

	var toolCalls []eval.ToolCallExpectation
	for _, tc := range e.ExpectedToolCalls {
		toolCalls = append(toolCalls, eval.ToolCallExpectation{
			Tool:     eval.Name(tc.Tool),
			MinCount: tc.Min,
			MaxCount: tc.Max,
		})
	}

	var structured *eval.StructuredOutputExpectation
	if e.StructuredOutput != nil {
		structured = &eval.StructuredOutputExpectation{
			Schema: eval.Revision(e.StructuredOutput.Schema),
			Strict: e.StructuredOutput.Strict,
		}
	}

	var refs []eval.ReferenceAnswer
	for _, r := range e.ReferenceAnswers {
		refs = append(refs, eval.ReferenceAnswer(r))
	}

	return &eval.Expectation{
		RequiredFacts:     facts,
		ForbiddenActions:  actions,
		ExpectedToolCalls: toolCalls,
		StructuredOutput:  structured,
		ReferenceAnswers:  refs,
		PolicyRef:         eval.Revision(e.PolicyRef),
	}
}

// labels converts the label map into a deterministically ordered
// []eval.Label, sorted by key so map iteration order never leaks into the
// scenario's identity.
func (s ScenarioSpec) labels() []eval.Label {
	if len(s.Labels) == 0 {
		return nil
	}
	keys := make([]string, 0, len(s.Labels))
	for k := range s.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	labels := make([]eval.Label, 0, len(keys))
	for _, k := range keys {
		labels = append(labels, eval.Label{Key: eval.Name(k), Value: s.Labels[k]})
	}
	return labels
}
