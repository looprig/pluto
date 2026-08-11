package packfile

//go:generate go run ./internal/genschema

import (
	"encoding/json"
	"fmt"
)

// portableSubsetWarning is the exact warning carried on every author-supplied
// JSON Schema fragment field (a tool's argument schema, a structured-output
// schema): both are validated by inference.ValidateOutputSchema against a
// bounded portable subset of JSON Schema, not against the full spec, and
// provider-specific keywords are rejected outright rather than silently
// translated.
const portableSubsetWarning = "portable JSON Schema subset (see inference.ValidateOutputSchema); provider-specific keywords are rejected, not translated"

// schemaDraft is the JSON Schema dialect every definition in Schema's output
// is written against.
const schemaDraft = "http://json-schema.org/draft-07/schema#"

// schemaID identifies the generated document; it is not resolved over the
// network anywhere in this module.
const schemaID = "https://github.com/looprig/pluto/blob/main/pkg/packfile/schema.json"

// Schema hand-assembles the single draft-07 JSON Schema document describing
// both YAML pack file shapes (pack.yaml and a table file) accepted by this
// package's decoders. The evaluators[] shape is generated from reg's Kinds so
// the schema always reflects the registry's actual set of evaluator kinds and
// their real per-kind options, rather than a hand-maintained shadow copy.
// Object keys are written in the sorted order encoding/json's map marshaling
// already produces, so two calls with the same registry content always
// produce byte-identical output -- the property TestSchemaMatchesCommittedFile
// depends on.
func Schema(reg *Registry) ([]byte, error) {
	defs, err := definitions(reg)
	if err != nil {
		return nil, fmt.Errorf("packfile: schema: %w", err)
	}

	root := map[string]any{
		"$schema":     schemaDraft,
		"$id":         schemaID,
		"title":       "Pluto pack file",
		"description": "A pack.yaml identity/manifest file or one table file from a Pluto pack directory (pkg/packfile).",
		"oneOf": []any{
			map[string]any{"$ref": "#/definitions/packFile"},
			map[string]any{"$ref": "#/definitions/tableFile"},
		},
		"definitions": defs,
	}

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("packfile: schema: encode: %w", err)
	}
	return append(out, '\n'), nil
}

// definitions builds every named sub-schema Schema's root oneOf and its
// nested $refs resolve against.
func definitions(reg *Registry) (map[string]any, error) {
	evaluatorSpec, err := evaluatorSpecSchema(reg)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"packFile":             packFileSchema(),
		"tableFile":            tableFileSchema(),
		"environment":          environmentSchema(),
		"toolSpec":             toolSpecSchema(),
		"outputSchemaSpec":     outputSchemaSpecSchema(),
		"rubricSpec":           rubricSpecSchema(),
		"criterionSpec":        criterionSpecSchema(),
		"anchorSpec":           anchorSpecSchema(),
		"evaluatorSpec":        evaluatorSpec,
		"runSpec":              runSpecSchema(),
		"scenarioSpec":         scenarioSpecSchema(),
		"messageSpec":          messageSpecSchema(),
		"expectSpec":           expectSpecSchema(),
		"toolCallExpectSpec":   toolCallExpectSpecSchema(),
		"structuredExpectSpec": structuredExpectSpecSchema(),
		"scriptSpec":           scriptSpecSchema(),
		"scriptToolCall":       scriptToolCallSchema(),
		"structuredSpec":       structuredSpecSchema(),
		"structuredErrSpec":    structuredErrSpecSchema(),
	}, nil
}

// packFileSchema describes pack.yaml (document.go's PackFile).
func packFileSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "pack.yaml: identity plus explicit, ordered table membership.",
		"required":    []any{"pack", "revision"},
		"properties": map[string]any{
			"pack":     map[string]any{"type": "string", "minLength": 1, "description": "pack identity name (eval.Name)."},
			"revision": map[string]any{"type": "string", "minLength": 1, "description": "pack revision (eval.Revision)."},
			"tables": map[string]any{
				"type":        "array",
				"description": "table file names, in the order they are loaded and built.",
				"items":       map[string]any{"type": "string"},
			},
		},
		"additionalProperties": false,
	}
}

