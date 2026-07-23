package packfile

import (
	"testing"

	"github.com/looprig/inference"
	"gopkg.in/yaml.v3"
)

// schemaNode unmarshals the given YAML/JSON-ish source into a yaml.Node the
// same way a strictly-decoded ToolSpec/OutputSchemaSpec would carry it.
func schemaNode(t *testing.T, yamlSrc string) yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(yamlSrc), &n); err != nil {
		t.Fatalf("schemaNode: %v", err)
	}
	return n
}

func TestEnvironmentTemplate(t *testing.T) {
	// NOTE: the plan's literal fixture (`{type: object, properties: ...,
	// required: [command]}`, no `additionalProperties`) does not satisfy
	// inference.ValidateOutputSchema: every object-typed schema node --
	// root or nested -- requires `additionalProperties: false` (see
	// inference/output_test.go's "object missing additionalProperties"
	// case, and output.go's validateObject, which has no root-only
	// exemption). Confirmed empirically against the live validator before
	// making this change. `additionalProperties: false` is added here so
	// the fixture is an actual member of the portable subset; see the
	// commit message for this deviation from the plan text.
	env := &Environment{
		System:     "Be careful.",
		Tools:      []ToolSpec{{Name: "bash", Description: "Run a shell command", Schema: schemaNode(t, `{type: object, properties: {command: {type: string}}, required: [command], additionalProperties: false}`)}},
		ToolChoice: "auto",
	}
	req, err := env.Template()
	if err != nil {
		t.Fatalf("Template: %v", err)
	}
	if req.System != "Be careful." || len(req.Tools) != 1 || req.Tools[0].Name != "bash" {
		t.Fatalf("template: %+v", req)
	}
	if req.ToolChoice != inference.ToolChoiceAuto {
		t.Fatalf("tool choice: %v", req.ToolChoice)
	}
}

func TestEnvironmentTemplateRejectsNonPortableSchema(t *testing.T) {
	// $ref is outside the portable subset accepted by inference.ValidateOutputSchema.
	// Verified empirically: inference's schema decoder uses
	// json.Decoder.DisallowUnknownFields, so "$ref" is rejected as an
	// unknown keyword (SchemaFieldKeyword/SchemaReasonUnknownKeyword) --
	// the plan's own example holds as written; no substitution needed.
	env := &Environment{Tools: []ToolSpec{{Name: "t", Description: "d", Schema: schemaNode(t, `{"$ref": "#/x"}`)}}}
	if _, err := env.Template(); err == nil {
		t.Fatal("non-portable schema accepted")
	}
}

func TestEnvironmentTemplateNilReceiver(t *testing.T) {
	var env *Environment
	req, err := env.Template()
	if err != nil {
		t.Fatalf("Template: %v", err)
	}
	if req.System != "" || req.Messages != nil || req.Tools != nil || req.Output != nil ||
		req.ToolChoice != inference.ToolChoiceAuto || req.Override != nil {
		t.Fatalf("expected zero Request, got %+v", req)
	}
}

func TestEnvironmentTemplateToolChoiceRequired(t *testing.T) {
	env := &Environment{ToolChoice: "required"}
	req, err := env.Template()
	if err != nil {
		t.Fatalf("Template: %v", err)
	}
	if req.ToolChoice != inference.ToolChoiceRequired {
		t.Fatalf("tool choice: %v", req.ToolChoice)
	}
}

func TestEnvironmentTemplateRejectsUnknownToolChoice(t *testing.T) {
	env := &Environment{ToolChoice: "sometimes"}
	if _, err := env.Template(); err == nil {
		t.Fatal("unknown tool choice accepted")
	}
}

func TestEnvironmentTemplateOutputSchema(t *testing.T) {
	env := &Environment{
		OutputSchema: &OutputSchemaSpec{
			Name:   "result_v1",
			Schema: schemaNode(t, `{type: object, properties: {ok: {type: boolean}}, required: [ok], additionalProperties: false}`),
			Strict: true,
		},
	}
	req, err := env.Template()
	if err != nil {
		t.Fatalf("Template: %v", err)
	}
	if req.Output == nil || req.Output.Name != "result_v1" || !req.Output.Strict {
		t.Fatalf("output schema: %+v", req.Output)
	}
}

func TestEnvironmentTemplateRejectsNonPortableOutputSchema(t *testing.T) {
	env := &Environment{
		OutputSchema: &OutputSchemaSpec{
			Name:   "result_v1",
			Schema: schemaNode(t, `{"$ref": "#/x"}`),
		},
	}
	if _, err := env.Template(); err == nil {
		t.Fatal("non-portable output schema accepted")
	}
}
