# MPQT Phase 2 — Data-Driven Packs, Generation, CLI — Detailed Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Execute the Phase 2 design (`docs/2026-07-23-phase2-packfiles-generation-cli-design.md`): restructure the module under `pkg/`, replace the five Go packs with a strict YAML corpus, add the evaluator registry + JSON Schema, LLM scenario generation, pricing preflight, and the `mpqt` CLI as a nested module.

**Architecture:** The root module gains `pkg/packfile` (strict YAML trust boundary → `qual.Pack`), `pkg/run` (target construction + execution, extracted from `mpqttest`), `pkg/pricing`, `pkg/gen`, and `pkg/cli` — all of which accept `inference.Client` values and never construct them. A new nested module `cmd/mpqt` is the only importer of `github.com/looprig/llm`. The YAML corpus in `packs/` becomes the single source of pack content after a golden equivalence migration.

**Tech Stack:** Go (stdlib + `gopkg.in/yaml.v3` in `pkg/packfile` only), `github.com/looprig/eval`, `github.com/looprig/inference` (already required via eval), `github.com/looprig/llm` (CLI module only).

---

## Ground rules (read before every task)

These are verified against the live modules on 2026-07-23. Do not "fix" them.

1. **Sealed content interfaces need POINTER literals.**
   `content.AgenticMessages{&content.UserMessage{Message: content.Message{Role: content.RoleUser, Blocks: []content.Block{&content.TextBlock{Text: "..."}}}}}` — value literals do not compile.
2. **`eval.Sample.Validate` requires `Observation.Subject.Revision == Scenario.Revision`.** Live targets must be built with `inferenceeval.WithRevision(rev)`; the scripted fixture stamps it automatically.
3. **One evaluator set per table.** `eval.Run(ctx, cfg, suite, target, evaluators...)` applies every evaluator to every scenario in the suite. Exact evaluators are constructor-parameterized and never read `Scenario.Expectation`.
4. **`exact.ToolErrorRate` yields Unverified (never Pass) when a scenario makes no tool calls.** Never wire it into a table whose scenarios legitimately call no tools (see the comment in the current `packs/tooluse/v1.go`).
5. **Verified signatures** (do not re-derive, do not invent alternatives):
   - `inference.Client` interface: `Invoke(ctx, inference.Request) (*inference.Response, error)` and `Stream(...)`.
   - Structured output: set `Request.Output = &inference.OutputSchema{Name, Description, Schema json.RawMessage, Strict bool}`; extract with `inference.StructuredResult(resp) (json.RawMessage, error)`.
   - `inference.Request{Model model.Model, System string, Messages content.AgenticMessages, Tools []inference.Tool, Output *inference.OutputSchema, ToolChoice inference.ToolChoice, Override *model.Sampling}`; `inference.Tool{Name, Description string, Schema json.RawMessage}`; `inference.ToolChoiceAuto` / `inference.ToolChoiceRequired`.
   - `inference.ValidateOutputSchema(inference.OutputSchema) error` — the portable-subset schema validator.
   - `model.Model{Provider model.ProviderName, APIFormat model.APIFormat, BaseURL string, Name string, Origin model.Origin, Caps model.Capabilities, Limits model.ContextLimits, Sampling model.Sampling}`.
   - `auto.New(selected model.Model, key auth.APIKey) (inference.Client, error)` and `auto.NewCounter(model.Model, auth.APIKey) (contextcount.ContextCounter, error)` in `github.com/looprig/llm/auto`; `auth.APIKey` is `github.com/looprig/llm/auth`. CLI MODULE ONLY.
   - `contextcount.ContextCounter`: `CountContext(context.Context, inference.Request) (contextcount.ContextCount, error)`; `CounterCapability() contextcount.CounterCapability`.
   - `judge.New(r rubric.Rubric, client inference.Client, template inference.Request, opts ...judge.Option) eval.Evaluator`.
   - `rubric.Rubric{Name eval.Name, Revision eval.Revision, Scope eval.Scope, Definition string, Criteria []rubric.Criterion, Anchors []rubric.Anchor}`; `rubric.Criterion{ID eval.Name, Description string, MinScore, MaxScore float64}` (MinScore < MaxScore); `rubric.Anchor{Score float64, Label eval.Name, Description string}`.
   - `exact` constructors: `RequiredText(substrings ...string)`, `ForbiddenText(substrings ...string)`, `RequiredTool(name string)`, `ForbiddenTool(name string)`, `NoToolCall(name string)`, `ToolErrorRate(opts ...exact.RateOption)` with `exact.MaxErrorRate(r float64)`, `MaxDuration(limit time.Duration)`, `SchemaResult()`.
   - `eval.RunConfig{Trials, Concurrency int, TargetTimeout, EvaluatorTimeout time.Duration}` (Trials ≤ 1000; no seed field exists).
   - Scripted fixture (`fixture/target`, becomes `pkg/qual/target` — see Task 1): `target.Script{Reply string, Duration time.Duration, ToolCalls []target.ToolCall, Structured *target.Structured, StructuredErr *target.StructuredErr, Err error}`; `target.ToolCall{Name eval.Name, ID string, IsError bool}`; `target.Structured{SchemaName eval.Name, SchemaRevision eval.Revision}`; `target.NewScripted(name string, scripts map[string]target.Script) *target.Scripted`.
6. **yaml.v3 strictness:** always decode via `yaml.NewDecoder(r)` with `dec.KnownFields(true)`. Naked `yaml.Unmarshal` silently ignores unknown fields — never use it for pack input.
7. **Gates per task:** `CGO_ENABLED=0 go build -trimpath ./...`, `go vet ./...`, `go test -race ./...`, then `make secure` before each commit. After ANY root `go.mod` change, additionally run the nested-module gate(s): `cd <nested> && GOWORK=off go mod tidy && GOWORK=off go test -race ./...` for every nested module that exists at that point (`examples/qualification` until Task 8; `cmd/mpqt` from Task 12).
8. **Commits:** imperative subject in repo style (`feat: …`, `refactor: …`, `test: …`, `docs: …`, `chore: …`). No trailers of any kind.
9. **Never weaken a failing assertion to make a test pass.** If reality disagrees with this plan, stop and report the discrepancy instead of improvising.
10. All work happens in `/Users/ipotter/code/looprig/mpqt` unless a path says otherwise. `GOWORK=off` on every go command.

---

## Task 1: Restructure under pkg/ and rename the core package to qual

**Files:**
- Move: every root `*.go` → `pkg/qual/` (package `mpqt` → package `qual`)
- Move: `profile/` → `pkg/profile/`, `compare/` → `pkg/compare/`, `reportjson/` → `pkg/reportjson/`, `mpqttest/` → `pkg/mpqttest/`, `fixture/target/` → `pkg/qual/target/`
- Keep: `internal/reporttest/` where it is
- Modify: every import of `github.com/looprig/mpqt` → `github.com/looprig/mpqt/pkg/qual` (identifier `mpqt.` → `qual.`), and the other moved paths accordingly
- Modify: `examples/qualification/*.go` imports the same way

**Step 1: Move the packages**

