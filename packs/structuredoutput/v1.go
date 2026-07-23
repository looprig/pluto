// Package structuredoutput is MPQT's structured-output qualification pack:
// can the target produce schema-conformant output across shapes, and does it
// fail cleanly when it cannot. All evaluators are programmatic.
package structuredoutput

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

// V1 constructs the structured-output pack. Construction is pure: no I/O.
func V1() mpqt.Pack {
	return mpqt.Pack{
		Name:     "structured-output",
		Revision: Revision,
		Tables: []mpqt.Table{{
			Name:      "conformance",
			Revision:  Revision,
			Dimension: dimension,
			Requires:  []mpqt.Capability{mpqt.CapabilityStructuredOutput},
			Scenarios: scenarios(),
			Evaluators: []eval.Evaluator{
				exact.SchemaResult(),
			},
		}},
	}
}

func scenarios() []eval.Scenario {
	cases := []struct {
		id     string
		prompt string
		schema eval.Revision
	}{
		{"so-001-flat-object", "Return the user's name and age as JSON.", "person/v1"},
		{"so-002-nested-object", "Return an order with nested line items as JSON.", "order/v1"},
		{"so-003-enum-selection", "Classify the sentiment as one of positive, negative, neutral.", "sentiment/v1"},
		{"so-004-required-fields", "Return an event; every field in the schema is required.", "event/v1"},
		{"so-005-unicode-values", "Return the city name 'München' and country '日本' as JSON.", "location/v1"},
		{"so-006-large-array", "Return a list of the first 200 positive integers as JSON.", "integers/v1"},
	}
	out := make([]eval.Scenario, 0, len(cases))
	for _, c := range cases {
		out = append(out, eval.Scenario{
			ID:       c.id,
			Name:     "structured-output",
			Revision: Revision,
			Input: content.AgenticMessages{&content.UserMessage{Message: content.Message{
				Role:   content.RoleUser,
				Blocks: []content.Block{&content.TextBlock{Text: c.prompt}},
			}}},
			Expectation: &eval.Expectation{
				StructuredOutput: &eval.StructuredOutputExpectation{
					Schema: c.schema, Strict: true,
				},
			},
			Labels: []eval.Label{{Key: "category", Value: "structured-output"}},
		})
	}
	return out
}