// tableFileSchema describes one table YAML file (document.go's TableFile).
func tableFileSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "one table file: a named, versioned scenario family sharing one evaluator set and one score dimension.",
		"required":    []any{"table", "revision", "dimension", "scenarios", "evaluators"},
		"properties": map[string]any{
			"table":     map[string]any{"type": "string", "minLength": 1, "description": "table identity name (eval.Name)."},
			"revision":  map[string]any{"type": "string", "minLength": 1, "description": "table revision (eval.Revision)."},
			"dimension": map[string]any{"type": "string", "minLength": 1, "description": "score dimension name (eval.Name) this table contributes to."},
			"requires": map[string]any{
				"type":        "array",
				"description": "capabilities the target must declare for this table to run (qual.Capability).",
				"items":       map[string]any{"type": "string"},
			},
			"environment": map[string]any{"$ref": "#/definitions/environment"},
			"rubrics": map[string]any{
				"type":        "array",
				"description": "judge rubrics defined by this table, referenced by name from a judge evaluator's rubric option.",
				"items":       map[string]any{"$ref": "#/definitions/rubricSpec"},
			},
			"evaluators": map[string]any{
				"type":        "array",
				"description": "the evaluators applied to every scenario in this table; must not be empty.",
				"minItems":    1,
				"items":       map[string]any{"$ref": "#/definitions/evaluatorSpec"},
			},
			"run": map[string]any{"$ref": "#/definitions/runSpec"},
			"scenarios": map[string]any{
				"type":        "array",
				"description": "the test cases in this table; must not be empty.",
				"minItems":    1,
				"items":       map[string]any{"$ref": "#/definitions/scenarioSpec"},
			},
			"script": map[string]any{
				"type":                 "object",
				"description":          "offline fixture replies keyed by scenario ID (qual/target.Script), for scripted target runs with no live model.",
				"additionalProperties": map[string]any{"$ref": "#/definitions/scriptSpec"},
			},
		},
		"additionalProperties": false,
	}
}

// environmentSchema describes Environment: the per-table stimulus applied to
// the target template.
func environmentSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "the per-table stimulus applied to the target template.",
		"properties": map[string]any{
			"system": map[string]any{"type": "string", "description": "system prompt text."},
			"tools": map[string]any{
				"type":        "array",
				"description": "model-visible tools offered to the target.",
				"items":       map[string]any{"$ref": "#/definitions/toolSpec"},
			},
			"tool-choice": map[string]any{
				"type":        "string",
				"enum":        []any{"", "auto", "required"},
				"description": "tool choice mode; \"\" and \"auto\" are equivalent, anything else besides \"required\" is rejected.",
			},
			"output-schema": map[string]any{"$ref": "#/definitions/outputSchemaSpec"},
		},
		"additionalProperties": false,
	}
}

// toolSpecSchema describes ToolSpec: one model-visible tool.
func toolSpecSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "one model-visible tool.",
		"required":    []any{"name", "schema"},
		"properties": map[string]any{
			"name":        map[string]any{"type": "string", "minLength": 1},
			"description": map[string]any{"type": "string"},
			"schema": map[string]any{
				"type":        "object",
				"description": "the tool's argument schema, arbitrary YAML converted to canonical JSON at build time. " + portableSubsetWarning,
			},
		},
		"additionalProperties": false,
	}
}

// outputSchemaSpecSchema describes OutputSchemaSpec: the structured-output
// contract for a table.
func outputSchemaSpecSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "the structured-output contract for the table.",
		"required":    []any{"name", "schema"},
		"properties": map[string]any{
			"name":        map[string]any{"type": "string", "minLength": 1},
			"description": map[string]any{"type": "string"},
			"schema": map[string]any{
				"type":        "object",
				"description": "the output's JSON Schema, arbitrary YAML converted to canonical JSON at build time. " + portableSubsetWarning,
			},
			"strict": map[string]any{"type": "boolean", "description": "mirrors inference.OutputSchema.Strict."},
		},
		"additionalProperties": false,
	}
}

// rubricSpecSchema describes RubricSpec: a judge rubric expressed as data.
func rubricSpecSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "a judge rubric expressed as data.",
		"required":    []any{"name", "revision", "definition", "criteria"},
		"properties": map[string]any{
			"name":     map[string]any{"type": "string", "minLength": 1},
			"revision": map[string]any{"type": "string", "minLength": 1},
			"scope": map[string]any{
				"type":        "string",
				"enum":        []any{"", "case", "turn", "session", "run"},
				"description": "rubric scope; \"\" and \"case\" are equivalent.",
			},
			"definition": map[string]any{"type": "string", "minLength": 1, "description": "prose definition of the quality being judged."},
			"criteria": map[string]any{
				"type":        "array",
				"minItems":    1,
				"description": "the dimensions the judge weighs; must not be empty.",
				"items":       map[string]any{"$ref": "#/definitions/criterionSpec"},
			},
			"anchors": map[string]any{
				"type":        "array",
				"description": "labeled reference points on the rubric's overall score scale.",
				"items":       map[string]any{"$ref": "#/definitions/anchorSpec"},
			},
		},
		"additionalProperties": false,
	}
}

