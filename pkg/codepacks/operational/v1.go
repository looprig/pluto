// Package operational is Pluto's operational-stability qualification pack:
// bounded latency across prompt sizes, and tolerance for a bounded rate of
// tool-call errors. All evaluators are programmatic.
package operational

import (
	"strings"
	"time"

	"github.com/looprig/core/content"
	"github.com/looprig/eval"
	"github.com/looprig/eval/exact"
	"github.com/looprig/pluto/pkg/qual"
)

// Revision is the pack revision. Any semantic change to scenarios or
// evaluator wiring requires a bump.
const Revision eval.Revision = "v1"

// maxLatency bounds every scenario in the latency table.
const maxLatency = 30 * time.Second

// maxToolErrorRate bounds the tool-errors table: up to one in three tool
// calls may error before the table fails.
const maxToolErrorRate = 0.34

// longPromptLines is the number of repeated lines used to build the long
// prompt for op-003.
const longPromptLines = 40

// dimension is shared by every table in this pack.
const dimension eval.Name = "operational"

// V1 constructs the operational-stability pack. Construction is pure: no I/O.
func V1() qual.Pack {
	return qual.Pack{
		Name:     "operational-stability",
		Revision: Revision,
		Tables: []qual.Table{
			{
				Name:      "latency",
				Revision:  Revision,
				Dimension: dimension,
				Scenarios: latencyScenarios(),
				Evaluators: []eval.Evaluator{
					exact.MaxDuration(maxLatency),
				},
			},
			{
				Name:      "tool-errors",
				Revision:  Revision,
				Dimension: dimension,
				Requires:  []qual.Capability{qual.CapabilityTools},
				Scenarios: toolErrorScenarios(),
				Evaluators: []eval.Evaluator{
					exact.ToolErrorRate(exact.MaxErrorRate(maxToolErrorRate)),
				},
			},
		},
	}
}

func latencyScenarios() []eval.Scenario {
	short := "Say hello."
	medium := "Write one paragraph explaining, at a high level, how a hash table resolves collisions."
	long := strings.Repeat("Please consider this line of context before answering.\n", longPromptLines) +
		"Given all of the above, say hello."

	cases := []struct {
		id     string
		prompt string
	}{
		{"op-001-short-prompt", short},
		{"op-002-medium-prompt", medium},
		{"op-003-long-prompt", long},
	}
	out := make([]eval.Scenario, 0, len(cases))
	for _, c := range cases {
		out = append(out, eval.Scenario{
			ID:       c.id,
			Name:     "operational-latency",
			Revision: Revision,
			Input:    userTurn(c.prompt),
			Labels:   []eval.Label{{Key: "category", Value: "operational"}},
		})
	}
	return out
}

func toolErrorScenarios() []eval.Scenario {
	return []eval.Scenario{
		{
			ID:       "op-101-flaky-tools",
			Name:     "operational-tool-errors",
			Revision: Revision,
			Input:    userTurn("Look up the current status of three unrelated services."),
			Labels:   []eval.Label{{Key: "category", Value: "operational"}},
		},
	}
}

func userTurn(text string) content.AgenticMessages {
	return content.AgenticMessages{&content.UserMessage{Message: content.Message{
		Role:   content.RoleUser,
		Blocks: []content.Block{&content.TextBlock{Text: text}},
	}}}
}
