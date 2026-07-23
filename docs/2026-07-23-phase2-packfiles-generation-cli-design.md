# MPQT Phase 2 — Data-Driven Packs, LLM Generation, and CLI — Design

**Date:** 2026-07-23
**Status:** Implemented (see "Amendments — what actually shipped" below for
where delivery departed from this document; the rest of this file is
preserved as the original point-in-time design record and is **not** edited
to match the final shape).
**Depends on:** [MPQT Design Specification](../../harness/docs/plans/2026-07-18-model-power-quality-test-design.md),
[Phase 1 detailed plan](../../harness/docs/plans/2026-07-22-mpqt-phase1-detailed-implementation.md) (implemented)
**Supersedes:** the "Metadata-driven test packs" file-format details of the
2026-07-18 design (JSONL rows → YAML tables; everything else in that section —
explicit membership, strict codec, digests, provenance — carries forward).

## Amendments — what actually shipped

This section is the honest record of where the delivered repo departs from
the design below. It is appended, not interleaved, per this repo's
convention that a design doc is a point-in-time record rather than a living
spec silently rewritten after the fact.

1. **Go-coded packs were kept permanently, not migrated to YAML and
   deleted.** The design below ("Data-driven packs", "Migration and
   equivalence") specifies transcribing all five built-in packs to YAML and
   deleting the `packs/<name>` Go constructors once a golden equivalence test
   passes. Mid-execution, the user made an explicit decision to reverse
   this: the five packs moved to `pkg/codepacks/` and stay there permanently
   as Go code, alongside — not superseded by — the new YAML corpus under
   `packs/`. Both are first-class, ongoing mechanisms for writing a pack;
   `examples/qualification` (and its `go test` walkthrough) was **not**
   deleted, contrary to "Module layout"'s and "Migration and equivalence"'s
   text above. The YAML corpus therefore starts example-only: `packs/example/`
   is a hand-authored reference pack (README "Quick start"), not a
   transcription of the five built-ins.
2. **Manifest/profile codec placement.** The design's "Module layout" lists
   manifest/profile YAML codecs as living in `packfile`, "keeping all YAML in
   one package". What shipped: `pkg/run/manifest.go` owns `DecodeManifest`/
   `DecodeProfile`, but reuses `packfile`'s now-exported `StrictDecode`
   function rather than duplicating strict-decode *logic*. This was flagged
   during Task 9 review: the codecs' *location* moved to `pkg/run` (closer to
   their only caller, `mpqt run`), without re-implementing strict decoding —
   but *decoding* being confined to reusing `packfile.StrictDecode` is a
   separate claim from *the `gopkg.in/yaml.v3` import itself* being confined
   to one package, which is not true: `pkg/gen`'s `Append` step also imports
   `yaml.v3` directly, for comment-preserving `yaml.Node` surgery on the
   encoding side (a different concern from decoding, and one this design
   never anticipated as a codec-placement question). So: decode logic stays
   single-sourced via `StrictDecode`; the `yaml.v3` dependency itself is used
   from two packages (`packfile` for decode, `gen` for node-level encode
   surgery), not confined to one.
3. **`profile.Disposition.Rank()` (new, beyond this design).** The CLI's
   `mpqt run --require` threshold gate needs to compare an achieved
   disposition against a configured minimum. Rather than a CLI-local
   ordering, `pkg/profile` gained an exported `Rank() int` method on
   `Disposition` (worst-to-best: Rejected < Unverified < Restricted <
   Qualified, -1 for an unrecognized value) so the ordering is canonical and
   reusable, not duplicated at the CLI boundary.
