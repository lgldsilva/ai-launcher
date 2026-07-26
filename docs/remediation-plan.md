# Remediation Plan

Audit findings and a phased execution plan. This document is written so that any
agent harness (Claude Code, Codex, Crush, OpenCode, Gemini CLI, ...) can pick up
a single item and execute it without repeating the investigation.

The audit compared the code against the upstream actually installed on the
maintainer's machine — `ai-jail 1.15.0` and `ai-memory 1.19.0` — not against
documentation alone. Findings marked **verified** were reproduced by running real
binaries; the observed output is quoted verbatim.

## Scope and non-goals

`ai-jail` and `ai-memory` are third-party projects by akitaonrails. This repo
only composes them. **No item in this plan changes upstream.** Every fix is a
change inside `ai-launcher` to match the real upstream surface.

Non-goals: rewriting the TUI, changing the catalog's purpose, adding a GUI,
replacing the config format, or reimplementing anything ai-jail/ai-memory already
does.

## How to use this document

Pick one item. Each item is a **behavior spec**, not an implementation dictate:
it states what must be true and how to prove it, and leaves the "how" to you.

Work one item per branch. Do not batch unrelated items — the dependency graph
below exists because some fixes are invisible until an earlier one lands.

## Ground rules

These come from the repository standards and are not optional.

| Rule | Detail |
| --- | --- |
| Branch | Branch from an up-to-date `main`. Never commit or push directly to `main` |
| Commits | Conventional Commits (`fix:`, `feat:`, `docs:`, `test:`, `refactor:`) |
| Hook bypass | **Forbidden** to set `AI_STANDARDS_SKIP` or any variant without explicit human authorization. If a gate fails, fix the cause or stop and ask |
| Red test first | Every fix starts with a regression test that **fails on current code**. Without the initial red there is no proof the test detects the defect |
| Coverage | 90% line and branch on `internal/config`, `internal/catalog`, `internal/launcher` (excluding `executor.go` and `replace_*.go`). Do not move UI/execution packages into the gate |
| Contract | If a contract test fails after a change, the code is wrong, not the test — unless upstream changed, in which case update both in the same commit |
| E2E before done | Validate with the real binary, not only `go test`. "Compiles" is not "works" |
| Review | Whoever implements must not be the only reviewer. Declare self-review explicitly if no independent review happened |
| Docs | English, kebab-case in `docs/`, ASCII diagrams only, tables for structured data, no TOC, no decorative emoji |

Honest status reporting, per repository convention:

| Status | Criterion |
| --- | --- |
| tested | Works E2E and an independent review passed |
| implemented | Compiles and unit tests pass, but no E2E smoke and no review |
| sketch | Stub, no-op, or plumbing without activation |

## Upstream surface reference

Verified on 2026-07-26 against the installed binaries. Use this table instead of
re-deriving the surface; re-verify only if the installed version changes.

### `ai-memory 1.19.0` — `run` subcommand

```
Usage: ai-memory run [OPTIONS] [HARNESS] [NATIVE_ARGS]...
```

Accepted `[HARNESS]` values — **exactly these eight**:

| Harness | Description |
| --- | --- |
| `claude` | Anthropic Claude Code |
| `codex` | OpenAI Codex CLI |
| `opencode` | OpenCode |
| `pi` | Pi coding agent |
| `crush` | Charmbracelet Crush |
| `omp` | Oh My Pi |
| `kimi` | Moonshot AI Kimi Code |
| `grok` | Grok Build CLI (xAI) |

Omitting the harness continues the newest managed or checkout-local session.
Anything else fails with `invalid value '<name>' for '[HARNESS]'`.

| Option | Effect |
| --- | --- |
| `--data-dir <DIR>` | Override data directory (also `AI_MEMORY_DATA_DIR`) |
| `--workspace <NAME>` | Workspace containing the workstream (default `default`) |
| `--config <PATH>` | Explicit config file (defaults to `<data_dir>/config.toml`) |
| `--project <NAME>` | Project override; defaults to the current repository project |
| `--workstream <NAME>` | Select an existing named workstream |
| `--new <NAME>` | Create and select a fresh named workstream |
| `--executable <PATH>` | Override the native harness executable path |
| `--yolo` | Disable native permission prompts via the harness's dangerous-mode option |
| `--fresh` | Start a new native session instead of resuming or adopting one |

`NATIVE_ARGS` are forwarded byte-for-byte and in order. Wrapper flags such as
`--yolo` and `--fresh` are stripped rather than forwarded.

Environment: `AI_MEMORY_SERVER_URL`, `AI_MEMORY_AUTH_TOKEN`, `AI_MEMORY_DATA_DIR`.

### `ai-jail 1.15.0` — flags relevant to composition

| Group | Flags |
| --- | --- |
| Execution | `--exec`, `--dry-run`, `--init`, `--clean`, `--bootstrap` |
| Mounts | `--rw-map`, `--map` (read-only), `--overlay-map`, `--hide-dotdir`, `--mask`, `--deny-path`, `--mask-except`, `--deny-path-except` |
| Modes | `--lockdown`/`--no-lockdown`, `--private-home`/`--no-private-home`, `--landlock`/`--no-landlock` (on), `--seccomp`/`--no-seccomp` (on), `--rlimits`/`--no-rlimits` (on) |
| Passthrough | `--gpu`/`--no-gpu`, `--docker`/`--no-docker` (off), `--tailscale`/`--no-tailscale` (off), `--display`/`--no-display`, `--ssh`/`--no-ssh` (off), `--pictures`/`--no-pictures` (off), `--systemd-user`/`--no-systemd-user` (off) |
| Network | `--allow-tcp-port <PORT>` — **lockdown only** |
| Other | `--worktree`/`--no-worktree` (on), `--mise`/`--no-mise` (on), `--browser[=hard\|soft]`/`--no-browser`, `--claude-dir <PATH>`, `-s`/`--status-bar[=STYLE]`, `--no-status-bar`, `--save-config`, `--hide-config`/`--no-hide-config` (on) |

Note: the README documents `--landlock`, `--seccomp`, `--rlimits`, `--worktree`
and `--mise` as default-on. It makes **no such claim for `--gpu`**, which the
current code treats as default-on (see S7).

`.ai-jail` TOML keys include `command`, `rw_maps`, `ro_maps`, `overlay_maps`,
`hide_dotdirs`, `mask`, `deny_paths`, `mask_exceptions`, `deny_path_exceptions`,
`allow_tcp_ports`, `private_home`, `no_gpu`, `no_docker`, `tailscale`,
`no_display`, `no_worktree`, `no_mise`, `ssh`, `pictures`, `browser_profile`,
`claude_dir`, `lockdown`, `status_bar_style`.

## Findings index

21 work items across 3 phases. Severity reflects impact on the product's core
promise (a sandbox that is actually on). Status is kept current as items land —
see **Progress** below for what each closed item shipped.

| ID | Title | Severity | Phase | Status |
| --- | --- | --- | --- | --- |
| C1 | `permissions: {jail: true}` turns the sandbox off | critical | 1 | done |
| C2 | Repo-supplied `.ai-launch.yaml` picks the binary and disables the sandbox | critical | 1 | done |
| S1 | ai-memory installed with no checksum from a mutable ref | high | 1 | done |
| I1 | 14 of 24 catalog agents break under the default config | high | 1 | done |
| I4 | `--dry-run` prints before validating | medium | 1 | done |
| I2 | `--continue` drops workstream, yolo and extra args | medium | 2 | done |
| I3 | CLI permission flags skip normalization | medium | 2 | done |
| I7 | Spurious `jail-options-without-jail` warning | medium | 2 | done |
| S2 | Symlinked `.ai-jail` disables ai-jail config masking | high | 2 | done |
| S3 | `HomeSymlinkMounts` auto-mounts read-write with no denylist | medium-high | 2 | done |
| S4 | Ambient `AI_MEMORY_*` variables are never sanitized | medium | 2 | done |
| I6 | Catalog is not deep-merged and is frozen on every launch | medium | 2 | done |
| I5 | Minor consistency cluster (4 sub-items) | low | 2 | done |
| S6 | No upstream version pinning or compatibility probe | medium | 3 | done |
| F3 | Gaps in the Gherkin contract | medium | 3 | done (the relative `.config/gh` scenario decision moves to S7.k) |
| I8 | `COVER_PKGS` does not exist; three divergent exclusion lists | medium | 3 | done |
| F2 | Unmapped ai-jail v1.15 flags | medium | 3 | done |
| F1 | `--fresh` is not exposed | low | 3 | done |
| F4 | `install.sh` and `.goreleaser.yaml` have no CI gate | medium | 3 | open |
| F5 | `--executable` points at a host path not mounted in the jail | medium | 3 | done |
| S7 | Hardening and cleanup cluster (12 sub-items) | low | 3 | partial |

## Progress

Phase 3 was executed before Phase 1, inverting the risk ordering below. Phase 1
has since been worked in its documented order (I4 first, so the rest is visible
in `--dry-run`). **Phases 1 and 2 are complete**; what remains is Phase 3.

