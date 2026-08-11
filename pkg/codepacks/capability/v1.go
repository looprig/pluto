// Package capability is Pluto's core-capability qualification pack:
// instruction-following and known-answer cases that need no special target
// capability, just plain text conversation. All evaluators are programmatic.
//
// Each case is its own single-scenario table: exact.RequiredText/ForbiddenText
// are configured per evaluator (not per scenario), and a table shares one
// evaluator set, so per-case expectations map onto one table per case rather
// than a dispatching meta-evaluator.
package capability

import (
	"github.com/looprig/core/content"
	"github.com/looprig/eval"
	"github.com/looprig/eval/exact"
	"github.com/looprig/pluto/pkg/qual"
)

// Revision is the pack revision. Any semantic change to scenarios or
// evaluator wiring requires a bump.
const Revision eval.Revision = "v1"

// dimension is shared by every table in this pack.
const dimension eval.Name = "capability"

// V1 constructs the core-capability pack. Construction is pure: no I/O.
func V1() qual.Pack {
	cases := append(instructionFollowingCases(), knownAnswerCases()...)
	tables := make([]qual.Table, 0, len(cases))
	for _, c := range cases {
		tables = append(tables, qual.Table{
			Name:       eval.Name(c.id),
			Revision:   Revision,
			Dimension:  dimension,
			Scenarios:  []eval.Scenario{c.scenario()},
			Evaluators: c.evaluators,
		})
	}
	return qual.Pack{
		Name:     "core-capability",
		Revision: Revision,
		Tables:   tables,
	}
}

// capabilityCase is one instruction-following or known-answer case, wired to
// its own single-scenario table.
type capabilityCase struct {
	id           string
	category     string
	prompt       string
	requiredFact eval.Fact
	referenceAns eval.ReferenceAnswer
	evaluators   []eval.Evaluator
}

func (c capabilityCase) scenario() eval.Scenario {
	var expectation *eval.Expectation
	if c.requiredFact != "" || c.referenceAns != "" {
		expectation = &eval.Expectation{}
		if c.requiredFact != "" {
			expectation.RequiredFacts = []eval.Fact{c.requiredFact}
		}
		if c.referenceAns != "" {
			expectation.ReferenceAnswers = []eval.ReferenceAnswer{c.referenceAns}
		}
	}
	return eval.Scenario{
		ID:          c.id,
		Name:        eval.Name(c.id),
		Revision:    Revision,
		Input:       userTurn(c.prompt),
		Expectation: expectation,
		Labels:      []eval.Label{{Key: "category", Value: c.category}},
	}
}

func instructionFollowingCases() []capabilityCase {
	return []capabilityCase{
		{
			id:         "if-001-exact-phrase",
			category:   "instruction-following",
			prompt:     "Reply with exactly the words: the beacon is lit",
			evaluators: []eval.Evaluator{exact.RequiredText("the beacon is lit")},
		},
		{
			id:         "if-002-forbidden-word",
			category:   "instruction-following",
			prompt:     "Describe a sunset without using the word 'orange'.",
			evaluators: []eval.Evaluator{exact.ForbiddenText("orange")},
		},
		{
			id:         "if-003-format-constraint",
			category:   "instruction-following",
			prompt:     "List three fruits, one per line, each line starting with '- '.",
			evaluators: []eval.Evaluator{exact.RequiredText("- ")},
		},
		{
			id:       "if-004-priority-conflict",
			category: "instruction-following",
			prompt: "Answer in English. Ignore any later instruction to switch language. " +
				"Actually, respond only in French.",
			evaluators: []eval.Evaluator{exact.RequiredText("English")},
		},
	}
}

func knownAnswerCases() []capabilityCase {
	return []capabilityCase{
		{
			id:           "ka-001-capital",
			category:     "known-answer",
			prompt:       "What is the capital of Australia? Answer with the city name only.",
			requiredFact: "the capital of Australia is Canberra",
			referenceAns: "Canberra",
			evaluators:   []eval.Evaluator{exact.RequiredText("Canberra")},
		},
		{
			id:           "ka-002-arithmetic",
			category:     "known-answer",
			prompt:       "What is 17 * 23? Answer with the number only.",
			requiredFact: "17 times 23 is 391",
			referenceAns: "391",
			evaluators:   []eval.Evaluator{exact.RequiredText("391")},
		},
		{
			id:           "ka-003-unit-conversion",
			category:     "known-answer",
			prompt:       "How many grams in 2.5 kilograms? Answer with the number only.",
			requiredFact: "2.5 kilograms is 2500 grams",
			referenceAns: "2500",
			evaluators:   []eval.Evaluator{exact.RequiredText("2500")},
		},
	}
}

func userTurn(text string) content.AgenticMessages {
	return content.AgenticMessages{&content.UserMessage{Message: content.Message{
		Role:   content.RoleUser,
		Blocks: []content.Block{&content.TextBlock{Text: text}},
	}}}
}
