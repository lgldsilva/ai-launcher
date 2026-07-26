# Architecture

The README is operational; the long form lives in `docs/`. This page is the
operational summary of "what it is and what shape it has" for anyone about to
read the code.

## Purpose

ai-launcher is an orchestrator of AI CLIs. It reimplements neither sandbox
nor memory: it composes `ai-jail` (third-party sandbox) and `ai-memory`
(third-party memory/sessions) into the canonical launch chain, giving the
user a TUI and a CLI to declare agent, permissions, mounts, and options — an
installer that downloads the managed tools straight from GitHub releases with
mandatory checksums, and a self-update command that upgrades the launcher
binary itself from the same releases.

## Data flow

```
┌──────────┐   flags/config    ┌──────────────────┐
│ TUI/CLI  │──────────────────►│ cmd/ai-launcher  │  parsing + precedence
└──────────┘                   └────────┬─────────┘
                                        │ LaunchConfig (pure, no I/O)
                                        ▼
                               ┌──────────────────┐
                               │ internal/launcher│  Build(argv) + Validator
                               └────────┬─────────┘
                                        │ argv + env
              ┌─────────────────────────┼─────────────────────────┐
              ▼                         ▼                         ▼
       ┌────────────┐          ┌────────────────┐          ┌────────────┐
       │  ai-jail   │─────────►│ ai-memory run  │─────────►│  harness   │
       │ (sandbox)  │          │ (memory/MCP)   │          │ (claude…)  │
       └────────────┘          └────────────────┘          └────────────┘

       internal/cmd ──► internal/installer ──► GitHub Releases (SHA-256)
       (--install)        (download/verify)         ai-jail, ai-memory, harnesses

       upgrade command ──► internal/selfupdate ──► GitHub Releases (checksums.txt)
       (self-update)         (verify + atomic replace)   ai-launcher itself
```

The three flows (launch, install, self-update) share only `internal/config`
and `internal/catalog` — plus `internal/installer`, whose traversal-guarded
archive extraction and checksum parsing `internal/selfupdate` reuses.
`launcher.Build` is deliberately pure: dry-run, table tests, and the TUI
consume exactly the same argv.

## Component map

| Package | Responsibility |
| --- | --- |
| `cmd/ai-launcher` | Entry point: flag parsing, precedence (defaults < local < profile < flags), dispatch (TUI, `upgrade`, install, add, profiles), final execution via `exec`/PTY; `--version` prints ldflags-injected build metadata |
| `internal/cmd` | Orchestration outside launching: `--install`/`--upgrade`, ai-memory MCP/hooks wiring, `--add`; provisions the managed ai-memory native runner; keeps `install.log` (0600, no tokens) |
| `internal/config` | Versioned schema (`2.0`) of the global and local configs; safe defaults; profiles; tri-state `JailFlags`; atomic 0600 saves |
| `internal/catalog` | Resolves agents against the PATH (`path` > command > aliases) and normalizes dependencies between permissions |
| `internal/launcher` | `Build` (pure argv), `Validator` (preflight with stable codes), `ConstrainToPlatform` (Windows without jail), PTY executor and per-platform `exec`; exports `AI_MEMORY_NATIVE_BIN` when the managed runner exists |
| `internal/installer` | GitHub Releases client: per-platform asset selection, mandatory SHA-256 verification, tar.gz/zip extraction, `install-state.json` |
| `internal/selfupdate` | `ai-launcher upgrade`: resolves the latest tag, downloads the GoReleaser archive, verifies it strictly against `checksums.txt` (missing/mismatch is a hard error), atomically replaces the running executable |
| `internal/tui` | Bubbletea frontend: 5 sections (Agent, Permissions, Mounts, Options, Profiles); all durable state lives in `launcher.LaunchConfig` |
| `test/gherkin` + `test/features` | Executable contract suite (in-repo Gherkin reader, no BDD dependency) over the argv composition and preflight rules |

