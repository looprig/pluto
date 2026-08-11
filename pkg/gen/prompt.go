package gen

import (
	"fmt"
	"strings"

	"github.com/looprig/pluto/pkg/packfile"
)

// doNotDuplicateHeading introduces the existing-scenario-ID list so the model
// (and a human reading Result.InputText) can find it unambiguously.
const doNotDuplicateHeading = "Do not duplicate these existing scenario IDs:"

// buildPrompt renders the single user-message text sent to the generation
// model: the table's environment (system prompt, tool schemas, output
// schema), every registered evaluator kind's evidence requirement, this
// table's own wired evaluators and rubric definitions, the requested count
// and optional focus/intent steer, and every existing scenario ID under a
// "do not duplicate" heading.
func buildPrompt(doc *packfile.Document, tf packfile.TableFile, reg *packfile.Registry, req Request) (string, error) {
	envReq, err := tf.Environment.Template()
	if err != nil {
		return "", fmt.Errorf("environment template: %w", err)
	}

	var b strings.Builder

	b.WriteString("You are generating candidate test scenarios for the Pluto evaluation harness.\n")
	b.WriteString("Respond with a JSON object matching the provided output schema; do not include any other text.\n\n")

	fmt.Fprintf(&b, "Pack: %s\nTable: %s\nDimension: %s\n", doc.Pack.Pack, tf.Table, tf.Dimension)
	if len(tf.Requires) > 0 {
		fmt.Fprintf(&b, "Requires: %s\n", strings.Join(tf.Requires, ", "))
	}
	b.WriteString("\n")

	if envReq.System != "" {
		b.WriteString("Environment system prompt:\n")
		b.WriteString(envReq.System)
		b.WriteString("\n\n")
	}

	if len(envReq.Tools) > 0 {
		b.WriteString("Tools available to the target:\n")
		for _, tool := range envReq.Tools {
			fmt.Fprintf(&b, "- %s: %s\n  schema: %s\n", tool.Name, tool.Description, string(tool.Schema))
		}
		b.WriteString("\n")
	}

	if envReq.Output != nil {
		fmt.Fprintf(&b, "Target output schema contract %q:\n  schema: %s\n\n", envReq.Output.Name, string(envReq.Output.Schema))
	}

	b.WriteString("Evaluator kinds known to Pluto (name: doc -- evidence requirement):\n")
	for _, k := range reg.Kinds() {
		fmt.Fprintf(&b, "- %s: %s -- evidence: %s\n", k.Name, k.Doc, k.Evidence)
	}
	b.WriteString("\n")

	if len(tf.Evaluators) > 0 {
		kinds := make([]string, 0, len(tf.Evaluators))
		for _, es := range tf.Evaluators {
			kinds = append(kinds, es.Kind)
		}
		fmt.Fprintf(&b, "Evaluators wired for this table: %s\n\n", strings.Join(kinds, ", "))
	}

	if len(tf.Rubrics) > 0 {
		b.WriteString("Rubric definitions available to judge evaluators in this table:\n")
		for _, r := range tf.Rubrics {
			fmt.Fprintf(&b, "- %s: %s\n", r.Name, r.Definition)
		}
		b.WriteString("\n")
	}

	if req.Intent != "" {
		b.WriteString("Bootstrap intent (no existing scenarios to anchor on -- generate from this description instead):\n")
		b.WriteString(req.Intent)
		b.WriteString("\n\n")
	}

	if req.Focus != "" {
		fmt.Fprintf(&b, "Focus: %s\n\n", req.Focus)
	}

	b.WriteString(doNotDuplicateHeading + "\n")
	for _, sc := range tf.Scenarios {
		fmt.Fprintf(&b, "- %s\n", sc.ID)
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "Generate %d new scenarios.\n", req.N)

	return b.String(), nil
}