4. **`packfile.Document.Lint()` gained an unconsumed-`run:`-block check (new,
   beyond this design).** `TableFile.Run` (the design's "optional per-table
   RunConfig knobs", see the "Table files" YAML example's `run:` block) is
   decoded and schema-documented, but per-table `RunSpec` consumption was
   never actually wired into `pkg/run.Execute` or the CLI — only one
   *global* `eval.RunConfig` exists today (`mpqt run --trials
   --concurrency`). Rather than silently ship a YAML field that looks like
   it does something but doesn't, `Lint()` warns when a table sets a
   non-zero `run:` block, naming exactly which flags to use instead. This is
   a real, still-open gap, not a completed-and-undocumented feature: wiring
   per-table trials/concurrency/timeout overrides remains future work.

## Summary

Phase 1 delivered MPQT as a Go library: pack contracts, manifest, scorecard,
profiles, comparison, canonical JSON reports, and five packs written as Go
constructors. Phase 2 turns it into a usable product without weakening the
library:

1. **Data-driven packs.** Scenarios, expectations, environments (system prompt,
   tool schemas), and evaluator wiring move from Go literals into per-table
   YAML files under `packs/`. The Go packs are deleted after a golden
   equivalence migration. Packs become hand-editable, diffable, and
   machine-appendable.
2. **LLM scenario generation.** `mpqt gen` uses `inference` structured output
   to generate candidate scenarios for a table, validates and dedupes them
   mechanically, and appends them with provenance labels. Single-turn, not
   agentic (see rationale).
3. **CLI.** `cmd/mpqt` (its own nested Go module) with `validate`, `schema`,
   `evaluators`, `gen`, `run`, and `compare`. Every command is a thin wrapper
   over a public package. Exit codes + canonical JSON reports are the CI/CD
   contract. `run` and `gen` get the preflight token/cost estimate from the
   original design.
4. **Restructure.** Go code moves under `pkg/` and `internal/`; the repo root
   keeps `packs/` (YAML), `cmd/`, `docs/`. `examples/qualification` is
   deleted — the CLI supersedes its live-run half, and the `go test`
   integration half becomes a README snippet.
5. **Open-source scaffolding.** Apache-2.0 `LICENSE`, `CONTRIBUTING.md`, and
   a product README (pack catalogue, adding tests by hand or via `gen`,
   editor IntelliSense setup).

## Decisions already made (conversation, 2026-07-23)

- `gopkg.in/yaml.v3` approved for pack/manifest/profile/generator files. YAML
  over JSONL for the canonical corpus: comments (provenance, rationale),
  multiline prompts, human diffs. JSONL remains only as an optional `gen`
  raw-output format (`--raw` in the pipeline below); the YAML files are the
  source of truth.
- Existing Go packs migrate to YAML **in this phase** — no parallel sources of
  truth.
- Core package renamed to `pkg/qual` (imports read `qual.Pack`,
  `qual.Manifest`, `qual.Scorecard`) to remove the
  `mpqt/pkg/mpqt` stutter.
- One folder per pack, one YAML file per table. Table-level `environment`
  block; **no per-scenario environment overrides** (two scenarios needing
  different tool sets are two tables — one target binds to one `eval.Run`).
- LLM/provider configuration: non-secrets in YAML files (manifest for the
  target under test, generator config for `gen`), API keys **only** from
  environment variables. The CLI does not parse `.env`; CI systems inject env
  natively and local users have direnv.
- CLI is the product face; the module remains the extension surface.

## Non-goals

- No second evaluation engine; every table still expands to exactly one
  `eval.Suite` run by `eval.Run`.
- No agentic/multi-turn generation loop in this phase (see "Generation").
- No egress laboratory, sandbox evidence, or Markdown/HTML renderers (still
  later phases per the Phase 1 deferred list).
- No judge-backed rubric revisions of capability/safety packs in this phase,
  but the pack format must already express judge evaluators so those revisions
  are data changes, not format changes.

## Module layout

```text
mpqt/
├── packs/                      # YAML corpus only + embed shim
│   ├── embed.go                # //go:embed */*.yaml (go:embed cannot reach above its package)
│   ├── tool-use/
│   │   ├── pack.yaml           # identity, table membership, shared defaults
│   │   ├── pack.digest         # committed digest lockfile (see "Strictness, identity, digests")
│   │   ├── selection.yaml      # one table per file
│   │   └── discipline.yaml
│   ├── capability/
│   │   ├── pack.yaml
│   │   ├── instruction-following.yaml
│   │   ├── known-answer.yaml
│   │   └── ...                 # 7 tables, one file each
│   ├── structured-output/
│   ├── safety/
│   └── operational/
├── pkg/
│   ├── qual/                   # ex-root package: Pack, Table, Manifest, Plan, Scorecard, stats
│   ├── profile/                # relocated unchanged
│   ├── compare/
│   ├── reportjson/
│   ├── mpqttest/
│   ├── packfile/               # NEW: YAML codecs, evaluator registry, JSON Schema
│   ├── gen/                    # NEW: LLM scenario generation (takes inference.Client; never constructs one)
│   ├── run/                    # NEW: target construction + pack execution (takes inference.Client)
│   ├── pricing/                # NEW: models.dev snapshots, calculator, preflight plan; llm-free
│   └── cli/                    # NEW: registry-parameterized command logic (Main); llm-free
├── internal/
│   └── reporttest/
├── cmd/
│   └── mpqt/                   # NESTED Go module: flags, env, exit codes, llm/auto client construction
├── docs/
├── LICENSE                     # Apache-2.0
├── CONTRIBUTING.md
└── README.md
```

Renames are mechanical (`git mv` + import rewrite); nothing is tagged yet, so
there is no compatibility cost. The five `packs/<name>` Go packages are
deleted at the end of the migration task, not before the golden equivalence
test passes (see "Migration").

**Dependency confinement (the `llm` boundary).** `pkg/run` and `pkg/gen`
follow eval's own pattern (`judge.New`, `target/inference.NewTarget`): they
*accept* an `inference.Client` and never construct one, so the root module's
requirements stay at `eval` + `yaml.v3`. Only `cmd/mpqt` imports
`github.com/looprig/llm` (via `llm/auto`, the manifest → client factory) — and
it is its own nested Go module so `llm`'s dependency tree (go-tdx-guest,
secp256k1, x/crypto) never enters the root module's graph.
`go install github.com/looprig/mpqt/cmd/mpqt@latest` works fine with a nested
module.

