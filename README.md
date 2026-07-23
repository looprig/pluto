# mpqt — Model & Prompt Qualification Test suite

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

A typical MPQT run looks like: build a `mpqt.Manifest` describing the
candidate (provider, model, base URL, declared capabilities), plan one or
more `mpqt.Pack`s against it with `mpqt.Plan` (which skips any table whose
required capability the manifest doesn't declare), execute every runnable
table with `eval.Run` to produce a `mpqt.Scorecard`, and then either compare
that scorecard against an incumbent's (`compare.Compare`) or gate a
deployment decision on it against an organization `profile.Profile`
(`profile.Evaluate`). `mpqttest.Run` and `mpqttest.RequireDisposition` wire
this whole flow into ordinary `go test`, and `reportjson` gives the result a
canonical, redacted, versioned JSON form for storage or transport.

## Quick start

The offline example in `examples/qualification/qualification_test.go` runs
the structured-output pack against a scripted, deterministic target and gates
on `profile.Qualified`:

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
		Manifest: mpqt.Manifest{
			TargetID: "offline-example", Role: mpqt.RoleCandidate,
			Provider: "test", Model: "fake", APIFormat: "openai",
			BaseURL: "https://example.invalid/v1", Revision: "r-fake",
			EndpointClass: mpqt.EndpointRemote,
			Capabilities:  []mpqt.Capability{mpqt.CapabilityStructuredOutput},
		},
		Packs:  []mpqt.Pack{pack},
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
as part of the default suite:

```
GOWORK=off go test -tags qualification -count=1 ./examples/qualification
```

Set `OPENROUTER_API_KEY` in the environment first; the test skips itself when
the key is absent. `-count=1` defeats test caching, which matters here since
the point of the run is to observe the live target again, not to reuse a
cached pass.

## Pack catalogue

| Pack | Revision | Dimension | Required capabilities |
|---|---|---|---|
| `core-capability` (`packs/capability`) | v1 | capability | none |
| `tool-use` (`packs/tooluse`) | v1 | capability | `tools` |
| `structured-output` (`packs/structuredoutput`) | v1 | capability | `structured_output` |
| `safety-conduct` (`packs/safety`) | v1 | safety | none |
| `operational-stability` (`packs/operational`) | v1 | operational | `tools` (tool-errors table only; latency needs none) |

Every pack's `V1()` constructor is pure (no I/O) and every table declares its
own `Requires` capabilities independently, so `mpqt.Plan` can skip individual
tables — for example `operational-stability`'s latency table still runs
against a manifest with no declared capabilities, while its tool-errors table
is skipped.

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

`mpqt.Manifest.EndpointClass` records where the target under test actually
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

## Phase 2

Deferred out of this build, tracked for a later phase: an egress and
agentic-security lab (deeper misuse/exfiltration testing that needs more than
wire-level observation), judge-backed rubric evaluators (beyond the
programmatic `exact` evaluators used throughout Phase 1), pricing/cost
accounting per qualification run, a CLI for running packs and profiles
outside of `go test`, and Markdown/HTML report renderers on top of the
canonical `reportjson` form.
