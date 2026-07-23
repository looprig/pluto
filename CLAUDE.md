# CLAUDE.md — Development Guidelines

## Design guidance

Keep packages and types cohesive. Split code when responsibilities have different owners, invariants, or reasons to change, not because a description contains a particular word.

Prefer simple changes to existing types when the behavior belongs there. Use composition when capabilities are genuinely independent.

**Liskov Substitution** — Every implementation of an interface must honor the full contract. If a concrete type can't satisfy a method without panicking, returning errors the caller doesn't expect, or silently doing less, redesign the interface.

**Interface Segregation** — Interfaces are small and focused. A caller should never be forced to depend on methods it doesn't use. Prefer many small interfaces over one large one.

Define small interfaces at the package that consumes them when substitution, testing, or a stable boundary requires one. Concrete dependencies are fine when they are the intended abstraction.

## Security — First-Class, Not an Afterthought

**Validate at every boundary.** All external input (HTTP, CLI args, env vars, files, queues) is untrusted until validated. Validate before it enters business logic, not inside it.

**Least privilege always.** Every component, goroutine, and service gets only the permissions it needs. Never pass a full config or god-object when a narrow interface suffices.

**No secrets in code.** No hardcoded tokens, passwords, keys, or connection strings — ever. Use environment variables or a secrets manager. Fail loudly on startup if required secrets are missing.

**Authenticate before authorize, authorize before act.** Every action that crosses a trust boundary must check identity first, then permission, then execute. Never assume a caller is trusted.

**Sanitize before use.** Never interpolate external data into queries, shell commands, file paths, or log messages without sanitization. Use parameterized queries, exec with argument lists, and filepath.Clean.

**Fail secure.** On error or ambiguity, deny by default. A failed permission check must block the action, not fall through.

**Log security events, not secrets.** Audit auth failures, permission denials, and unexpected inputs. Never log credentials, tokens, or PII.

## Repo layout (Phase 2: packfiles, generation, CLI)

`pkg/` holds every llm-free package; `cmd/mpqt` is the sole composition root
allowed to import `github.com/looprig/llm`:

- `pkg/qual` — the core domain: `Pack`, `Table`, `Manifest`, `Plan`, `Scorecard`.
- `pkg/profile` — turns a `Scorecard` into a release disposition
  (Qualified/Restricted/Unverified/Rejected) against an org `Profile`;
  `Disposition.Rank()` gives that four-way ordering a canonical, exported
  worst-to-best value so a `--require` threshold gate never needs its own
  CLI-local copy.
- `pkg/compare` — candidate-vs-incumbent scorecard diff, rolled up per table.
- `pkg/reportjson` — canonical, versioned, fail-closed JSON codec for a
  `Scorecard` (+ optional `profile.Result`).
- `pkg/mpqttest` — wires a pack execution into ordinary `go test` and gates
  on a disposition.
- `pkg/packfile` — the YAML trust boundary: strict pack/table decoding,
  environment templates, the evaluator registry, directory loading, digests,
  `Lint()`, and the generated JSON Schema. The **only** package that imports
  `gopkg.in/yaml.v3`; `pkg/run`'s manifest/profile YAML codecs reuse its
  exported `packfile.StrictDecode` rather than importing yaml.v3 themselves,
  so the dependency stays confined to this one package.
- `pkg/codepacks` — the five built-in packs as Go code
  (`capability`, `tooluse`, `structuredoutput`, `safety`, `operational`).
  **Kept permanently, not a legacy holdover**: mid-execution the plan to
  migrate these to YAML and delete the Go source was reversed by explicit
  user decision, so Go-authored packs are a first-class, parallel mechanism
  to `pkg/packfile`'s YAML corpus (`packs/`), not a deprecated one.
- `pkg/run` — target construction, pack execution, partial-result
  preservation; the shared core behind both `mpqttest` and the CLI's `run`.
- `pkg/pricing` — models.dev snapshot parsing, cost/token estimation,
  redirect-safe snapshot fetch; llm-free (takes a `Counter` interface).
- `pkg/gen` — single-turn, structured-output scenario generation plus the
  mechanical validate/dedupe/append post-pass.
- `pkg/cli` — every `mpqt` command's logic (`init`, `validate`, `schema`,
  `evaluators`, `gen`, `run`, `compare`), parameterized over an injected
  `App` (client/counter constructors, registry, I/O). llm-free.