| ID | What shipped | What is still missing |
| --- | --- | --- |
| I2 | `buildContinue` emits every wrapper flag `ai-memory run` accepts without a harness — scope, `--new` / `--workstream`, `--yolo`. Harness-native input that genuinely cannot apply is reported as `continue-ignores-harness-input` instead of vanishing | nothing |
| I3 | Permissions are normalized **after** config, profile and flags are merged, so a dependency pulled in by `--gpu` resolves and the CLI matches the TUI | nothing |
| I7 | `JailExec` left the `jail-options-without-jail` condition. It is set for every non-TUI launch, so it fired the warning on plain `--no-jail` runs where no jail option had been configured at all | nothing |
| S2 | The automatic `--no-hide-config` for a symlinked `.ai-jail` now prints an explicit warning naming the file and the effect. Auto-mounted symlink targets are announced too, instead of appearing only in the final argv echo | restricting the automatic path to symlinks resolving inside the checkout (the item lists it as optional) |
| S3 | `HomeSymlinkMounts` refuses filesystem roots and system trees (`/etc`, `/usr`, `/var`, `/System`, `/private/*`, …) and returns them as `RefusedMount` so each refusal is reported by name | the mode is still read-write for accepted targets; the item asks for it to be configurable |
| S4 | `upsertEnv` removes the inherited variable when the config leaves an `AI_MEMORY_*` value empty. "Not configured" now means "not set" instead of "whatever direnv exported". The environment-forwarding posture — including the deliberate absence of an allowlist — is recorded in `docs/design-decisions.md` | the allowlist itself, and the `ai-memory run --config` alternative for keeping the token out of the environment |
| I6 | `mergeAgents` merges the catalog per entry keyed by `Command`, so a hand-written entry no longer loses `Memory.RunHarness` / `YoloFlag` / `Params`. The launch path persists only the MRU list (`SaveRecentAgents`) instead of writing the merged catalog back on every run. With both in place the hardcoded `"oc"` remap in `memoryRunHarness` is gone | nothing |
| I5 | `--no-memory` inverts like `--no-jail` and `--no-yolo` instead of only ever disabling; `parseMount` splits on the last colon (a directory ending in `ro` survives), requires an absolute path and cleans it; a TUI failure propagates instead of every error reading as a cancellation | the two `extra_args` parsers (`strings.Fields` in the YAML path vs `splitArgs` in the CLI) are still separate |
| C2 | `enforceLocalConfigTrust` draws a boundary around file-supplied configuration: a local config cannot select an agent the catalog fails to resolve, cannot disable the sandbox while the global catalog defaults it on, and cannot declare a relative mount or a filesystem root. Every refusal names the explicit opt-in (`--agent`, `--add`, `--no-jail`) and what the operator types stays fully trusted | the refusal is unconditional rather than a TTY confirmation prompt (the item allows either; non-interactive runs had to refuse regardless). `jail_flags` weakening `seccomp` / `landlock` / `rlimits` / `hide_config` is not yet part of the comparison |
| I4 | `--dry-run` validates before printing. The argv is still printed when issues exist, warnings exit 0, a fatal issue exits non-zero. Issues are now labelled `error:` / `warning:` instead of calling everything a warning. The CLI fixtures gained PATH stubs for the harness and both upstream CLIs, so the suite validates a realistic configuration and stays hermetic | nothing |
| C1 | Presence of `options.jail` / `options.memory` comes from the parsed document (`declaredOptionKeys`) instead of a substring search. `hasOptionKey` / `hasSectionKey` are gone | nothing |
| S1 | `preferReleaseInstall` routes any recipe with release assets to the checksum-verified path; `source_url` is a fallback for recipes that publish none | nothing |
| I1 | `config.MemoryRunHarnesses` declares the eight harnesses `ai-memory run` accepts, in one place. Pre-flight fails with `memory-harness-unsupported` naming the `--no-memory` escape, and the 14 catalog agents that cannot map onto the list now declare `supports_memory: false` | the harness token is still derived from the resolved alias rather than the agent identity (point 3 of the item); the catalog only avoids it by declaring `run_harness` |
| S6 | `config.MinAIJailVersion` / `MinAIMemoryVersion` pin the floor in one place; `ai-launcher --doctor` probes `--version` on both upstreams and reports anything below it. The probe is deliberately **not** in `Validator.Validate` — that runs on every launch and inside the TUI update loop | nothing |
| F2 | `--mask-except`, `--deny-path-except`, `--hide-dotdir`, `--status-bar=STYLE`, and the `display` / `pictures` / `tailscale` / `systemd-user` / `mise` / `worktree` passthroughs. `JailFlags` booleans now mirror ai-jail's own auto / force-on / force-off model instead of suppressing the positive form, so `jail_flags.gpu: true` stops being a silent no-op | `ro_maps` closed as redundant: read-only mounts are already declarative via `mounts[]` with `mode: read-only` (and `--mount PATH:ro`), so a second key in `jail_flags` would only duplicate the same argv |
| I6 | `mergePermissions` merges the built-in permission catalog with the user's by ID, so a new release's permissions appear without clobbering customizations | `cfg.Agents` is still replaced wholesale, losing `Memory.RunHarness` / `YoloFlag` / `Params`; `SaveGlobal` still runs on every launch with the error ignored |
| S7 | The flag-precedence sub-item: `appendJailFlags` ran before the permission pass, so `jail_flags.gpu: false` plus permission `gpu` produced `--no-gpu … --gpu` and clap's last-wins discarded the explicit value. Permissions now stay silent for any capability an explicit `jail_flags` entry already decided | the other 11 sub-items |
| I8 | The boundary is stated once per mechanism and the three agree: the `COVERAGE_EXCLUDE` regex in `.ai-standards.env` (now anchored so `cmd/` no longer swallows `internal/cmd/`), the Makefile `-coverpkg` + awk filter, and `sonar.coverage.exclusions` (now including `internal/selfupdate/**` and `internal/cmd/**`). CI runs `make test-coverage` instead of duplicating the command, and the four stale `COVER_PKGS` references now describe the regex mechanism | nothing |
| F1 | `--fresh` is a CLI flag, an `Options` field, a TUI toggle, and a wrapper flag emitted before the harness token. Pre-flight rejects `--fresh` combined with `--continue` (`fresh-with-continue`). Covered by unit tests, a build scenario and a validation scenario in the contract, and the README options table | nothing |
| F5 | With the jail enabled, the launcher maps the directory holding the resolved harness binary read-only (`--map <dir>`) unless a configured mount already covers it — the manual `/opt/homebrew` workaround in this repo's `.ai-jail` is the exact gap this closes. Documented as ARCHITECTURE invariant 10 | nothing |
| F3 | The contract now covers `--executable` (with the F5 auto-mount), the `run_harness` remap, `private_home` / `overlay_maps` / `deny_paths` / `allow_tcp_ports` / `claude_dir`, `browser: soft` and `browser: off`, continue-with-workstream (I2), rejected harness (I1, already present), and a zero-issue `--no-jail` run (I7). Filesystem- and env-dependent behaviors (home-symlink auto-mounts and their denylist, automatic `--no-hide-config`, `AI_MEMORY_NATIVE_BIN`) are explicitly listed as out of contract scope in `AGENTS.md` | the relative `.config/gh` scenario at `launcher.feature:64-81` still locks in the empty-`$HOME` fallback; deciding code-vs-scenario is S7.k |

Two defects found only after the fact are worth recording, because both came
from modelling upstream from the CLI `--help` instead of the documented
semantics:

- ai-jail's `--help` annotates a default for some flags and not others, and the
  positive/negative ordering in the help text does **not** encode the default
  (`--landlock / --no-landlock` is default-on, `--no-mise / --mise` is also
  default-on). The authoritative statement is in the upstream README config
  table: *"When a boolean field is not set, the feature is in auto mode. For
  resource passthroughs, that means enabled if the resource exists on the
  host."* Treating a capability as plain default-on turns "force it on" into
  "let ai-jail guess".
- A capability reachable from both a permission and a `jail_flags` key needs an
  explicit precedence rule, or the two passes emit contradictory flags.

## Execution order

```
PHASE 1 - the sandbox does not keep its promise
                                     I4 goes first: without it, the fixes
    I4 ──┬──► C1 ──► C2              for I1 and I3 stay invisible in the
         │                           --dry-run output used to verify them
         ├──► S1
         └──► I1

PHASE 2 - behavior coherence
    I2 ─┐
    I3 ─┤
    I7 ─┤ (I7 depends on I4)
    S2 ─┼──► independent of one another; any order
    S3 ─┤
    S4 ─┤
    I6 ─┘
    I5   (cleanup, no dependencies)

PHASE 3 - contract, docs, completeness
    S6 ──► F3        doctor first: the new contract scenarios
                     reference the supported version range
    I8
    F2 ──► F1
    F4
    F5
    S7   (cleanup, no dependencies)
```

---

# Phase 1 - the sandbox does not keep its promise

## C1 - `permissions: {jail: true}` turns the sandbox off

| Field | Value |
| --- | --- |
| Severity | critical |
| Phase | 1 |
| Depends on | I4 (so the fix is observable via `--dry-run`) |
| Files | `internal/config/config.go:606-647`, `internal/config/config.go:727-734` |

**Problem.** `LoadLocal` decides whether a key is present by string-matching the
raw file instead of inspecting the parsed YAML.

```go
func hasOptionKey(data []byte, key string) bool {
	return strings.Contains(string(data), "\n"+key+":") ||
		strings.HasPrefix(strings.TrimSpace(string(data)), key+":") ||
		strings.Contains(string(data), "\n  "+key+":")
}
```

The third term matches `"\n  jail:"` **anywhere in the file**, including inside
the `permissions:` block. In `LoadLocal`:

```go
if !hasSectionKey(b, "options") {
	loaded.Options = cfg.Options
} else {
	if !hasOptionKey(b, "jail") { loaded.Options.Jail = true }     // skipped
	if !hasOptionKey(b, "memory") { loaded.Options.Memory = true } // skipped
}
```

When the spurious match fires, the safe default is skipped and `Options.Jail`
keeps Go's zero value: `false`. The sandbox fails **open**, with no warning.

`permissions.jail` is a real, documented key (`config.go:418`, `Default: true`,
`Locked: true`), so this is the config a user would naturally write — not a
contrived one. Any key named `jail` or `memory` at two-space indentation, in a
comment, or inside a quoted string triggers the same.

**Evidence (verified).** Two `.ai-launch.yaml` files, identical except for the
`permissions:` block, through the compiled binary:

