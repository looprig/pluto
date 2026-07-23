package packfile

import (
	"github.com/looprig/inference"
)

// Template converts an Environment into the provider-neutral inference.Request
// template later merged with a manifest's model (pkg/run, Task 9) to build a
// live target. Every tool schema and the output schema (if any) are converted
// from arbitrary pack-author YAML to canonical JSON and validated through
// inference.ValidateOutputSchema against the bounded portable JSON Schema
// subset shared by provider codecs -- the design's "portable subset at lint
// time" enforcement point. A nil receiver is legal (tables without
// environments exist) and yields a zero Request.
func (e *Environment) Template() (inference.Request, error) {
	if e == nil {
		return inference.Request{}, nil
	}

	var req inference.Request
	req.System = e.System

	if len(e.Tools) > 0 {
		req.Tools = make([]inference.Tool, 0, len(e.Tools))
		for i := range e.Tools {
			ts := &e.Tools[i]
			raw, err := jsonFromNode(&ts.Schema)
			if err != nil {
				return inference.Request{}, wrapEnvironmentErr("environment/tools/"+ts.Name, err)
			}
			if err := inference.ValidateOutputSchema(inference.OutputSchema{
				Name:   ts.Name,
				Schema: raw,
				Strict: true,
			}); err != nil {
				return inference.Request{}, wrapEnvironmentErr("environment/tools/"+ts.Name, err)
			}
			req.Tools = append(req.Tools, inference.Tool{
				Name:        ts.Name,
				Description: ts.Description,
				Schema:      raw,
			})
		}
	}

	toolChoice, err := toolChoiceFromSpec(e.ToolChoice)
	if err != nil {
		return inference.Request{}, err
	}
	req.ToolChoice = toolChoice

	if e.OutputSchema != nil {
		out, err := outputSchemaFromSpec(e.OutputSchema)
		if err != nil {
			return inference.Request{}, err
		}
		req.Output = out
	}

	return req, nil
}

// toolChoiceFromSpec maps the pack's tool-choice string onto
// inference.ToolChoice. "" and "auto" are equivalent; anything else besides
// "required" is rejected rather than silently defaulted.
func toolChoiceFromSpec(spec string) (inference.ToolChoice, error) {
	switch spec {
	case "", "auto":
		return inference.ToolChoiceAuto, nil
	case "required":
		return inference.ToolChoiceRequired, nil
	default:
		return 0, &Error{Path: "environment/tool-choice", Reason: "unknown tool choice: " + spec}
	}
}

// outputSchemaFromSpec converts and validates the table's structured-output
// contract the same way tool schemas are converted and validated.
func outputSchemaFromSpec(spec *OutputSchemaSpec) (*inference.OutputSchema, error) {
	raw, err := jsonFromNode(&spec.Schema)
	if err != nil {
		return nil, wrapEnvironmentErr("environment/output-schema", err)
	}
	out := inference.OutputSchema{
		Name:        spec.Name,
		Description: spec.Description,
		Schema:      raw,
		Strict:      spec.Strict,
	}
	if err := inference.ValidateOutputSchema(out); err != nil {
		return nil, wrapEnvironmentErr("environment/output-schema", err)
	}
	return &out, nil
}

// wrapEnvironmentErr wraps err in a *Error naming the environment path,
// unless err is already a *Error (in which case it is returned as-is to
// avoid double-wrapping).
func wrapEnvironmentErr(path string, err error) error {
	if _, ok := err.(*Error); ok {
		return err
	}
	return &Error{Path: path, Reason: err.Error()}
}
