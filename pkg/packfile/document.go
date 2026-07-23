package packfile

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// MaxFileBytes bounds any single pack or table file.
const MaxFileBytes = 1 << 20 // 1 MiB

// PackFile mirrors pack.yaml: identity plus explicit, ordered table membership.
type PackFile struct {
	Pack     string   `yaml:"pack"`
	Revision string   `yaml:"revision"`
	Tables   []string `yaml:"tables"`
}

// TableFile mirrors one table YAML file.
type TableFile struct {
	Table       string                `yaml:"table"`
	Revision    string                `yaml:"revision"`
	Dimension   string                `yaml:"dimension"`
	Requires    []string              `yaml:"requires"`
	Environment *Environment          `yaml:"environment"`
	Rubrics     []RubricSpec          `yaml:"rubrics"`
	Evaluators  []EvaluatorSpec       `yaml:"evaluators"`
	Run         *RunSpec              `yaml:"run"`
	Scenarios   []ScenarioSpec        `yaml:"scenarios"`
	Script      map[string]ScriptSpec `yaml:"script"`
}

// Environment is the per-table stimulus applied to the target template.
type Environment struct {
	System       string            `yaml:"system"`
	Tools        []ToolSpec        `yaml:"tools"`
	ToolChoice   string            `yaml:"tool-choice"` // "", "auto", "required"
	OutputSchema *OutputSchemaSpec `yaml:"output-schema"`
}

// ToolSpec is one model-visible tool. Schema is arbitrary YAML converted to
// portable JSON Schema at build time (Task 4).
type ToolSpec struct {
	Name        string    `yaml:"name"`
	Description string    `yaml:"description"`
	Schema      yaml.Node `yaml:"schema"`
}

// OutputSchemaSpec is the structured-output contract for the table.
type OutputSchemaSpec struct {
	Name        string    `yaml:"name"`
	Description string    `yaml:"description"`
	Schema      yaml.Node `yaml:"schema"`
	Strict      bool      `yaml:"strict"`
}

// RubricSpec is a judge rubric expressed as data (design: "Custom packs").
type RubricSpec struct {
	Name       string          `yaml:"name"`
	Revision   string          `yaml:"revision"`
	Scope      string          `yaml:"scope"` // "", "case", "turn", "session", "run"
	Definition string          `yaml:"definition"`
	Criteria   []CriterionSpec `yaml:"criteria"`
	Anchors    []AnchorSpec    `yaml:"anchors"`
}

type CriterionSpec struct {
	ID          string  `yaml:"id"`
	Description string  `yaml:"description"`
	MinScore    float64 `yaml:"min-score"`
	MaxScore    float64 `yaml:"max-score"`
}

type AnchorSpec struct {
	Score       float64 `yaml:"score"`
	Label       string  `yaml:"label"`
	Description string  `yaml:"description"`
}

// EvaluatorSpec is a registry kind plus its raw options node; per-kind option
// structs are decoded strictly by the registry (Task 5).
type EvaluatorSpec struct {
	Kind    string
	Options yaml.Node
}

// UnmarshalYAML captures the kind and retains the full mapping node so the
// registry can strict-decode kind-specific options later.
func (e *EvaluatorSpec) UnmarshalYAML(node *yaml.Node) error {
	var probe struct {
		Kind string `yaml:"kind"`
	}
	if err := node.Decode(&probe); err != nil {
		return err
	}
	if probe.Kind == "" {
		return &Error{Path: "evaluators", Reason: "missing kind"}
	}
	e.Kind = probe.Kind
	e.Options = *node
	return nil
}

// RunSpec carries optional per-table eval.RunConfig defaults.
//
// RunSpec is decoded and schema-documented but not yet consumed by anything:
// pkg/qual.TablePlan has no Run field and pkg/run.Execute only takes one
// global eval.RunConfig from Spec.Config. Wiring per-table trials/
// concurrency/timeouts into execution is deferred; Document.Lint warns a
// pack author whose table sets a non-zero RunSpec that it currently has no
// effect (see isSet).
type RunSpec struct {
	Trials           int    `yaml:"trials"`
	Concurrency      int    `yaml:"concurrency"`
	TargetTimeout    string `yaml:"target-timeout"`    // Go duration string
	EvaluatorTimeout string `yaml:"evaluator-timeout"` // Go duration string
}

// isSet reports whether r is non-nil and the pack author set at least one
// field on it. A table's `run:` key can decode to a non-nil *RunSpec with
// every field at its zero value (an explicit `run: {}`), which isSet treats
// the same as no `run:` key at all (a nil *RunSpec): neither is something
// Lint should warn about, since neither declares any actual per-table
// override.
func (r *RunSpec) isSet() bool {
	return r != nil && (r.Trials != 0 || r.Concurrency != 0 || r.TargetTimeout != "" || r.EvaluatorTimeout != "")
}

