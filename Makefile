GO ?= go
COVERAGE_FILE ?= coverage.out
COVERAGE_PACKAGES ?= ./internal/config ./internal/catalog ./internal/launcher ./internal/container
COVERAGE_MIN ?= 90
DIST_DIR ?= dist
# Build metadata injected into cmd/ai-launcher's version/commit/date vars.
# Kept as shell command substitutions so `make build` stays honest even when
# the variables are evaluated in a recipe. GoReleaser injects the same vars
# with its own template values for release builds.
LDFLAGS ?= -ldflags "-X main.version=$$(git describe --tags --always --dirty 2>/dev/null || echo dev) -X main.commit=$$(git rev-parse --short HEAD 2>/dev/null || echo none) -X main.date=$$(date -u +%Y-%m-%d)"
# Quality tools run through `go run` so no system-wide install is required;
# override with the installed binary to speed up local runs (for example
# `make lint-full GOLANGCI_LINT=golangci-lint`). Versions are pinned (not
# @latest) so a newly published upstream tag cannot execute on the next
# full-gate run without a deliberate repository change. Keep golangci-lint
# aligned with .github/workflows/ci.yml (version: v2.12).
GOLANGCI_LINT ?= $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
GOSEC ?= $(GO) run github.com/securego/gosec/v2/cmd/gosec@v2.28.0
GOVULNCHECK ?= $(GO) run golang.org/x/vuln/cmd/govulncheck@v1.6.0
SHFMT ?= $(GO) run mvdan.cc/sh/v3/cmd/shfmt@v3.13.1
GORELEASER ?= $(GO) run github.com/goreleaser/goreleaser/v2@v2.13.0
MUTATION_IMAGE ?= gogremlins/gremlins@sha256:1a026981a9155871ccaae7101e5ff8dc3a62616f6e054ace07336d5b559d5efe
MUTATION_BASE ?= origin/main
MUTATION_EFFICACY_MIN ?= 70
MUTATION_COVERAGE_MIN ?= 90
MUTATION_OUTPUT ?= .mutation/gremlins.json

.PHONY: build build-linux build-macos build-windows build-release release-checksums release-local test fmt lint lint-full lint-dist sec sec-static sec-vuln test-unit test-property test-gherkin test-race test-coverage test-mutation-script test-mutation test-all

build:
	$(GO) build -buildvcs=false $(LDFLAGS) -o bin/ai-launcher ./cmd/ai-launcher

build-linux:
	@mkdir -p $(DIST_DIR)
	GOOS=linux GOARCH=amd64 $(GO) build -buildvcs=false $(LDFLAGS) -o $(DIST_DIR)/ai-launcher-linux-amd64 ./cmd/ai-launcher
	GOOS=linux GOARCH=arm64 $(GO) build -buildvcs=false $(LDFLAGS) -o $(DIST_DIR)/ai-launcher-linux-arm64 ./cmd/ai-launcher

build-macos:
	@mkdir -p $(DIST_DIR)
	GOOS=darwin GOARCH=amd64 $(GO) build -buildvcs=false $(LDFLAGS) -o $(DIST_DIR)/ai-launcher-darwin-amd64 ./cmd/ai-launcher
	GOOS=darwin GOARCH=arm64 $(GO) build -buildvcs=false $(LDFLAGS) -o $(DIST_DIR)/ai-launcher-darwin-arm64 ./cmd/ai-launcher

build-windows:
	@mkdir -p $(DIST_DIR)
	GOOS=windows GOARCH=amd64 $(GO) build -buildvcs=false $(LDFLAGS) -o $(DIST_DIR)/ai-launcher-windows-amd64.exe ./cmd/ai-launcher
	GOOS=windows GOARCH=arm64 $(GO) build -buildvcs=false $(LDFLAGS) -o $(DIST_DIR)/ai-launcher-windows-arm64.exe ./cmd/ai-launcher

build-release: build-linux build-macos build-windows

