.PHONY: test fmt fmt-check staticcheck lint vuln secure fuzz packs

GO ?= go

# Module's own package dirs. go list stops at the nested module boundary
# (cmd/mpqt has its own go.mod), so it is never touched by these targets.
GO_DIRS = $(shell go list -f '{{.Dir}}' ./...)

test:
	go test -race ./...

# Smoke-run the whole shipped YAML pack corpus offline through the CLI:
# strict load + lint + digest check on every pack, plus a scripted-fixture
# execution of every programmatic table (judge tables are skipped, no network,
# no cost). Complements the compiled TestShippedCorpus guard in pkg/packfile.
packs:
	cd cmd/mpqt && GOWORK=off $(GO) build -trimpath -o mpqt .
	cmd/mpqt/mpqt validate --execute packs/*

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
