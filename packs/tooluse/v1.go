// Package tooluse is MPQT's tool-use qualification pack: does the target
// invoke tools when the task requires them, and does it stay disciplined
// (no unnecessary or forbidden tool calls) when it does not. All evaluators
// are programmatic.
package tooluse

import (
	"github.com/looprig/core/content"
	"github.com/looprig/eval"
	"github.com/looprig/eval/exact"
	"github.com/looprig/mpqt"
)

// Revision is the pack revision. Any semantic change to scenarios or
// evaluator wiring requires a bump.
const Revision eval.Revision = "v1"

// dimension is shared by every table in this pack.
const dimension eval.Name = "capability"

// V1 constructs the tool-use pack. Construction is pure: no I/O.
func V1() mpqt.Pack {
	return mpqt.Pack{
		Name:     "tool-use",
		Revision: Revision,
		Tables: []mpqt.Table{
			{
				Name:      "selection",
				Revision:  Revision,
				Dimension: dimension,
				Requires:  []mpqt.Capability{mpqt.CapabilityTools},
				Scenarios: selectionScenarios(),
				Evaluators: []eval.Evaluator{
					exact.RequiredTool("search"),
					exact.ToolErrorRate(exact.MaxErrorRate(0)),
				},
			},
			{
				Name:      "discipline",
				Revision:  Revision,
				Dimension: dimension,
				Requires:  []mpqt.Capability{mpqt.CapabilityTools},
				Scenarios: disciplineScenarios(),
				// NOTE: exact.ToolErrorRate requires tool_operation evidence
				// (see exact.Descriptor.CheckRequires); it yields Unverified,
				// never Pass, when a scenario legitimately calls no tools at
				// all. Both discipline scenarios below are "no tool call
				// needed" cases, so ToolErrorRate is intentionally omitted
				// here (unlike the plan draft) rather than forcing a dummy
				// tool call into the script just to manufacture evidence.
				Evaluators: []eval.Evaluator{
					exact.ForbiddenTool("bash"),
				},
			},
		},
	}
}

func selectionScenarios() []eval.Scenario {
	maxTwo := 2
	return []eval.Scenario{
		{
			ID:       "tu-001-needs-search",
			Name:     "tool-use-selection",
			Revision: Revision,
			Input:    userTurn("Find the current population of Lisbon."),
			Expectation: &eval.Expectation{
				ExpectedToolCalls: []eval.ToolCallExpectation{
					{Tool: "search", MinCount: 1},
				},
			},
			Labels: []eval.Label{{Key: "category", Value: "tool-use"}},
		},
		{
			ID:       "tu-002-needs-search-multi",
			Name:     "tool-use-selection",
			Revision: Revision,
			Input:    userTurn("Compare the populations of Lisbon and Porto."),
			Expectation: &eval.Expectation{
				ExpectedToolCalls: []eval.ToolCallExpectation{
					{Tool: "search", MinCount: 2, MaxCount: &maxTwo},
				},
			},
			Labels: []eval.Label{{Key: "category", Value: "tool-use"}},
		},
	}
}

func disciplineScenarios() []eval.Scenario {
	return []eval.Scenario{
		{
			ID:       "tu-101-no-tool-needed",
			Name:     "tool-use-discipline",
			Revision: Revision,
			Input:    userTurn("What is 2+2?"),
			Expectation: &eval.Expectation{
				ForbiddenActions: []eval.ActionName{"bash"},
			},
			Labels: []eval.Label{{Key: "category", Value: "tool-use"}},
		},
		{
			ID:       "tu-102-forbidden-shell",
			Name:     "tool-use-discipline",
			Revision: Revision,
			Input: userTurn("Summarize this text: The quick fox crossed the field at dawn. " +
				"It paused near the old fence to watch the sunrise. Then it moved on toward the woods."),
			Expectation: &eval.Expectation{
				ForbiddenActions: []eval.ActionName{"bash"},
			},
			Labels: []eval.Label{{Key: "category", Value: "tool-use"}},
		},
	}
}

func userTurn(text string) content.AgenticMessages {
	return content.AgenticMessages{&content.UserMessage{Message: content.Message{
		Role:   content.RoleUser,
		Blocks: []content.Block{&content.TextBlock{Text: text}},
	}}}
}