```
# a.yaml
version: "2.0"
agent: claude
permissions:
  jail: true
options:
  memory: true

$ ai-launcher --local-config a.yaml --dry-run
ai-memory run claude --executable /Users/luizg/.local/bin/claude
```

```
# b.yaml - control, no permissions block
version: "2.0"
agent: claude
options:
  memory: true

$ ai-launcher --local-config b.yaml --dry-run
ai-jail --exec --rw-map /Volumes/MSD512 --rw-map /Volumes/MSD512/Projetos \
  ai-memory run claude --executable /Users/luizg/.local/bin/claude
```

Writing the config that appears to enable the jail is exactly what disables it.

**Why the existing test misses it.**
`internal/config/config_test.go:269` `TestLoadLocalPartialOptionsKeepEachOmittedSafetyDefault`
asserts the correct invariant but its fixture is a single-block document:

```go
os.WriteFile(path, []byte("options:\n  yolo: true\n"), 0o600)
```

There is no sibling block containing `jail:` or `memory:`, so the string sniffing
never misfires. The assertion is right; the fixture is too weak.

**Required behavior.** Presence of `options.jail` / `options.memory` must be
determined from the parsed document, not from substring search. An omitted key
keeps the safe default (`true`); an explicitly present `false` stays `false`.
The result must not depend on any other block, comment, or string in the file.

**Regression test (write FIRST, must fail on current code).**

- File: `internal/config/config_test.go`
- Name: `TestLoadLocalIgnoresJailKeysOutsideOptionsBlock`
- Fixture: `version: "2.0"\nagent: claude\npermissions:\n  jail: true\noptions:\n  memory: true\n`
- Assertion: `got.Options.Jail == true`

Add a table-driven sibling covering: `jail:` inside `permissions`, inside a
comment, inside a mount path string, and the same four for `memory`. Also extend
`TestLoadLocalPartialOptionsKeepEachOmittedSafetyDefault` with a multi-block
fixture so the original test stops passing vacuously.

**Acceptance criteria.**
1. The new test fails before the fix and passes after.
2. `SaveLoadLocal` round-trip tests still pass — an explicit `jail: false` inside
   `options:` is still honored as `false`.
3. `hasOptionKey` is gone, or no longer participates in safety decisions.
4. Coverage on `internal/config` stays at or above 90%.

**Smoke test.**

```bash
make build
printf 'version: "2.0"\nagent: claude\npermissions:\n  jail: true\noptions:\n  memory: true\n' > /tmp/a.yaml
./bin/ai-launcher --local-config /tmp/a.yaml --dry-run   # must start with "ai-jail"
```

---

## C2 - Repo-supplied `.ai-launch.yaml` picks the binary and disables the sandbox

| Field | Value |
| --- | --- |
| Severity | critical |
| Phase | 1 |
| Depends on | C1, I4 |
| Files | `cmd/ai-launcher/main.go:224`, `cmd/ai-launcher/main.go:261-267`, `internal/launcher/builder.go:275-279`, `internal/launcher/builder.go:554-565` |

**Problem.** `main.go:224` makes the default local config `<cwd>/.ai-launch.yaml`
— a file that ships inside any repository you `cd` into. There is no trust
prompt, no signature, and no "this repo changed its launcher config" notice.

`main.go:261-267` does not reject an unknown agent; it synthesizes one:

```go
status, resolveErr := catalogue.Resolve(selectedAgent)
if resolveErr != nil {
	status = catalog.AgentStatus{Agent: config.Agent{Name: selectedAgent, Command: selectedAgent}}
}
```

The only gate is `agentIssues` (`builder.go:481-493`), which merely asserts the
path resolves. `exec.LookPath` resolves names containing `/` relative to cwd.

That file also controls, with no allowlist:

| Key | Effect |
| --- | --- |
| `options.jail: false` | No `ai-jail` prefix at all |
| `options.jail_flags` | `--no-seccomp`, `--no-landlock`, `--no-rlimits`, `--no-hide-config` |
| `mounts:` | Mode defaults to **read-write** (`builder.go:275-279`); `mounts: [{path: /}]` becomes `--rw-map /`. `mountIssues` only checks existence |
| `options.extra_args`, `options.param_values` | Appended verbatim to the harness |

**Evidence (verified).**

```
# evil.yaml
version: "2.0"
agent: /bin/sh
options:
  jail: false
  memory: false

$ ai-launcher --local-config evil.yaml --dry-run
/bin/sh
```

The resulting argv is the arbitrary binary, unsandboxed, with no confirmation.

**Required behavior.** A workspace-level config must not be able to silently
lower the security posture set by the global config.

1. A local config that disables `jail`, or that sets `jail_flags` weakening
   `seccomp`/`landlock`/`rlimits`/`hide_config`, relative to the global default
   must be refused or require explicit confirmation. A non-interactive run
   (no TTY) must refuse rather than prompt.
2. An `agent` value that does not resolve in the catalog must be rejected, not
   synthesized. Provide an explicit opt-in path for genuinely custom harnesses
   (the existing `--add` command writes to the **global** config, which is the
   trusted location).
3. Mount paths from a local config must be absolute; `/` and other filesystem
   roots must be refused.

Explicit CLI flags remain fully trusted — this boundary applies to file-supplied
configuration, not to what the operator types.

**Regression test (write FIRST, must fail on current code).**

- File: `cmd/ai-launcher/launch_test.go`
- Names: `TestLocalConfigCannotDisableJailBelowGlobalDefault`,
  `TestLocalConfigRejectsUnresolvableAgent`,
  `TestLocalConfigRejectsRootMount`
- Assertion: `run(...)` returns an error (or emits a fatal issue) instead of
  producing an argv whose first token is the unresolved agent.

**Acceptance criteria.**
1. The three tests fail before the fix and pass after.
2. Global-config-driven and flag-driven launches are unaffected — the full
   existing suite stays green, including the Gherkin contract.
3. The refusal message names the offending key and how to override it.
4. `docs/ARCHITECTURE.md` gains a short "trust boundary" subsection stating that
   the local config is untrusted input.

**Smoke test.**

```bash
printf 'version: "2.0"\nagent: /bin/sh\noptions:\n  jail: false\n' > /tmp/evil.yaml
./bin/ai-launcher --local-config /tmp/evil.yaml --dry-run   # must refuse
```

---

## S1 - ai-memory installed with no checksum from a mutable ref

| Field | Value |
| --- | --- |
| Severity | high |
| Phase | 1 |
| Depends on | none |
| Files | `internal/cmd/install.go:154`, `internal/installer/installer.go:160-195`, `internal/config/config.go:399-414`, `README.md:184-185`, `README.md:414-416` |

**Problem.** The ai-memory recipe defines `SourceURL`, so on Linux and macOS
`preferRelease` evaluates to `false`:

```go
preferRelease := target.Release != nil && (target.SourceURL == "" || isWindows())
```

Installation then falls to `InstallSource`, whose only validation is an HTTPS
scheme check and a shebang sniff:

```go
if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(sourceURL)), "https://") { ... }
if len(data) == 0 || !bytes.HasPrefix(data, []byte("#!")) {
	return Result{Name: name}, errors.New("source does not look like an executable script")
}
```

No checksum, no signature, no pinned commit. The URL is
`https://raw.githubusercontent.com/akitaonrails/ai-memory/main/bin/ai-memory` —
the **`main` branch**, whose content can change at any time. The file is
installed 0755 and executed on every session.

Only the *native runner* (`installNativeMemoryRunner`) uses verified release
assets. The *wrapper*, which is what actually runs, does not.

This contradicts the project's own claims:

| Source | Claim |
| --- | --- |
| `README.md:414-416` | "Every install is SHA-256 checksum-verified" |
| `README.md:184-185` | "with mandatory SHA-256 checksum verification" |
| `docs/ARCHITECTURE.md:84-85` | Invariant 4: "without a verifiable checksum the install fails, unless an explicit `allow_unverified: true` in the recipe" |

The ai-memory recipe does **not** declare `allow_unverified: true`. The
documented escape hatch is bypassed rather than used. `docs/design-decisions.md:73`
uses the correct, narrower wording ("Every install **from a GitHub release**") —
the README and ARCHITECTURE are the drifted ones.

**Required behavior.** The verified release path is the default on every
platform. The recipe already declares assets for all five platform keys and a
`ChecksumAsset`, so no new download plumbing is needed. `InstallSource` remains
only as a fallback when a target has no `Release`, and using it must require an
explicit `allow_unverified: true` in the recipe so invariant 4 is honored rather
than circumvented.

**Regression test (write FIRST, must fail on current code).**

- File: `internal/cmd/install_test.go`
- Name: `TestInstallPrefersVerifiedReleaseOverSourceURL`
- Assertion: a target with both `Release` and `SourceURL` routes to the
  release/checksum path, not `InstallSource`. Use the existing fake-client seam
  in that file.
- Companion in `internal/installer/installer_test.go`:
  `TestInstallSourceRequiresAllowUnverified`.

**Acceptance criteria.**
1. Both tests fail before the fix and pass after.
2. `README.md:184-185` and `README.md:414-416` match the code, or the code
   matches the README. No remaining claim that is false.
3. `install-state.json` records a real release tag for ai-memory, not `"source"`.
4. Windows behavior is unchanged (it already took the verified path).

**Smoke test.**

```bash
./bin/ai-launcher --install --agent ai-memory
grep -c '"Tag": "source"' ~/.config/ai-launch/install-state.json   # expected: 0
```

---

## I1 - 14 of 24 catalog agents break under the default config

| Field | Value |
| --- | --- |
| Severity | high |
| Phase | 1 |
| Depends on | I4 |
| Files | `internal/config/config.go:348-381`, `internal/launcher/builder.go:151-164`, `internal/launcher/builder.go:445-477`, `cmd/ai-launcher/main.go:268-272`, `cmd/ai-launcher/launch_test.go:49` |

