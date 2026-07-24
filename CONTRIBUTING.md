# Contributing to mpqt

Thanks for your interest in improving MPQT. This document covers the
practical rules; design rationale lives in `docs/` and the module-wide
engineering guidelines in `CLAUDE.md` apply to every change.

## Building and testing

```sh
CGO_ENABLED=0 go build -trimpath ./...
go test -race ./...
make secure          # gofmt check + vet + staticcheck + gosec + govulncheck
```

All three must pass before a change is proposed. A test that passes without
`-race` but not with it is not passing.

`cmd/mpqt` is its own nested Go module. If your change touches the root
`go.mod`, re-verify the nested module too — its `replace ../..` directive
propagates root requirements:

```sh
cd cmd/mpqt && GOWORK=off go mod tidy && GOWORK=off go build -trimpath ./...
```

## Changing packs, scenarios, or evaluators

MPQT ships packs in two first-class forms; the rules differ slightly.

**YAML packs (`packs/<name>/`, the shipped corpus).**

- Scenario IDs are stable identities and unique across a whole pack. Never
  reuse or repurpose an ID.
- **Any semantic change bumps the table `revision:`** — new or edited
  scenarios, evaluator wiring, tool schemas, system prompts. A stale committed
  `pack.digest` fails `mpqt validate` on exactly this ("revision bump
  required"). After a deliberate change and revision bump, regenerate the
  lockfiles: `mpqt validate --write-digests packs/*`.
- Run `make packs` (or `mpqt validate --execute packs/*`) before proposing a
  change: it strict-loads, lints, digest-checks, and offline-executes every
  programmatic table. The compiled `TestShippedCorpus` guard in
  `pkg/packfile` enforces the same over `go test`.
- Every programmatic scenario carries a `script:` fixture encoding the
  intended good-model behavior so the offline smoke run is meaningful; judge
  tables need no script (they are skipped by `--execute`).
- Never ship a scenario whose evaluator can't actually check what it tests.
  Either enforce it, make it a judge table, or document the gap with a
  `DEFERRED:` note in the table's comment header (see
  `packs/agentic-security/DEFERRED.yaml` for the pattern).

**Go codepacks (`pkg/codepacks/<name>/`).**

- **Any semantic change bumps the pack `Revision`** constant.
- Every codepack keeps the three-test pattern: construction validity, a
  conforming target passes the qualification profile, and a deviant target
  produces failures.
- Evaluators must be honest about missing evidence: return Unverified, never
  Pass, when the evidence a check needs is absent (see
  `pkg/codepacks/tooluse/v1.go` for a worked example of why).

## Dependencies

External packages require explicit maintainer approval before entering
`go.mod`, and approved packages are recorded in `CLAUDE.md` with their
rationale. Prefer the Go standard library; if stdlib can meet a need with a
bit more code, use stdlib.

## Proposing changes

Keep commits focused and in the repo's style (imperative subject, e.g.
`feat: add operational stability pack`). For anything beyond a mechanical
fix — new packs, new evaluator kinds, format changes — open an issue or a
draft PR referencing the relevant design doc in `docs/` first, so the design
conversation happens before the code review.

## License

By contributing, you agree that your contributions are licensed under the
Apache License 2.0, the same license that covers the project (inbound =
outbound). See [LICENSE](LICENSE).