// criterionSpecSchema describes CriterionSpec.
func criterionSpecSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "one qualitative dimension a judge weighs when scoring a rubric.",
		"required":    []any{"id", "min-score", "max-score"},
		"properties": map[string]any{
			"id":          map[string]any{"type": "string", "minLength": 1},
			"description": map[string]any{"type": "string"},
			"min-score":   map[string]any{"type": "number", "description": "must be less than max-score."},
			"max-score":   map[string]any{"type": "number", "description": "must be greater than min-score."},
		},
		"additionalProperties": false,
	}
}

// anchorSpecSchema describes AnchorSpec.
func anchorSpecSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "a labeled reference point on the rubric's overall score scale.",
		"required":    []any{"score", "label"},
		"properties": map[string]any{
			"score":       map[string]any{"type": "number"},
			"label":       map[string]any{"type": "string", "minLength": 1},
			"description": map[string]any{"type": "string"},
		},
		"additionalProperties": false,
	}
}

// evaluatorSpecSchema builds the evaluators[] shape: a oneOf over one object
// schema per registered Kind, each assembled from that Kind's own
// OptionsSchema plus a "kind" const discriminator and Doc+Evidence as the
// entry's description.
func evaluatorSpecSchema(reg *Registry) (map[string]any, error) {
	kinds := reg.Kinds()
	oneOf := make([]any, 0, len(kinds))
	for _, k := range kinds {
		ks, err := evaluatorKindSchema(k)
		if err != nil {
			return nil, err
		}
		oneOf = append(oneOf, ks)
	}
	return map[string]any{
		"description": "one evaluator entry; \"kind\" selects the registered evaluator kind and its option shape.",
		"oneOf":       oneOf,
	}, nil
}

// evaluatorKindSchema turns one registry Kind into an object schema: k's own
// OptionsSchema properties/required/additionalProperties, plus a "kind" const
// property that every entry must also satisfy.
func evaluatorKindSchema(k Kind) (map[string]any, error) {
	var opts map[string]any
	if err := json.Unmarshal(k.OptionsSchema, &opts); err != nil {
		return nil, fmt.Errorf("evaluator kind %q: decode OptionsSchema: %w", k.Name, err)
	}

	properties := map[string]any{
		"kind": map[string]any{"type": "string", "const": k.Name},
	}
	if existing, ok := opts["properties"].(map[string]any); ok {
		for name, prop := range existing {
			properties[name] = prop
		}
	}

	required := []any{"kind"}
	if existing, ok := opts["required"].([]any); ok {
		required = append(required, existing...)
	}

	additionalProperties := any(false)
	if ap, ok := opts["additionalProperties"]; ok {
		additionalProperties = ap
	}

	return map[string]any{
		"type":                 "object",
		"description":          k.Doc + " (evidence: " + k.Evidence + ")",
		"required":             required,
		"properties":           properties,
		"additionalProperties": additionalProperties,
	}, nil
}

// runSpecSchema describes RunSpec: optional per-table eval.RunConfig
// defaults.
func runSpecSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "optional per-table eval.RunConfig defaults.",
		"properties": map[string]any{
			"trials":            map[string]any{"type": "integer"},
			"concurrency":       map[string]any{"type": "integer"},
			"target-timeout":    map[string]any{"type": "string", "description": "a Go duration string, e.g. \"30s\"."},
			"evaluator-timeout": map[string]any{"type": "string", "description": "a Go duration string, e.g. \"30s\"."},
		},
		"additionalProperties": false,
	}
}

// scenarioSpecSchema describes ScenarioSpec: one test case.
func scenarioSpecSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "one test case.",
		"required":    []any{"id", "input"},
		"properties": map[string]any{
			"id":   map[string]any{"type": "string", "minLength": 1},
			"name": map[string]any{"type": "string", "description": "optional; defaults to \"<pack>-<table>\"."},
			"input": map[string]any{
				"type":        "array",
				"minItems":    1,
				"description": "the input message thread; must not be empty.",
				"items":       map[string]any{"$ref": "#/definitions/messageSpec"},
			},
			"expect": map[string]any{"$ref": "#/definitions/expectSpec"},
			"labels": map[string]any{
				"type":                 "object",
				"description":          "free-form string labels.",
				"additionalProperties": map[string]any{"type": "string"},
			},
		},
		"additionalProperties": false,
	}
}

