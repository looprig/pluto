// Package gen generates candidate scenarios for one table via a single
// structured-output call, then validates, dedupes, and appends them.
// It accepts an inference.Client and never constructs one (dependency
// confinement, matching pkg/run's own BuildTarget).
package gen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/looprig/core/content"
	"github.com/looprig/inference"
	"github.com/looprig/inference/model"
	"github.com/looprig/pluto/pkg/packfile"
)

// Bounds on Request.N: generating zero candidates is pointless and an
// unbounded N risks an unreviewable batch and an unbounded paid call.
const (
	minN = 1
	maxN = 50
)

// outputName is the OutputSchema.Name sent on the generation call.
const outputName = "scenario-batch"

// Request selects the table and steers generation.
type Request struct {
	Doc    *packfile.Document
	Table  string // table name within Doc
	N      int    // 1..50
	Focus  string // optional steer for seeded generation
	Intent string // bootstrap mode: required when the table has no scenarios
	Model  model.Model
}

// Result reports exactly what happened; nothing is dropped silently.
type Result struct {
	Accepted  []packfile.ScenarioSpec
	Rejected  []Rejection // failed Validate/lint or duplicate ID
	InputText string      // the prompt, for --no-write inspection
}

// Rejection names one candidate that did not make it into Result.Accepted and
// why, so a caller (and a human reviewing --no-write output) can see exactly
// what was dropped and reason about it -- never a silent drop.
type Rejection struct {
	ID     string
	Reason string
}

// batchSchema is the structured-output contract: {"scenarios": [ScenarioSpec-shaped objects]}.
// Keep it in sync with packfile.ScenarioSpec -- the decode test enforces it.
const batchSchema = `{
  "type": "object",
  "properties": {
    "scenarios": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "id": {"type": "string"},
          "input": {"type": "array", "items": {"type": "object", "properties": {"role": {"type": "string"}, "text": {"type": "string"}}, "required": ["role", "text"]}},
          "expect": {"type": "object", "properties": {"required-facts": {"type": "array", "items": {"type": "string"}}, "forbidden-actions": {"type": "array", "items": {"type": "string"}}, "expected-tool-calls": {"type": "array", "items": {"type": "object", "properties": {"tool": {"type": "string"}, "min": {"type": "integer"}, "max": {"type": "integer"}}, "required": ["tool", "min"]}}, "reference-answers": {"type": "array", "items": {"type": "string"}}}},
          "labels": {"type": "object"}
        },
        "required": ["id", "input"]
      }
    }
  },
  "required": ["scenarios"]
}`

// scenarioBatchDTO mirrors batchSchema for decoding the model's structured
// output. It is a wire DTO, not a domain type: Generate converts each entry
// to a packfile.ScenarioSpec before validating it.
type scenarioBatchDTO struct {
	Scenarios []scenarioDTO `json:"scenarios"`
}

type scenarioDTO struct {
	ID     string            `json:"id"`
	Input  []messageDTO      `json:"input"`
	Expect *expectDTO        `json:"expect"`
	Labels map[string]string `json:"labels"`
}

type messageDTO struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type expectDTO struct {
	RequiredFacts     []string      `json:"required-facts"`
	ForbiddenActions  []string      `json:"forbidden-actions"`
	ExpectedToolCalls []toolCallDTO `json:"expected-tool-calls"`
	ReferenceAnswers  []string      `json:"reference-answers"`
}

type toolCallDTO struct {
	Tool string `json:"tool"`
	Min  int    `json:"min"`
	Max  *int   `json:"max"`
}

// scenarioSpec converts the wire DTO into the domain packfile.ScenarioSpec.
// Name is left empty (Scenario defaults it) and Revision is not this type's
// concern -- both are supplied by ScenarioSpec.Scenario at validation time.
func (d scenarioDTO) scenarioSpec() packfile.ScenarioSpec {
	spec := packfile.ScenarioSpec{
		ID:     d.ID,
		Labels: d.Labels,
	}
	for _, m := range d.Input {
		spec.Input = append(spec.Input, packfile.MessageSpec{Role: m.Role, Text: m.Text})
	}
	if d.Expect != nil {
		expect := &packfile.ExpectSpec{
			RequiredFacts:    d.Expect.RequiredFacts,
			ForbiddenActions: d.Expect.ForbiddenActions,
			ReferenceAnswers: d.Expect.ReferenceAnswers,
		}
		for _, tc := range d.Expect.ExpectedToolCalls {
			expect.ExpectedToolCalls = append(expect.ExpectedToolCalls, packfile.ToolCallExpectSpec{
				Tool: tc.Tool,
				Min:  tc.Min,
				Max:  tc.Max,
			})
		}
		spec.Expect = expect
	}
	return spec
}

