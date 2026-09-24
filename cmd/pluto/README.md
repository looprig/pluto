# pluto (command)

`github.com/looprig/pluto/cmd/pluto` is the `pluto` command-line tool for
[Pluto](../../README.md), the model qualification layer over
`github.com/looprig/eval`. It is a nested module inside the `pluto` repository,
released on its own cadence at `cmd/pluto/vX.Y.Z` tags, so its version is
independent of the root module's.

The binary is only a composition root: it wires `pkg/cli.App` from the root
module to real inference clients and token counters built with
`github.com/looprig/llm/auto`. It is the only place in the Pluto repository
that imports `github.com/looprig/llm`, which keeps that dependency tree out of
the root module's graph.

## Install

```sh
go install github.com/looprig/pluto/cmd/pluto@latest
```

Or, from a checkout of the repository, `make build` builds `./cmd/pluto/pluto`.

## Commands

```
pluto init <name> [dir]   scaffold a custom pack directory
pluto validate [dir...]   strict load + lint + digest check (+ optional --execute)
pluto schema              print the pack file JSON Schema
pluto evaluators          list evaluator kinds, options, and evidence requirements
pluto gen                 generate candidate scenarios for one table
pluto run                 execute packs against a live target and gate on a profile
pluto compare             gate a candidate report against an incumbent report
```

Run `pluto <command> -h` for command-specific flags. The root
[README](../../README.md) walks through each command.

`gen` and `run` read the provider's API key from the environment variable
named after the provider: the provider name upper-cased, `-` replaced by `_`,
plus `_API_KEY`.

## Where it sits

Depends on `github.com/looprig/core`, `inference`, `llm`, and the published
`github.com/looprig/pluto` root module (a released version, not the working
tree).

## Development

The Go baseline is 1.26.8. From this directory:

```sh
GOWORK=off go test ./...
CGO_ENABLED=0 GOWORK=off go build -trimpath -o pluto .
```

## License

Apache License 2.0. See the repository [LICENSE](../../LICENSE).