## State and storage

| File | Format | Written by |
| --- | --- | --- |
| `~/.config/ai-launch/config.yaml` | YAML (schema `2.0`) | `--add`, `--save-profile`, `--delete-profile`, manual edits |
| `<project>/.ai-launch.yaml` | YAML (schema `2.0`) | `--save`, `Ctrl+S` in the TUI |
| `~/.config/ai-launch/install-state.json` | JSON | `internal/installer` (installed release tags) |
| `~/.config/ai-launch/install.log` | text (0600) | `internal/cmd` (never logs tokens) |
| `~/.local/share/ai-launcher/bin/ai-memory` | binary | `internal/cmd` (managed ai-memory native runner, exported as `AI_MEMORY_NATIVE_BIN`) |

All config saves are atomic (temporary file + `rename`) with 0600 permission.

## Cross-cutting invariants

1. **Canonical chain order**: `ai-jail [jail flags] ai-memory run [wrapper
   flags] <harness> [native args]`. No code path builds a different order;
   the Gherkin suite locks regressions out.
2. **Selection precedence**: built-in defaults < local `.ai-launch.yaml` <
   profile < explicit flags. Profiles only replace the blocks they define.
2b. **The local config is untrusted input**: `.ai-launch.yaml` ships with the
   repository, so `enforceLocalConfigTrust` refuses one that selects an
   unresolvable agent, disables the sandbox while the global catalog defaults
   it on, or declares a relative mount or a filesystem root. Global config,
   profiles and command-line flags are trusted; the boundary is around the
   workspace file.
3. **Atomic 0600 saves** in the global and local configs (`internal/config`).
4. **Mandatory checksum on installs**: without a verifiable checksum the
   install fails, unless an explicit `allow_unverified: true` in the recipe.
   A recipe that publishes release assets always installs from them; the
   unverified `source_url` path is reserved for recipes with no assets.
5. **Strict checksum on self-update**: `ai-launcher upgrade` and `install.sh`
   require the release `checksums.txt`; a missing file, a missing entry, or a
   mismatch is a hard error with no `allow_unverified` escape hatch, because
   the flow overwrites the executable you will run.
6. **Safe defaults**: omitted `jail` and `memory` mean `true`; an explicit
   `false` is preserved (tested contract — see docs/test-strategy.md).
   "Omitted" is decided from the **parsed** document, never from a substring
   search over the raw bytes: `jail:` also occurs inside `permissions`, inside
   comments and inside mount paths, and a false positive there skips the
   default and leaves the sandbox off.
6b. **Harness must be one ai-memory accepts**: `ai-memory run` takes a fixed
   list (`config.MemoryRunHarnesses`). Pre-flight fails with
   `memory-harness-unsupported` instead of emitting an argv whose rejection
   would surface as an opaque clap error inside the jail.
7. **Token via environment only**: `AI_MEMORY_AUTH_TOKEN` goes into the child
   process env and never into logs or argv. The same holds for
   `AI_LAUNCHER_UPDATE_TOKEN` in the self-update flow.
8. **No downloads inside the jail**: when the managed ai-memory native runner
   exists, the launcher exports `AI_MEMORY_NATIVE_BIN` so the memory wrapper
   uses it directly.
9. **Home symlink targets ride along**: ai-jail rebuilds `$HOME` as a tmpfs
   and recreates dotfile symlinks without their targets, leaving them
   dangling (for example `~/.cache -> /storage/cache` with no `/storage` in
   the sandbox). With the jail enabled, the launcher mounts every home
   dotfile symlink target that resolves outside `$HOME` as `--rw-map`
   (`internal/launcher/symlink.go`), skipping targets already covered by a
   configured mount.
10. **The harness binary must be reachable inside the jail**: `--executable`
    names an absolute host path, and ai-jail hides everything not explicitly
    mapped. With the jail enabled, the launcher maps the directory holding
    the resolved binary read-only (`--map <dir>`), unless a configured mount
    already covers it. Read-only is sufficient: the agent executes the
    binary, it never writes it.
