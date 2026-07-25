# Design decisions

Thematic decisions, not numbered ADRs. Each one follows **Decision → Why →
How → Trade-offs**, anchored on the concrete incident that forced it.

## Orchestrate upstream ai-jail/ai-memory instead of reimplementing

**Decision.** The launcher never forks or embeds sandbox or memory: it
invokes the third-party binaries (`akitaonrails/ai-jail`,
`akitaonrails/ai-memory`).

**Why.** Both tools have a fast release cadence and an active author;
reimplementing bubblewrap/sandbox-exec or the ai-memory MCP protocol would be
a fork forever behind. The price of coupling is CLI drift — and it is real:
the modeling of the ai-jail v1.15 flags (`--lockdown`, `--private-home`,
`--no-*` toggles for default-on capabilities) only exists because the CLI
changed under us.

**How.** Explicit contract: the Gherkin suite in
`test/features/launcher.feature` (run by `test/gherkin`) locks the exact argv
composition — `ai-jail → ai-memory run → harness` order, `--no-*` forms,
workstream/workspace/project scope. If upstream changes, the contract breaks
in CI, not on the user's machine. The jail flags live in a declarative
structure (tri-state `config.JailFlags`), so absorbing a new ai-jail version
is adding a row to a table, not rewriting `if`s.

**Trade-offs.** We depend on the third-party CLI surface and the release
asset formats (e.g. ai-jail publishes only linux-x86_64 and macos-aarch64).
In exchange, we never carry sandbox code of our own.

## Data-driven per-harness parameters

**Decision.** Harness-specific flags are declared in the catalog
(`agents[].params`: `name`, `flag`, `takes_value`) and filled via `--param
name=value` or the parameter row in the TUI — never by per-agent special-case
code.

**Why.** The trigger was Kimi: besides `--model`, it accepts `--query`
(initial query). Encoding that in Go would mean rebuilding the launcher for
every new flag of any harness — unsustainable with ~20 agents in the catalog.

**How.** `launcher.Build` emits the declared parameters in declaration order;
undeclared names are ignored in the argv and reported by the validator
(`param-not-declared`). The TUI renders one text row per parameter declared
by the selected agent.

**Trade-offs.** The catalog can go stale relative to the harness (same drift
problem as the previous item, same mitigation: data, not code).
`--extra-args` remains as the escape valve for anything undeclared.

## Windows as a first-class citizen, without jail

**Decision.** Windows gets a native binary and the full flow, but the jail is
disabled automatically — with a warning, never with an error.

**Why.** ai-jail will "probably never" support Windows (bubblewrap and
sandbox-exec are Unix mechanisms). Blocking the launcher on Windows because
of that would expel the user; hiding the absence of the sandbox would be
dishonest.

**How.** `launcher.ConstrainToPlatform` forces `UseJail=false` on Windows and
turns off every permission that requires jail (ssh, gh, docker, gpu),
emitting the `jail-unsupported-windows` warning. The TUI hides the Jail
toggle and filters the dependent permissions. The installer skips ai-jail and
installs ai-memory from the native `ai-memory-windows-x86_64.zip` asset. The
recommended path for sandboxing on Windows is WSL2.

**Trade-offs.** On Windows the harness runs with all the user's privileges —
this is stated explicitly in the README security section. We accept the
asymmetry because the alternative (no Windows) is worse.

## Mandatory SHA-256 checksum on installs — and a stricter one on self-update

**Decision.** Every install from a GitHub release requires a verifiable
checksum; without one, the install fails. Self-update (`ai-launcher upgrade`)
and the `install.sh` curl installer go further: the release `checksums.txt`
is mandatory and there is no `allow_unverified` escape hatch.

**Why.** The launcher downloads and executes third-party binaries on the
user's behalf — that is exactly where a supply-chain attack gets in. Release
tags and assets are mutable; "downloaded, ran" without verification is
unacceptable in a tool whose purpose is security. Self-update is worse: it
overwrites the very executable you will run next, so a silent skip of
verification (the behavior we rejected from other upgraders) is not an
option.

