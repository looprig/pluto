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

`examples/qualification` is its own nested Go module. If your change touches
the root `go.mod`, re-verify the nested module too — its `replace ../..`
directive propagates root requirements:

```sh
cd examples/qualification && GOWORK=off go mod tidy && GOWORK=off go test -race ./...
```

## Changing packs, scenarios, or evaluators

- Scenario IDs are stable identities and unique across a whole pack. Never
  reuse or repurpose an ID.
- **Any semantic change bumps the pack revision** — new or edited scenarios,
  evaluator wiring, tool schemas, system prompts. Two scorecards are only
  comparable when their pack revisions match, so an unbumped semantic change
  silently corrupts comparisons.
- Every pack keeps the three-test pattern: construction validity, a
  conforming target passes the qualification profile, and a deviant target
  produces failures. A new scenario that no deviant script exercises is not
  yet tested.
- Evaluators must be honest about missing evidence: return Unverified, never
  Pass, when the evidence a check needs is absent (see
  `packs/tooluse/v1.go` for a worked example of why).

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