```bash
mkdir -p pkg/qual
git mv doc.go manifest.go manifest_test.go pack.go pack_test.go scorecard.go scorecard_test.go stats.go stats_test.go pkg/qual/
git mv profile pkg/profile
git mv compare pkg/compare
git mv reportjson pkg/reportjson
git mv mpqttest pkg/mpqttest
mkdir -p pkg/qual/target && git mv fixture/target/*.go pkg/qual/target/ && rmdir fixture/target fixture
```

**Step 2: Rename package and rewrite imports**

In `pkg/qual/*.go`: change `package mpqt` → `package qual` (and `package mpqt` in `_test.go` files → `package qual`). Then rewrite imports across the repo:

```bash
grep -rl 'github.com/looprig/mpqt"' --include='*.go' . | xargs sed -i '' 's#"github.com/looprig/mpqt"#"github.com/looprig/mpqt/pkg/qual"#g'
grep -rl 'mpqt\.' --include='*.go' pkg examples | xargs sed -i '' 's/\bmpqt\./qual./g'
grep -rl 'github.com/looprig/mpqt/profile' --include='*.go' . | xargs sed -i '' 's#github.com/looprig/mpqt/profile#github.com/looprig/mpqt/pkg/profile#g'
```

Repeat the path rewrite for `compare`, `reportjson`, `mpqttest`, and `fixture/target` → `pkg/qual/target` (import alias `fixtarget` in the example keeps working if you alias `fixtarget "github.com/looprig/mpqt/pkg/qual/target"`). Check `doc.go`'s package comment still reads correctly for `qual`.

**Step 3: Run all gates**

```bash
GOWORK=off CGO_ENABLED=0 go build -trimpath ./... && GOWORK=off go vet ./... && GOWORK=off go test -race ./...
cd examples/qualification && GOWORK=off go mod tidy && GOWORK=off go test -race ./... && GOWORK=off go vet -tags qualification ./... && cd ../..
make secure
```

Expected: everything passes with zero test edits beyond import/identifier renames. If any test fails on behavior, STOP — the move broke something.

**Step 4: Commit**

```bash
git add -A && git commit -m "refactor: move packages under pkg/ and rename core package to qual"
```

---

## Task 2: packfile document types + strict table decode

**Files:**
- Create: `pkg/packfile/doc.go`, `pkg/packfile/document.go`, `pkg/packfile/errors.go`
- Test: `pkg/packfile/document_test.go`

**Step 1: Get the dependency (already approved — recorded in CLAUDE.md)**

```bash
GOWORK=off go get gopkg.in/yaml.v3@v3.0.1
```

**Step 2: Write the failing test**

```go
package packfile

import (
	"strings"
	"testing"
)

const minimalTable = `
table: discipline
revision: v1
dimension: capability
requires: [tools]
evaluators:
  - kind: forbidden-tool
    tool: bash
scenarios:
  - id: tu-101-no-tool-needed
    input:
      - role: user
        text: What is 2+2?
    expect:
      forbidden-actions: [bash]
    labels: {category: tool-use}
`

func TestDecodeTableFile(t *testing.T) {
	tf, err := DecodeTable(strings.NewReader(minimalTable))
	if err != nil {
		t.Fatalf("DecodeTable: %v", err)
	}
	if tf.Table != "discipline" || tf.Revision != "v1" || tf.Dimension != "capability" {
		t.Fatalf("identity mismatch: %+v", tf)
	}
	if len(tf.Requires) != 1 || tf.Requires[0] != "tools" {
		t.Fatalf("requires: %+v", tf.Requires)
	}
	if len(tf.Evaluators) != 1 || tf.Evaluators[0].Kind != "forbidden-tool" {
		t.Fatalf("evaluators: %+v", tf.Evaluators)
	}
	sc := tf.Scenarios[0]
	if sc.ID != "tu-101-no-tool-needed" || sc.Input[0].Role != "user" || sc.Input[0].Text != "What is 2+2?" {
		t.Fatalf("scenario: %+v", sc)
	}
	if sc.Expect == nil || len(sc.Expect.ForbiddenActions) != 1 || sc.Expect.ForbiddenActions[0] != "bash" {
		t.Fatalf("expect: %+v", sc.Expect)
	}
	if sc.Labels["category"] != "tool-use" {
		t.Fatalf("labels: %+v", sc.Labels)
	}
}

func TestDecodeTableRejectsUnknownField(t *testing.T) {
	_, err := DecodeTable(strings.NewReader("table: t\nrevision: v1\ndimension: d\nbogus: 1\n"))
	if err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestDecodeTableRejectsOversizedInput(t *testing.T) {
	big := "table: t\n# " + strings.Repeat("x", MaxFileBytes) + "\n"
	if _, err := DecodeTable(strings.NewReader(big)); err == nil {
		t.Fatal("oversized input accepted")
	}
}
```

**Step 3: Run to verify failure** — `GOWORK=off go test -race ./pkg/packfile/` → FAIL (undefined: DecodeTable).

**Step 4: Implement the document types**

`pkg/packfile/document.go` — these are the canonical DTOs for the whole phase; later tasks add methods, not fields, without a design-doc change:

```go
// Package packfile is the strict, versioned trust boundary between the YAML
// pack corpus and qual. Decoding rejects unknown fields, bounds sizes, and
// never executes anything; building (Task 6) turns validated documents into
// qual.Pack values via the evaluator registry.
package packfile

import (
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
	Table       string                  `yaml:"table"`
	Revision    string                  `yaml:"revision"`
	Dimension   string                  `yaml:"dimension"`
	Requires    []string                `yaml:"requires"`
	Environment *Environment            `yaml:"environment"`
	Rubrics     []RubricSpec            `yaml:"rubrics"`
	Evaluators  []EvaluatorSpec         `yaml:"evaluators"`
	Run         *RunSpec                `yaml:"run"`
	Scenarios   []ScenarioSpec          `yaml:"scenarios"`
	Script      map[string]ScriptSpec   `yaml:"script"`
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
type RunSpec struct {
	Trials           int    `yaml:"trials"`
	Concurrency      int    `yaml:"concurrency"`
	TargetTimeout    string `yaml:"target-timeout"`    // Go duration string
	EvaluatorTimeout string `yaml:"evaluator-timeout"` // Go duration string
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
	Reply         string           `yaml:"reply"`
	Duration      string           `yaml:"duration"` // Go duration string
	ToolCalls     []ScriptToolCall `yaml:"tool-calls"`
	Structured    *StructuredSpec  `yaml:"structured"`
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

func strictDecode(r io.Reader, out any) error {
	lr := &io.LimitedReader{R: r, N: MaxFileBytes + 1}
	dec := yaml.NewDecoder(lr)
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		return &Error{Reason: fmt.Sprintf("decode: %v", err)}
	}
	if lr.N <= 0 {
		return &Error{Reason: "file exceeds MaxFileBytes"}
	}
	return nil
}
```

`pkg/packfile/errors.go`:

```go
package packfile

import "fmt"

// Error is the typed failure for every packfile boundary rejection.
type Error struct {
	Path   string // "<pack>/<file>:<yaml path>" when known
	Reason string
}

func (e *Error) Error() string {
	if e.Path == "" {
		return "packfile: " + e.Reason
	}
	return fmt.Sprintf("packfile: %s: %s", e.Path, e.Reason)
}
```