11. **`--exec` marks programmatic launches**: the launcher passes ai-jail's
    `--exec` (direct exec without the PTY proxy or status bar) whenever the
    invocation carries arguments — agent selection, flags, `--dry-run`.
    Bare `ai-launcher` opens the TUI, and the TUI launch omits `--exec` so
    ai-jail's defaults keep charge of the terminal (`JailExec` in
    `cmd/ai-launcher/main.go` is exactly `len(args) > 0`).

## Configuration (summary)

Global config (`~/.config/ai-launch/config.yaml`):

| Key | Type | Purpose |
| --- | --- | --- |
| `version` | string | Schema (`2.0`; accepts `1`/`1.0`) |
| `memory_server_url` | string | ai-memory server (default `https://aimemory.internal.lgldsilva.com.br`) |
| `memory_auth_token` | string | Bearer token, forwarded via env only |
| `agents[]` | list | `name`, `command`, `aliases`, `path`, `supports_memory`, `supports_yolo`, `yolo_flag`, `params[]` (`name`/`flag`/`takes_value`), `release` (GitHub recipe), `memory` (MCP/hooks adapter), `source_url` |
| `tools[]` | list | Auxiliary tool recipes (ai-jail, ai-memory) |
| `permissions[]` | list | `id`, `name`, `default`, `locked`, `requires`, `platforms` (empty = all platforms) |
| `default_mounts[]` | list | Mounts suggested when neither the local config/profile nor `--mount`/`--map`/`--rw-map` define any; read-write by default, with the same optional `:ro`/`:rw` suffix as `--mount`. Built-in candidates are OS-specific (Linux: `/storage…`; macOS: `/Volumes/MSD512…`); only paths that exist on the host are applied. Pre-selected in the TUI mount manager (removable) |
| `profiles{}` | map | Named selection snapshots (`agent`, `permissions`, `mounts`, `options`) |
| `recent_agents[]` | list | Most-recently-used agent commands (newest first); the TUI lists installed agents in this order |

Local config (`.ai-launch.yaml`): `agent`, `permissions{}`, `mounts[]`
(`path`/`mode`), and `options`: `jail`, `memory`, `yolo`, `fresh`,
`new_workstream`, `workstream`, `workspace`, `project`, `jail_flags`,
`extra_args`, `param_values`.

### Permission → jail argv (implementation)

Permissions are **optional auxiliaries**. Pre-flight never requires host tools
such as `gh`, `ssh`, or Docker to be installed; it only enforces dependency
edges among permissions (e.g. permission requires jail; gpu requires docker).

`internal/launcher.Build` / `appendJailArgs` maps **enabled** permissions to
ai-jail (all require `UseJail` when on):

| Permission id | Argv contribution |
| --- | --- |
| `ssh` | `--ssh` |
| `gh` | `--rw-map $HOME/.config/gh` (config mount only; does not LookPath `gh`; omitted when no home is known or a configured mount already covers it) |
| `docker` | `--docker` |
| `gpu` | `--gpu` |
| `display` | `--display` (Linux only) |
| `pictures` | `--pictures` |
| `tailscale` | `--tailscale` |
| `systemd-user` | `--systemd-user` (Linux only) |
| `mise` | `--mise` |
| `worktree` | `--worktree` |

Each permission declares its supported platforms in the catalog
(`permissions[].platforms`; empty means all). The TUI hides permissions
unsupported on the current OS, and pre-flight reports an
`unsupported-platform` warning when a config enables one anyway. Platform
metadata is read from the effective catalog, so a hand-edited
`permissions[].platforms` drives the TUI and pre-flight identically.

