package packfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/looprig/eval"
	"github.com/looprig/eval/exact"
	"github.com/looprig/eval/judge"
	"github.com/looprig/eval/rubric"
	"github.com/looprig/inference"
	"gopkg.in/yaml.v3"
)

// BuildContext supplies cross-cutting inputs a kind may need at build time.
// Zero value is valid for all programmatic kinds.
type BuildContext struct {
	Rubrics       map[string]rubric.Rubric // resolved from RubricSpec at load (Task 6)
	JudgeClient   inference.Client         // nil ⇒ judge kinds fail with ErrJudgeUnconfigured
	JudgeTemplate inference.Request        // model + defaults for judge calls
}

// ErrJudgeUnconfigured is returned when a pack needs a judge and no judge
// client was supplied. run surfaces it at preflight, before any paid call.
var ErrJudgeUnconfigured = errors.New("packfile: judge evaluator needs a judge client (--config llm block)")

// Kind is one registry entry. Doc and Evidence are DATA: they feed
// schema.json, `mpqt evaluators`, and the gen prompt (design: "Evaluator
// registry and discoverability").
type Kind struct {
	Name          string
	Doc           string
	Evidence      string          // e.g. "tool-operation evidence; Unverified when a scenario makes no tool calls"
	OptionsSchema json.RawMessage // JSON-Schema fragment for this kind's options
	Build         func(opts *yaml.Node, bc BuildContext) (eval.Evaluator, error)
}

// Registry holds the known evaluator kinds and builds live eval.Evaluator
// values from EvaluatorSpec + BuildContext.
type Registry struct{ kinds map[string]Kind }

// NewRegistry returns a Registry with every built-in kind registered. It
// panics if the built-in kind list itself contains a duplicate or empty
// name -- a programming error that must never reach a released binary.
func NewRegistry() *Registry {
	r := &Registry{kinds: make(map[string]Kind, len(builtinKinds))}
	for _, k := range builtinKinds {
		if err := r.Register(k); err != nil {
			panic("packfile: built-in evaluator kind registration: " + err.Error())
		}
	}
	return r
}

// Register adds k to the registry. It rejects an empty Name and a Name that
// collides with an already-registered kind.
func (r *Registry) Register(k Kind) error {
	if k.Name == "" {
		return &Error{Path: "evaluators/kind", Reason: "kind name must not be empty"}
	}
	if _, exists := r.kinds[k.Name]; exists {
		return &Error{Path: "evaluators/kind", Reason: "duplicate evaluator kind: " + k.Name}
	}
	r.kinds[k.Name] = k
	return nil
}