# Checksums cover every file already in dist/ except SHA256SUMS itself,
# sorted by filename so the file is reproducible. Verify with:
#   cd dist && sha256sum -c SHA256SUMS
release-checksums: build-release
	@cd $(DIST_DIR) && ls -1 | grep -vx 'SHA256SUMS' | LC_ALL=C sort | xargs sha256sum > SHA256SUMS

# Local fallback for cutting release artifacts without CI. The canonical
# local check of what release.yml publishes is the GoReleaser snapshot:
#   go run github.com/goreleaser/goreleaser/v2@v2.13.0 release --snapshot --clean --skip=publish
release-local: build-release release-checksums

test:
	$(GO) test ./...

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

lint:
	$(GO) vet ./...

lint-full:
	$(GOLANGCI_LINT) run ./...

# Gate for the distribution surface: the install script users curl-pipe and
# the release config that ships the binaries. shellcheck comes from the host
# (preinstalled on the CI runners); shfmt and goreleaser run via `go run`.
lint-dist:
	shellcheck install.sh scripts/mutation.sh scripts/mutation_test.sh
	$(SHFMT) -i 2 -ci -d install.sh scripts/mutation.sh scripts/mutation_test.sh
	$(GORELEASER) check

# The two halves are separate targets because CI runs them as separate jobs:
# a gosec finding and a vulnerable dependency are different failures with
# different owners, and splitting them keeps the pinned versions here instead
# of duplicated in the workflow.
sec: sec-static sec-vuln

# SAST. This is also what validates the #nosec annotations in the tree: an
# annotation that no longer sits on a finding is dead weight, and one that hides
# a real finding is only visible when the scanner actually runs.
sec-static:
	$(GOSEC) ./...

sec-vuln:
	$(GOVULNCHECK) ./...

test-unit:
	$(GO) test ./internal/config ./internal/catalog ./internal/launcher ./internal/container

test-property:
	$(GO) test ./internal/config ./internal/catalog -run '^TestProperty' -count=1

test-gherkin:
	$(GO) test ./test/gherkin -run '^TestGherkin' -count=1

test-race:
	$(GO) test -race -shuffle=on ./...

test-coverage:
	$(GO) test -covermode=atomic -coverpkg=github.com/lgldsilva/ai-launcher/internal/config,github.com/lgldsilva/ai-launcher/internal/catalog,github.com/lgldsilva/ai-launcher/internal/launcher,github.com/lgldsilva/ai-launcher/internal/container -coverprofile=$(COVERAGE_FILE) $(COVERAGE_PACKAGES)
	@logic_profile=$$(mktemp); \
	trap 'rm -f "$$logic_profile"' EXIT; \
	awk 'NR == 1 || $$1 !~ /internal\/launcher\/(executor.*|replace_.*)\.go:/' $(COVERAGE_FILE) > "$$logic_profile"; \
	$(GO) tool cover -func="$$logic_profile"; \
	coverage=$$($(GO) tool cover -func="$$logic_profile" | awk '/^total:/{gsub("%", "", $$3); print $$3}'); \
	awk -v coverage="$$coverage" -v minimum="$(COVERAGE_MIN)" 'BEGIN { if (coverage + 0 < minimum + 0) { printf "FAIL: logic coverage %.1f%% is below %.1f%%\n", coverage, minimum; exit 1 } printf "PASS: logic coverage %.1f%% meets %.1f%% gate\n", coverage, minimum }'

# Mutation testing runs in the pinned Gremlins container. The wrapper provisions
# hermetic command stubs for catalog discovery and forwards the host UID/GID so
# tests that inspect the launch identity remain meaningful.
test-mutation-script:
	./scripts/mutation_test.sh

test-mutation: test-mutation-script
	MUTATION_IMAGE="$(MUTATION_IMAGE)" \
	MUTATION_BASE="$(MUTATION_BASE)" \
	MUTATION_EFFICACY_MIN="$(MUTATION_EFFICACY_MIN)" \
	MUTATION_COVERAGE_MIN="$(MUTATION_COVERAGE_MIN)" \
	MUTATION_OUTPUT="$(MUTATION_OUTPUT)" \
		./scripts/mutation.sh

test-all: test-unit test-property test-gherkin test-race test-coverage lint lint-full sec test-mutation
