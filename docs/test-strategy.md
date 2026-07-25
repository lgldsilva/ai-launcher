# Test strategy

## Evidence model

The launcher has three separable contracts:

| Layer | Evidence | Scope |
| --- | --- | --- |
| Unit | `internal/config/config_test.go` plus existing package tests | defaults, YAML persistence, error fallback, and pure command/permission rules |
| Property | `testing/quick` in the config unit suite | randomized lossless YAML persistence for safe launcher selections |
| Contract | `test/features/launcher.feature`, executed by `test/gherkin` | user-visible argv composition, preflight rules, and safe local-config defaults |
| CLI/TUI | `cmd/ai-launcher/main_test.go` and `internal/tui/tui_test.go` | legacy flag aliases, shell-like argument parsing, section navigation, mount editing, help, and save shortcut |

The command builder is intentionally pure, so the contract suite verifies argv
without starting a terminal, agent, container, or network service. This keeps
the normal test path deterministic and safe for local and CI execution.

## Commands

```bash
make build           # build bin/ai-launcher from the CLI entry point
make test            # deterministic complete Go suite
make fmt             # format all Go sources
make lint            # run go vet across all packages
make test-unit       # package-level tests
make test-property   # standard-library property checks only
make test-gherkin    # executable launcher contract scenarios
make test-coverage   # atomic Go coverage profile and function report
make test-mutation   # optional local mutation check; skips when unavailable
make test-all        # all deterministic checks, then optional mutation check
```

`go test ./...` remains the baseline deterministic command. The project uses a
small in-repository Gherkin reader instead of a BDD dependency; feature files
use standard `Feature`, `Scenario`, `Given`, `When`, `Then`, `And`, and
triple-quoted YAML/argv blocks.

## Coverage and mutation testing

`test-coverage` uses Go's atomic instrumentation, prints per-function line
coverage, and enforces a **minimum 90% aggregate line-coverage gate** for the
launcher logic packages: `internal/config`, `internal/catalog`, and
`internal/launcher`. Override `COVERAGE_MIN` only for diagnostic work; CI and
merge validation must use the default gate.

Go does not expose native branch coverage, so the suite targets both outcomes
of configuration/defaulting decisions explicitly; a branch percentage must not
be inferred from the line total. `internal/tui` and PTY execution are outside
the coverage gate because they depend on an interactive terminal and spawned
processes. They remain subject to build, vet, and targeted smoke/manual checks;
the pure command-building contract is covered by the unit and Gherkin suites.

Mutation testing is deliberately optional. If a developer or dedicated CI image
has `go-mutesting` installed, `make test-mutation` runs it against
`internal/config`; otherwise it reports a successful, explicit skip and never
downloads tooling. Suggested one-time local installation:

```bash
go install github.com/zimmski/go-mutesting/cmd/go-mutesting@latest
```

Pin that tool in a CI image before making mutation score a merge gate. Do not
make normal `go test ./...` depend on it.

## Known regression expectations

The suite treats an omitted `options.jail` or `options.memory` key as the safe
default (`true`), while preserving explicitly written `false` values. These are
security-relevant persistence contracts. If either regression test fails,
release or merge should be blocked until the configuration implementation—not
the test—is corrected.
