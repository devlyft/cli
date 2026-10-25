# Tool versions are pinned here and in .github/workflows; keep them in sync.
GOLANGCI_LINT_VERSION ?= v2.15.0
GOVULNCHECK_VERSION   ?= v1.9.0
GORELEASER_VERSION    ?= v2.19.0

BIN     ?= bin/devlyft
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/devlyft/cli/internal/version.Version=$(VERSION)

.DEFAULT_GOAL := build
.PHONY: build test cover cover-check lint fmt vuln tidy generate vendor-specs check snapshot clean help

build: ## Build the devlyft binary into bin/
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/devlyft

test: ## Run unit tests with the race detector
	go test -race ./...

COVER_MIN     ?= 80
# Generated code, mocks and the test harness itself don't count.
COVER_EXCLUDE ?= /internal/(api/(gen|mock)|testutil)/

cover: ## Run tests and write coverage.out (all of internal/, across packages)
	go test -race -covermode=atomic -coverpkg=./internal/... -coverprofile=coverage.out ./...

cover-check: cover ## Fail if coverage of internal/ is below COVER_MIN percent
	go run ./hack/covercheck -profile coverage.out -min $(COVER_MIN) -exclude '$(COVER_EXCLUDE)' -v

lint: ## Run golangci-lint
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run

fmt: ## Format code
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) fmt

# govulncheck checks the code that ships. Like _test.go files, the test
# harness in internal/testutil is left out: it runs kin-openapi's validator
# over our own fixtures, and kin-openapi is pinned by oapi-codegen.
VULN_PACKAGES = $(shell go list ./... | grep -v '/internal/testutil/')

vuln: ## Check dependencies for known vulnerabilities in shipped code
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) $(VULN_PACKAGES)

tidy: ## Tidy go.mod and go.sum
	go mod tidy

generate: ## Regenerate API clients and mocks from internal/api/specs
	go generate ./internal/api/... ./internal/auth/...

vendor-specs: ## Re-vendor pruned specs from the platform repo (needs access; REF=<sha>)
	hack/vendor-specs.sh $(REF)
	$(MAKE) generate

check: lint cover-check vuln ## Run everything CI runs

snapshot: ## Build unsigned release archives for every platform into dist/
	go run github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION) release --snapshot --clean --skip=sign,sbom

clean:
	rm -rf bin dist coverage.out

help: ## Show this help
	@grep -E '^[a-z]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-8s %s\n", $$1, $$2}'