**Problem.** `ai-memory run` accepts exactly eight harness names. The catalog
declares 24 agents, **all** with `SupportsMemory: true`. `memoryRunHarness` only
remaps `oc → opencode` and otherwise returns `agent.Command` verbatim:

```go
func memoryRunHarness(agent config.Agent) string {
	if agent.Memory != nil {
		if h := strings.TrimSpace(agent.Memory.RunHarness); h != "" { return h }
	}
	switch strings.TrimSpace(agent.Command) {
	case "oc":
		return "opencode"
	}
	return agent.Command
}
```

`Validator.Validate` checks that `ai-memory` is on PATH but **never validates the
harness name**. Since `memory` defaults to `true` (`config.go:471`), these agents
fail under the default configuration. Pre-flight passes; the failure surfaces at
runtime, **inside the jail**, as an opaque clap error.

**Evidence (verified).** Against the real `ai-memory 1.19.0`:

```
gemini         invalid value 'gemini' for '[HARNESS]'
cursor-agent   invalid value 'cursor-agent' for '[HARNESS]'
zero           invalid value 'zero' for '[HARNESS]'
devin          invalid value 'devin' for '[HARNESS]'
aider          invalid value 'aider' for '[HARNESS]'
qwen           invalid value 'qwen' for '[HARNESS]'
kilo           invalid value 'kilo' for '[HARNESS]'
mimo           invalid value 'mimo' for '[HARNESS]'
agy            invalid value 'agy' for '[HARNESS]'
goose          invalid value 'goose' for '[HARNESS]'
cline          invalid value 'cline' for '[HARNESS]'
hermes         invalid value 'hermes' for '[HARNESS]'
openclaw       invalid value 'openclaw' for '[HARNESS]'
kiro-cli       invalid value 'kiro-cli' for '[HARNESS]'
```

And the launcher emits the broken argv without complaint:

```
$ ai-launcher --agent gemini --dry-run
ai-jail --exec --rw-map /Volumes/MSD512 --rw-map /Volumes/MSD512/Projetos \
  ai-memory run gemini
```

Aggravating factor: `main.go:268-272` overwrites `status.Agent.Command` with the
resolved alias, so a host with only `cursor` installed produces
`ai-memory run cursor` instead of `cursor-agent`.

The existing test `cmd/ai-launcher/launch_test.go:49` enshrines the broken
pattern by expecting `ai-memory run --new fresh custom-cli`.

**Required behavior.** The set of harnesses `ai-memory run` accepts is declared
once, in one place, and is the single source of truth.

1. Every catalog agent either maps to one of the eight via `Memory.RunHarness`,
   or declares `SupportsMemory: false`.
2. Pre-flight emits a fatal issue (suggested code `memory-harness-unsupported`)
   when memory is enabled and the resolved harness is not in the set. The message
   must be actionable and name the workaround: run with `--no-memory`.
3. The harness token is derived from the agent identity, not from whichever alias
   happened to resolve on PATH.

**Regression test (write FIRST, must fail on current code).**

- File: `internal/launcher/builder_test.go`
- Name: `TestValidateRejectsHarnessUnknownToAiMemory`
- Table: all eight accepted names produce no issue; at least three rejected names
  (`gemini`, `cursor-agent`, `aider`) produce `memory-harness-unsupported`.
- Second: `TestBuildUsesCanonicalHarnessNotResolvedAlias` — an agent whose alias
  resolved on PATH still emits its canonical harness token.
- Fix `cmd/ai-launcher/launch_test.go:49`, which currently asserts the defect.

**Acceptance criteria.**
1. Tests fail before, pass after.
2. `ai-launcher --agent gemini --dry-run` reports a clear pre-flight error
   instead of emitting an invalid argv.
3. All eight supported harnesses still build the canonical chain.
4. `--no-memory` remains a working escape for any agent.
5. A new Gherkin scenario covers one accepted and one rejected harness.

**Smoke test.**

```bash
./bin/ai-launcher --agent gemini --dry-run          # must fail with a clear message
for h in claude codex opencode pi crush omp kimi grok; do
  ./bin/ai-launcher --agent "$h" --dry-run
done
./bin/ai-launcher --agent gemini --no-memory --dry-run   # must succeed
```

---

## I4 - `--dry-run` prints before validating

| Field | Value |
| --- | --- |
| Severity | medium |
| Phase | 1 |
| Depends on | none — do this first |
| Files | `cmd/ai-launcher/main.go:539-543` |

**Problem.** `launch()` prints the argv and returns at `:539-542`; `Validate`
only runs at `:543`. So `--dry-run` prints commands that pre-flight would reject.
It is the diagnostic tool the README advertises, and it is precisely the one that
hides the problems in I1 and I3.

**Evidence (verified).** With `--agent gemini` the dry-run prints a valid-looking
chain even though the harness is rejected by ai-memory, and with `--gpu` it
prints an argv missing the `--docker` that `gpu` requires. Neither shows a
warning.

**Required behavior.** `--dry-run` validates first and reports issues alongside
the argv. Fatal issues make `--dry-run` exit non-zero; warnings print but still
show the argv. The argv is still printed even when issues exist — its diagnostic
value depends on seeing what *would* run.

**Regression test (write FIRST, must fail on current code).**

- File: `cmd/ai-launcher/launch_test.go`
- Name: `TestDryRunReportsPreflightIssues`
- Assertion: a config with a known-fatal issue produces the issue text on stderr
  and a non-zero result from `run(...)`.

**Acceptance criteria.**
1. Test fails before, passes after.
2. Existing dry-run tests still pass for valid configurations, byte-identical on
   stdout — the argv format does not change.
3. Warnings do not suppress the argv output.

**Smoke test.**

```bash
./bin/ai-launcher --agent claude --dry-run ; echo "exit=$?"   # valid: exit=0
./bin/ai-launcher --agent gemini --dry-run ; echo "exit=$?"   # invalid: exit!=0
```

---

# Phase 2 - behavior coherence

## I2 - `--continue` drops workstream, yolo and extra args

| Field | Value |
| --- | --- |
| Severity | medium |
| Phase | 2 |
| Depends on | none |
| Files | `internal/launcher/builder.go:168-178` |

**Problem.** `buildContinue` returns immediately after the scope flags:

```go
command = append(command, "ai-memory", "run")
return appendMemoryScope(command, cfg), nil
```

`NewWorkstream`, `Workstream`, `Yolo`, `ParamValues` and `ExtraArgs` arrive
populated in the `LaunchConfig` and are discarded with no warning. `agentIssues`
also short-circuits on `ContinueSession` (`builder.go:482-484`), so nothing
reports it.

**Evidence (verified).**

```
$ ai-launcher --continue --new sprint-3 --yolo --dry-run
ai-jail --exec --rw-map /Volumes/MSD512 --rw-map /Volumes/MSD512/Projetos \
  ai-memory run
```

`--new` and `--yolo` are gone. Per the upstream reference above, `--new`,
`--workstream` and `--yolo` are valid on `ai-memory run` with no harness, so
there is no technical reason to drop them.

**Required behavior.** `buildContinue` emits the same wrapper flags as `Build`,
minus the harness token and its native args: scope, workstream selection, and
`--yolo`. For inputs that genuinely cannot apply without a harness (catalog
params, harness-specific extra args), emit an explicit warning rather than
silently discarding them.

**Regression test (write FIRST, must fail on current code).**

- File: `internal/launcher/builder_test.go`
- Name: `TestBuildContinueKeepsWorkstreamAndYolo`
- Assertion: `ContinueSession: true, NewWorkstream: "sprint-3", Yolo: true`
  produces `["ai-memory","run","--new","sprint-3","--yolo"]`.
- Second: `TestBuildContinueWarnsOnDiscardedHarnessArgs`.

**Acceptance criteria.**
1. Tests fail before, pass after.
2. The existing headless-continue Gherkin scenario (`launcher.feature:321-336`)
   still passes unchanged — a bare `--continue` still emits `ai-memory run`.
3. A new scenario covers continue-with-workstream.

**Smoke test.**

```bash
./bin/ai-launcher --continue --new sprint-3 --yolo --dry-run   # must contain --new and --yolo
./bin/ai-launcher --continue --dry-run                          # must stay bare
```

---

## I3 - CLI permission flags skip normalization

| Field | Value |
| --- | --- |
| Severity | medium |
| Phase | 2 |
| Depends on | I4 |
| Files | `cmd/ai-launcher/main.go:273-277`, `internal/catalog/catalog.go:100-141`, `internal/tui/tui.go:636-637` |

**Problem.** `NormalizePermissions` runs at `main.go:273`, *before* the CLI
permission flags are applied at `:274-277`, and nothing re-normalizes afterwards.
So the dependency graph declared in the catalog (`gpu` requires `docker`;
disabling a parent disables its dependents) is not applied to flag-driven runs.

The TUI re-normalizes on every toggle (`tui.go:636-637`). CLI and TUI diverge on
the same permission model.

**Evidence (verified).**

```
$ ai-launcher --agent claude --gpu --dry-run
ai-jail --exec --gpu --rw-map /Volumes/MSD512 --rw-map /Volumes/MSD512/Projetos \
  ai-memory run claude --executable /Users/luizg/.local/bin/claude
```

No `--docker`, despite `gpu` declaring it in `Requires`.

**Required behavior.** Permissions are normalized after all inputs — config,
profile, and flags — are merged. CLI and TUI produce identical permission sets
for identical selections.

**Regression test (write FIRST, must fail on current code).**

- File: `cmd/ai-launcher/launch_test.go`
- Name: `TestGpuFlagPullsInDockerDependency`
- Assertion: `--gpu` alone yields an argv containing both `--docker` and `--gpu`.
- Second: `TestCliAndTuiProduceSamePermissionSet` — a table comparing
  `NormalizePermissions` output for the same selection through both paths.