**Step 5: Run tests** — `GOWORK=off go test -race ./pkg/packfile/` → PASS.

**Step 6: Root go.mod changed (yaml.v3): run the nested-module gate for `examples/qualification`, then all gates, then commit**

```bash
git add -A && git commit -m "feat: add packfile document types with strict yaml decoding"
```

---

## Task 3: ScenarioSpec → eval.Scenario mapping

**Files:**
- Create: `pkg/packfile/scenario.go`
- Test: `pkg/packfile/scenario_test.go`

**Step 1: Failing test**

```go
func TestScenarioSpecToEval(t *testing.T) {
	max := 2
	spec := ScenarioSpec{
		ID:    "tu-002",
		Input: []MessageSpec{{Role: "user", Text: "Compare Lisbon and Porto."}},
		Expect: &ExpectSpec{
			ExpectedToolCalls: []ToolCallExpectSpec{{Tool: "search", Min: 2, Max: &max}},
			ForbiddenActions:  []string{"bash"},
		},
		Labels: map[string]string{"category": "tool-use"},
	}
	sc, err := spec.Scenario("tool-use-selection", "v1")
	if err != nil {
		t.Fatalf("Scenario: %v", err)
	}
	if sc.ID != "tu-002" || sc.Name != eval.Name("tool-use-selection") || sc.Revision != eval.Revision("v1") {
		t.Fatalf("identity: %+v", sc)
	}
	um, ok := sc.Input[0].(*content.UserMessage)
	if !ok {
		t.Fatalf("input[0] is %T", sc.Input[0])
	}
	tb, ok := um.Message.Blocks[0].(*content.TextBlock)
	if !ok || tb.Text != "Compare Lisbon and Porto." {
		t.Fatalf("block: %#v", um.Message.Blocks[0])
	}
	exp := sc.Expectation
	if exp == nil || len(exp.ExpectedToolCalls) != 1 || exp.ExpectedToolCalls[0].Tool != "search" ||
		exp.ExpectedToolCalls[0].MinCount != 2 || exp.ExpectedToolCalls[0].MaxCount == nil || *exp.ExpectedToolCalls[0].MaxCount != 2 {
		t.Fatalf("expectation: %+v", exp)
	}
	if err := sc.Validate(); err != nil {
		t.Fatalf("eval validate: %v", err)
	}
}

func TestScenarioSpecRejectsUnknownRole(t *testing.T) {
	spec := ScenarioSpec{ID: "x", Input: []MessageSpec{{Role: "system", Text: "no"}}}
	if _, err := spec.Scenario("n", "v1"); err == nil {
		t.Fatal("system role accepted") // system prompt belongs to environment, never to input
	}
}
```

Add cases for: assistant role produces `*content.AIMessage` (mirror the `content` package's assistant message type — check `core/content` for the exact struct name and required fields before writing; the scripted fixture's `Observe` in `pkg/qual/target/scripted.go` shows a working assistant-message construction to copy), empty input → error, `Expect` nil → `sc.Expectation == nil`, labels sorted deterministically (sort keys before building `[]eval.Label` so output is stable), structured-output/policy-ref/reference-answers/required-facts mapping.

**Step 2: Run** → FAIL (undefined: Scenario method).

**Step 3: Implement `(ScenarioSpec).Scenario(defaultName, revision string) (eval.Scenario, error)`**

Mapping rules (each violation returns `*Error` naming the scenario ID):
- `Name` empty → `defaultName`.
- Roles: `"user"` → `&content.UserMessage{...}` (ground rule 1); `"assistant"` → the content assistant message type; anything else → error.
- `Expect` → `*eval.Expectation` field-for-field: `RequiredFacts []string` → `[]eval.Fact`, `ForbiddenActions` → `[]eval.ActionName`, `ToolCallExpectSpec{Tool,Min,Max}` → `eval.ToolCallExpectation{Tool: eval.Name(t), MinCount: min, MaxCount: max}`, `StructuredExpectSpec` → `&eval.StructuredOutputExpectation{Schema: eval.Revision(s.Schema), Strict: s.Strict}`, `ReferenceAnswers` → `[]eval.ReferenceAnswer`, `PolicyRef` → `eval.Revision`.
- Labels: sort keys, emit `[]eval.Label{{Key: eval.Name(k), Value: v}}`.
- Finish by calling `sc.Validate()` and wrapping any failure — packfile never emits a scenario eval would reject.

**Step 4: Run tests → PASS. Step 5: gates + commit** — `feat: map scenario specs to eval scenarios`

---

## Task 4: Environment → inference.Request template (+ portable-schema validation)

**Files:**
- Create: `pkg/packfile/environment.go`, `pkg/packfile/yamljson.go`
- Test: `pkg/packfile/environment_test.go`, `pkg/packfile/yamljson_test.go`

**Step 1: Failing tests**

`yamljson_test.go` — YAML node → canonical JSON:

```go
func TestYAMLNodeToJSON(t *testing.T) {
	var n yaml.Node
	src := "type: object\nproperties: {command: {type: string}}\nrequired: [command]\n"
	if err := yaml.Unmarshal([]byte(src), &n); err != nil { t.Fatal(err) }
	raw, err := jsonFromNode(&n)
	if err != nil { t.Fatalf("jsonFromNode: %v", err) }
	want := `{"properties":{"command":{"type":"string"}},"required":["command"],"type":"object"}`
	if string(raw) != want { t.Fatalf("got %s", raw) }
}

func TestYAMLNodeToJSONRejectsNonStringKeys(t *testing.T) {
	var n yaml.Node
	if err := yaml.Unmarshal([]byte("1: x\n"), &n); err != nil { t.Fatal(err) }
	if _, err := jsonFromNode(&n); err == nil { t.Fatal("non-string key accepted") }
}
```

`environment_test.go`:

```go
func TestEnvironmentTemplate(t *testing.T) {
	env := &Environment{
		System: "Be careful.",
		Tools: []ToolSpec{{Name: "bash", Description: "Run a shell command", Schema: schemaNode(t, `{type: object, properties: {command: {type: string}}, required: [command]}`)}},
		ToolChoice: "auto",
	}
	req, err := env.Template()
	if err != nil { t.Fatalf("Template: %v", err) }
	if req.System != "Be careful." || len(req.Tools) != 1 || req.Tools[0].Name != "bash" {
		t.Fatalf("template: %+v", req)
	}
	if req.ToolChoice != inference.ToolChoiceAuto { t.Fatalf("tool choice: %v", req.ToolChoice) }
}

func TestEnvironmentTemplateRejectsNonPortableSchema(t *testing.T) {
	// $ref is outside the portable subset accepted by inference.ValidateOutputSchema.
	env := &Environment{Tools: []ToolSpec{{Name: "t", Description: "d", Schema: schemaNode(t, `{"$ref": "#/x"}`)}}}
	if _, err := env.Template(); err == nil { t.Fatal("non-portable schema accepted") }
}
```