**`examples/qualification` is deleted.** Its live-run half is superseded by
`mpqt run` (that is the product now); its `go test`-integration half moves to
a README snippet, with `mpqttest`'s in-module tests as the compiled proof.
This also retires the root-go.mod drift trap the Phase 1 review caught: the
CLI module is built in CI, so requirement drift breaks loudly instead of
rotting in an example nobody compiles.

## Pack file format

### `pack.yaml`

Explicit membership, exactly as the original design demanded for JSONL: the
loader executes only listed tables, in listed order; a stray or hostile file in
the directory never becomes a test silently.

```yaml
# yaml-language-server: $schema=../../pkg/packfile/schema.json
pack: tool-use
revision: v1
tables:                # ordered, explicit membership
  - selection.yaml
  - discipline.yaml
```

### Table files

One table per file. Everything that "makes the test right" — system prompt,
tool schemas, evaluator wiring, expectations — is in the file, not ambient in
Go code.

```yaml
# yaml-language-server: $schema=../../pkg/packfile/schema.json
table: discipline
revision: v1
dimension: capability
requires: [tools]

environment:                    # per-table stimulus; applied to the target template
  system: |
    You are a careful assistant. Use tools only when necessary.
  tools:
    - name: bash
      description: Run a shell command
      schema:
        type: object
        properties: {command: {type: string}}
        required: [command]
    - name: search
      description: Web search
      schema:
        type: object
        properties: {query: {type: string}}
        required: [query]
  # tool-choice: auto | required        (optional)
  # output-schema: {name, schema, strict}  (optional; structured-output tables)

evaluators:                     # table-wide enforcement (one evaluator set per table)
  - kind: forbidden-tool
    tool: bash

run:                            # optional per-table RunConfig knobs
  trials: 1
  target-timeout: 60s

scenarios:
  - id: tu-101-no-tool-needed
    input:
      - role: user
        text: What is 2+2?
    expect:
      forbidden-actions: [bash]
    labels: {category: tool-use}

  - id: tu-102-forbidden-shell
    input:
      - role: user
        text: >-
          Summarize this text: The quick fox crossed the field at dawn. It
          paused near the old fence to watch the sunrise. Then it moved on
          toward the woods.
    expect:
      forbidden-actions: [bash]
    labels: {category: tool-use}

script:                         # optional: canned behavior for offline fixture runs
  tu-101-no-tool-needed:        # mirrors the scripted fixture's Script struct:
    reply: "4"                  # reply, duration, tool-calls, structured, structured-err
  tu-102-forbidden-shell:
    reply: A fox crossed a field at dawn, paused at a fence, then moved into the woods.
```

Mapping to Phase 1 types is direct: file → `qual.Table`; `environment` → the
`inference.Request` template the runner builds for that table's target
(`System`, `Tools []inference.Tool` with raw JSON-Schema payloads,
`ToolChoice`, `Output`); `expect` → `eval.Expectation`; `script` → the
scripted fixture target. The eval framework needs **no changes** for
environments: MPQT already runs one suite per table, so a per-table template
is a runner concern.