// Kinds returns every registered Kind, sorted by name.
func (r *Registry) Kinds() []Kind {
	out := make([]Kind, 0, len(r.kinds))
	for _, k := range r.kinds {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Build resolves spec.Kind to a registered Kind and builds a live
// eval.Evaluator from spec.Options and bc. An unknown kind is rejected with
// the sorted list of known kinds in the error's Reason.
func (r *Registry) Build(spec EvaluatorSpec, bc BuildContext) (eval.Evaluator, error) {
	k, ok := r.kinds[spec.Kind]
	if !ok {
		return nil, &Error{
			Path:   "evaluators/kind",
			Reason: "unknown evaluator kind " + spec.Kind + ": known kinds: " + strings.Join(r.kindNames(), ", "),
		}
	}
	return k.Build(&spec.Options, bc)
}

// kindNames returns every registered kind name, sorted.
func (r *Registry) kindNames() []string {
	names := make([]string, 0, len(r.kinds))
	for name := range r.kinds {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// decodeOptions strictly decodes an EvaluatorSpec's options mapping node into
// out: it drops the "kind" field (which belongs to the registry, not any
// per-kind option struct), re-encodes the remaining mapping as YAML, and
// strict-decodes it into out so an unrecognized option key is rejected the
// same way the rest of the packfile boundary rejects unknown fields.
func decodeOptions(node *yaml.Node, out any) error {
	if node == nil || node.Kind != yaml.MappingNode {
		return &Error{Path: "evaluators", Reason: "evaluator options must be a mapping"}
	}
	filtered := yaml.Node{Kind: node.Kind, Tag: node.Tag, Style: node.Style}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i]
		if key.Value == "kind" {
			continue
		}
		filtered.Content = append(filtered.Content, key, node.Content[i+1])
	}
	data, err := yaml.Marshal(&filtered)
	if err != nil {
		return &Error{Path: "evaluators", Reason: "encode options: " + err.Error(), Err: err}
	}
	return strictDecode(bytes.NewReader(data), out)
}

// --- built-in kinds ---

// builtinKinds is the fixed set of evaluator kinds NewRegistry registers.
var builtinKinds = []Kind{
	{
		Name:          "required-text",
		Doc:           "asserts every required substring appears in the assistant's text output",
		Evidence:      "none required; a vacuous (no substrings) configuration errors rather than passing",
		OptionsSchema: json.RawMessage(`{"type":"object","required":["substrings"],"properties":{"substrings":{"type":"array","items":{"type":"string"},"minItems":1}},"additionalProperties":false}`),
		Build:         buildRequiredText,
	},
	{
		Name:          "forbidden-text",
		Doc:           "asserts no forbidden substring appears in the assistant's text output",
		Evidence:      "none required; a vacuous (no substrings) configuration errors rather than passing",
		OptionsSchema: json.RawMessage(`{"type":"object","required":["substrings"],"properties":{"substrings":{"type":"array","items":{"type":"string"},"minItems":1}},"additionalProperties":false}`),
		Build:         buildForbiddenText,
	},
	{
		Name:          "required-tool",
		Doc:           "asserts a tool call with the given name was made",
		Evidence:      "none required beyond the conversation trace itself",
		OptionsSchema: json.RawMessage(`{"type":"object","required":["tool"],"properties":{"tool":{"type":"string","minLength":1}},"additionalProperties":false}`),
		Build:         buildRequiredTool,
	},
	{
		Name:          "forbidden-tool",
		Doc:           "asserts a tool call with the given name was not made",
		Evidence:      "none required beyond the conversation trace itself",
		OptionsSchema: json.RawMessage(`{"type":"object","required":["tool"],"properties":{"tool":{"type":"string","minLength":1}},"additionalProperties":false}`),
		Build:         buildForbiddenTool,
	},
	{
		Name:          "tool-error-rate",
		Doc:           "measures the proportion of tool operations that errored, optionally failing above a threshold",
		Evidence:      "tool-operation evidence; Unverified when a scenario makes no tool calls",
		OptionsSchema: json.RawMessage(`{"type":"object","properties":{"max-error-rate":{"type":"number","minimum":0,"maximum":1}},"additionalProperties":false}`),
		Build:         buildToolErrorRate,
	},
	{
		Name:          "max-duration",
		Doc:           "fails when the longest recorded timed span exceeds a limit",
		Evidence:      "timing evidence; Unverified when a scenario carries no recorded timing",
		OptionsSchema: json.RawMessage(`{"type":"object","required":["limit"],"properties":{"limit":{"type":"string"}},"additionalProperties":false}`),
		Build:         buildMaxDuration,
	},
	{
		Name:          "schema-result",
		Doc:           "reports whether the subject's structured output satisfied its schema",
		Evidence:      "structured-output evidence; Unverified when a scenario produced no structured output",
		OptionsSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
		Build:         buildSchemaResult,
	},
	{
		Name:          "judge",
		Doc:           "scores the sample's conversation against a named rubric using a model judge",
		Evidence:      "model usage evidence recorded by the judge call itself",
		OptionsSchema: json.RawMessage(`{"type":"object","required":["rubric"],"properties":{"rubric":{"type":"string","minLength":1}},"additionalProperties":false}`),
		Build:         buildJudge,
	},
}

type textOptions struct {
	Substrings []string `yaml:"substrings"`
}

func buildRequiredText(opts *yaml.Node, _ BuildContext) (eval.Evaluator, error) {
	var o textOptions
	if err := decodeOptions(opts, &o); err != nil {
		return nil, err
	}
	if len(o.Substrings) == 0 {
		return nil, &Error{Path: "evaluators/required-text", Reason: "substrings must not be empty"}
	}
	return exact.RequiredText(o.Substrings...), nil
}

func buildForbiddenText(opts *yaml.Node, _ BuildContext) (eval.Evaluator, error) {
	var o textOptions
	if err := decodeOptions(opts, &o); err != nil {
		return nil, err
	}
	if len(o.Substrings) == 0 {
		return nil, &Error{Path: "evaluators/forbidden-text", Reason: "substrings must not be empty"}
	}
	return exact.ForbiddenText(o.Substrings...), nil
}

type toolOptions struct {
	Tool string `yaml:"tool"`
}

func buildRequiredTool(opts *yaml.Node, _ BuildContext) (eval.Evaluator, error) {
	var o toolOptions
	if err := decodeOptions(opts, &o); err != nil {
		return nil, err
	}
	if o.Tool == "" {
		return nil, &Error{Path: "evaluators/required-tool", Reason: "tool must not be empty"}
	}
	return exact.RequiredTool(o.Tool), nil
}

func buildForbiddenTool(opts *yaml.Node, _ BuildContext) (eval.Evaluator, error) {
	var o toolOptions
	if err := decodeOptions(opts, &o); err != nil {
		return nil, err
	}
	if o.Tool == "" {
		return nil, &Error{Path: "evaluators/forbidden-tool", Reason: "tool must not be empty"}
	}
	return exact.ForbiddenTool(o.Tool), nil
}

type toolErrorRateOptions struct {
	MaxErrorRate *float64 `yaml:"max-error-rate"`
}

func buildToolErrorRate(opts *yaml.Node, _ BuildContext) (eval.Evaluator, error) {
	var o toolErrorRateOptions
	if err := decodeOptions(opts, &o); err != nil {
		return nil, err
	}
	if o.MaxErrorRate == nil {
		return exact.ToolErrorRate(), nil
	}
	r := *o.MaxErrorRate
	if math.IsNaN(r) || r < 0 || r > 1 {
		return nil, &Error{Path: "evaluators/tool-error-rate", Reason: "max-error-rate must be within [0,1]"}
	}
	return exact.ToolErrorRate(exact.MaxErrorRate(r)), nil
}

type maxDurationOptions struct {
	Limit string `yaml:"limit"`
}

func buildMaxDuration(opts *yaml.Node, _ BuildContext) (eval.Evaluator, error) {
	var o maxDurationOptions
	if err := decodeOptions(opts, &o); err != nil {
		return nil, err
	}
	if o.Limit == "" {
		return nil, &Error{Path: "evaluators/max-duration", Reason: "limit must not be empty"}
	}
	d, err := time.ParseDuration(o.Limit)
	if err != nil {
		return nil, &Error{Path: "evaluators/max-duration", Reason: "invalid limit duration: " + err.Error(), Err: err}
	}
	if d <= 0 {
		return nil, &Error{Path: "evaluators/max-duration", Reason: "limit must be positive"}
	}
	return exact.MaxDuration(d), nil
}

func buildSchemaResult(opts *yaml.Node, _ BuildContext) (eval.Evaluator, error) {
	var o struct{}
	if err := decodeOptions(opts, &o); err != nil {
		return nil, err
	}
	return exact.SchemaResult(), nil
}

type judgeOptions struct {
	Rubric string `yaml:"rubric"`
}

func buildJudge(opts *yaml.Node, bc BuildContext) (eval.Evaluator, error) {
	var o judgeOptions
	if err := decodeOptions(opts, &o); err != nil {
		return nil, err
	}
	if o.Rubric == "" {
		return nil, &Error{Path: "evaluators/judge", Reason: "rubric must not be empty"}
	}
	if bc.JudgeClient == nil {
		return nil, ErrJudgeUnconfigured
	}
	rb, ok := bc.Rubrics[o.Rubric]
	if !ok {
		return nil, &Error{Path: "evaluators/judge", Reason: "unknown rubric: " + o.Rubric}
	}
	return judge.New(rb, bc.JudgeClient, bc.JudgeTemplate), nil
}

// Rubric converts a RubricSpec into a rubric.Rubric. An empty Scope defaults
// to eval.ScopeCase (RubricSpec.Scope's own doc comment lists "" alongside
// "case"); any other unrecognized scope string is rejected.
func (rs RubricSpec) Rubric() (rubric.Rubric, error) {
	scope, err := scopeFromSpec(rs.Scope)
	if err != nil {
		return rubric.Rubric{}, err
	}
	criteria := make([]rubric.Criterion, 0, len(rs.Criteria))
	for _, c := range rs.Criteria {
		criteria = append(criteria, rubric.Criterion{
			ID:          eval.Name(c.ID),
			Description: c.Description,
			MinScore:    c.MinScore,
			MaxScore:    c.MaxScore,
		})
	}
	anchors := make([]rubric.Anchor, 0, len(rs.Anchors))
	for _, a := range rs.Anchors {
		anchors = append(anchors, rubric.Anchor{
			Score:       a.Score,
			Label:       eval.Name(a.Label),
			Description: a.Description,
		})
	}
	return rubric.Rubric{
		Name:       eval.Name(rs.Name),
		Revision:   eval.Revision(rs.Revision),
		Scope:      scope,
		Definition: rs.Definition,
		Criteria:   criteria,
		Anchors:    anchors,
	}, nil
}

// scopeFromSpec maps a RubricSpec.Scope string onto eval.Scope. "" and "case"
// are equivalent (eval.ScopeCase is the zero-value default); anything else
// besides "turn", "session", and "run" is rejected rather than silently
// defaulted.
func scopeFromSpec(spec string) (eval.Scope, error) {
	switch spec {
	case "", "case":
		return eval.ScopeCase, nil
	case "turn":
		return eval.ScopeTurn, nil
	case "session":
		return eval.ScopeSession, nil
	case "run":
		return eval.ScopeRun, nil
	default:
		return 0, &Error{Path: "rubrics/scope", Reason: "unknown scope: " + spec}
	}
}