// messageSpecSchema describes MessageSpec: one input message. v1 supports
// text-only user/assistant turns.
func messageSpecSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "one input message; v1 supports text-only user/assistant turns.",
		"required":    []any{"role"},
		"properties": map[string]any{
			"role": map[string]any{"type": "string", "enum": []any{"user", "assistant"}},
			"text": map[string]any{"type": "string"},
		},
		"additionalProperties": false,
	}
}

// expectSpecSchema describes ExpectSpec: mirrors eval.Expectation
// field-for-field. Every field is independently optional.
func expectSpecSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "mirrors eval.Expectation field-for-field; every field is independently optional.",
		"properties": map[string]any{
			"required-facts": map[string]any{
				"type":        "array",
				"description": "statements a correct answer must establish or support.",
				"items":       map[string]any{"type": "string"},
			},
			"forbidden-actions": map[string]any{
				"type":        "array",
				"description": "actions a correct interaction must not take.",
				"items":       map[string]any{"type": "string"},
			},
			"expected-tool-calls": map[string]any{
				"type":  "array",
				"items": map[string]any{"$ref": "#/definitions/toolCallExpectSpec"},
			},
			"structured-output": map[string]any{"$ref": "#/definitions/structuredExpectSpec"},
			"reference-answers": map[string]any{
				"type":        "array",
				"description": "author-supplied golden answers for reference-based evaluators.",
				"items":       map[string]any{"type": "string"},
			},
			"policy-ref": map[string]any{"type": "string", "description": "references an external policy revision this scenario qualifies against."},
		},
		"additionalProperties": false,
	}
}

// toolCallExpectSpecSchema describes ToolCallExpectSpec.
func toolCallExpectSpecSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "asserts a named tool is invoked a bounded number of times.",
		"required":    []any{"tool"},
		"properties": map[string]any{
			"tool": map[string]any{"type": "string", "minLength": 1},
			"min":  map[string]any{"type": "integer", "minimum": 0, "description": "inclusive lower bound."},
			"max":  map[string]any{"type": []any{"integer", "null"}, "minimum": 0, "description": "inclusive upper bound; omitted leaves the count unbounded above."},
		},
		"additionalProperties": false,
	}
}

// structuredExpectSpecSchema describes StructuredExpectSpec.
func structuredExpectSpecSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "asserts the interaction produces a terminal structured output conforming to a named schema revision.",
		"required":    []any{"schema"},
		"properties": map[string]any{
			"schema": map[string]any{"type": "string", "minLength": 1, "description": "schema revision name (eval.Revision)."},
			"strict": map[string]any{"type": "boolean"},
		},
		"additionalProperties": false,
	}
}

// scriptSpecSchema describes ScriptSpec: mirrors qual/target.Script for
// offline fixture runs.
func scriptSpecSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "an offline fixture reply for one scenario ID (mirrors qual/target.Script).",
		"properties": map[string]any{
			"reply":    map[string]any{"type": "string"},
			"duration": map[string]any{"type": "string", "description": "a Go duration string, e.g. \"1.5s\"."},
			"tool-calls": map[string]any{
				"type":  "array",
				"items": map[string]any{"$ref": "#/definitions/scriptToolCall"},
			},
			"structured":     map[string]any{"$ref": "#/definitions/structuredSpec"},
			"structured-err": map[string]any{"$ref": "#/definitions/structuredErrSpec"},
		},
		"additionalProperties": false,
	}
}

// scriptToolCallSchema describes ScriptToolCall.
func scriptToolCallSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "one scripted tool call in a ScriptSpec.",
		"required":    []any{"name"},
		"properties": map[string]any{
			"name":     map[string]any{"type": "string", "minLength": 1},
			"id":       map[string]any{"type": "string"},
			"is-error": map[string]any{"type": "boolean"},
		},
		"additionalProperties": false,
	}
}

// structuredSpecSchema describes StructuredSpec.
func structuredSpecSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "the scripted structured-output success case.",
		"properties": map[string]any{
			"schema-name":     map[string]any{"type": "string"},
			"schema-revision": map[string]any{"type": "string"},
		},
		"additionalProperties": false,
	}
}

// structuredErrSpecSchema describes StructuredErrSpec.
func structuredErrSpecSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "the scripted structured-output failure case.",
		"properties": map[string]any{
			"schema": map[string]any{"type": "string"},
			"reason": map[string]any{"type": "string"},
		},
		"additionalProperties": false,
	}
}