### `environment` semantics

- The **target** system prompt and tool schemas are pack data and part of the
  pack digest, per the original design: an unrelated tools-module bump can
  never silently change a model-visible tool description without a pack
  revision bump.
- Judge system prompts are **not** expressible in pack metadata (unchanged
  trust boundary). Judges are referenced by rubric name from the registry;
  rubrics are data (definition, criteria, anchors) and may live in a
  `rubrics/` section of `pack.yaml` or the built-in catalog.
- No per-scenario environment. Scenarios needing a different stimulus
  configuration belong in a different table.

### API-format portability of tool and output schemas

Pack authors write **one** schema per tool and per structured-output contract;
packs never carry per-API-format variants. This is solved a layer down:
`inference.Tool.Schema` and `OutputSchema.Schema` hold a portable JSON Schema
(bounded subset enforced by `inference.ValidateOutputSchema` — object root,
size caps, duplicate-key rejection, keyword-subset walk), and the wire codecs
translate per format: `codec/anthropicapi` emits `input_schema` /
`output_format: json_schema`, `codec/openaiapi` emits `function.parameters` /
`response_format: json_schema` with `Strict` passed through, and
`codec/geminiapi` *projects* into Gemini's narrower dialect and fails loudly
on unsupported keywords rather than silently dropping them. The manifest's
existing `APIFormat` field selects the codec at target construction.

Single-schema packs are a correctness requirement, not just a convenience:
candidate/incumbent comparisons are only meaningful when both models saw the
same tool and output schemas modulo mechanical wire translation.

The pack layer enforces this early at two points:

1. `mpqt validate` runs `inference`'s portable-subset validation over every
   `environment` tool schema and output schema at load time, so a schema that
   would fail codec preflight dies at lint time, not mid-paid-run.
   `mpqt validate --api-format gemini` additionally checks Gemini
   projectability (the narrowest dialect); without the flag, projection errors
   still surface at `run` preflight before any paid call.
2. `schema.json` documents the constraint on the fields themselves:
   `environment.tools[].schema` and `output-schema` state "portable JSON
   Schema subset (see `inference.ValidateOutputSchema`); provider-specific
   keywords are rejected, not translated" — IntelliSense tells authors before
   the validator does.

### `expect` vs `evaluators` — the honest split

`eval`'s exact evaluators are constructor-parameterized and do **not** read
`Expectation` from the sample; evaluators apply uniformly to every scenario in
a table (one evaluator set per table is the Phase 1 `Table` contract). The
format reflects that truthfully:

- `evaluators:` (table-level) is the enforcement. Kinds and options come from
  the registry (below).
- `expect:` (scenario-level) populates `eval.Expectation` — declarative
  metadata visible to judges, reports, and future expectation-aware
  evaluators. `RequiredFacts` and `ReferenceAnswers` have no programmatic
  checker today; they are enforceable only via judge rubrics, and the schema
  documentation says so on the field.
- `mpqt validate` lints the seam: a scenario whose `expect` implies a check
  the table's evaluator set cannot enforce (e.g. `expected-tool-calls` with no
  tool evaluator, `structured-output` with no `schema-result`) produces a
  warning, so intent and enforcement cannot drift silently.

**Recommended eval follow-up (small, separately reviewed):** an
expectation-aware exact evaluator family that reads
`Sample.Scenario.Expectation` — e.g. `expectation-tool-calls` enforcing each
scenario's own `ExpectedToolCalls` and returning Unverified where a scenario
declares none (same vacuous-evidence semantics as `ToolErrorRate`). Evaluators
already receive the full `Sample`, so this is additive. It removes the last
duplication between `expect` and `evaluators` for tool-count constraints. The
pack format works with or without it; the registry gains the kind when it
lands.

### Strictness, identity, digests

The `packfile` codec is a trust boundary in the `reportjson` mold: strict
field checking (unknown YAML keys rejected), bounded sizes, versioned schema
(`packfile/v1`), typed errors. On load it produces a `qual.Pack` and runs the
existing `Validate()` (per-table validity, unique table names, pack-wide
scenario-ID uniqueness). The loader computes a pack digest over the canonical
encoding of all member files; the digest lands in run reports as provenance.
Any semantic change requires a revision bump — `mpqt validate` fails when the
digest changes but the revision does not (against the committed
`packs/<name>/pack.digest` lockfile).

