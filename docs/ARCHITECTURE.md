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
3. **Atomic 0600 saves** in the global and local configs (`internal/config`).
4. **Mandatory checksum on installs**: without a verifiable checksum the
   install fails, unless an explicit `allow_unverified: true` in the recipe.
5. **Strict checksum on self-update**: `ai-launcher upgrade` and `install.sh`
   require the release `checksums.txt`; a missing file, a missing entry, or a
   mismatch is a hard error with no `allow_unverified` escape hatch, because
   the flow overwrites the executable you will run.
6. **Safe defaults**: omitted `jail` and `memory` mean `true`; an explicit
   `false` is preserved (tested contract — see docs/test-strategy.md).
7. **Token via environment only**: `AI_MEMORY_AUTH_TOKEN` goes into the child
   process env and never into logs or argv. The same holds for
   `AI_LAUNCHER_UPDATE_TOKEN` in the self-update flow.
8. **No downloads inside the jail**: when the managed ai-memory native runner
   exists, the launcher exports `AI_MEMORY_NATIVE_BIN` so the memory wrapper
   uses it directly.

## Configuration (summary)

Global config (`~/.config/ai-launch/config.yaml`):

| Key | Type | Purpose |
| --- | --- | --- |
| `version` | string | Schema (`2.0`; accepts `1`/`1.0`) |
| `memory_server_url` | string | ai-memory server (default `https://aimemory.raspberrypi.lan`) |
| `memory_auth_token` | string | Bearer token, forwarded via env only |
| `agents[]` | list | `name`, `command`, `aliases`, `path`, `supports_memory`, `supports_yolo`, `yolo_flag`, `params[]` (`name`/`flag`/`takes_value`), `release` (GitHub recipe), `memory` (MCP/hooks adapter), `source_url` |
| `tools[]` | list | Auxiliary tool recipes (ai-jail, ai-memory) |
| `permissions[]` | list | `id`, `name`, `default`, `locked`, `requires` |
| `default_mounts[]` | list | Mounts suggested in new selections |
| `profiles{}` | map | Named selection snapshots (`agent`, `permissions`, `mounts`, `options`) |

Local config (`.ai-launch.yaml`): `agent`, `permissions{}`, `mounts[]`
(`path`/`mode`), and `options`: `jail`, `memory`, `yolo`, `new_workstream`,
`workstream`, `workspace`, `project`, `jail_flags`, `extra_args`,
`param_values`.

`jail_flags` mirrors the ai-jail v1.15 toggles with tri-state booleans
(absent = ai-jail default): `lockdown`, `private_home`, `tailscale`, `gpu`,
`landlock`, `seccomp`, `rlimits`, `status_bar`, `browser`
(`hard`/`soft`/`off`), `claude_dir`, `overlay_maps`, `mask`, `deny_paths`,
`allow_tcp_ports`.

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