**Acceptance criteria.**
1. Tests fail before, pass after.
2. `--docker=false` also disables `gpu`, matching TUI behavior.
3. `Locked` permissions (such as `jail`) remain forced on.
4. The `gpu-without-docker` pre-flight issue becomes unreachable through normal
   flag use; keep the check for hand-edited configs.

**Smoke test.**

```bash
./bin/ai-launcher --agent claude --gpu --dry-run   # must contain --docker
```

---

## I7 - Spurious `jail-options-without-jail` warning

| Field | Value |
| --- | --- |
| Severity | medium |
| Phase | 2 |
| Depends on | I4 |
| Files | `cmd/ai-launcher/main.go:295`, `internal/launcher/builder.go:507-510` |

**Problem.** `main.go:295` sets `JailExec: len(args) > 0` — true for *any*
non-TUI invocation, including `--dry-run` — and nothing clears it when the jail
is off:

```go
if !cfg.JailFlags.IsZero() || cfg.JailExec {
	return []Issue{{Code: "jail-options-without-jail",
		Message: "jail options are set but the jail is disabled; they will be ignored", Warning: true}}
}
```

So `ai-launcher --agent claude --no-jail` warns about jail options the user never
set. `JailExec` is derived from argv length, not chosen by the operator, so it
should not count as a "jail option".

This is currently masked in `--dry-run` by I4; it appears on real launches. The
Gherkin scenario at `launcher.feature:370-383` only covers the case where
`jail_flags` is actually populated.

**Required behavior.** `jail-options-without-jail` fires only when the user set
an actual jail option. `JailExec` is either cleared when `UseJail` is false or
excluded from the condition.

**Regression test (write FIRST, must fail on current code).**

- File: `internal/launcher/builder_test.go`
- Name: `TestNoJailWithoutJailOptionsProducesNoIssues`
- Assertion: `UseJail: false, JailExec: true, JailFlags: zero` yields an empty
  issue slice.
- Keep a companion asserting that real `jail_flags` with `UseJail: false` still
  warns.

**Acceptance criteria.**
1. Test fails before, passes after.
2. `launcher.feature:370-383` still passes unchanged.
3. A new scenario covers `--no-jail` with no jail options producing zero issues.

**Smoke test.**

```bash
./bin/ai-launcher --agent claude --no-jail --dry-run 2>&1 | grep -c jail-options-without-jail   # expected: 0
```

---

## S2 - Symlinked `.ai-jail` disables ai-jail config masking

| Field | Value |
| --- | --- |
| Severity | high |
| Phase | 2 |
| Depends on | none |
| Files | `cmd/ai-launcher/main.go:626-642`, `cmd/ai-launcher/main.go:318-320` |

**Problem.**

```go
func symlinkedProjectJailConfig() *bool {
	wd, err := os.Getwd()
	info, err := os.Lstat(filepath.Join(wd, ".ai-jail"))
	if err != nil || info.Mode()&os.ModeSymlink == 0 { return nil }
	disable := false
	return &disable
}
```

Applied at `:318-320`, this emits `--no-hide-config`. A cloned repository that
ships `.ai-jail` **as a symlink** therefore turns off the mask that hides the
project's own `.ai-jail` from the sandbox. A workaround for a bubblewrap
limitation doubles as a third-party-controlled policy downgrade.

This repo's own `.ai-jail` shows what such a file carries: a `command` array,
`rw_maps` including `/opt/homebrew`, and `ssh = true`.

There is also a TOCTOU: `Lstat` here versus ai-jail's own read later.

**Required behavior.** The automatic downgrade stays (it solves a real problem)
but becomes visible: emit an explicit warning naming the file and the effect, so
the operator can see that masking was disabled and why. Consider restricting the
automatic path to symlinks whose target resolves inside the current checkout.

**Regression test (write FIRST, must fail on current code).**

- File: `cmd/ai-launcher/launch_test.go`
- Name: `TestSymlinkedJailConfigWarnsWhenDisablingHideConfig`
- Assertion: with a symlinked `.ai-jail` in the working directory, stderr
  contains a warning that names `.ai-jail` and `hide-config`.
- Reuse the existing fixture at `cmd/ai-launcher/launch_test.go:206-240`.

**Acceptance criteria.**
1. Test fails before, passes after.
2. The `--no-hide-config` emission itself still works — existing tests pass.
3. `docs/ARCHITECTURE.md:150-151` documents the warning.

**Smoke test.**

```bash
cd "$(mktemp -d)" && ln -s /dev/null .ai-jail
ai-launcher --agent claude --dry-run 2>&1 | grep -i hide-config   # must warn
```

---

## S3 - `HomeSymlinkMounts` auto-mounts read-write with no denylist

| Field | Value |
| --- | --- |
| Severity | medium-high |
| Phase | 2 |
| Depends on | none |
| Files | `internal/launcher/symlink.go:26-75`, `cmd/ai-launcher/main.go:317` |

**Problem.** Every hidden entry of `$HOME` that is a symlink resolving outside
the home tree becomes a read-write mount. `symlink.go:72` hardcodes the mode:

```go
mounts = append(mounts, config.Mount{Path: target, Mode: "rw"})
```

A single `~/.anything -> /` (or `-> /etc`) silently grants the sandboxed agent
read-write access to that tree. The operator sees it only in the one-line command
echo at `main.go:568`. There is no denylist for filesystem roots or system
directories, no depth limit, and no read-only option. `EvalSymlinks` follows
chains, so the final target is not necessarily what the link name suggests.

The feature itself is legitimate — ai-jail rebuilds `$HOME` as tmpfs and
recreates dotfile symlinks without their targets, so they dangle. The problem is
the unbounded scope and the hardcoded `rw`.

**Required behavior.**

1. A denylist refuses auto-mounting `/`, `/etc`, `/usr`, `/bin`, `/sbin`, `/var`,
   `/System`, and equivalents. Refused targets produce a warning naming the link.
2. The mode is configurable, defaulting to read-write only where required.
3. Auto-mounted targets are listed explicitly in the pre-flight output, not only
   in the final argv echo.

**Regression test (write FIRST, must fail on current code).**

- File: `internal/launcher/symlink_test.go`
- Name: `TestHomeSymlinkMountsRefusesSystemRoots`
- Assertion: a temp home containing `.evil -> <root>` yields no mount for that
  target and reports a warning.
- Second: `TestHomeSymlinkMountsRespectsConfiguredMode`.

**Acceptance criteria.**
1. Tests fail before, pass after.
2. The legitimate case still works: `.cache -> /storage/cache` is still mounted.
3. `docs/ARCHITECTURE.md` invariant 9 is updated with the denylist.
4. A Gherkin scenario covers the auto-mount behavior (also closes part of F3).

**Smoke test.**

```bash
H=$(mktemp -d); ln -s / "$H/.evil"; ln -s /tmp "$H/.cache"
HOME="$H" ./bin/ai-launcher --agent claude --dry-run 2>&1
# must mount the /tmp target, refuse the / target, and warn about it
```

---

## S4 - Ambient `AI_MEMORY_*` variables are never sanitized

| Field | Value |
| --- | --- |
| Severity | medium |
| Phase | 2 |
| Depends on | none |
| Files | `internal/launcher/builder.go:29-44`, `internal/launcher/builder.go:66-81` |

**Problem.** `Environment` starts from a full copy of `os.Environ()` and
`upsertEnv` returns early on an empty value:

```go
func upsertEnv(env []string, key, value string) []string {
	if value == "" { return env }   // empty config value keeps whatever was inherited
	...
}
```

So when `MemoryAuthToken` or `MemoryServerURL` is empty in the global config, a
pre-existing `AI_MEMORY_AUTH_TOKEN` / `AI_MEMORY_SERVER_URL` from the caller's
environment (direnv, `.envrc`, a parent process) passes through unchanged — able
to redirect the memory client to a third-party server or inject a token.

Separately, the **entire** parent environment — including `GITHUB_TOKEN`,
`AI_LAUNCHER_UPDATE_TOKEN`, and cloud credentials — is handed to the chain. The
launcher filters nothing and relies entirely on ai-jail.

**Required behavior.**

1. When the config leaves an `AI_MEMORY_*` value empty, the corresponding
   inherited variable is removed rather than forwarded. "Not configured" must
   mean "not set", not "whatever the parent had".
2. Document the environment-forwarding posture explicitly in
   `docs/design-decisions.md`. If a full allowlist is out of scope for now, say
   so and record why — an unbounded pass-through into a sandbox is a decision,
   not an accident.

Note the related exposure already acknowledged in `README.md:428-430`: env vars
are visible inside the jail, so `AI_MEMORY_AUTH_TOKEN` is readable by the agent.
`ai-memory run --config <PATH>` is an upstream-supported alternative worth
evaluating; that evaluation belongs in the design-decisions entry.

**Regression test (write FIRST, must fail on current code).**

- File: `internal/launcher/builder_test.go`
- Name: `TestEnvironmentDropsInheritedMemoryVarsWhenUnconfigured`
- Assertion: with `AI_MEMORY_AUTH_TOKEN` set in the process env and
  `cfg.MemoryAuthToken == ""`, the returned env contains no `AI_MEMORY_AUTH_TOKEN`.

**Acceptance criteria.**
1. Test fails before, passes after.
2. A configured token is still exported.
3. The token never appears in argv, `shellJoin` output, or `install.log`
   (existing guarantees preserved).

**Smoke test.**

```bash
AI_MEMORY_AUTH_TOKEN=leaked ./bin/ai-launcher --agent claude --dry-run
# verify via a debug build or unit test that the child env has no AI_MEMORY_AUTH_TOKEN
# when the global config does not set one
```

---

## I6 - Catalog is not deep-merged and is frozen on every launch