**The `cmd/mpqt` nested-module rule.** `cmd/mpqt` is its own nested Go module
(own `go.mod`) and is the **only** place anywhere in this repo that may
import `github.com/looprig/llm` (via `llm/auto`, to construct a real
`inference.Client`/`pricing.Counter`). Every package under `pkg/` takes an
injected `inference.Client` / `pricing.Counter` instead of constructing one,
so the root module's dependency graph never pulls in `llm`'s transitive
weight (TEE attestation, secp256k1, x/crypto). A custom-evaluator binary
follows the same shape: register kinds on a `packfile.Registry`, call
`pkg/cli.Main` from your own `main`.

`gopkg.in/yaml.v3` is already listed under Dependencies below (approved
2026-07-23) — that entry is current; do not add a second one.

## Dependencies

**Prefer stdlib.** Always reach for the Go standard library first. If a need can be met with stdlib — even with a bit more code — use stdlib.

**External packages require explicit user approval.** Before adding any external dependency, stop and ask the user. State what the package is, why stdlib is insufficient, and what the package adds. Do not `go get` or add to `go.mod` without a clear "yes" from the user in the current conversation.

**Amend this file when approved.** Once a package is approved, add it here so future sessions know it is sanctioned:

<!-- Approved external packages -->
- `github.com/securego/gosec/v2` — security static analysis tool (dev/tool only)
- `golang.org/x/vuln/cmd/govulncheck` — official Go vulnerability scanner (dev/tool only)
- `honnef.co/go/tools/cmd/staticcheck` — extended static analysis (dev/tool only)
- `github.com/google/go-tdx-guest` — Intel TDX quote parsing and verification; required by internal/llm/tee for phala and chutes TEE attestation
- `golang.org/x/crypto` — ChaCha20-Poly1305 AEAD; required by internal/llm/e2e for ML-KEM E2E envelope (stdlib has no chacha20poly1305). Also provides `x/crypto/sha3` (Keccak-256) needed by `pkg/llm/aci` for eth-address derivation + KMS-custody digest, and `x/crypto/hkdf` (HMAC-based HKDF) for the E2EE v2 key-derivation (KDF).
- `github.com/decred/dcrd/dcrec/secp256k1/v4` (+`/ecdsa`) — secp256k1 ECDSA verify (64-byte) and public-key recovery (65-byte r‖s‖v), required by `pkg/llm/aci` to verify Dstack `aci/1` receipt signatures, keyset endorsements, and the KMS-custody chain (the gateway signs with `ecdsa-secp256k1`; stdlib has no secp256k1). **Approved 2026-06-24.** Chosen over `go-ethereum/crypto` for a far smaller dependency surface; same curve math. Note: secp256k1 is classical (protocol-mandated, verify-only) — not a quantum-safety choice.
- `github.com/looprig/storage` — leaf storage contracts (`Ledger`/`Leaser`/`KV`/`Blobs`) + in-memory reference backend (`memstore`) + conformance suite (`storetest`); stdlib-only. The NATS deps moved to the `looprig/natsstore` backend module; `fsstore`/`rclonestore` are the other storage backends.
- `github.com/looprig/eval` — the reusable evaluation framework module (a first-party looprig sibling, like `github.com/looprig/storage`/`tui`): conversations, evaluators, rubrics, findings, reports, plus `exact`, `judge`, `rubric`, and `evaltest`. Built on `github.com/looprig/core` (and `github.com/looprig/inference` in its `judge`/`target/inference` packages only). Depended on by `pkg/evalmigration`, and supersedes the legacy `pkg/eval`. Approved 2026-07-18 as part of the eval-framework migration.
- `gopkg.in/yaml.v3` — YAML codec for pack/manifest/profile/generator files (`pkg/packfile`); stdlib has no YAML and the pack corpus is a hand-edited YAML format by design. **Approved 2026-07-23** (Phase 2 design, docs/2026-07-23-phase2-packfiles-generation-cli-design.md).
- The interactive terminal presentation layer and its charm.land stack live in the sibling module github.com/looprig/tui.
- The transcript reconstruction and HTML-export layer was archived out of Harness on 2026-07-09. Harness core does not carry a CommonMark renderer.

## Secure Coding Patterns

**Randomness** — Use `crypto/rand` for anything security-sensitive (tokens, nonces, IDs). Never use `math/rand` for secrets.

**Queries** — Always use parameterized queries via `database/sql`. Never format SQL with `fmt.Sprintf` or string concatenation.

**HTTP server** — Always set explicit timeouts. No naked `http.ListenAndServe` with default server:
```go
srv := &http.Server{
    ReadTimeout:    5 * time.Second,
    WriteTimeout:   10 * time.Second,
    IdleTimeout:    60 * time.Second,
    MaxHeaderBytes: 1 << 20,
}
```

**TLS** — Never set `InsecureSkipVerify: true`. Never use TLS versions below 1.2. Default to `tls.Config{MinVersion: tls.VersionTLS12}`.

