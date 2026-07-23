# mpqt — Model Power Quality Test

MPQT qualifies a newly released or newly configured model for enterprise use.
The name borrows from electrical power-quality testing: a model is placed
under representative load, faults, distorted inputs, and hostile conditions;
its output quality, stability, safety, and unintended side effects are
measured before it is connected to an organization's workload.

MPQT is a product and test-pack layer over
[`github.com/looprig/eval`](https://github.com/looprig/eval), the reusable
agentic evaluation framework. Where `eval` provides conversations,
evaluators, findings, and reports, MPQT contributes the parts specific to
qualifying an LLM model or configuration for enterprise use: versioned test
packs (structured output, tool use, core capability, safety, operational
stability), a run manifest that identifies exactly what was tested and how,
a bounded scorecard that rolls raw evaluator verdicts up by dimension without
smuggling in policy, and an organization qualification profile that turns a
scorecard into a disposition. MPQT never forks the `eval` runner and takes no
runtime action against live sessions — it only plans, executes, and
interprets `eval` suites.

A typical MPQT run looks like: build a `qual.Manifest` describing the
candidate (provider, model, base URL, declared capabilities), plan one or
more `qual.Pack`s against it with `qual.Plan` (which skips any table whose
required capability the manifest doesn't declare), execute every runnable
table with `eval.Run` to produce a `qual.Scorecard`, and then either compare
that scorecard against an incumbent's (`compare.Compare`) or gate a
deployment decision on it against an organization `profile.Profile`
(`profile.Evaluate`). `mpqttest.Run` and `mpqttest.RequireDisposition` wire
this whole flow into ordinary `go test`, and `reportjson` gives the result a
canonical, redacted, versioned JSON form for storage or transport.

## Quick start

MPQT packs come in two equally first-class forms, and neither supersedes the
other:

- **YAML + CLI** — hand-editable (or `mpqt gen`-generated) YAML under
  `packs/`, loaded by `pkg/packfile` and run by the `mpqt` binary
  (`cmd/mpqt`). No Go required to write or run a pack.
- **Go code** — packs as `qual.Pack` values in `pkg/codepacks/`, run directly
  with `pkg/mpqttest` inside ordinary `go test`, as
  `examples/qualification/qualification_test.go` still does. This path is
  permanent, not a legacy holdover: it is how the five built-in packs
  (`core-capability`, `tool-use`, `structured-output`, `safety-conduct`,
  `operational-stability`) ship, and it is the natural choice for a
  Go-native test suite that wants packs as ordinary compiled values rather
  than a separate file format.

Pick whichever fits your workflow; both compile down to the same `qual.Pack`
/ `eval.Run` execution core (`pkg/run`).

### YAML + CLI walkthrough

Build the CLI (its own nested Go module — `cmd/mpqt` is the only place in
this repo that imports `github.com/looprig/llm`, keeping that dependency
tree out of the root module's graph):

```
cd cmd/mpqt && GOWORK=off go build -o mpqt ./...
```

`packs/example/` is a minimal, committed reference pack — a single table
(`discipline.yaml`) with one tool (`search`) and a `forbidden-tool` evaluator
asserting the assistant doesn't call it for trivial requests. Use it as the
model for a hand-authored pack, or scaffold your own:

```
$ mpqt init my-assistant
init: wrote my-assistant/pack.yaml, my-assistant/example.yaml, my-assistant/schema.json
```

This writes `pack.yaml`, a commented, already-valid template table
(`example.yaml`), and a local `schema.json` copy so any YAML-language-server
editor (VS Code + Red Hat YAML, JetBrains, Neovim) gives completion and
inline validation without needing this repo's own relative `$schema` path.
Edit the template — paste your real system prompt and tools, replace the
placeholder evaluator and scenario — then validate:

```
$ mpqt validate --execute packs/example
validate: packs/example
  --execute: 1 table(s) executed, 0 skipped
```

`validate` strictly loads every pack directory, runs `Document.Lint` (unlisted
files, an `expect` block with no evaluator that enforces it, an unconsumed
`run:` block — see "Known gaps" below), checks every tool/output schema
against the portable JSON-Schema subset, and verifies the pack's digest
against its committed `pack.digest` lockfile when one exists. `--execute`
additionally smoke-runs every table's `script:` section offline through
`pkg/run.Execute` — no network, no cost, useful in CI as a pack-level test
independent of any live model. Run `mpqt schema` to print the JSON Schema and
`mpqt evaluators` to list every evaluator kind:

```
$ mpqt evaluators
KIND             OPTIONS         EVIDENCE                                                                              DOC
forbidden-text   substrings      none required; a vacuous (no substrings) configuration errors rather than passing     asserts no forbidden substring appears in the assistant's text output
forbidden-tool   tool            none required beyond the conversation trace itself                                    asserts a tool call with the given name was not made
judge            rubric          model usage evidence recorded by the judge call itself                                scores the sample's conversation against a named rubric using a model judge
max-duration     limit           timing evidence; Unverified when a scenario carries no recorded timing                fails when the longest recorded timed span exceeds a limit
required-text    substrings      none required; a vacuous (no substrings) configuration errors rather than passing     asserts every required substring appears in the assistant's text output
required-tool    tool            none required beyond the conversation trace itself                                    asserts a tool call with the given name was made
schema-result    -               structured-output evidence; Unverified when a scenario produced no structured output  reports whether the subject's structured output satisfied its schema
tool-error-rate  max-error-rate  tool-operation evidence; Unverified when a scenario makes no tool calls               measures the proportion of tool operations that errored, optionally failing above a threshold
```

Generate more candidate scenarios with an LLM (needs a committed, secret-free
`gen.yaml` naming a provider/model, and that provider's API key in the
environment — see `LLMConfig` in `pkg/cli/cli.go`):

```
mpqt gen --pack packs/example --table discipline -n 5 --config gen.yaml
```

`gen` prints a preflight token/cost estimate before the paid call, mechanically
dedupes and validates candidates against the table's own evaluators, and
appends survivors with a `generated-by: <model>/<date>` provenance label —
review the diff before committing it.

Run a pack against a live target (needs a `qual.Manifest` and a
`profile.Profile`, each its own small YAML file — see `pkg/run/manifest.go`
for the exact field mapping):

```
mpqt run --manifest target.yaml --profile enterprise.yaml --packs packs/example --require qualified
```

`run` performs the same capability preflight as `qual.Plan`, prints a
token/cost estimate (unless `--skip-cost-estimate`), executes every runnable
table against the live target, writes a canonical `reportjson` report, and
exits nonzero (`ExitGateFailed`, 3) unless the resulting disposition meets
`--require` (default `qualified`). Gate a candidate against an incumbent's
prior report the same way CI would:

```
$ mpqt compare --candidate candidate-report.json --incumbent incumbent-report.json
compare: example/discipline regressed=0 improved=1 unchanged=1 incompatible=0
compare: total regressions=0
```

(exit 0 with no regressions; `ExitGateFailed` the moment any matched table's
`regressed` count is nonzero.)

### Known gaps in the YAML/CLI path

- `TableFile.Run` (`run:` per-table trials/concurrency/timeout overrides) is
  decoded and schema-documented but **not yet wired**: `pkg/run.Execute` and
  the CLI only support one *global* `eval.RunConfig` (`mpqt run --trials
  --concurrency`). A table that sets a non-zero `run:` block gets a
  `Document.Lint` warning saying exactly that, rather than silently
  pretending the override took effect. Per-table overrides remain a
  documented future task.
- `mpqt validate --api-format` dialect-projectability checking (beyond the
  default portable-subset check) is not yet implemented; passing a non-empty
  value prints a note rather than actually checking Gemini/other-dialect
  projection.

### Go + codepacks walkthrough

The offline example in `examples/qualification/qualification_test.go` runs
the structured-output pack (`pkg/codepacks/structuredoutput`) against a
scripted, deterministic target and gates on `profile.Qualified` — no YAML, no
CLI, just `qual.Pack` values and `pkg/mpqttest` inside `go test`:

```go
func TestOfflineQualification(t *testing.T) {
	pack := structuredoutput.V1()
	scripts := map[string]fixtarget.Script{}
	for _, tbl := range pack.Tables {
		for _, sc := range tbl.Scenarios {
			scripts[sc.ID] = fixtarget.Script{
				Reply: "ok",
				Structured: &fixtarget.Structured{
					SchemaName:     "output",
					SchemaRevision: sc.Expectation.StructuredOutput.Schema,
				},
			}
		}
	}
	card := mpqttest.Run(t, mpqttest.RunSpec{
		Manifest: qual.Manifest{
			TargetID: "offline-example", Role: qual.RoleCandidate,
			Provider: "test", Model: "fake", APIFormat: "openai",
			BaseURL: "https://example.invalid/v1", Revision: "r-fake",
			EndpointClass: qual.EndpointRemote,
			Capabilities:  []qual.Capability{qual.CapabilityStructuredOutput},
		},
		Packs:  []qual.Pack{pack},
		Target: fixtarget.NewScripted("offline-example", scripts),
	})
	minScore := 90.0
	mpqttest.RequireDisposition(t, card, profile.Profile{
		Name: "example", Revision: "1",
		Requirements: []profile.Requirement{
			{Dimension: "capability", MinScore: &minScore},
		},
	}, profile.Qualified)
}
```

Run it like any other test:

```
GOWORK=off go test -race ./examples/qualification
```

## Live qualification

`examples/qualification/live_test.go` runs the same pack against a real
OpenRouter model over `github.com/looprig/llm/auto` and
`github.com/looprig/eval/target/inference`. It is gated behind the
`qualification` build tag so it never runs (or requires network/credentials)
as part of the default suite. `examples/qualification` is its own nested Go
module (`examples/qualification/go.mod`) rather than part of the root
module: `llm`/`inference` pull in a sizable transitive dependency chain (TEE
attestation, crypto) that only this one example needs, and nesting it keeps
that weight and audit surface out of every other MPQT consumer's module
graph:

```
cd examples/qualification
GOWORK=off go test -tags qualification -count=1 ./...
```

Set `OPENROUTER_API_KEY` in the environment first; the test skips itself when
the key is absent. `-count=1` defeats test caching, which matters here since
the point of the run is to observe the live target again, not to reuse a
cached pass.

The live test also uses a distinct `qualification` build tag rather than this
repo's house `integration` convention, to distinguish a live-credentialed,
cost-incurring example from generic process-boundary integration tests.

## Pack catalogue

| Pack | Revision | Dimension | Required capabilities |
|---|---|---|---|
| `core-capability` (`pkg/codepacks/capability`) | v1 | capability | none |
| `tool-use` (`pkg/codepacks/tooluse`) | v1 | capability | `tools` |
| `structured-output` (`pkg/codepacks/structuredoutput`) | v1 | capability | `structured_output` |
| `safety-conduct` (`pkg/codepacks/safety`) | v1 | safety | none |
| `operational-stability` (`pkg/codepacks/operational`) | v1 | operational | `tools` (tool-errors table only; latency needs none) |

Every pack's `V1()` constructor is pure (no I/O) and every table declares its
own `Requires` capabilities independently, so `qual.Plan` can skip individual
tables — for example `operational-stability`'s latency table still runs
against a manifest with no declared capabilities, while its tool-errors table
is skipped.

These five stay Go code permanently (`pkg/codepacks/`) — see "Quick start"
above. The parallel YAML corpus under `packs/` currently ships one reference
pack, `example` (dimension `capability`, requires `tools`), meant as a
worked example for hand-authoring a pack rather than a transcription of the
five built-ins; nothing requires the two corpora to mirror each other.

## Profile semantics

A `profile.Profile` is a named, versioned set of mandatory requirements and
optional restrictions; evaluating one against a `Scorecard` (`profile.Evaluate`)
yields exactly one of four dispositions, in this precedence:

- **Rejected** — at least one mandatory requirement is violated (a dimension
  fell below its minimum score or coverage, or a finding/severity bound was
  exceeded). Takes precedence over everything else: a standing violation is
  never masked by missing evidence elsewhere.
- **Unverified** — no requirement is violated, but at least one is undecided
  (the relevant dimension has zero verdicts, or too little verified evidence
  to trust the score). Unknown is never silently treated as a pass.
- **Restricted** — every mandatory requirement is met, but at least one
  optional restriction's requirement is not, so the disposition downgrades
  from qualified to a reduced deployment scope.
- **Qualified** — every mandatory requirement is met and no restriction
  applies.

## Target-class limitations

`qual.Manifest.EndpointClass` records where the target under test actually
executes, because that placement bounds what MPQT can honestly claim to have
observed. A **remote** endpoint (hosted inference over HTTPS, the common
case for third-party model providers) is observed purely at the wire: MPQT
sees the requests it sent, the responses and tool calls that came back,
reported token usage, latency, and any transport-level errors — nothing
about the process that produced them. It cannot observe the provider's
internal sandboxing, resource limits, or any side effects that never cross
the API boundary, and a passing safety or tool-discipline table against a
remote endpoint is evidence about *behavior over the wire*, not a proof about
the provider's infrastructure. A **local** endpoint (an inference server
running on the same host) and a **process** endpoint (a foreign process
executed under sandbox control) can in principle support deeper
instrumentation — resource accounting, filesystem/network egress
monitoring, process-level enforcement — but MPQT's Phase 1 packs do not yet
exploit that; the endpoint class is recorded today so that later, richer
target adapters have a place to declare and act on the distinction, and so
that every scorecard is honest about which class of evidence it rests on.

## Adding tests

Both paths from "Quick start" are live, ongoing ways to add tests — pick
whichever fits, per pack:

- **Go pack** (`pkg/codepacks/<name>/v1.go`): add a scenario to the relevant
  table, keep its ID unique pack-wide, and bump the pack `Revision` for any
  semantic change — `Pack.Validate()` and the pack's conforming/deviant tests
  enforce the rest. Custom evaluators implement `eval.Evaluator` and are wired
  at the composition root; custom packs are just values of `qual.Pack`, so
  private packs live in your own repo and run through the same
  `mpqttest.Run`.
- **YAML pack** (`packs/<name>/*.yaml`, `packs/example/` as the worked
  example):
  - **Manually**: append a scenario block to the table's YAML file and run
    `mpqt validate`. Every file carries a `# yaml-language-server: $schema=…`
    header, so any editor running the standard YAML language server (VS Code
    via the Red Hat YAML extension, JetBrains, Neovim) gives completion,
    hover documentation, and inline validation against the shipped JSON
    Schema; `mpqt schema` prints it and `mpqt evaluators` lists every
    evaluator kind, its options, and the evidence it needs.
  - **With an LLM**: `mpqt gen --pack packs/example --table discipline -n 5
    --config gen.yaml` generates candidate scenarios via structured output —
    prompted with the table's real tool schemas, evaluator constraints, and
    existing scenarios as seeds — then validates, dedupes, and appends them
    with provenance labels. You review the git diff. Model choice lives in a
    committed config file; the API key comes only from the environment.

Either way, bump the table's `revision:` (YAML) or the pack's `Revision`
constant (Go) for any semantic change, and re-run `mpqt validate` (YAML) or
the pack's own tests (Go) — a stale committed `pack.digest` lockfile fails
`mpqt validate` on exactly this ("revision bump required").

## Roadmap

Phase 2 (the [design doc](docs/2026-07-23-phase2-packfiles-generation-cli-design.md),
now marked Implemented) delivered everything in "Quick start" above: the YAML
pack corpus and `pkg/packfile` trust boundary, `mpqt gen`, a live `mpqt run`
with preflight token/cost estimation, `mpqt compare` as a CI model-upgrade
gate, and custom packs (`mpqt init`) — paste your own system prompt and
tools, describe evaluation criteria as a plain-language rubric, and generate
scenarios for *your* application rather than only the built-in packs. The
design doc's "Amendments" section records where delivery departed from the
original plan (Go packs kept permanently rather than deleted,
`profile.Disposition.Rank()`, the `Lint()` unconsumed-`run:` warning, and the
still-open per-table `RunSpec` gap noted above).

Later phases (per the original design, still ahead): an egress and
agentic-security lab, judge-backed rubric revisions of the capability/safety
packs, and Markdown/HTML report renderers over the canonical `reportjson`
form.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). In short: `make secure` must pass,
tests run with `-race`, scenario or evaluator changes bump the pack revision,
and external dependencies need explicit maintainer approval before they enter
`go.mod`.

## License

Apache License 2.0 — see [LICENSE](LICENSE).