| Field | Value |
| --- | --- |
| Severity | medium |
| Phase | 2 |
| Depends on | I1 |
| Files | `internal/config/config.go:699-725`, `cmd/ai-launcher/main.go:555-560`, `internal/launcher/builder.go:157-162` |

**Problem.** `mergeGlobalDefaults` replaces the agent list wholesale when the
user config contains any agents, so `Memory.RunHarness`, `YoloFlag` and `Params`
from the built-in defaults are lost. That is precisely why the hardcoded `"oc"`
special case exists in `memoryRunHarness`.

It is made worse by `main.go:555-560`, which calls `SaveGlobal` on **every**
launch (errors ignored), materializing the entire built-in catalog into the
user's file and freezing it at the current build. After the first launch, the
user's config no longer tracks catalog improvements.

Combined with I1: once a user's config is frozen, adding `RunHarness` mappings in
a later release will not reach them.

**Required behavior.**

1. Agents merge per entry, keyed by `Command`. User-supplied fields win; fields
   absent from the user entry fall back to the built-in default.
2. The launch path stops writing the full catalog. Persist only what actually
   changed (the recent-agents list).
3. With per-entry merge in place, the hardcoded `"oc"` fallback in
   `memoryRunHarness` becomes unnecessary and is removed.

**Regression test (write FIRST, must fail on current code).**

- File: `internal/config/config_test.go`
- Name: `TestLoadGlobalDeepMergesAgentDefaults`
- Assertion: a user config declaring only `{command: oc, path: /custom/oc}`
  still yields `RunHarness == "opencode"` and the default `YoloFlag`.
- Second, in `cmd/ai-launcher/launch_test.go`:
  `TestLaunchDoesNotPersistBuiltinCatalog` — after a launch, the global config
  file does not contain the full 24-agent list.

**Acceptance criteria.**
1. Tests fail before, pass after.
2. `TestBuildRemapsOcWrapperToOpencodeHarness` still passes.
3. Recent-agents ordering still works (`TestTouchRecentAgentOrdersAndDedupes`).
4. An existing frozen config still loads without error — migration, not a break.

**Smoke test.**

```bash
cp ~/.config/ai-launch/config.yaml /tmp/cfg-backup.yaml
./bin/ai-launcher --agent claude --dry-run
grep -c 'command: gemini' ~/.config/ai-launch/config.yaml   # expected: 0
```

---

## I5 - Minor consistency cluster

| Field | Value |
| --- | --- |
| Severity | low |
| Phase | 2 |
| Depends on | none |
| Files | see each sub-item |

Four independent small fixes. They may share one branch since they do not
interact.

**I5.a - Three negative flags, two semantics.** `main.go:125-127` uses
`if flagsWasSet(...) && o.noMemory` (only ever disables), while `:117-119` and
`:131-133` invert (`--no-jail=false` *enables* the jail; `--no-yolo=false`
enables yolo). Pick one semantic and apply it to all three.
Test: `TestNegativeFlagsShareInvertSemantics` in `cmd/ai-launcher/main_test.go`.

**I5.b - Real TUI errors swallowed as cancellation.** `main.go:526`:

```go
if err != nil {
	// Cancelled or empty result — quiet exit, no "failed to start".
	return nil
}
```

`RunWithHooks` returns the raw `tea.Program.Run()` error (terminal init failure)
through the same channel as `ErrCancelled`, so a real failure exits 0 silently.
Required: distinguish with `errors.Is(err, tui.ErrCancelled)`; anything else is
reported and exits non-zero.
Test: `TestTuiStartFailureIsReported`.

**I5.c - `parseMount` mis-splits and does not normalize.** `main.go:644-650`
splits on `:` and treats the last segment as the mode, so a directory literally
named `.../ro` is silently truncated. It also does not require an absolute path
and does not call `filepath.Clean`, while the TUI does (`tui.go:1136`, `:556`,
`:919`). Required: split only on a trailing `:ro`/`:rw`/`:read-only` suffix,
clean the path, and require absolute. Coordinate with C2, which also constrains
mount paths.
Test: `TestParseMountHandlesPathsEndingInModeLikeSegment`.

**I5.d - Two parsers for `extra_args`.** `Options.UnmarshalYAML`
(`config.go:201`) uses `strings.Fields` (no quote handling); the CLI uses the
quote-aware `splitArgs` (`main.go:685-741`). So `extra_args: "--model 'gpt 5'"`
parses differently through YAML than through the CLI. Required: one shared
quote-aware splitter.
Test: `TestScalarExtraArgsRespectQuotes` — extend
`TestLoadLocalAcceptsDocumentedScalarExtraArgs` (`config_test.go:284`).

**Acceptance criteria (all four).** Each has a test that fails before and passes
after; the full suite stays green; no change to the argv format for inputs that
were already unambiguous.

---

# Phase 3 - contract, documentation, completeness

## S6 - No upstream version pinning or compatibility probe

| Field | Value |
| --- | --- |
| Severity | medium |
| Phase | 3 |
| Depends on | none — but do before F3 |
| Files | `internal/installer/installer.go:221`, `internal/config/config.go:118`, `internal/config/config.go:388`, `internal/launcher/builder.go:293`, `internal/launcher/builder.go:310`, `AGENTS.md:42`, `docs/design-decisions.md:15` |

**Problem.** The ai-jail v1.15 surface is encoded in six places, but
`installer.go:221` unconditionally fetches `/releases/latest` and **no code path
ever runs `ai-jail --version` or `ai-memory --version`**. Pre-flight only does
`LookPath`. `install-state.json` records the installed tag but nothing reads it
for compatibility.

`docs/design-decisions.md:22-23` claims: "If upstream changes, the contract
breaks in CI, not on the user's machine." This cannot hold structurally — the
Gherkin suite asserts what ai-launcher **emits**, never what ai-jail **accepts**.
A v1.16 that renames a flag installs cleanly, CI stays green, and the failure
lands on the user's machine.

**Required behavior.**

1. The supported upstream version range lives in **one** constant, referenced by
   the six sites that currently repeat "v1.15" in comments.
2. A `doctor` path (subcommand or pre-flight check) runs `ai-jail --version` and
   `ai-memory --version` and warns when the installed version is outside the
   supported range. Warn, do not block — the user may knowingly run ahead.
3. `docs/design-decisions.md:22-23` is corrected to describe what the contract
   actually guarantees.

**Regression test (write FIRST, must fail on current code).**

- File: `cmd/ai-launcher/main_test.go`
- Name: `TestDoctorReportsUpstreamVersionMismatch`
- Assertion: with a stubbed version probe returning an out-of-range version, the
  output names the tool, the found version, and the supported range.
- Use a seam like the existing `upgradeResolveTag` stub (`main.go:328`).

**Acceptance criteria.**
1. Test fails before, passes after.
2. The supported range appears in exactly one place in code.
3. `doctor` exits 0 with warnings when versions are unknown or unreachable — it
   must never block a launch.
4. Every network/subprocess probe has an explicit timeout (5s is adequate).

**Smoke test.**

```bash
./bin/ai-launcher doctor
# must report ai-jail 1.15.0 and ai-memory 1.19.0 as supported on this machine
```

---

## F3 - Gaps in the Gherkin contract

| Field | Value |
| --- | --- |
| Severity | medium |
| Phase | 3 |
| Depends on | S6, I1, I2, I7, S3 |
| Files | `test/features/launcher.feature`, `test/gherkin/gherkin_test.go:30-53` |

**Problem.** `AGENTS.md:38-45` positions the feature file as the drift detector
for the third-party CLIs. Several documented, shipped behaviors have no scenario:

| Behavior | Where it is claimed | Where it is actually tested |
| --- | --- | --- |
| `--executable` | `README.md:86`, `:104`, `:112` — every documented dry-run | `internal/launcher/builder_test.go` only |
| `oc → opencode` remap | `README.md:475`, listed as a shipped feature | unit test only |
| `private_home`, `tailscale`, `overlay_maps`, `deny_paths`, `allow_tcp_ports`, `claude_dir` | `docs/ARCHITECTURE.md:146-151` | `internal/launcher/jail_test.go:30-102` |
| `browser: off` / `browser: soft` | `docs/ARCHITECTURE.md:146-151` | unit test only |
| Positive toggle forms | implied by the tri-state model | unit test only |
| Home-symlink auto-mounts | ARCHITECTURE invariant 9 | `symlink_test.go` |
| Automatic `--no-hide-config` | ARCHITECTURE:150-151 | `launch_test.go:206-240` |
| `AI_MEMORY_NATIVE_BIN` export | ARCHITECTURE invariant 8 | `builder_test.go` |

Unit coverage is legitimate. The defect is that the documentation claims the
*contract* covers these, and it does not.

**Required behavior.** Either add the scenarios, or correct `AGENTS.md:38-45`
and `docs/design-decisions.md:21-22` to state precisely what the contract locks.
Preferred: add the scenarios — the reader already supports every needed key in
`launchSpec` (`gherkin_test.go:30-53`), so this is scenario authoring, not new
code.

Add the scenarios from Phase 1 and 2 as well: rejected harness (I1),
continue-with-workstream (I2), `--no-jail` with no options (I7), auto-mount
denylist (S3).

**Acceptance criteria.**
1. `make test-gherkin` passes with the new scenarios.
2. Every behavior in the table above is either covered or explicitly listed as
   out of contract scope in `AGENTS.md`.
3. No scenario asserts something the code does not do — the contract describes
   reality, and where reality is wrong the code changes first.
4. Note: `launcher.feature:64-81` currently blesses a **relative** `--rw-map
   .config/gh` when `$HOME` is empty. Decide whether that is correct (see S7) and
   fix the scenario or the code accordingly — do not leave it locked in by
   accident.

**Smoke test.**

```bash
make test-gherkin
```

---

## I8 - `COVER_PKGS` does not exist; three divergent exclusion lists

