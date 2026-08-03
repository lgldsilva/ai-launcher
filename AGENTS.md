# AGENTS.md — instructions for AI agents in this repository

## What this project is

TUI/CLI launcher in Go that orchestrates AI CLIs composed from two
third-party tools: `ai-jail` (sandbox) and `ai-memory` (memory/sessions). The
canonical chain is `ai-jail [jail flags] ai-memory run [wrapper flags]
<harness> [native args]`. Details: `docs/ARCHITECTURE.md`.

## Build and tests

| Command | Use |
| --- | --- |
| `make build` | Compiles `bin/ai-launcher` |
| `make test` | Deterministic Go suite (`go test ./...`) |
| `make test-all` | Full gate: unit + property + gherkin + race + coverage + lint + sec + mutation |
| `make test-coverage` | 90% line gate on the logic packages |
| `make test-gherkin` | Contract suite (`test/features/launcher.feature`) |
| `make lint` / `make lint-full` | `go vet` / golangci-lint |
| `make lint-dist` | ShellCheck + shfmt on `install.sh`, `goreleaser check` |

**Coverage gate**: measures only `internal/config`, `internal/catalog`, and
`internal/launcher` (excluding `executor.go` and `replace_*.go`); the minimum
is 90% (`COVERAGE_MIN`). Everything else stays **out of the denominator** —
`cmd/`, `internal/tui`, `internal/installer`, `internal/selfupdate`,
`internal/cmd`, `test/`, and the PTY executor — and is covered by
`go test -race -shuffle=on ./...` (race-only). The `COVERAGE_EXCLUDE` regex is
authoritative; this list only describes it. The commit hooks read the same boundary from
the `COVERAGE_EXCLUDE` regex in `.ai-standards.env`, and SonarCloud from
`sonar.coverage.exclusions`; change the three together. Do not move
UI/execution packages into the gate.

## Git and hooks (ai-standards)

- **Conventional Commits** required (`feat:`, `fix:`, `docs:`, `test:`,
  `refactor:`…), enforced by hook.
- **Never commit or push directly to `main`.** Branch from an up-to-date
  `main`.
- **A breaking change must say so in the squash commit** — `!` after the type
  or a `BREAKING CHANGE:` footer. `autotag.yml` computes the next version from
  those commits with `svu`, and GoReleaser generates the release notes from the
  same log. v0.3.0 carried a config-schema break and an argv change under a
  plain `feat(memory):` subject: it was tagged as a minor and its release notes
  never mentioned either. The `CHANGELOG.md` entry has to land in the *same*
  merge, not the one after — the tag is cut on push to `main`.
- **One concern per PR.** v0.3.0 was six planned PRs squashed into one, which
  is how the highest-value change of that week (the ai-jail 1.16 floor and the
  Docker argv) ended up invisible in both the log and the release notes.
- **Never** set `AI_STANDARDS_SKIP` or a variant without explicit human
  authorization — the hooks enforce coverage and quality automatically.

## Contract with upstream (ai-jail / ai-memory)

The Gherkin suite (`test/features/`, in-repo reader in `test/gherkin/`) is the
drift detector for the third-party CLIs: it locks the exact argv composition
(wrapper order, ai-jail v1.16 `--no-*` toggles, `ai-memory run` scope). If a
contract test fails after a change, **the code is wrong, not the test** —
unless upstream changed, in which case the contract is updated together with
the code, in the same commit.

Out of contract scope (covered by unit tests instead, because they depend on
the real filesystem or the process environment rather than on argv): the
home-symlink auto-mounts and their denylist (ARCHITECTURE invariant 9,
`internal/launcher/symlink_test.go`), the automatic `--no-hide-config` for a
symlinked project `.ai-jail` (`cmd/ai-launcher/launch_test.go`), and the
`AI_MEMORY_NATIVE_BIN` export (ARCHITECTURE invariant 8,
`internal/launcher/builder_test.go`).

## Quality tools

golangci-lint, gosec, and govulncheck run via pinned `go run ...@vX.Y.Z`
using the Makefile variables (`GOLANGCI_LINT`, `GOSEC`, `GOVULNCHECK`) —
nothing needs to be installed system-wide; override the variable with the
installed binary to speed things up (e.g.
`make lint-full GOLANGCI_LINT=golangci-lint`). Bump pins deliberately; do not
use `@latest`. Lint config: `.golangci.yml` (errcheck, gocognit, gosec,
govet, revive, staticcheck…).

## Sonar and CI

- SonarCloud (sonarcloud.io) runs in CI as the `sonar` job of
  `.github/workflows/ci.yml`, gated on the `SONAR_TOKEN` secret; org/project
  come from the `SONAR_ORGANIZATION` / `SONAR_PROJECT_KEY` repo variables
  (defaults `lgldsilva` / `lgldsilva_ai-launcher`).
  `sonar-project.properties` is host-agnostic — host, org, key, and token are
  passed as `-D` flags by CI. The old homelab SonarQube CE and the vendored
  `scripts/sonar` are gone; do not recreate them.
- CI is GitHub Actions (`.github/workflows/ci.yml`), with actions pinned by
  SHA. **Jenkins is decommissioned — never** create a Jenkinsfile or webhook.
  There is no Gitea workflow either — GitHub is the only forge.
- See `docs/cicd.md` for the full pipeline and the release process.

## Documentation conventions

- **English** in all documentation (README, docs/, this file).
- **ASCII box-and-arrow diagrams only** — never mermaid.
- Files in `docs/` in **kebab-case**; anchors in CAPS: `README.md`,
  `docs/ARCHITECTURE.md`.
- Tables for structured data (flags, config, matrices); no table of contents
  (TOC); no decorative emoji; `License` is always the last H2 of the README.
- Every documented flag/option must exist in the code — check
  `cmd/ai-launcher/main.go` and `internal/config/config.go` before
  documenting.