// ScenarioSpec is one test case.
type ScenarioSpec struct {
	ID     string            `yaml:"id"`
	Name   string            `yaml:"name"` // optional; defaults to "<pack>-<table>"
	Input  []MessageSpec     `yaml:"input"`
	Expect *ExpectSpec       `yaml:"expect"`
	Labels map[string]string `yaml:"labels"`
}

// MessageSpec is one input message. v1 supports text-only user/assistant turns.
type MessageSpec struct {
	Role string `yaml:"role"` // "user" | "assistant"
	Text string `yaml:"text"`
}

// ExpectSpec mirrors eval.Expectation field-for-field.
type ExpectSpec struct {
	RequiredFacts     []string              `yaml:"required-facts"`
	ForbiddenActions  []string              `yaml:"forbidden-actions"`
	ExpectedToolCalls []ToolCallExpectSpec  `yaml:"expected-tool-calls"`
	StructuredOutput  *StructuredExpectSpec `yaml:"structured-output"`
	ReferenceAnswers  []string              `yaml:"reference-answers"`
	PolicyRef         string                `yaml:"policy-ref"`
}

type ToolCallExpectSpec struct {
	Tool string `yaml:"tool"`
	Min  int    `yaml:"min"`
	Max  *int   `yaml:"max"`
}

type StructuredExpectSpec struct {
	Schema string `yaml:"schema"`
	Strict bool   `yaml:"strict"`
}

// ScriptSpec mirrors qual/target.Script for offline fixture runs.
type ScriptSpec struct {
	Reply         string             `yaml:"reply"`
	Duration      string             `yaml:"duration"` // Go duration string
	ToolCalls     []ScriptToolCall   `yaml:"tool-calls"`
	Structured    *StructuredSpec    `yaml:"structured"`
	StructuredErr *StructuredErrSpec `yaml:"structured-err"`
}

type ScriptToolCall struct {
	Name    string `yaml:"name"`
	ID      string `yaml:"id"`
	IsError bool   `yaml:"is-error"`
}

type StructuredSpec struct {
	SchemaName     string `yaml:"schema-name"`
	SchemaRevision string `yaml:"schema-revision"`
}

type StructuredErrSpec struct {
	Schema string `yaml:"schema"`
	Reason string `yaml:"reason"`
}

// DecodeTable strictly decodes one table file. It does not validate semantics;
// Document validation happens at load (Task 6).
func DecodeTable(r io.Reader) (TableFile, error) {
	var tf TableFile
	if err := strictDecode(r, &tf); err != nil {
		return TableFile{}, err
	}
	return tf, nil
}

// DecodePack strictly decodes pack.yaml.
func DecodePack(r io.Reader) (PackFile, error) {
	var pf PackFile
	if err := strictDecode(r, &pf); err != nil {
		return PackFile{}, err
	}
	return pf, nil
}

// StrictDecode strictly decodes r into out: unknown YAML fields are rejected
// (yaml.v3's KnownFields(true)) and the input is bounded by MaxFileBytes. It
// is the one place packfile's strict-decode logic is implemented; every
// caller in this package, and pkg/run's manifest/profile codecs, route
// through it rather than re-implementing strict decoding. This confines
// decode logic to one implementation, not the gopkg.in/yaml.v3 import
// itself: pkg/gen also imports yaml.v3 directly, for comment-preserving
// yaml.Node surgery on the encode side, a different concern from decoding.
func StrictDecode(r io.Reader, out any) error {
	return strictDecode(r, out)
}

func strictDecode(r io.Reader, out any) error {
	data, err := boundedReadAll(r, MaxFileBytes)
	if err != nil {
		return &Error{Reason: err.Error(), Err: err}
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		return &Error{Reason: fmt.Sprintf("decode: %v", err), Err: err}
	}
	return nil
}

// boundedReadAll reads r fully, rejecting input beyond limit bytes. It never
// buffers more than limit+1 bytes: one byte past the limit is read (if
// present) solely to detect the overflow, then discarded. This is the single
// place packfile bounds an io.Reader's size, shared by strictDecode's YAML
// decode path and load.go's raw pack/table byte reads.
func boundedReadAll(r io.Reader, limit int64) ([]byte, error) {
	lr := &io.LimitedReader{R: r, N: limit + 1}
	data, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("file exceeds MaxFileBytes")
	}
	return data, nil
}