// Generate builds one generation prompt for req.Table, sends it to client as
// a single structured-output call, and converts the model's batch response
// into validated packfile.ScenarioSpecs. Every candidate is accounted for:
// a candidate whose ID duplicates an existing scenario or another candidate
// in the same batch, or that fails packfile.ScenarioSpec.Scenario validation,
// lands in Result.Rejected with a Reason rather than being dropped, while
// every other candidate still lands in Result.Accepted (partial success, not
// all-or-nothing).
//
// Generate validates req.N and locates req.Table before doing anything else,
// so a caller mistake is rejected before any paid call.
func Generate(ctx context.Context, client inference.Client, req Request) (Result, error) {
	if req.N < minN || req.N > maxN {
		return Result{}, fmt.Errorf("gen: N must be between %d and %d, got %d", minN, maxN, req.N)
	}
	if req.Doc == nil {
		return Result{}, errors.New("gen: Request.Doc must not be nil")
	}
	if client == nil {
		return Result{}, errors.New("gen: client must not be nil")
	}

	tf, err := findTable(req.Doc, req.Table)
	if err != nil {
		return Result{}, err
	}

	if len(tf.Scenarios) == 0 && req.Intent == "" {
		return Result{}, fmt.Errorf("gen: table %q has no existing scenarios; Request.Intent is required to bootstrap generation", req.Table)
	}

	// NewRegistry always returns the built-in evaluator registry: Generate
	// does not yet accept a caller-supplied *packfile.Registry, so a
	// consumer's custom evaluator kinds (if registered elsewhere, e.g. by a
	// future CLI composition root) will not appear as evidence in the prompt.
	reg := packfile.NewRegistry()
	prompt, err := buildPrompt(req.Doc, tf, reg, req)
	if err != nil {
		return Result{}, fmt.Errorf("gen: build prompt: %w", err)
	}

	infReq := inference.Request{
		Model: req.Model,
		Messages: content.AgenticMessages{
			&content.UserMessage{Message: content.Message{
				Role:   content.RoleUser,
				Blocks: []content.Block{&content.TextBlock{Text: prompt}},
			}},
		},
		Output: &inference.OutputSchema{
			Name:   outputName,
			Schema: json.RawMessage(batchSchema),
			Strict: true,
		},
	}

	resp, err := client.Invoke(ctx, infReq)
	if err != nil {
		return Result{InputText: prompt}, fmt.Errorf("gen: invoke: %w", err)
	}

	raw, err := inference.StructuredResult(resp)
	if err != nil {
		return Result{InputText: prompt}, fmt.Errorf("gen: structured result: %w", err)
	}

	var batch scenarioBatchDTO
	if err := json.Unmarshal(raw, &batch); err != nil {
		return Result{InputText: prompt}, fmt.Errorf("gen: decode scenario batch: %w", err)
	}

	result := Result{InputText: prompt}
	postPass(&result, req.Doc, tf, batch)
	return result, nil
}

// postPass is the mechanical post-pass: dedupe IDs against the table's
// existing scenarios and against other candidates already accepted from this
// same batch, then run packfile.ScenarioSpec.Scenario validation. Every
// candidate lands in exactly one of result.Accepted or result.Rejected.
func postPass(result *Result, doc *packfile.Document, tf packfile.TableFile, batch scenarioBatchDTO) {
	existing := make(map[string]bool, len(tf.Scenarios))
	for _, sc := range tf.Scenarios {
		existing[sc.ID] = true
	}
	seen := make(map[string]bool, len(batch.Scenarios))

	defaultName := doc.Pack.Pack + "-" + tf.Table

	for _, dto := range batch.Scenarios {
		spec := dto.scenarioSpec()

		if existing[spec.ID] {
			result.Rejected = append(result.Rejected, Rejection{ID: spec.ID, Reason: "duplicate of existing scenario ID"})
			continue
		}
		if seen[spec.ID] {
			result.Rejected = append(result.Rejected, Rejection{ID: spec.ID, Reason: "duplicate scenario ID within generated batch"})
			continue
		}
		seen[spec.ID] = true

		if _, err := spec.Scenario(defaultName, tf.Revision); err != nil {
			result.Rejected = append(result.Rejected, Rejection{ID: spec.ID, Reason: rejectionReason(err)})
			continue
		}
		result.Accepted = append(result.Accepted, spec)
	}
}

// rejectionReason extracts a flat, ID-free reason string from a
// ScenarioSpec.Scenario validation error for Rejection.Reason. That call
// wraps validation failures in a *packfile.Error whose Reason field already
// holds the flat human-readable message (unlike Error(), which prepends a
// "packfile: <id>: " prefix and would otherwise make Rejection.Reason
// redundantly repeat Rejection.ID); using Reason directly keeps this
// Reason's shape consistent with the other two hand-written duplicate-
// rejection reasons above. Any other error type (not expected here) falls
// back to err.Error().
func rejectionReason(err error) string {
	var perr *packfile.Error
	if errors.As(err, &perr) {
		return perr.Reason
	}
	return err.Error()
}

// findTable locates the table named name within doc, in doc.Tables order.
func findTable(doc *packfile.Document, name string) (packfile.TableFile, error) {
	for _, tf := range doc.Tables {
		if tf.Table == name {
			return tf, nil
		}
	}
	return packfile.TableFile{}, fmt.Errorf("gen: table %q not found in pack %q", name, doc.Pack.Pack)
}