A permission is a one-way switch: it only ever emits the positive form. ai-jail
treats an unset capability as auto (enabled when the resource exists), so an
off permission means "leave auto-detection alone". Forcing a capability off is
a `jail_flags` decision. When both name the same capability (`gpu`, `display`,
`tailscale`, `mise`, `worktree`), the explicit `jail_flags` value wins and the
permission emits nothing — otherwise `appendJailFlags` and the permission pass
would produce `--no-tailscale --tailscale`, where clap's last-wins silently
discards the declared intent.

User-declared mounts are separate (`--map` / `--rw-map` from the mounts list).
Home dotfile symlink targets outside `$HOME` are auto-merged when the jail is
on. See the README section **Permissions: CLI + config** for the operator view.

`jail_flags` mirrors the ai-jail toggles with tri-state booleans that follow
ai-jail's own model — absent = auto (enabled when the resource exists on the
host), `true` = force on (`--flag`), `false` = force off (`--no-flag`). Forcing
on is a distinct state from leaving unset, so neither form is ever suppressed:
`lockdown`, `private_home`, `tailscale`, `gpu`, `display`, `mise`, `worktree`,
`landlock`, `seccomp`, `rlimits`, `status_bar`, `hide_config`, `browser`
(`hard`/`soft`/`off`), `claude_dir`, `overlay_maps`, `mask`, `mask_exceptions`,
`deny_paths`, `deny_path_exceptions`, `hide_dotdirs`, `allow_tcp_ports`, and
`status_bar_style` (`dark`/`light`/`pastel`, emitted as `--status-bar=STYLE`;
when set it suppresses both boolean `--status-bar` forms). When unset,
`hide_config` is auto-disabled for projects whose `.ai-jail` is a symlink
(bwrap cannot mask a symlink).

## Upstream version compatibility

The launcher composes a specific upstream surface, and that floor is pinned in
two constants in `internal/config`: `MinAIJailVersion` (`1.15.0`) and
`MinAIMemoryVersion` (`1.19.0`). `ai-launcher --doctor` probes the installed
binaries with `ai-jail --version` / `ai-memory --version` (5-second timeout
each) and reports `ai-jail-version-too-old` / `ai-memory-version-too-old` when
the installed version is below the floor. A probe that fails or cannot be
parsed stays silent.

The probe lives in `--doctor` and **not** in pre-flight validation on purpose.
`Validator.Validate` runs on every launch and inside the Bubble Tea update
loop; forking two children there costs ~300 ms of startup latency and blocks
the UI, and the same check would be invisible in `--dry-run`, which returns
before validation. Validation stays hermetic — PATH lookups and `Stat` only.

The Gherkin contract plus these constants are the drift detector: bumping the
floor means reviewing the upstream changelog and updating the contract in the
same commit.

## Build metadata vs. config schema

Two different "versions" exist and must not be conflated: the **binary
version** (`main.version`/`main.commit`/`main.date`, injected via `-ldflags`
by the Makefile and GoReleaser, printed by `--version` and used by
`ai-launcher upgrade` to decide whether an update exists) and the **config
schema version** (`config.CurrentVersion`, currently `"2.0"`), which versions
the YAML files, not the binary.

## Reading order

For a new contributor, in this sequence:

1. `README.md` — what the user sees.
2. `cmd/ai-launcher/main.go` — flags, precedence, and dispatch.
3. `internal/config/config.go` — schema, defaults, and persistence.
4. `internal/launcher/builder.go` — the pure argv composition (the heart).
5. `internal/catalog/catalog.go` — agent and permission resolution.
6. `internal/tui/tui.go` — how the TUI reuses the same `LaunchConfig`.
7. `internal/cmd/install.go` + `internal/installer/installer.go` — the
   install flow and checksum verification.
8. `internal/selfupdate/selfupdate.go` — the self-update flow and the atomic
   executable replace.
9. `test/features/launcher.feature` — the executable end-to-end contract.
10. `docs/design-decisions.md` — why it is this way.