**Context** — Every I/O call (HTTP, DB, file, external service) must use a `context.Context` with a timeout or deadline. No unbounded blocking.

**Shell commands** — Never pass user input to `exec.Command` as a shell string. Always pass args as separate parameters.

Harness defines runner and access-gate contracts but does not implement a shell tool. Ownership is split along the preparation/evaluation/enforcement boundary:

- `pkg/gate` owns the generic three-state evaluation (Deny/Gated/Allow, deny-before-allow, one combined approval with exactly Approve / Approve always for this workspace / Deny) and response routing. It never parses tool arguments, never defines sandbox profiles, never imports an enforcement package, and never implements a permission-file format (see `pkg/gate/README.md`).
- Tools own preparation: each tool decodes and validates its own untrusted arguments, normalizes commands/URLs/paths, and produces the typed `tool.Request` (`tool.CallPreparer`). Durable rule matching and persistence are consumer-provided behind `gate.RuleMatcher`/`gate.RuleWriter`.
- The sandbox module owns access profiles and OS enforcement, satisfies the structural `gate.AccessSource`/`gate.GrantIssuer` seams without importing harness, and never opens an interactive gate itself.

The optional `github.com/looprig/tools` module owns Bash and the other standard tool implementations. Consumers assemble sandbox profiles and executors directly; there is no confinement bridge.

**File paths** — Always call `filepath.Clean` and verify the result stays within the expected root before opening files from user-supplied paths.

## Build & Testing Requirements

**Build** — Always build with `CGO_ENABLED=0 go build -trimpath`. Never ship a binary without `-trimpath` (leaks local paths).

**Format** — All Go code must be `gofmt`-clean. Run `make fmt` to format the whole module in place; `make fmt-check` fails if anything is unformatted and is wired into `make lint` (so `make secure` enforces it). Scope is `go list -f '{{.Dir}}' ./...`, which excludes `vendor/` and the nested `.worktrees/` modules — never reformat vendored or worktree files.

**Tests** — Always run with `-race`: `go test -race ./...`. A test that passes without `-race` but not with it is not passing.

Use table-driven tests when several cases share the same setup and assertion shape. Use a focused test when one scenario is clearer. Across the relevant test suite, cover:
- Happy path (valid, expected input → expected output)
- Boundary values (zero, empty, max, minimum valid)
- Error cases (invalid input, missing required fields, wrong types)
- Edge cases specific to the domain (e.g. nil blocks, empty message threads, unknown block types)

```go
func TestFoo(t *testing.T) {
    tests := []struct {
        name    string
        input   Bar
        want    Baz
        wantErr bool
    }{
        {name: "happy path", ...},
        {name: "empty input", ...},
        {name: "nil field returns error", ..., wantErr: true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            t.Parallel()
            got, err := Foo(tt.input)
            if (err != nil) != tt.wantErr {
                t.Fatalf("Foo() error = %v, wantErr %v", err, tt.wantErr)
            }
            if !tt.wantErr && got != tt.want {
                t.Errorf("Foo() = %v, want %v", got, tt.want)
            }
        })
    }
}
```

**Integration tests** — Write integration tests (tagged `//go:build integration`) for any code that crosses a process boundary: HTTP providers, database queries, filesystem operations, TEE attestation. Integration tests live in `*_integration_test.go` files and are excluded from the default `go test ./...` run. Run them explicitly with `go test -tags integration -race ./...`.

**Fuzzing** — For any function that parses external input, write a fuzz target: `go test -fuzz=FuzzXxx ./pkg -fuzztime=30s`.

**Security checks** — Run `make secure` before every commit. It runs `lint` (gofmt check + vet + staticcheck + gosec) and `vuln` (go mod verify + govulncheck).

## Code Rules

- **Strict typing everywhere.** Never use `any` or `interface{}` except at explicit serialization boundaries (JSON unmarshal, plugin APIs). Immediately narrow to a concrete type; never pass `any` deeper into business logic. No untyped magic numbers or strings — use named constants or typed enums. Prefer named types (`type UserID string`) over bare primitives when the value has domain meaning.
- All domain concepts are typed structs — no `map[string]interface{}` for domain data.
- Return errors explicitly; never swallow them with `_`.
- Use typed or sentinel errors for public failures that callers need to classify, recover from, or inspect. Use wrapped ordinary errors for contextual failures that callers only report.
- Keep packages shallow and cohesive; avoid circular imports.
- Introduce interfaces when a consumer boundary or multiple implementations justify them.
- Split long functions when doing so clarifies ownership, invariants, or control flow. Do not optimize for an arbitrary line count.