**How.** `internal/installer` looks for the digest in `.sha256`,
`.sha256sum`, `checksums.txt`, `SHA256SUMS`, or the release body, and only
extracts after checking. The `allow_unverified: true` valve exists for
recipes with another trusted form of verification, but it is an explicit
operator decision, recorded in the YAML. `internal/selfupdate` and
`install.sh` reuse the same checksum parsing but treat a missing
`checksums.txt`, a missing entry, or a mismatch as a hard error, then replace
the binary atomically (rename on POSIX, move-aside + rollback on Windows).

**Trade-offs.** Projects that do not publish checksums are not installable by
recipe (they remain executable if already on the PATH). We prefer that to
installing blindly. Self-update additionally requires the launcher to live in
a user-writable directory (e.g. `~/.local/bin`) — the error message says so
instead of suggesting sudo.

## 90% coverage gate only on the pure-logic packages

**Decision.** The 90% gate measures only `internal/config`,
`internal/catalog`, and `internal/launcher` (excluding the PTY executor and
the `replace_*.go` files). `internal/tui` and `internal/installer` stay out
of the denominator.

**Why.** Line coverage in code coupled to an interactive terminal and to
spawned processes measures theater, not safety — forcing 90% there would
generate fragile, fake tests. What really cannot break is the pure logic:
config persistence, safe defaults, argv composition.

**How.** `make test-coverage` (and the CI `test` job) filter the profile with
`awk` and fail below 90% on the filtered total; `COVER_PKGS` in
`.ai-standards.env` repeats the same boundary for the hooks. TUI, installer,
and executor are covered by `go test -race -shuffle=on ./...` (race-only) and
by the Gherkin contract suite.

**Trade-offs.** Interaction bugs (keys, terminal) depend on race tests and
manual verification. It is the honest split: a gate where the metric means
something.

## The TUI executes for real

**Decision.** `Enter` in the TUI launches the harness; the TUI is not a
command generator.

**Why.** The previous version printed the argv and exited — the user had to
copy and paste, which defeats the purpose of a "launcher" and invited
transcription error precisely in the sensitive part (wrapper order).

**How.** The TUI keeps all state in `launcher.LaunchConfig` and returns the
confirmed configuration; `main` builds the argv with the same
`launcher.Build` as the CLI and executes via `exec` (process replacement) or
PTY. Explicit dry-run (`--dry-run`, `d`/`Ctrl+D` inside the TUI) is the only
path that only prints.

**Trade-offs.** Executing for real demands serious preflight — hence the
`launcher.Validator` with stable issue codes and non-fatal warnings (e.g.
jail on Windows), instead of fail-fast on the first problem.

## What we are NOT doing (yet)

- **Native sandbox on Windows.** Depends on upstream (ai-jail "probably
  never"); the supported path is WSL2.
- **GUI.** The TUI is the interface; there is no graphical interface planned.
- **Plugin system.** Extensibility is by data (YAML catalog), not by loadable
  code.
- **Own harness registry.** The catalog is local and per-machine; we neither
  publish nor consume a central registry.
- **Package managers.** The installer talks only to GitHub Releases;
  `npm`/`apt`/`brew` are out by decision.

## Mistakes to avoid

- [ ] Do not reimplement ai-jail/ai-memory functionality — absorb upstream and update the Gherkin contract.
- [ ] Do not add `if agent == "x"` in the builder — declare a `params:` entry in the catalog.
- [ ] Do not emit jail flags as loose strings — use `config.JailFlags` (tri-state) to respect ai-jail defaults.
- [ ] Do not install anything without a verifiable checksum — do not "work around" it with `allow_unverified` in the default catalog.
- [ ] Do not weaken self-update verification — a missing `checksums.txt` is a hard error, never a silent skip.
- [ ] Do not put `internal/tui` or the PTY executor in the coverage gate denominator.
- [ ] Do not write tokens to logs, argv, or local configs — token only via the child process env.
- [ ] Do not change the canonical `ai-jail → ai-memory run → harness` order — if it changes, the contract suite must fail first.
- [ ] Do not turn the TUI back into a command generator — dry-run is the explicit print-only path.
- [ ] Do not treat the Windows jail warning as an error — degradation with a warning is the correct behavior.
