# Test strategy

## Evidence model

The launcher has three separable contracts:

| Layer | Evidence | Scope |
| --- | --- | --- |
| Unit | `internal/config/config_test.go` and the other package tests | defaults, YAML persistence, error fallback, and pure command/permission rules |
| Property | `pgregory.net/rapid` in the `internal/config` and `internal/catalog` suites | lossless YAML persistence and normalization rules under random inputs |
| Contract | `test/features/launcher.feature`, run by `test/gherkin` | user-visible argv composition, preflight rules, and safe local-config defaults |
| CLI/TUI | `cmd/ai-launcher/main_test.go` and `internal/tui/tui_test.go` | flag aliases, shell-style argument parsing, section navigation, mount editing, help, and save shortcuts |

The command builder is intentionally pure, so the contract suite verifies the
argv without starting a terminal, agent, container, or network service. This
keeps the normal test path deterministic and safe to run locally and in CI.

## Commands

```bash
make build           # compiles bin/ai-launcher from the CLI entry point
make test            # complete and deterministic Go suite
make fmt             # formats all Go sources
make lint            # go vet on all packages
make test-unit       # tests of the logic packages (config, catalog, launcher)
make test-property   # property-based tests (rapid)
make test-gherkin    # executable launcher-contract scenarios
make test-race       # full suite with -race -shuffle=on
make test-coverage   # atomic coverage profile + 90% gate on the logic
make test-mutation   # optional mutation check; skips explicitly when absent
make test-all        # all deterministic checks, then the optional mutation
```

`go test ./...` remains the baseline deterministic command. The project uses
a small in-repo Gherkin reader instead of a BDD dependency; feature files use
`Feature`, `Scenario`, `Given`, `When`, `Then`, `And`, and YAML/argv blocks
in triple quotes.

## Coverage and mutation testing

`test-coverage` uses Go's atomic instrumentation, prints per-function
coverage, and enforces a **minimum gate of 90% aggregated line coverage** on
the logic packages: `internal/config`, `internal/catalog`, and
`internal/launcher`. Only override `COVERAGE_MIN` for diagnosis; CI and merge
validation use the default gate. The commit hooks express the same boundary as
the `COVERAGE_EXCLUDE` regex in `.ai-standards.env` — a regex rather than a
package list, so the hook can race-test `./...` while measuring only the logic
packages.

Go does not expose native branch coverage, so the suite explicitly targets
both outcomes of the configuration/default decisions; a branch percentage
must not be inferred from the line total. `internal/tui` and the PTY
execution stay out of the gate because they depend on an interactive terminal
and spawned processes. They remain subject to build, vet, `-race`, and
smoke/manual checks; the pure command-assembly contract is covered by the
unit and Gherkin suites.

Mutation tests are deliberately optional. If a developer or a dedicated CI
image has `go-mutesting` installed, `make test-mutation` runs against
`internal/config`; otherwise it reports an explicit, successful skip and
never downloads tools. Suggested one-time local install:

```bash
go install github.com/zimmski/go-mutesting/cmd/go-mutesting@latest
```

Pin that tool in a CI image before making the mutation score a merge gate. Do
not make the normal `go test ./...` depend on it.

## Known regression expectations

The suite treats an omitted `options.jail` or `options.memory` key as the
safe default (`true`), preserving explicitly written `false` values. These
are security-relevant persistence contracts. If any of these regression tests
fails, release or merge must be blocked until the configuration
implementation — not the test — is fixed.
