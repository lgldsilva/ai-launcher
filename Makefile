GO ?= go
COVERAGE_FILE ?= coverage.out
COVERAGE_PACKAGES ?= ./internal/config ./internal/catalog ./internal/launcher
COVERAGE_MIN ?= 90
DIST_DIR ?= dist

.PHONY: build build-linux build-macos build-windows build-release test fmt lint test-unit test-property test-gherkin test-coverage test-mutation test-all

build:
	$(GO) build -buildvcs=false -o bin/ai-launcher ./cmd/ai-launcher

build-linux:
	@mkdir -p $(DIST_DIR)
	GOOS=linux GOARCH=amd64 $(GO) build -buildvcs=false -o $(DIST_DIR)/ai-launcher-linux-amd64 ./cmd/ai-launcher
	GOOS=linux GOARCH=arm64 $(GO) build -buildvcs=false -o $(DIST_DIR)/ai-launcher-linux-arm64 ./cmd/ai-launcher

build-macos:
	@mkdir -p $(DIST_DIR)
	GOOS=darwin GOARCH=amd64 $(GO) build -buildvcs=false -o $(DIST_DIR)/ai-launcher-darwin-amd64 ./cmd/ai-launcher
	GOOS=darwin GOARCH=arm64 $(GO) build -buildvcs=false -o $(DIST_DIR)/ai-launcher-darwin-arm64 ./cmd/ai-launcher

build-windows:
	@mkdir -p $(DIST_DIR)
	GOOS=windows GOARCH=amd64 $(GO) build -buildvcs=false -o $(DIST_DIR)/ai-launcher-windows-amd64.exe ./cmd/ai-launcher
	GOOS=windows GOARCH=arm64 $(GO) build -buildvcs=false -o $(DIST_DIR)/ai-launcher-windows-arm64.exe ./cmd/ai-launcher

build-release: build-linux build-macos build-windows

test:
	$(GO) test ./...

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

lint:
	$(GO) vet ./...

test-unit:
	$(GO) test ./internal/config ./internal/catalog ./internal/launcher

test-property:
	$(GO) test ./internal/config -run '^TestProperty' -count=1

test-gherkin:
	$(GO) test ./test/gherkin -run '^TestGherkin' -count=1

test-coverage:
	$(GO) test -covermode=atomic -coverpkg=github.com/lgldsilva/ai-launcher/internal/config,github.com/lgldsilva/ai-launcher/internal/catalog,github.com/lgldsilva/ai-launcher/internal/launcher -coverprofile=$(COVERAGE_FILE) $(COVERAGE_PACKAGES)
	@logic_profile=$$(mktemp); \
	trap 'rm -f "$$logic_profile"' EXIT; \
	awk 'NR == 1 || $$1 !~ /internal\/launcher\/(executor|replace_.*)\.go:/' $(COVERAGE_FILE) > "$$logic_profile"; \
	$(GO) tool cover -func="$$logic_profile"; \
	coverage=$$($(GO) tool cover -func="$$logic_profile" | awk '/^total:/{gsub("%", "", $$3); print $$3}'); \
	awk -v coverage="$$coverage" -v minimum="$(COVERAGE_MIN)" 'BEGIN { if (coverage + 0 < minimum + 0) { printf "FAIL: logic coverage %.1f%% is below %.1f%%\n", coverage, minimum; exit 1 } printf "PASS: logic coverage %.1f%% meets %.1f%% gate\n", coverage, minimum }'

# Mutation testing is intentionally optional: it is useful locally or in a
# dedicated CI image, but must not add a download or a flaky tool requirement.
test-mutation:
	@if command -v go-mutesting >/dev/null 2>&1; then \
		go-mutesting ./internal/config/...; \
	else \
		printf '%s\n' 'SKIP: optional go-mutesting is not installed; see docs/TEST_STRATEGY.md'; \
	fi

test-all: test-unit test-property test-gherkin test-coverage test-mutation