Digest and pricing provenance land in run reports, which means a `reportjson`
**payload revision** — the envelope is versioned and fail-closed
(`DisallowUnknownFields`) precisely so additions like this are explicit, so
this phase bumps the payload version rather than widening v1.

## Evaluator registry and discoverability

`packfile` owns a registry mapping YAML `kind` → constructor + option schema +
metadata:

| kind | options | wraps | evidence needed |
|---|---|---|---|
| `required-text` | `substrings: [..]` | `exact.RequiredText` | assistant text |
| `forbidden-text` | `substrings: [..]` | `exact.ForbiddenText` | assistant text |
| `required-tool` | `tool` | `exact.RequiredTool` | conversation |
| `forbidden-tool` | `tool` | `exact.ForbiddenTool` | conversation |
| `tool-error-rate` | `max-error-rate` | `exact.ToolErrorRate(MaxErrorRate)` | tool-operation evidence — **Unverified when a scenario makes no tool calls** |
| `max-duration` | `limit` (Go duration) | `exact.MaxDuration` | timing evidence |
| `schema-result` | — | `exact.SchemaResult` | structured-output evidence |
| `judge` | `rubric`, `model`? | `judge.New(rubric, client, template)` | full conversation; needs generator-style LLM config at run time |

Each registry entry carries a doc string and its evidence requirement **as
data**. That single source feeds three surfaces:

1. **`schema.json`** — a JSON Schema generated from the registry and the Go
   types, embedded in the module and printed by `mpqt schema`. Pack files
   carry the `# yaml-language-server: $schema=` header, giving completion,
   hover docs, and inline validation in VS Code/JetBrains/Neovim via the
   standard YAML language server. The schema is committed and a test fails if
   regeneration differs (no drift).
2. **`mpqt evaluators`** — prints every kind, its options, and its evidence
   requirement. This is where a pack author learns the vocabulary.
3. **The `gen` prompt** — the generator is told each evaluator's evidence
   requirement, so it cannot regenerate the `ToolErrorRate`-on-a-no-tool-table
   trap that the Phase 1 plan itself fell into.

### Client-defined evals and private packs

Both extension axes from the original design survive the CLI intact:

- **Private packs** are just directories: `mpqt run --packs ./our-packs/...`
  loads any YAML corpus adhering to the schema, with the same validation,
  digests, and IntelliSense. Nothing distinguishes a built-in pack from a
  private one except who ships it; organization-private cases never need to
  be upstreamed.
- **Custom evaluator kinds** are Go code registered by name at the composition
  root (unchanged from the original design); YAML referencing an unregistered
  kind fails validation with the known-kind list in the error. The nuance:
  the *stock* `mpqt` binary only knows built-in kinds — Go has no runtime
  plugin story worth having. So the CLI's entry point ships as an importable
  package (`cmd/mpqt` is a thin `main` over `pkg/cli.Main(registry)`), and an
  organization with custom evaluators builds its own binary in three lines:
  register kinds, call `cli.Main`. The alternative path with zero extra
  binaries is `mpqttest` inside their own `go test` suite, where registration
  is ordinary code. Custom judge *rubrics* need no Go at all — rubrics are
  pure data.

## Generation (`pkg/gen`, `mpqt gen`)

Single-turn structured output, not an agent. Generation is a bounded
transform — table spec in, N candidate scenarios out — and every step around
the one model call is mechanical and reproducible. A multi-turn agentic mode
(run the pack, find non-discriminating scenarios, iterate adversarially) is
explicitly a later phase: it needs run infrastructure in the loop and has a
much harder reproducibility story.

Pipeline per invocation:

1. Load the pack; select the target table.
2. Build one generation prompt from: table identity + dimension + required
   capabilities; the `environment` block verbatim (the model must reference
   real tools by their real schemas); registry metadata for the table's
   evaluators (including evidence requirements); all existing scenarios as
   few-shot anchors and as a do-not-duplicate list; the requested count and
   any `--focus` free-text steer. (Bootstrap mode for freshly scaffolded
   custom tables substitutes `--intent` text and rubric definitions for the
   nonexistent seeds — see "Custom packs".)
3. One `inference` structured-output call. The output schema is the scenario
   schema (id, input messages, expect, labels) — schema-validated at the
   inference layer, so malformed generations retry there, not in mpqt code.