(If `$ref` turns out to be accepted by the validator, pick a keyword the validator's own tests reject — read `inference/output_test.go` for a guaranteed-invalid input — and note the substitution in the commit message.)

**Step 2: Run → FAIL. Step 3: Implement**

- `jsonFromNode(*yaml.Node) (json.RawMessage, error)`: recursively convert (mapping → `map[string]any` with string-key enforcement and key-sorted `json.Marshal`, sequence → `[]any`, scalars via node decoding). Reject aliases and non-string keys.
- `(*Environment) Template() (inference.Request, error)`:
  - `System` copied; each `ToolSpec` → `inference.Tool{Name, Description, Schema: jsonFromNode(...)}`.
  - Validate EVERY tool schema and the output schema through `inference.ValidateOutputSchema(inference.OutputSchema{Name: <tool name>, Schema: raw, Strict: true})` — this is the design's "portable subset at lint time" enforcement point.
  - `ToolChoice`: `""`/`"auto"` → `inference.ToolChoiceAuto`; `"required"` → `inference.ToolChoiceRequired`; else error.
  - `OutputSchema` → `&inference.OutputSchema{...}` with the same validation.
  - A nil `*Environment` receiver returns a zero `inference.Request` and nil error (tables without environments are legal).

**Step 4: PASS. Step 5: gates + commit** — `feat: build inference templates from pack environments with portable schema validation`

---

## Task 5: Evaluator registry

**Files:**
- Create: `pkg/packfile/registry.go`
- Test: `pkg/packfile/registry_test.go`

**Step 1: Failing tests**

```go
func TestBuiltinRegistryBuildsForbiddenTool(t *testing.T) {
	reg := NewRegistry()
	spec := evaluatorSpec(t, "kind: forbidden-tool\ntool: bash\n")
	ev, err := reg.Build(spec, BuildContext{})
	if err != nil { t.Fatalf("Build: %v", err) }
	if ev.Descriptor().Name != exact.ForbiddenTool("bash").Descriptor().Name {
		t.Fatalf("descriptor mismatch")
	}
}

func TestRegistryRejectsUnknownKind(t *testing.T) {
	reg := NewRegistry()
	_, err := reg.Build(evaluatorSpec(t, "kind: nope\n"), BuildContext{})
	var pe *Error
	if !errors.As(err, &pe) || !strings.Contains(pe.Reason, "known kinds:") {
		t.Fatalf("want known-kind list in error, got %v", err)
	}
}

func TestRegistryRejectsUnknownOption(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.Build(evaluatorSpec(t, "kind: forbidden-tool\ntool: bash\nbogus: 1\n"), BuildContext{}); err == nil {
		t.Fatal("unknown option accepted")
	}
}

func TestJudgeKindRequiresClient(t *testing.T) {
	reg := NewRegistry()
	spec := evaluatorSpec(t, "kind: judge\nrubric: support-answer-quality\n")
	bc := BuildContext{Rubrics: map[string]rubric.Rubric{"support-answer-quality": testRubric()}}
	if _, err := reg.Build(spec, bc); !errors.Is(err, ErrJudgeUnconfigured) {
		t.Fatalf("want ErrJudgeUnconfigured, got %v", err)
	}
}
```

Add: one build test per kind (`required-text`, `forbidden-text`, `required-tool`, `tool-error-rate` with `max-error-rate: 0.34`, `max-duration` with `limit: 30s`, `schema-result`), duplicate `Register` rejected, `Kinds()` sorted, `tool-error-rate` with rate outside [0,1] rejected at build (don't rely on the evaluator's runtime Errored).

**Step 2: Run → FAIL. Step 3: Implement**

```go
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

type Registry struct{ kinds map[string]Kind }

func NewRegistry() *Registry            // registers all built-ins below
func (r *Registry) Register(k Kind) error // rejects duplicates and empty names
func (r *Registry) Kinds() []Kind         // sorted by name
func (r *Registry) Build(spec EvaluatorSpec, bc BuildContext) (eval.Evaluator, error)
```

Built-in kinds and their strict option structs (each `Build` decodes `opts` via a per-kind struct through a `yaml.Node.Decode` wrapper that re-encodes and strict-decodes so unknown options are rejected — write helper `decodeOptions(node *yaml.Node, out any) error` that round-trips through `yaml.Marshal` + `strictDecode`, dropping the `kind` field first):

| kind | options struct | constructor call |
|---|---|---|
| `required-text` | `{Substrings []string \`yaml:"substrings"\`}` (non-empty) | `exact.RequiredText(o.Substrings...)` |
| `forbidden-text` | same | `exact.ForbiddenText(...)` |
| `required-tool` | `{Tool string}` (non-empty) | `exact.RequiredTool(o.Tool)` |
| `forbidden-tool` | same | `exact.ForbiddenTool(o.Tool)` |
| `tool-error-rate` | `{MaxErrorRate *float64 \`yaml:"max-error-rate"\`}` (0≤r≤1 when set) | `exact.ToolErrorRate(exact.MaxErrorRate(*o.MaxErrorRate))` or no option |
| `max-duration` | `{Limit string}` (parses via `time.ParseDuration`, > 0) | `exact.MaxDuration(d)` |
| `schema-result` | none (any option key → error) | `exact.SchemaResult()` |
| `judge` | `{Rubric string}` (must resolve in `bc.Rubrics`; client required) | `judge.New(rb, bc.JudgeClient, bc.JudgeTemplate)` |

`RubricSpec` → `rubric.Rubric` conversion also lands here (`func (rs RubricSpec) Rubric() (rubric.Rubric, error)`, scope strings `case/turn/session/run` → `eval.ScopeCase` etc., default case).

**Step 4: PASS. Step 5: gates + commit** — `feat: add evaluator registry with strict per-kind options`

---

## Task 6: Directory loader, Document, digest, Build → qual.Pack

**Files:**
- Create: `pkg/packfile/load.go`, `pkg/packfile/digest.go`
- Test: `pkg/packfile/load_test.go`, `pkg/packfile/digest_test.go` (use `testing/fstest.MapFS` — no disk I/O in unit tests)

**Step 1: Failing tests**

```go
func TestLoadPackFromFS(t *testing.T) {
	fsys := fstest.MapFS{
		"tool-use/pack.yaml":       {Data: []byte("pack: tool-use\nrevision: v1\ntables:\n  - discipline.yaml\n")},
		"tool-use/discipline.yaml": {Data: []byte(minimalTable)},
	}
	doc, err := Load(fsys, "tool-use")
	if err != nil { t.Fatalf("Load: %v", err) }
	if doc.Pack.Pack != "tool-use" || len(doc.Tables) != 1 { t.Fatalf("doc: %+v", doc) }

	p, err := doc.Build(NewRegistry(), BuildContext{})
	if err != nil { t.Fatalf("Build: %v", err) }
	if err := p.Validate(); err != nil { t.Fatalf("qual validate: %v", err) }
	if p.Name != eval.Name("tool-use") || p.Tables[0].Dimension != eval.Name("capability") {
		t.Fatalf("pack: %+v", p)
	}
}

func TestLoadRejectsUnlistedTableReference(t *testing.T) { /* tables: [missing.yaml] → *Error naming the file */ }
func TestLoadIgnoresUnlistedFiles(t *testing.T)          { /* stray.yaml present but unlisted → loaded doc has 1 table; Lint (below) reports it */ }
func TestLoadRejectsDuplicateScenarioIDsAcrossTables(t *testing.T) { /* via doc.Build + qual Validate */ }
```

`digest_test.go`:

```go
func TestDigestDeterministicAndOrderSensitive(t *testing.T) { /* same fs → same hex; swapping table order in pack.yaml → different hex */ }
func TestVerifyDigest(t *testing.T) {
	// pack.digest content format: "packfile-digest/v1 <revision> <sha256-hex>\n"
	// VerifyDigest(doc, lockfileBytes): match → nil; hash differs, revision same → *Error "revision bump required";
	// hash differs, revision differs → nil (bump acknowledged; caller rewrites lockfile); malformed lockfile → *Error.
}
```

**Step 2: Run → FAIL. Step 3: Implement**

```go
// Document is a loaded, structurally validated pack: raw file bytes retained
// for digesting, decoded files for building. It contains no evaluators and
// needs no clients — `mpqt validate` stops here.
type Document struct {
	Dir    string
	Pack   PackFile
	Raw    map[string][]byte // filename → bytes, pack.yaml included
	Tables []TableFile       // in pack.yaml order
}

func Load(fsys fs.FS, dir string) (*Document, error)
func LoadDir(path string) (*Document, error) // os.DirFS wrapper for the CLI

// Digest hashes "packfile/v1\n" then each member file's name and sha256 in
// pack.yaml order (pack.yaml first). Returns lowercase hex.
func (d *Document) Digest() string

// VerifyDigest enforces the change-requires-revision-bump rule against the
// committed pack.digest lockfile bytes.
func VerifyDigest(d *Document, lockfile []byte) error

// DigestLockfile renders the pack.digest content for writing.
func DigestLockfile(d *Document) []byte

// Build assembles the qual.Pack: per table — scenarios via ScenarioSpec.Scenario
// (default name "<pack>-<table>", revision from the table file), evaluators via
// the registry (rubrics from ALL tables' RubricSpecs merged into bc.Rubrics;
// duplicate rubric names across a pack are an error), then qual.Pack.Validate.
func (d *Document) Build(reg *Registry, bc BuildContext) (qual.Pack, error)

// Lint returns non-fatal findings: unlisted *.yaml files in the directory,
// expect/evaluator seam warnings (design: "the honest split") — a scenario
// with expected-tool-calls but no required-tool/tool-error-rate kind in the
// table, or structured-output expect without schema-result.
func (d *Document) Lint() []string
```

**Step 4: PASS. Step 5: gates + commit** — `feat: add pack directory loader with digest lockfiles and lint`

---

## Task 7: JSON Schema generation (`schema.json`)

**Files:**
- Create: `pkg/packfile/schema.go`, `pkg/packfile/schema.json` (generated, committed)
- Test: `pkg/packfile/schema_test.go`

**Step 1: Failing tests**

```go
func TestSchemaMatchesCommittedFile(t *testing.T) {
	generated, err := Schema(NewRegistry())
	if err != nil { t.Fatal(err) }
	committed, err := os.ReadFile("schema.json")
	if err != nil { t.Fatal(err) }
	if !bytes.Equal(bytes.TrimSpace(generated), bytes.TrimSpace(committed)) {
		t.Fatal("schema.json is stale: run `go generate ./pkg/packfile` and commit")
	}
}

func TestSchemaIsValidJSONAndCoversEvaluatorKinds(t *testing.T) {
	raw, _ := Schema(NewRegistry())
	var s map[string]any
	if err := json.Unmarshal(raw, &s); err != nil { t.Fatalf("not JSON: %v", err) }
	for _, k := range NewRegistry().Kinds() {
		if !bytes.Contains(raw, []byte(`"`+k.Name+`"`)) { t.Fatalf("kind %s missing", k.Name) }
	}
}
```

**Step 2: Run → FAIL. Step 3: Implement**

`Schema(reg *Registry) ([]byte, error)` hand-assembles (stdlib `encoding/json`, sorted keys, 2-space indent) one draft-07 JSON Schema whose root is `oneOf: [packFileSchema, tableFileSchema]` (both file shapes validate under one `$schema` URL). Field `description` strings carry the doc text — including the portable-subset warning on `environment.tools[].schema` and `output-schema` ("portable JSON Schema subset (see inference.ValidateOutputSchema); provider-specific keywords are rejected, not translated"). The `evaluators[]` schema is `oneOf` over per-kind entries assembled from `Kind.OptionsSchema` + a `kind` const, with `Kind.Doc` + `Kind.Evidence` as the description. Add `//go:generate go run ./internal/genschema` (a 20-line main that writes `schema.json`) or simply have the test's failure message instruct regeneration via a small `regen` test helper guarded by an env var — choose the `go:generate` program; create `pkg/packfile/internal/genschema/main.go`.

**Step 4: Generate the file, run tests → PASS. Step 5: gates + commit** — `feat: generate committed json schema from the evaluator registry`

---

## Task 8: Migrate the five packs to YAML (golden equivalence, then delete Go)

**Files:**
- Create: `packs/embed.go`, `packs/<name>/pack.yaml`, `packs/<name>/*.yaml`, `packs/<name>/pack.digest` for: `tool-use`, `capability`, `structured-output`, `safety`, `operational`
- Create: `pkg/packfile/golden_test.go` (temporary — deleted at the end of this task)
- Delete (end of task only): `packs/tooluse/`, `packs/capability/`, `packs/structuredoutput/`, `packs/safety/`, `packs/operational/` Go packages, `examples/qualification/`
- Modify: `pkg/mpqttest` callers, `README.md`

**Step 1: Write `packs/embed.go`**

```go
// Package packs embeds the built-in MPQT pack corpus. Load with
// packfile.Load(packs.FS, "<pack-name>").
package packs

import "embed"

//go:embed */*.yaml */pack.digest
var FS embed.FS

// Names lists the built-in packs in canonical order.
func Names() []string {
	return []string{"capability", "tool-use", "structured-output", "safety", "operational"}
}
```

**Step 2: Golden equivalence test FIRST (against the still-present Go constructors)**

```go
func TestYAMLPacksMatchGoConstructors(t *testing.T) {
	cases := []struct {
		dir  string
		want qual.Pack
	}{
		{"tool-use", tooluse.V1()},
		{"capability", capability.V1()},
		{"structured-output", structuredoutput.V1()},
		{"safety", safety.V1()},
		{"operational", operational.V1()},
	}
	for _, tc := range cases {
		t.Run(tc.dir, func(t *testing.T) {
			doc, err := packfile.Load(packs.FS, tc.dir)
			if err != nil { t.Fatal(err) }
			got, err := doc.Build(packfile.NewRegistry(), packfile.BuildContext{})
			if err != nil { t.Fatal(err) }
			requireEqualPacks(t, tc.want, got)
		})
	}
}
```

`requireEqualPacks` compares: pack identity; per-table Name/Revision/Dimension/Requires; scenario-for-scenario deep equality of ID, Name, Revision, Labels, Expectation (via `reflect.DeepEqual` on `*eval.Expectation`), and Input (compare flattened text + roles, since message pointers differ); evaluator lists by `Descriptor()` (Name, Revision, Method) in order — function values can never be compared directly.

**Step 3: Transcribe each pack.** Open each `packs/<x>/v1.go` and copy every scenario verbatim into YAML. Rules:
- Table file names = table names (`selection.yaml`, `discipline.yaml`, …). `pack.yaml` lists them in the Go constructor's order.
- Scenario `name:` only where the Go Name ≠ `"<pack>-<table>"` default.
- The tool-use `discipline` table's evaluator list is exactly `forbidden-tool(bash)` — preserve the Phase 1 deviation and copy its explanatory NOTE as a YAML comment.
- Where current pack *tests* build scripted targets, transcribe those scripts into the tables' `script:` sections so the conforming/deviant tests can be driven from pack data later; the deviant scripts stay in Go tests (they are test fixtures, not pack content).
- Run the golden test after each pack: `GOWORK=off go test -race ./pkg/packfile/ -run TestYAMLPacksMatchGoConstructors` — fix the YAML, never the assertion.
- Generate each `pack.digest` with a tiny throwaway `go run` using `packfile.DigestLockfile`, or add a `-update`-style flag in a `TestMain` helper; commit the lockfiles.

**Step 4: Flip consumers.** Replace every use of `<pack>.V1()` in `pkg/` tests with the loader (`mustPack(t, "tool-use")` helper on top of `packs.FS`). Move each Go pack's conforming/deviant test into a corpus test file `pkg/packfile/corpus_test.go` (same assertions, YAML-loaded pack, scripted target from the `script:` section — conversion helper `ScriptSpec` → `target.Script` written here as `func (s ScriptSpec) Target() (target.Script, error)`).

**Step 5: Delete.** Remove the five Go pack packages and the golden test (its job is done — the corpus tests remain). Delete `examples/qualification/` entirely; add the `go test` integration snippet from the old README quick-start (updated to load `packs.FS`) to `README.md`. All gates + `make secure`.

**Step 6: Commit** — split into two commits for reviewability:

```bash
git commit -m "feat: add yaml pack corpus with golden equivalence to go constructors"
git commit -m "refactor: delete go pack constructors and qualification example in favor of yaml corpus"
```

---

## Task 9: pkg/run — execution extracted from mpqttest + live target construction

**Files:**
- Create: `pkg/run/run.go`, `pkg/run/manifest.go`
- Modify: `pkg/mpqttest/run.go` (delegate to `pkg/run`)
- Test: `pkg/run/run_test.go`, `pkg/run/manifest_test.go`

**Step 1: Read `pkg/mpqttest/run.go` fully.** It already implements plans → per-table `eval.Run` → scorecard. This task moves that flow into `pkg/run` and re-points `mpqttest` at it; behavior must not change (mpqttest's own tests are the regression net).

**Step 2: Failing tests**

```go
func TestExecuteOfflinePack(t *testing.T) {
	doc := mustLoad(t, "structured-output")
	pack := mustBuild(t, doc)
	res, err := run.Execute(context.Background(), run.Spec{
		Manifest: conformingManifest(), // declares structured_output
		Packs:    []qual.Pack{pack},
		Target:   scriptedFromDoc(t, doc), // ScriptSpec → target.Script conversion from Task 8
	})
	if err != nil { t.Fatalf("Execute: %v", err) }
	if len(res.Reports) == 0 { t.Fatal("no reports") }
	if res.Scorecard.Dimensions == nil { t.Fatal("empty scorecard") } // adjust to the real Scorecard field names in pkg/qual/scorecard.go
}

func TestManifestYAMLRoundTrip(t *testing.T) { /* DecodeManifest(strict) → qual.Manifest → Validate; unknown field rejected; credential-looking field ("api-key") rejected by schema */ }
func TestManifestModel(t *testing.T) {
	m := qual.Manifest{Provider: "openrouter", Model: "meta-llama/llama-3-70b", APIFormat: "openai", BaseURL: "https://openrouter.ai/api/v1", Capabilities: []qual.Capability{qual.CapabilityTools}}
	mm := run.ManifestModel(m)
	if mm.Provider != model.ProviderName("openrouter") || mm.Name != "meta-llama/llama-3-70b" || mm.APIFormat != model.APIFormat("openai") || mm.BaseURL != m.BaseURL {
		t.Fatalf("model: %+v", mm)
	}
	// Capabilities: tools → Caps.Tools etc. Read model.Capabilities' field names in
	// inference/model/model.go and map qual.Capability{Tools,StructuredOutput,Images,Thinking}
	// onto them; unknown capability → error from BuildTarget, not silence.
}
```

**Step 3: Implement**

```go
// Spec is one qualification execution. Target may be any eval.Target
// (scripted fixture or live inference target); pkg/run never constructs
// clients (design: "Dependency confinement").
type Spec struct {
	Manifest qual.Manifest
	Packs    []qual.Pack
	Target   eval.Target
	Config   eval.RunConfig // zero value = eval defaults
}

// Result binds the rolled-up scorecard to its per-table eval reports and the
// skipped-table plans (visible coverage, never silent).
type Result struct {
	Scorecard qual.Scorecard
	Reports   []eval.Report
	Skipped   []qual.TablePlan
}

func Execute(ctx context.Context, s Spec) (Result, error)

// BuildTarget constructs the live inference target for one table:
// environment template + manifest model + WithRevision(tableRevision)
// (ground rule 2). client comes from the caller (the CLI module).
func BuildTarget(client inference.Client, m qual.Manifest, env *packfile.Environment, tableRevision eval.Revision) (eval.Target, error)

func ManifestModel(m qual.Manifest) model.Model
func DecodeManifest(r io.Reader) (qual.Manifest, error) // strict YAML, in packfile? NO — lives here but uses packfile.strictDecode via a small exported packfile.StrictDecode helper; add that export in this task
func DecodeProfile(r io.Reader) (profile.Profile, error)
```

(One correction to the design doc recorded here: manifest/profile codecs live in `pkg/run` but reuse `packfile.StrictDecode` — exporting the helper keeps yaml.v3 imports confined to `packfile` + this package's two decode functions. If reviewers prefer them fully inside `packfile`, moving the two functions is mechanical.)

Note: `Execute` runs each runnable `qual.TablePlan` through `eval.Run` with the plan's `Suite` and `Evaluators` — but a per-table live target needs per-table construction. Follow mpqttest's existing structure: `Spec.Target` used as-is for every table (offline case); add `Spec.TargetForTable func(qual.TablePlan) (eval.Target, error)` (nil ⇒ use `Target`) so the CLI can build one live target per table from its environment. Exactly one of `Target`/`TargetForTable` must be set — validate.

**Step 4: Re-point `pkg/mpqttest`** to call `run.Execute` internally; its public API and tests stay byte-identical.

**Step 5: gates + commit** — `feat: extract pack execution into pkg/run with live target construction`

---

## Task 10: pkg/pricing — snapshot, calculator, preflight

**Files:**
- Create: `pkg/pricing/snapshot.go`, `pkg/pricing/cost.go`, `pkg/pricing/preflight.go`
- Test: `pkg/pricing/snapshot_test.go`, `pkg/pricing/cost_test.go`, `pkg/pricing/preflight_test.go`

**Step 1: Definitions (write first, then tests against them)**

```go
// Package pricing implements list-price estimation for qualification runs.
// It is llm-free: token counting arrives behind Counter, supplied by the CLI
// module. Costs are estimates, never invoices (design: "Preflight token and
// cost estimate").
package pricing

// Snapshot is a frozen models.dev price table with provenance.
type Snapshot struct {
	SourceURL string
	FetchedAt time.Time
	Digest    string // sha256 hex of the raw snapshot bytes
	Rows      map[string]Rates // key: "<provider>/<model>"
}

// Rates are USD per million tokens. nil = dimension not priced (⇒ unknown,
// never zero).
type Rates struct {
	Input, Output, Reasoning, CacheRead, CacheWrite *float64
}

func ParseSnapshot(raw []byte, sourceURL string, fetchedAt time.Time) (Snapshot, error)
func FetchSnapshot(ctx context.Context, client *http.Client, url string) (Snapshot, error) // bounded body (8 MiB), timeout from ctx

// Usage is one call's normalized token usage. Reasoning ⊆ Output (invariant
// from the design; violation ⇒ error, never silent correction).
type Usage struct {
	Input, Output, Reasoning, CacheRead, CacheWrite int
	Complete bool // false ⇒ provider did not report usage
}

// Amount is a cost subtotal that is honest about unknowns.
type Amount struct {
	USD     float64
	Known   bool   // false ⇒ some dimension had no rate or usage was incomplete
	Reason  string // why unknown, when !Known
}

// Cost prices one Usage against Rates: (Output-Reasoning)×output + Reasoning×reasoning
// when a reasoning rate exists, else Output×output once; nonzero dimension with
// nil rate ⇒ Known=false (never fallback double-counting).
func Cost(u Usage, r Rates) Amount

// Counter abstracts the llm context counter (design: llm-free root module).
type Counter interface {
	Count(ctx context.Context, req inference.Request) (tokens int, quality string, err error)
}

// Plan is the preflight cost plan printed before paid inference.
type Plan struct {
	TargetCalls, JudgeCalls int
	InputTokens             [2]int // [expected, max]
	OutputTokens            [2]int
	Expected, Max           Amount
	CounterQuality          string
	Unknowns                []string
}

// Preflight builds the plan for a set of runnable table plans (target calls =
// scenarios × trials; judge calls = scenarios × trials × judge evaluators).
// counter nil ⇒ heuristic estimate (bytes/4) with CounterQuality "heuristic".
func Preflight(ctx context.Context, plans []qual.TablePlan, cfg eval.RunConfig, rates Rates, counter Counter, templates map[eval.Name]inference.Request) (Plan, error)
```

**Step 2: Failing tests** cover: parse of a realistic models.dev JSON fragment (fixture bytes in the test file — include `cost: {input: 3, output: 15, cache_read: 0.3}` shape; read https://models.dev/api.json shape ONCE during implementation and encode the actual field names into `ParseSnapshot` + fixture); reasoning-subset invariant violation → error; nil-rate + nonzero usage → `Known=false` with reason; zero accepted only when the catalog explicitly reports zero; `Cost` with distinct reasoning rate prices `output-reasoning` at output rate; `Preflight` call counts (2 scenarios × 3 trials × 1 judge = 6 judge calls); nil counter → heuristic quality.

**Step 3–4: Implement / PASS. Step 5: gates + commit** — `feat: add pricing snapshots, cost calculator, and preflight plans`

---

## Task 11: pkg/gen — generation pipeline

**Files:**
- Create: `pkg/gen/gen.go`, `pkg/gen/prompt.go`, `pkg/gen/append.go`
- Test: `pkg/gen/gen_test.go`, `pkg/gen/append_test.go`

**Step 1: Definitions**

```go
// Package gen generates candidate scenarios for one table via a single
// structured-output call, then validates, dedupes, and appends them.
// It accepts an inference.Client and never constructs one.
package gen

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
	Accepted   []packfile.ScenarioSpec
	Rejected   []Rejection // failed Validate/lint or duplicate ID
	InputText  string      // the prompt, for --no-write inspection
}

type Rejection struct {
	ID     string
	Reason string
}

func Generate(ctx context.Context, client inference.Client, req Request) (Result, error)

// batchSchema is the structured-output contract: {"scenarios": [ScenarioSpec-shaped objects]}.
// Keep it in sync with packfile.ScenarioSpec — the decode test enforces it.
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

// Append inserts accepted specs into the table file's scenarios sequence via
// yaml.Node surgery (comments elsewhere in the file are preserved), adding
// labels {generated-by: "<model>/<date>"}. date comes from the caller (no
// time.Now here — determinism in tests).
func Append(path string, tableFile []byte, specs []packfile.ScenarioSpec, generatedBy string) ([]byte, error)
```

**Step 2: Failing tests**

- `Generate` against a fake `inference.Client` (local struct with an `Invoke` returning a canned `*inference.Response` — build the response the way `inference`'s own `structured_result_test.go` builds one; read that file for the minimal valid shape, including `FinishReason`). Assert: prompt contains the environment system prompt, every tool name + schema, every evaluator kind's Evidence string, every existing scenario ID under a "do not duplicate" heading; candidates with duplicate/existing IDs land in `Rejected` with reasons; candidates failing `ScenarioSpec.Scenario(...)` land in `Rejected`; `N` out of range → error; empty table without `Intent` → error; with `Intent`, prompt contains the intent text and rubric definitions.
- `Append` on a fixture table file with a comment header: output still contains the comment, gains exactly the new scenarios with the `generated-by` label, and round-trips through `DecodeTable`.

**Step 3–4: Implement / PASS.** `Generate` flow: build prompt (one `content.UserMessage`, ground rule 1) → `client.Invoke` with `Output: &inference.OutputSchema{Name: "scenario-batch", Schema: json.RawMessage(batchSchema), Strict: true}` → `inference.StructuredResult(resp)` → `json.Unmarshal` into a DTO mirroring the schema → convert to `ScenarioSpec`s → mechanical post-pass.

**Step 5: gates + commit** — `feat: add single-call scenario generation with mechanical post-pass`

---

## Task 12: pkg/cli + nested cmd/mpqt module

**Files:**
- Create: `pkg/cli/cli.go`, `pkg/cli/{initcmd,validate,schema,evaluators,gencmd,runcmd,comparecmd}.go`
- Create: `cmd/mpqt/go.mod`, `cmd/mpqt/main.go`
- Test: `pkg/cli/cli_test.go` (+ one focused file per command)

**Step 1: Definitions**

```go
// Package cli implements every mpqt command against injected dependencies.
// It is llm-free; cmd/mpqt (the nested module) supplies the constructors.
package cli

// App wires the environment-specific pieces. Every field has a working
// zero-cost default except the client constructors, which nil out LLM
// commands with a clear error.
type App struct {
	Registry   *packfile.Registry
	NewClient  func(model.Model) (inference.Client, error) // nil ⇒ gen/run/judge unavailable
	NewCounter func(model.Model) (pricing.Counter, error)  // nil ⇒ heuristic preflight
	LookupEnv  func(string) (string, bool)                  // for key presence checks (never values in output)
	Stdout, Stderr io.Writer
	Now        func() time.Time
}

// Main parses args and dispatches. Returns the process exit code:
// 0 ok; 1 command failure; 2 usage; 3 disposition/comparison gate failed;
// 4 cost ceiling or pricing-completeness failure.
func Main(args []string, app App) int
```

Commands (stdlib `flag`, one `flag.FlagSet` per subcommand; `-h` prints usage to Stdout):

- `init <name> [dir]` — writes `<dir>/<name>/pack.yaml`, `<name>/example.yaml` (template with placeholder comments), `<name>/schema.json` (from `packfile.Schema`), header `# yaml-language-server: $schema=schema.json`.
- `validate [dir...]` — `packfile.LoadDir` each pack dir (default: every subdir containing `pack.yaml` under `.`), run `Lint`, environment `Template()` (portable-schema check), `VerifyDigest` against `pack.digest`, optional `--api-format <fmt>` (v1: accepted but only `""` implemented as a no-op with a "dialect projectability not yet implemented" note — geminiapi projection is inside the codec; wiring it is follow-up), optional `--execute` (script-backed tables through `run.Execute` with scripted targets). Exit 1 on any error; lint warnings print but exit 0.
- `schema` — print `packfile.Schema(app.Registry)`.
- `evaluators` — table of `Kind.Name`, options (from OptionsSchema property names), `Evidence`, `Doc`.
- `gen` — flags `--pack --table -n --focus --intent --config --no-write --raw --dry-run --skip-cost-estimate --max-estimated-cost-usd --pricing-snapshot --require-priced`; loads generator config (below), preflight via `pricing`, then `gen.Generate` + `gen.Append` (or stdout with `--no-write`; JSONL to stdout with `--raw`).
- `run` — flags `--manifest --profile --packs --require --config --trials --concurrency` + the pricing flag set; loads manifest/profile via `run.Decode*`, builds packs (judge `BuildContext` from config; `packfile.ErrJudgeUnconfigured` surfaces pre-paid-call), per-table live targets via `run.BuildTarget` closure on `Spec.TargetForTable`, preflight, execute, write `reportjson` to `--out` (default `mpqt-report.json`), evaluate profile, exit 3 unless disposition ≥ `--require` (default `qualified`).
- `compare` — `--candidate --incumbent` reportjson files through `pkg/compare`; exit 3 on regressions > 0.

Generator/judge config file (strict decode, same block for both):

```go
type LLMConfig struct {
	LLM struct {
		Provider  string `yaml:"provider"`
		Model     string `yaml:"model"`
		APIFormat string `yaml:"api-format"`
		BaseURL   string `yaml:"base-url"`
	} `yaml:"llm"`
}
```

**Step 2: Failing tests** — drive `Main` directly with an in-memory App (fake client, `t.TempDir()` packs): exit codes per command including 2 on bad flags, `init`+`validate` round-trip, `validate` catches a digest mismatch, `run` offline over a scripted corpus pack reaches exit 0/3 depending on profile, `gen --no-write` prints YAML containing the generated IDs, secrets never echoed (grep output for a canary env value).

**Step 3: Implement / PASS (root gates + `make secure`).**

**Step 4: Create the nested module**

`cmd/mpqt/go.mod`:

```
module github.com/looprig/mpqt/cmd/mpqt

go 1.24

require (
	github.com/looprig/inference vX.Y.Z
	github.com/looprig/llm vX.Y.Z
	github.com/looprig/mpqt v0.0.0
)

replace github.com/looprig/mpqt => ../..
```

(mirror the sibling `replace` lines the deleted example used for core/eval/inference/llm; run `GOWORK=off go mod tidy` to resolve versions.)

`cmd/mpqt/main.go` (complete):

```go
package main

import (
	"os"
	"strings"
	"time"

	"github.com/looprig/inference"
	"github.com/looprig/inference/model"
	"github.com/looprig/llm/auth"
	"github.com/looprig/llm/auto"
	"github.com/looprig/mpqt/pkg/cli"
	"github.com/looprig/mpqt/pkg/packfile"
	"github.com/looprig/mpqt/pkg/pricing"
)

func main() {
	app := cli.App{
		Registry:   packfile.NewRegistry(),
		NewClient:  newClient,
		NewCounter: newCounter,
		LookupEnv:  os.LookupEnv,
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
		Now:        time.Now,
	}
	os.Exit(cli.Main(os.Args[1:], app))
}

func keyFor(provider model.ProviderName) auth.APIKey {
	env := strings.ToUpper(strings.ReplaceAll(string(provider), "-", "_")) + "_API_KEY"
	return auth.APIKey(os.Getenv(env))
}

func newClient(m model.Model) (inference.Client, error) {
	return auto.New(m, keyFor(m.Provider))
}

func newCounter(m model.Model) (pricing.Counter, error) {
	c, err := auto.NewCounter(m, keyFor(m.Provider))
	if err != nil {
		return nil, err
	}
	return counterAdapter{c}, nil
}
```

plus a 15-line `counterAdapter` implementing `pricing.Counter` over `contextcount.ContextCounter` (map `ContextCount`'s token field and `CounterCapability()` to the quality string — read `inference/contextcount/contracts.go` for the exact field names when writing it).

**Step 5: Nested-module gates**

```bash
cd cmd/mpqt && GOWORK=off go mod tidy && GOWORK=off CGO_ENABLED=0 go build -trimpath ./... && GOWORK=off go vet ./... && cd ../..
```

Add `cmd/mpqt` to the Makefile's secure/test loops the same way `examples/qualification` was handled before deletion.

**Step 6: Commit** — `feat: add mpqt cli with nested command module`

---

## Task 13: Docs polish + final verification

**Files:**
- Modify: `README.md` (Quick start → CLI walkthrough: `init` → edit YAML → `gen` → `validate` → `run` → `compare`; keep the `go test` integration snippet added in Task 8; update the pack catalogue table to the YAML paths), `CLAUDE.md` (record the `pkg/` layout and the cmd/mpqt nested-module rule), `docs/2026-07-23-...-design.md` (mark Status: Implemented for delivered scope; note the manifest/profile-codec placement correction from Task 9)
- Verify only, then commit.

**Final gate (all of it, from a clean tree):**

```bash
GOWORK=off CGO_ENABLED=0 go build -trimpath ./... && GOWORK=off go vet ./... && GOWORK=off go test -race -count=1 ./...
cd cmd/mpqt && GOWORK=off go mod tidy && GOWORK=off go build ./... && GOWORK=off go vet ./... && cd ../..
make secure
go run ./cmd/mpqt validate packs/*/../..  # smoke: exit 0 over the shipped corpus — adjust to `go run github.com/looprig/mpqt/cmd/mpqt` from cmd/mpqt if the root module cannot run it
```

Commit — `docs: update readme and design status for phase 2 delivery`

---

## Explicitly out of scope (do not build, even if it seems easy)

- Gemini projectability checks in `validate --api-format` beyond the flag stub (codec-internal; needs an inference export first).
- The expectation-aware evaluator family (open question 1 — an `eval` change, separately reviewed).
- Agentic/multi-turn generation, `--critic` (design mentions it as optional; defer unless asked).
- Markdown/HTML renderers, egress lab, judge-backed revisions of built-in packs.
- Publishing/tagging any module version.

## Discrepancy protocol

When any signature, field, or behavior differs from this plan: stop the task, write the difference into the task's commit-message body is NOT enough — report it back to the operator with file:line evidence before continuing. The plan is wrong more often than the compiler.