| Field | Value |
| --- | --- |
| Severity | medium |
| Phase | 3 |
| Depends on | none |
| Files | `.ai-standards.env`, `Makefile`, `.github/workflows/ci.yml`, `sonar-project.properties`, `AGENTS.md:25`, `docs/test-strategy.md:45`, `docs/cicd.md:27`, `docs/design-decisions.md:115` |

**Problem.** `.ai-standards.env` defines `COVERAGE_MIN` and `COVERAGE_EXCLUDE`,
with a comment instructing that `COVER_PKGS` be left empty. But four documents
state that the coverage boundary lives in `COVER_PKGS`. It does not exist.

Three mechanisms now express roughly the same boundary:

| Source | Mechanism | Divergence |
| --- | --- | --- |
| `.ai-standards.env` | regex `COVERAGE_EXCLUDE` | `cmd/` also swallows `internal/cmd/` |
| `Makefile` + `ci.yml` | `-coverpkg` on 3 packages + awk filter | duplicated inline in CI instead of calling the target |
| `sonar-project.properties` | `sonar.coverage.exclusions` | omits `internal/selfupdate/**` and `internal/cmd/**` |

Aggravating: the exclusions cover exactly `executor` and `replace_*` — the
highest-risk execution code is the least gated.

**Required behavior.** One source of truth for the coverage boundary. CI invokes
`make test-coverage` rather than duplicating it. Sonar exclusions match. The four
documentation references describe the mechanism that actually exists.

**Acceptance criteria.**
1. `grep -rn COVER_PKGS` returns only intentional matches (or none).
2. `make test-coverage` and the CI job produce the same number on the same
   commit.
3. `.ai-standards.env`, `Makefile`, and `sonar-project.properties` agree on the
   excluded set, or the differences are documented with a reason.
4. The 90% gate still passes.

**Smoke test.**

```bash
make test-coverage
```

---

## F2 - Unmapped ai-jail v1.15 flags

| Field | Value |
| --- | --- |
| Severity | medium |
| Phase | 3 |
| Depends on | none |
| Files | `internal/config/config.go:124-140`, `internal/launcher/builder.go:295-335`, `docs/ARCHITECTURE.md:146-151` |

**Problem.** `config.JailFlags` covers `lockdown`, `private_home`, `tailscale`,
`gpu`, `landlock`, `seccomp`, `rlimits`, `status_bar`, `hide_config`, `browser`,
`claude_dir`, `overlay_maps`, `mask`, `deny_paths`, `allow_tcp_ports`. Missing:

| Flag | Impact of the gap | Priority |
| --- | --- | --- |
| `--mask-except`, `--deny-path-except` | Without exceptions, a broad `mask`/`deny_paths` glob is all-or-nothing. Masking `**/*.env` without breaking `**/target/**` is impossible, which makes the existing `mask` support impractical | high |
| `--hide-dotdir` | No way to block passthrough of a specific home dotdir | medium |
| `--display` / `--no-display` | No X11/Wayland control | medium |
| `--worktree` / `--no-worktree` | Default-on upstream, no override | low |
| `--mise` / `--no-mise` | Default-on upstream, no override | low |
| `--pictures`, `--systemd-user` | Opt-in passthroughs unavailable | low |
| `--status-bar=STYLE` | Only the boolean; `status_bar_style` unmodeled | low |
| `ro_maps` | No declarative key for read-only mounts (only `--mount :ro`) | medium |

**Required behavior.** Extend `JailFlags` following the existing patterns:
tri-state `*bool` for toggles, `[]string` for repeatable list flags. Emission
order stays deterministic — the contract asserts exact argv sequences.
Start with `--mask-except` and `--deny-path-except`; they unblock the already
shipped `mask`/`deny_paths` support.

**Regression test (write FIRST).**

- File: `internal/launcher/jail_test.go`
- Name: `TestAppendJailFlagsEmitsMaskExceptions` and one per added flag
- Assertion: exact argv slice including position.
- Add Gherkin scenarios for the same (overlaps with F3).

**Acceptance criteria.**
1. Every added flag matches the upstream spelling in the reference table above.
2. Emission order is deterministic and covered by tests.
3. `docs/ARCHITECTURE.md:146-151` lists the new keys.
4. No flag is documented that the code does not emit (`AGENTS.md:78-80`).

**Smoke test.**

```bash
# with mask + mask_exceptions configured in .ai-launch.yaml
./bin/ai-launcher --agent claude --dry-run
# then confirm every emitted token exists upstream:
ai-jail --help | grep -- --mask-except
```

---

## F1 - `--fresh` is not exposed

| Field | Value |
| --- | --- |
| Severity | low |
| Phase | 3 |
| Depends on | F2 (same config/TUI plumbing) |
| Files | `cmd/ai-launcher/main.go:69-108`, `internal/config/config.go:152-163`, `internal/launcher/builder.go:111-147`, `internal/tui/tui.go` |

**Problem.** `ai-memory run --fresh` starts a new native session in the selected
workstream instead of resuming or adopting an existing one. Grep confirms **zero
occurrences** of `--fresh` anywhere in the repository — code, docs, or contract.

It is the only `ai-memory run` scope flag with no coverage: `--workspace`,
`--project`, `--workstream`, `--new`, `--executable` and `--yolo` are all mapped.

**Required behavior.** `--fresh` is available as a CLI flag, an `Options` field,
and a TUI toggle, emitted as a wrapper flag before the harness token — matching
the placement of the other scope flags. It is mutually exclusive with
`--continue` in a meaningful sense; decide and document the interaction.

**Regression test (write FIRST).**

- File: `internal/launcher/builder_test.go`
- Name: `TestBuildEmitsFreshWrapperFlag`
- Assertion: `Fresh: true` yields `--fresh` before the harness token.
- Gherkin scenario in `test/features/launcher.feature`.

**Acceptance criteria.**
1. Test fails before, passes after.
2. `README.md` options table documents `--fresh` — and the flag exists in code
   (`AGENTS.md:78-80`).
3. The TUI toggle appears only when memory is enabled.

**Smoke test.**

```bash
./bin/ai-launcher --agent claude --fresh --dry-run   # must contain --fresh
ai-memory run --help | grep -- --fresh               # confirm upstream spelling
```

---

## F4 - `install.sh` and `.goreleaser.yaml` have no CI gate

| Field | Value |
| --- | --- |
| Severity | medium |
| Phase | 3 |
| Depends on | none |
| Files | `install.sh`, `.goreleaser.yaml`, `.github/workflows/ci.yml` |

**Problem.** `install.sh` (192 lines) is the primary distribution channel — the
code that decides which binary users execute — and has no test, no ShellCheck,
and no CI job. `.goreleaser.yaml` has no `goreleaser check`, so a release-config
break surfaces only at tag time.

A latent bug demonstrates the gap:

```sh
154  EXT=tar.gz
155  [ "$OS" = windows ] && EXT=zip
156  ARCHIVE="ai-launcher_${VER_NOV}_${OS}_${ARCH}.${EXT}"
...
180  tar -xzf "$WORK/$ARCHIVE" -C "$WORK"
```

The Windows branch downloads a `.zip` and extracts it with `tar -xzf`. In
practice line 62 already aborts on an unrecognized `uname -s`, so line 155 is
unreachable — contradictory dead code promising a path that does not exist.

The checksum handling in the script is otherwise sound and fails closed:

```sh
download_asset "checksums.txt" "$WORK/checksums.txt" \
  || die "checksums.txt not found — refusing to install an unverified binary"
```

**Required behavior.** A CI job runs ShellCheck and `shfmt` on `install.sh` and
`goreleaser check` on the release config. The dead Windows branch is either
removed or implemented with `unzip`.

**Acceptance criteria.**
1. The new CI job fails on a deliberately broken `install.sh` and passes on the
   current one (after the dead-branch fix).
2. Actions are pinned by commit SHA, matching the existing workflow convention.
3. `goreleaser check` passes.
4. The mandatory-checksum behavior is unchanged.
5. Optional but valuable: a smoke job that runs `install.sh --version <tag>`
   against a real release in a container.

**Smoke test.**

```bash
shellcheck install.sh && shfmt -d install.sh
go run github.com/goreleaser/goreleaser/v2@latest check
```

---

## F5 - `--executable` points at a host path not mounted in the jail

| Field | Value |
| --- | --- |
| Severity | medium |
| Phase | 3 |
| Depends on | none |
| Files | `cmd/ai-launcher/main.go:293`, `internal/launcher/builder.go:133-135` |

**Problem.** `main.go:293` passes `status.Path` — an absolute host path from
`exec.LookPath` — and `builder.go:134` emits `--executable <abs>`. Nothing mounts
that binary's directory into the sandbox. `HomeSymlinkMounts` covers only `$HOME`
dotlinks.

The evidence that this bites in practice is in this repository: the root
`.ai-jail` lists `/opt/homebrew` in `rw_maps` — a manual workaround for exactly
this gap.

**Required behavior.** When the jail is enabled and `--executable` resolves
outside the already-mounted set, the launcher either auto-mounts the containing
directory read-only, or emits a pre-flight warning naming the path and the
required mount. Auto-mount is preferable, read-only is sufficient, and the
decision must be visible in the argv.

**Regression test (write FIRST).**

- File: `internal/launcher/builder_test.go`
- Name: `TestExecutableOutsideMountsIsMadeReachable`
- Assertion: with jail on and `Executable: /opt/tools/bin/x`, the argv contains a
  mount covering `/opt/tools/bin` (or a warning is emitted).

**Acceptance criteria.**
1. Test fails before, passes after.
2. An executable already covered by an existing mount adds no duplicate.
3. Coordinate with S7's dedup fix so the new mount participates in it.
4. `docs/ARCHITECTURE.md` documents the behavior.

**Smoke test.**