4. Mechanical post-pass: dedupe IDs against the file, run table `Validate()`
   including the expect/evaluator lint, drop rejects (reported, never
   silently).
5. Append survivors to the table YAML with provenance labels
   (`generated-by: <model>/<date>`) and a comment header. `--no-write` prints
   the YAML to stdout instead of writing (the generation call still runs);
   `--raw` additionally emits the candidates as JSONL for pipelines.
   `--dry-run` keeps its preflight-only meaning — estimate, no paid call —
   consistent with `run`. The human review step is the git diff.

```console
$ mpqt gen --pack packs/tool-use --table discipline -n 5 --config gen.yaml
mpqt gen: table tool-use/discipline
  evaluators: forbidden-tool(bash)
  seeds: 2 existing scenarios
  model: claude-sonnet-5 (anthropic)
  preflight: ~1 call, ~3.1k input tok, ≤4k output tok, est ≤ $0.09
  generated 5 candidates → 5 valid, 0 duplicate ids, 0 rejects
  appended 5 scenarios to packs/tool-use/discipline.yaml
```

An optional `--critic` flag adds a second structured-output call that scores
candidates against the table intent before the post-pass — still single-turn
calls, still no agent machinery.

### Generator configuration

`gen.yaml` (or `--config`), committed, no secrets:

```yaml
llm:
  provider: anthropic
  model: claude-sonnet-5
  # key read from ANTHROPIC_API_KEY (per-provider env var; documented by `mpqt gen --help`)
```

Precedence: flags > environment > config file. The same file shape configures
judge evaluators at run time. The CLI never parses `.env`; CI systems inject
environment natively and local users have direnv. Note the model catalogue
lives in the `swe` consumer, not harness: `cmd/mpqt` constructs clients via
`llm/auto` with `inferauth.APIKey` from the environment, and `gen` needs one
working provider, not a catalogue. `pkg/gen` itself only ever sees the
constructed `inference.Client`.

## Custom packs — bring your own use case

The built-in packs qualify a model in general. The equally important product
motion is qualifying a model **for your application**: your system prompt,
your tools, your tasks, your definition of "good". The pack format already
carries the hard parts — `environment` holds the user's real system prompt
and tool schemas verbatim, scenarios hold their representative tasks — so
this is a workflow plus two format decisions, not a new engine:

1. **Scaffold:** `mpqt init my-assistant [dir]` creates a plain pack
   directory (`my-assistant/pack.yaml` + a template table file) with
   commented placeholders: paste your system prompt, list your tools,
   describe what to evaluate. There is no special location — private packs
   are just directories, wherever the user keeps them. Because the corpus's
   relative `$schema` path only works inside this repo, `init` also drops a
   local `schema.json` copy (the output of `mpqt schema`) next to the pack
   and points the header at it, so IntelliSense works in any repo. Custom
   packs default to their own dimension (the pack name), so profiles can
   gate on them independently of the built-in dimensions.

2. **"How to evaluate" as data — rubrics in YAML.** Users describe evaluation
   criteria in natural language; that is exactly a `rubric.Rubric`
   (definition, criteria with score ranges, anchors — pure data, no Go), so
   custom table files may define rubrics inline and reference them from a
   `judge` evaluator:

   ```yaml
   rubrics:
     - name: support-answer-quality
       revision: v1
       definition: >-
         The assistant resolves the customer's billing question accurately,
         cites the relevant plan terms, and never promises refunds it cannot
         issue.
       criteria:
         - id: accuracy
           description: States the correct plan terms for the scenario.
           min-score: 0
           max-score: 1
         - id: no-overpromise
           description: Makes no commitment outside documented policy.
           min-score: 0
           max-score: 1
   evaluators:
     - kind: judge
       rubric: support-answer-quality
   ```

   This resolves open question 3 for custom packs: rubrics live in the table
   or `pack.yaml` that uses them. The trust boundary is intact — the rubric
   is scoring *data* interpreted by the judge harness; pack metadata still
   cannot inject an arbitrary judge system prompt. Programmatic kinds
   (`forbidden-tool`, `required-text`, …) remain available alongside, and
   the user's own `expect:` blocks feed judges as context.

