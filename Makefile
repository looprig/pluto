.PHONY: test fmt fmt-check staticcheck lint vuln secure fuzz packs build run

GO ?= go

# The CLI binary. cmd/pluto is a nested Go module (its own go.mod) and the only
# place that imports github.com/looprig/llm, so it is built with GOWORK=off
# from inside its own directory; the output path below is repo-root-relative.
BIN = cmd/pluto/pluto

# Module's own package dirs. go list stops at the nested module boundary
# (cmd/pluto has its own go.mod), so it is never touched by these targets.
GO_DIRS = $(shell go list -f '{{.Dir}}' ./...)

test:
	go test -race ./...

# Build the Pluto CLI binary (CGO off + -trimpath per repo policy). The result
# is $(BIN); add cmd/pluto to PATH, or invoke it as ./$(BIN), or use `make run`.
build:
	cd cmd/pluto && CGO_ENABLED=0 GOWORK=off $(GO) build -trimpath -o pluto .

# Run a qualification against a live target. Override any variable on the
# command line, e.g.:
#   make run PACKS=packs/safety-conduct
#   make run MANIFEST=my-target.yaml PROFILE=prod.yaml CONFIG=judge.yaml PACKS=packs/tool-use
# CONFIG is only needed by packs that use judge evaluators; passing it for a
# programmatic-only pack is harmless. Extra flags go through FLAGS, e.g.:
#   make run FLAGS='--max-rpm 30 --require restricted'
MANIFEST ?= target.yaml
PROFILE  ?= profile.yaml
CONFIG   ?= gen.yaml
PACKS    ?= packs/core-capability
FLAGS    ?=
run: build
	./$(BIN) run --manifest $(MANIFEST) --profile $(PROFILE) --config $(CONFIG) --packs $(PACKS) $(FLAGS)

# Smoke-run the whole shipped YAML pack corpus offline through the CLI:
# strict load + lint + digest check on every pack, plus a scripted-fixture
# execution of every programmatic table (judge tables are skipped, no network,
# no cost). Complements the compiled TestShippedCorpus guard in pkg/packfile.
packs: build
	./$(BIN) validate --execute packs/*

# Format the whole module in place.
fmt:
	gofmt -w $(GO_DIRS)

# Fail (non-zero exit) if any tracked Go file is not gofmt-clean. Wired into lint.
fmt-check:
	@unformatted=$$(gofmt -l $(GO_DIRS)); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed (run 'make fmt'):"; echo "$$unformatted"; exit 1; \
	fi

lint: fmt-check
	go vet ./...
	$(MAKE) staticcheck
	go tool gosec $(GO_DIRS)

staticcheck:
	@GO="$(GO)" ./scripts/run-staticcheck.sh

vuln:
	go mod verify
	go tool govulncheck ./...

secure: lint vuln

fuzz:
	@echo "Usage: go test -fuzz=FuzzXxx ./path/to/pkg -fuzztime=30s"

# --- standardized check surface -------------------------------------------
# One target, the same set of checks, in every module. CI calls exactly this,
# so a check can no longer pass locally and be silently absent in CI (or the
# reverse). The lint/security tools are pinned by this module's go.mod tool directives.
#
# CHECK_GO_DIRS scopes gosec: gosec is NOT module-aware, so a bare ./... is a
# filesystem walk that descends into nested .worktrees/ checkouts, which are
# separate modules. go vet and staticcheck are module-aware and need no scope.
CHECK_GO_DIRS = $(shell GOWORK=off go list -f '{{.Dir}}' ./...)
# CHECK_GO_FILES is what gofmt gets. Never hand it CHECK_GO_DIRS: gofmt RECURSES
# into directory operands, so for a module with a root package it would walk the
# whole tree, nested .worktrees/ checkouts included.
CHECK_GO_FILES = $(foreach dir,$(CHECK_GO_DIRS),$(wildcard $(dir)/*.go))

vet:
	GOWORK=off go vet ./...

check-staticcheck:
	GOWORK=off go tool staticcheck ./...

check-gosec:
	GOWORK=off go tool gosec -quiet $(CHECK_GO_DIRS)

check-vuln:
	GOWORK=off go mod verify
	GOWORK=off go tool govulncheck ./...

check: fmt-check vet check-staticcheck check-gosec check-vuln test build

.PHONY: check check-staticcheck check-gosec check-vuln fmt fmt-check vet test build
