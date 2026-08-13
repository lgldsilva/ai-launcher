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
make test-unit       # tests of the logic packages (config, catalog, launcher, container)
make test-property   # property-based tests (rapid)
make test-gherkin    # executable launcher-contract scenarios
make test-race       # full suite with -race -shuffle=on
make test-coverage   # atomic coverage profile + 90% gate on the logic
make test-mutation-script # regression test for the mutation wrapper
make test-mutation   # containerized Gremlins mutation gate
make test-all        # all deterministic checks, including mutation
```

`go test ./...` remains the baseline deterministic command. The project uses
a small in-repo Gherkin reader instead of a BDD dependency; feature files use
`Feature`, `Scenario`, `Given`, `When`, `Then`, `And`, and YAML/argv blocks
in triple quotes.

## Coverage and mutation testing

`test-coverage` uses Go's atomic instrumentation, prints per-function
coverage, and enforces a **minimum gate of 90% aggregated line coverage** on
the logic packages: `internal/config`, `internal/catalog`, `internal/launcher`,
and `internal/container`. Only override `COVERAGE_MIN` for diagnosis; CI and merge
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

Mutation testing is a real gate and runs in the pinned
`gogremlins/gremlins` container. The runner is deliberately container-only:
it does not install a mutator on the host, and it mounts the checkout with the
host UID/GID so the report remains writable. It also creates hermetic command
stubs for catalog discovery, so host-installed agent CLIs do not change the
result.

The default command compares the checkout with `origin/main`, enforces 70%
test efficacy and 90% mutator coverage when mutation-eligible Go files changed,
and writes the detailed report to `.mutation/gremlins.json` (ignored by Git).
Workflow-only, documentation-only, and other diffs outside the mutation scope
are reported as a successful `SKIP` with a JSON report because no mutants exist
to measure. Override the base or thresholds only for diagnosis, for example:

```bash
MUTATION_BASE=HEAD~1 make test-mutation
MUTATION_EFFICACY_MIN=80 make test-mutation
```

CI fetches the complete history because the diff base is part of the test
contract. A missing Docker daemon or base ref is a hard failure when mutation
is applicable. Worktrees are supported by mounting the common Git directory
read-only and supplying the worktree `.git` file.

## Known regression expectations

The suite treats an omitted `options.jail` or `options.memory` key as the
safe default (`true`), preserving explicitly written `false` values. These
are security-relevant persistence contracts. If any of these regression tests
fails, release or merge must be blocked until the configuration
implementation — not the test — is fixed.