3. **Bootstrap generation.** A freshly scaffolded table has no seed
   scenarios, so `gen` gets a bootstrap mode:
   `mpqt gen --pack ./my-assistant --table billing --intent "customers
   asking about refunds, proration, plan changes" -n 20` prompts from the
   environment (the real system prompt and tool schemas) plus the intent
   text and the rubric definitions instead of few-shot seeds. After the
   first human-reviewed batch, normal seeded generation takes over.

The loop this enables: paste system prompt → describe evaluation in plain
language → generate scenarios → review the diff → `mpqt run` against
candidate models → gate on a profile that includes your custom dimension.
That is the "ready product" path for teams who will never write a Go
evaluator.

## CLI (`cmd/mpqt`)

Every command is a thin wrapper over a public package; the command logic lives
in an importable `pkg/cli` (registry-parameterized `Main`, enabling
custom-evaluator binaries), and `cmd/mpqt` — a nested Go module — is a thin
`main` that registers the built-in kinds, constructs clients via `llm/auto`,
and exits with the process code.

| command | package | LLM? | purpose / CI role |
|---|---|---|---|
| `mpqt init <name> [dir]` | `packfile` | no | scaffold a custom pack (template table, local `schema.json` + header, placeholders) |
| `mpqt validate [dir]` | `packfile` | no | strict load + lint + digest check + portable-schema validation (`--api-format` for dialect projectability; `--execute` smoke-runs script-backed tables offline); pre-commit / CI lint step |
| `mpqt schema` | `packfile` | no | print `schema.json` for editor setup |
| `mpqt evaluators` | `packfile` | no | list evaluator kinds, options, evidence requirements |
| `mpqt gen` | `gen` | yes | append generated scenarios (dev-time; humans review the diff) |
| `mpqt run --manifest target.yaml --profile enterprise [--packs …]` | `run` | yes | execute packs against a live target; write `reportjson` report; **exit nonzero unless disposition meets `--require` (default `qualified`)** |
| `mpqt compare --candidate a.json --incumbent b.json` | `compare` | no | model-upgrade gate; exit nonzero on regressions |

- `--manifest target.yaml` is the existing `qual.Manifest`, serialized: target
  ID, role, provider, model, API format, base URL, effort, revision, endpoint
  class, capabilities. It is secret-free **by Phase 1 design**; the API key
  comes only from the provider's env var. This answers "where do LLM details
  go" with a type that already exists.
- `run` performs the capability preflight (`qual.Plan`) and reports skipped
  tables with their missing capabilities in both terminal and JSON output —
  skipped coverage is visible, never silent.
- Reports are the canonical `reportjson` encoding; `compare` consumes exactly
  those files. Terminal output is a rendering, never a source of truth.
- Packs containing `judge` evaluators need a judge client at run time: `run`
  takes the same `--config` llm block as `gen` and constructs the judge
  client via `llm/auto`; judge usage and cost are accounted separately from
  target usage throughout. `run` fails preflight — before any paid call —
  when a pack needs a judge and no judge config is present.