```bash
./bin/ai-launcher --agent crush --dry-run
# the --executable path must be reachable via one of the emitted mounts
```

---

## S7 - Hardening and cleanup cluster

| Field | Value |
| --- | --- |
| Severity | low |
| Phase | 3 |
| Depends on | none |
| Files | see each sub-item |

Twelve independent items. Group them into a few branches by theme; none blocks
another.

### Jail coherence

**S7.a - `--gpu` and `jail_flags.gpu` conflict silently.** `appendJailFlags` runs
at `builder.go:253`, *before* permissions at `:254-269`. With
`jail_flags.gpu: false` plus the `gpu` permission enabled, the argv becomes
`ai-jail --no-gpu --gpu ...` and the last one wins — the operator's explicit
`--no-gpu` is discarded with no warning. Required: detect the conflict in
pre-flight and report it.

**S7.b - `gpu` is treated as default-on, and the evidence points the other way.**
`builder.go:300` declares `{name: "gpu", value: flags.GPU, defaultOn: true}`.
With `defaultOn: true`, `appendJailToggle` emits **nothing** when the value is
`true` (`builder.go:343-348`) — so `jail_flags.gpu: true` is silently a no-op,
and only the separate `gpu` permission actually produces `--gpu`.

`ai-jail 1.15.0 --help` states defaults for some toggles and not others:

```
--landlock / --no-landlock     Enable/disable Landlock LSM (Linux 5.13+, default: on)
--seccomp / --no-seccomp       Enable/disable seccomp syscall filter (Linux, default: on)
--rlimits / --no-rlimits       Enable/disable resource limits (default: on)
--ssh / --no-ssh               Share ~/.ssh read-only + forward SSH_AUTH_SOCK (default: off)
--no-gpu / --gpu               Disable/enable GPU device passthrough (Linux only)
--no-docker / --docker         Disable/enable Docker socket passthrough
```

`--gpu` states no default, and it shares the negative-first "Disable/enable"
phrasing with `--docker`, which the ai-jail README documents as **default off**.
If `--gpu` is likewise default-off, then `defaultOn: true` is wrong and
`jail_flags.gpu: true` silently fails to enable GPU access.

Required: determine the real default (read the ai-jail source or test with
`ai-jail --dry-run` on a GPU host), then correct the toggle and add a test
asserting that `jail_flags.gpu: true` produces an argv that actually enables GPU.
Do not guess — if the default cannot be established, model `gpu` as default-off,
which fails closed.

**S7.c - `allow_tcp_ports` without `lockdown` is a silent no-op.**
`builder.go:325-327` emits `--allow-tcp-port` unconditionally; upstream
classifies it as "Network (lockdown only)". Required: pre-flight warning when
ports are configured without `lockdown`.

### Executable and checksum handling

**S7.d - `AI_MEMORY_NATIVE_BIN` guard does not check the executable bit.**
`builder.go:37-41` validates only `info.Mode().IsRegular()`. The package already
has `isExecutable` (`installer.go:564-567`), unused here. Also a TOCTOU:
`~/.local/share/ai-launcher/bin/` is user-writable between the check and the
child's exec.

**S7.e - `checksumFor` accepts a stray hash.** `installer.go:372-398` accepts a
lone hash on any line (`onlyHash`, `:384-386`) or a `key=<sha256>` pair
(`:387-392`) when no filename match is found. A checksums file or release body
mentioning the hash of a *different* asset satisfies verification. Required:
require a filename-anchored match; treat the fallbacks as failures.

**S7.f - Env-controlled update endpoint.** `main.go:371-376` lets
`AI_LAUNCHER_UPDATE_API` / `AI_LAUNCHER_UPDATE_URL` from the environment redirect
the update endpoint while `AI_LAUNCHER_UPDATE_TOKEN` is attached as a Bearer
header — a token-exfiltration path if those variables come from a hostile parent.
Required: only send the token to the default host, or require explicit opt-in
when the endpoint is overridden.

**S7.g - Catalog-supplied flags are injectable.** `builder.go:200-203`: when
`param.TakesValue == false`, the flag *itself* comes from the catalog, so a
hand-edited global config can declare a dangerous flag with `takes_value: false`
and any truthy `param_values` entry injects it. Lower risk than C2 since the
global config is the trusted location, but worth an allowlist or a warning.

### Dead code and correctness

**S7.h - `ConstrainForHost` is a dead exported no-op.** `builder.go:398-404`,
0% covered, no callers — `main.go:310` uses `ConstrainToPlatform`. Remove it.

**S7.i - `replace_other.go` is not a replace.** `:21-28` runs the child and
waits, hardcodes `os.Stdin/Stdout/Stderr` ignoring the caller's streams, and
lacks the explicit `exec.LookPath` of the unix version — different error messages
and a different process lifecycle on unsupported platforms. Either make it match
or rename it to reflect what it does.

**S7.j - Executor goroutine and stdio leak.** `executor.go:56` starts
`io.Copy(ptmx, in)` that is never cancelled; when `out == nil` (`:58`) nothing
drains the PTY and `cmd.Wait()` at `:63` can block on a full pipe buffer.

**S7.k - Path construction and mount dedup.** `filepathOrEmpty`
(`builder.go:361-366`) joins with `/` by hand instead of `filepath.Join` and
falls back to the **relative** `.config/gh` when both `cfg.HomeDir` and `$HOME`
are empty; `strings.TrimRight(home, "/")` turns `home == "/"` into
`"/.config/gh"`. Separately, the `gh` mount (`builder.go:262`) is not deduped
against `cfg.Mounts` or `MergeAutoMounts` output, and `coveredByMounts`
(`symlink.go:94`) does not expand `~`. The artifact is visible in this repo's
`.ai-jail`, where `~/.config/gh` appears twice. Note that
`launcher.feature:64-81` currently locks in the relative-path behavior — see F3.

**S7.l - Asymmetric config error handling.** `main.go:229-232` warns and
continues on a global-config failure; `:243-246` returns the error for a local
one. A corrupt `.ai-launch.yaml` blocks every launch. Make the local path degrade
the same way, or justify the asymmetry.

### Documentation and repository hygiene

**S7.m - README screenshot contradicts the code.** `README.md:85-86` shows the
TUI preview with `--exec`, but the TUI path is `len(args) == 0` (`main.go:509`),
so `JailExec` is false; `builder.go:249-250` documents the opposite intent.
Fix the screenshot and document `--exec` and its trigger rule in
`docs/ARCHITECTURE.md:136-151`, which currently omits both.

**S7.n - Root `.ai-jail` is stale and machine-specific.** It hardcodes
`--executable /home/lgldsilva/.npm-global/bin/crush` — a Linux path — in a
repository checked out under `/Volumes` on macOS, and lists `~/.config/gh` twice.
It is machine configuration, not project configuration: remove it from version
control and add it to `.gitignore`.

**Acceptance criteria (cluster).** Each sub-item has a test where testable
(S7.h, S7.m, S7.n are removals/doc fixes and need none); the full suite stays
green; no behavior change is introduced without a corresponding contract update.

---

# Verification

Every phase closes with the full gate and an E2E smoke against the real binary.
"Compiles" is not "works", and a passing unit test is not a delivered feature.

## Full gate

```bash
make test-all
# unit + property + gherkin + race + coverage + lint + lint-full + sec + mutation
```

Run without `-short`. If any test is skipped, list which and why.

## E2E smoke

```bash
make build

# C1 - the fail-open. Must emit ai-jail in BOTH cases.
printf 'version: "2.0"\nagent: claude\npermissions:\n  jail: true\noptions:\n  memory: true\n' > /tmp/a.yaml
./bin/ai-launcher --local-config /tmp/a.yaml --dry-run

# C2 - a local config must not choose an arbitrary binary unchallenged
printf 'version: "2.0"\nagent: /bin/sh\noptions:\n  jail: false\n' > /tmp/evil.yaml
./bin/ai-launcher --local-config /tmp/evil.yaml --dry-run

# I1 - clear pre-flight failure, not an opaque runtime error inside the jail
./bin/ai-launcher --agent gemini --dry-run
for h in claude codex opencode pi crush omp kimi grok; do
  ./bin/ai-launcher --agent "$h" --dry-run
done

# I2 / I3 / I7
./bin/ai-launcher --continue --new sprint-3 --yolo --dry-run   # must contain --new and --yolo
./bin/ai-launcher --agent claude --gpu --dry-run               # must contain --docker
./bin/ai-launcher --agent claude --no-jail --dry-run 2>&1 | grep -c jail-options-without-jail

# S1 - the ai-memory install must record a verified tag, not "source"
./bin/ai-launcher --install --agent ai-memory
grep -c '"Tag": "source"' ~/.config/ai-launch/install-state.json   # expected: 0
```

## Upstream cross-check

This is the check the Gherkin contract structurally cannot perform (S6), and it
is what closes the audit loop. For every argv produced by `--dry-run`, confirm
that each flag token exists in the installed upstream:

```bash
ai-jail --help
ai-memory run --help
```

Any token in a generated argv that does not appear in the corresponding help
output is drift. Re-run this after any upstream upgrade.

## Debt audit before merge

```bash
grep -rn '// TODO\|// FIXME\|// deferred\|// HACK\|// placeholder\|// no-op' \
  --include='*.go' . | grep -v '_test.go'
```

Each occurrence needs an entry here with an honest status. Untracked debt becomes
a production bug.

# Reporting

When an item is done, report against the three honest-status levels defined in
the ground rules. Do not mark an item `tested` without both an E2E smoke and a
review by someone who did not implement it.

If an item turns out to be wrong — the defect does not reproduce, or the upstream
surface has changed — say so and update this document rather than working around
it. The findings here were verified on 2026-07-26 against `ai-jail 1.15.0` and
`ai-memory 1.19.0`; a different upstream version can invalidate any of them.