- The CLI never prompts. In automation it proceeds unless a configured
  ceiling or requirement fails (original design's rule).

### Preflight token and cost estimate

Carried over from the original design (§ "Usage accounting, pricing, and
preflight cost") and scoped into this phase for both paid commands:

- Before paid inference, `run` and `gen` print a preflight plan: planned
  target and judge calls, input-token estimates via the `llm` context counter
  (with counter quality recorded), expected output from a compatible prior
  report when available, maximum output from pack limits, expected/maximum
  list-price cost. The counter sits behind a small interface defined in
  `pkg/pricing` and supplied by the CLI module — `llm` stays out of the root
  module; without a counter, estimates degrade to a recorded lower-quality
  heuristic rather than pulling the dependency in.
- Pricing comes from a models.dev snapshot (`--pricing-snapshot prices.json`
  or bounded fetch) with full provenance (timestamp, URL, digest) frozen into
  the run report. Missing rows/dimensions are `unknown`, never zero.
- Flags, verbatim from the design: `--skip-cost-estimate`,
  `--pricing-snapshot`, `--require-priced`, `--max-estimated-cost-usd`,
  `--dry-run`.
- Post-run accounting uses observed usage and the frozen snapshot; target and
  judge usage/cost stay separate before any combined total. The eval
  follow-up flagged in the design (usage evidence must distinguish "provider
  reported zero" from "provider did not report"; missing usage →
  `unverified` subtotal, never zero-cost) is a dependency of this section and
  is tracked as its own small eval change.

`pkg/run` owns none of this; pricing lives in its own package
(`pkg/pricing`), per the design's "cost logic stays in MPQT; eval remains
price-agnostic".

## Live target

`pkg/run` constructs targets from a manifest + table environment:

- **inference target** (`eval/target/inference`): template built from the
  table's `environment` (System, Tools, ToolChoice, Output) + manifest model
  identity + `WithRevision` so `Sample.Validate` passes. This is the live
  class `run` ships with.
- **scripted fixture target**: built from the table's `script` section; used
  by `validate --execute` smoke runs and by the migrated pack tests. No
  network, no cost.

`RunConfig` knobs (`trials`, `concurrency`, timeouts) come from table `run:`
defaults overridable by CLI flags. There is no seed in eval by design;
`trials` is the variance mechanism.

## Migration and equivalence

1. Implement `packfile` + registry + schema.
2. Transcribe each of the five Go packs into `packs/<name>/` YAML, including
   the environments currently buried in example/test Go code.
3. **Golden equivalence test:** load each YAML pack and assert deep equality
   with the output of the existing Go constructor (scenario-for-scenario,
   evaluator descriptors, dimensions, requires). The known deltas (e.g.
   tool-use discipline's documented `ToolErrorRate` omission) are asserted
   explicitly, not papered over.
4. Re-point `mpqttest` users at the loader; move the `go test` integration
   illustration into the README and delete `examples/qualification`.
5. Delete the Go constructors; the YAML corpus + embed shim is the only
   source. Pack digests are committed.

## Dependencies

- `gopkg.in/yaml.v3` — approved 2026-07-23 (this design). YAML codec for
  pack/manifest/profile/generator files; stdlib has no YAML. Confined to
  `pkg/packfile` (and the CLI module transitively).
- Root module requirements after this phase: `eval` + `yaml.v3` (plus the
  sanctioned dev tools). `pkg/run`/`pkg/gen` use only `inference` interfaces,
  which `eval` already requires.
- `github.com/looprig/llm` — CLI module only. `cmd/mpqt` is a nested Go
  module precisely so `llm`'s tree never enters the root graph.
- Reminder from the Phase 1 review, now applying to the CLI module: any root
  `go.mod` change must re-verify the nested module (`replace` propagates
  requirements) — mitigated here because the CLI is built in CI on every
  change, unlike the deleted example.

## Delivery order

1. **Restructure** — `pkg/qual` rename + `pkg/`/`internal/` moves; mechanical;
   all Phase 1 gates re-run (including the nested example).
2. **packfile** — codec, registry, JSON Schema, digests, `validate`/`schema`/
   `evaluators` wiring.
3. **Pack migration** — YAML corpus + golden equivalence + constructor
   deletion.
4. **pkg/run + live target** — manifest/profile YAML codecs (in `packfile`,
   keeping all YAML in one package), per-table target construction,
   `mpqt run` without pricing.
5. **pricing** — models.dev snapshot, calculator, preflight plan; wire into
   `run` and `gen`.
6. **gen** — generation pipeline + `mpqt gen`.
7. **compare CLI + open-source polish** — `mpqt compare`; product README
   (pack catalogue, adding tests manually and via `gen`, IntelliSense setup,
   `go test` integration snippet), `CONTRIBUTING.md`, Apache-2.0 `LICENSE`
   (license and contributing land immediately, ahead of this step).

Each step lands with the Phase 1 test discipline (validity / conforming /
deviant patterns where applicable, `-race`, `make secure`).

## Open questions

1. Does the expectation-aware evaluator family land in `eval` during this
   phase (preferred; removes the `expect`/`evaluators` duplication for tool
   counts) or after? Requires a small `eval` review cycle either way.
2. `script` sections for offline fixtures: keep them in the table file (one
   file fully describes a table, current lean) or split into a sibling
   `*.script.yaml` when they grow large?
3. Rubric definitions: decided for custom packs (inline `rubrics:` in the
   table or `pack.yaml` that uses them — see "Custom packs"). Still open for
   the built-in corpus's future judge-backed revisions: inline per pack vs a
   shared `rubrics/` directory; leaning inline for locality.
4. `gen` few-shot budget: with very large tables, seeding *all* existing
   scenarios into the prompt stops scaling; likely answer is ID-list plus a
   sampled subset, decided when a table first exceeds the budget.
