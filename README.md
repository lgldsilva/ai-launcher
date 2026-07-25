# ai-launcher

TUI/CLI launcher that orchestrates AI CLIs (Claude Code, Codex, Kimi, Crush, OpenCode, Gemini etc.) through a sandbox and a memory layer (Go, bubbletea, ai-jail + ai-memory).

> ai-launcher is a "super facilitator": it reimplements nothing. It composes two
> third-party tools — [`ai-jail`](https://github.com/akitaonrails/ai-jail)
> (sandbox wrapper: bubblewrap on Linux, sandbox-exec on macOS) and
> [`ai-memory`](https://github.com/akitaonrails/ai-memory) (MCP server + hooks +
> managed workstreams) — and builds the canonical launch command with the
> permissions, mounts, and options you declare.

## How it works

```
┌──────────────┐     ┌─────────────┐     ┌──────────────┐     ┌────────────────┐     ┌──────────────────┐
│ user         │────►│ ai-launcher │────►│ ai-jail      │────►│ ai-memory run  │────►│ harness          │
│ (TUI or CLI) │     │(orchestrate)│     │ (sandbox)    │     │ (memory)       │     │ (claude, codex…) │
└──────────────┘     └─────────────┘     └──────────────┘     └────────────────┘     └──────────────────┘
                          │                                          │
                          ▼                                          ▼
              ~/.config/ai-launch/                     ai-memory server
              config.yaml · profiles                   (AI_MEMORY_SERVER_URL)
```

The canonical launch chain is always:

```
ai-jail [jail flags] ai-memory run [wrapper flags] <harness> [native args]
```

Every layer is optional (except the harness): without jail the command starts
at `ai-memory run`; without memory the harness runs directly. On Windows
ai-jail does not exist — the launcher disables the sandbox with a warning and
runs the rest.

When the managed ai-memory native binary exists
(`~/.local/share/ai-launcher/bin/ai-memory`, provisioned by `--install` /
`--upgrade` from the verified release assets), the launcher exports
`AI_MEMORY_NATIVE_BIN` into the launched process so the memory wrapper uses it
directly — nothing is downloaded inside the jail.

## Installation

### Curl installer (recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/lgldsilva/ai-launcher/main/install.sh | sh
```

Downloads the right release archive for your OS/arch (Linux/macOS,
amd64/arm64), verifies its SHA-256 against the release `checksums.txt`
(mandatory — an unverifiable download is a hard error), and installs to
`~/.local/bin`. No sudo. Overrides: `--version vX.Y.Z` to pin a release,
`--bin-dir <dir>` or `AI_LAUNCHER_INSTALL_DIR` to change the destination. On
Windows, download the `.zip` from the releases page instead.

### Self-update

Once installed, the binary updates itself from the same GitHub releases:

```bash
ai-launcher upgrade                  # update to the latest release
ai-launcher upgrade --check          # only report whether an update exists
ai-launcher upgrade --version v0.2.0 # install a specific release
```

Checksum verification is strict (a missing or mismatched `checksums.txt` is a
hard error) and the replacement is atomic. Environment overrides:
`AI_LAUNCHER_UPDATE_API` (release API base), `AI_LAUNCHER_UPDATE_URL`
(download base), `AI_LAUNCHER_UPDATE_TOKEN` (GitHub token for private
releases, sent as a Bearer header and never logged).

### go install

Prerequisite: Go 1.24+ (declared in `go.mod`).

```bash
go install github.com/lgldsilva/ai-launcher/cmd/ai-launcher@latest
```

### From source

```bash
git clone https://github.com/lgldsilva/ai-launcher.git
cd ai-launcher
make build          # produces bin/ai-launcher
make build-release  # Linux/macOS/Windows binaries (amd64/arm64) in dist/
```

### Managed tools (ai-jail, ai-memory, harnesses)

The launcher installs the tools it orchestrates from GitHub releases, with
mandatory SHA-256 checksum verification:

```bash
ai-launcher --install                     # everything that has a recipe in the catalog
ai-launcher --install --agent "Kilo Code" # a single target
ai-launcher --upgrade                     # force reinstall from the latest release
```

`--install`/`--upgrade` also provision the ai-memory native runner at
`~/.local/share/ai-launcher/bin/ai-memory` from the same verified release
assets. On Windows ai-jail is skipped automatically and ai-memory is installed
from the native `ai-memory-windows-x86_64.zip` asset. Install state lives in
`~/.config/ai-launch/install-state.json` and the log in `install.log` (same
directory, no tokens).

## Usage

With no arguments, `ai-launcher` opens the interactive TUI. With any flag it
runs in CLI mode and executes for real (it is not a command generator — use
`--dry-run` to only print the argv).

### Commands and modes

| Command | Effect |
| --- | --- |
| `ai-launcher` | Opens the TUI (agents, permissions, mounts, options, profiles) |
| `ai-launcher --agent claude [flags]` | Builds the argv and executes the harness |
| `ai-launcher --continue` | Resumes the most recent session of the checkout (`ai-memory run` without a harness) |
| `ai-launcher upgrade [--check] [--version vX.Y.Z]` | Self-updates the launcher binary from GitHub releases (strict checksum) |
| `ai-launcher --install [--agent X]` / `--upgrade` | Installs/upgrades the managed tools via GitHub releases |
| `ai-launcher --add Name --path /path [--command cmd] [--description txt]` | Adds/updates a harness in the global catalog |
| `ai-launcher --list-profiles` / `--delete-profile N` | Lists or removes profiles saved in the global config |
| `ai-launcher --save-profile N [flags]` | Saves the merged selection as a profile and exits without launching |
| `ai-launcher --save` (or `--save-only`) | Writes the selection to the local `.ai-launch.yaml` and exits |
| `ai-launcher --version` | Prints the binary version (release, commit, build date) and exits |
| `ai-launcher help` | Shows flag usage |

The positional `upgrade` (self-update of the launcher binary) is unrelated to
the `--upgrade` flag (reinstall of the third-party tools).

### Options

| Flag | Effect |
| --- | --- |
| `--agent <cmd>` | Selects the agent (claude, codex, opencode, kimi, …) |
| `--ssh` / `--gh` / `--docker` / `--gpu` | Enables permissions inside the jail (gpu requires docker) |
| `--no-jail` / `--sandbox` | Explicitly disables / enables ai-jail |
| `--memory` / `--no-memory` | Enables / disables the ai-memory layer |
| `--yolo` / `--no-yolo` | Passes (or not) the agent's dangerous-mode flag |
| `--new <name>` / `--workstream <name>` | Creates / resumes an ai-memory workstream |
| `--workspace <name>` / `--project <name>` | Scope forwarded to `ai-memory run` |
| `--continue` | Continues the last ai-memory session of this checkout |
| `--mount <path>[:ro\|:rw]` / `--map` | Read-only mount (ro by default; `--map` is an alias) |
| `--rw-map <path>` | Read-write mount |
| `--param <name=value>` | Sets a parameter declared in the agent catalog (repeatable) |
| `--extra-args "<args>"` / `--args` | Extra arguments forwarded to the harness (`--args` is an alias) |
| `--profile <name>` | Loads a saved profile as the base selection |
| `--config <path>` / `--local-config <path>` | Alternative paths for the global / local config |
| `--dry-run` | Prints the generated command without executing |
| `--version` | Prints the binary version and exits |
| `--save`, `--save-only`, `--save-profile`, `--list-profiles`, `--delete-profile`, `--install`, `--upgrade`, `--add`, `--path`, `--command`, `--description` | See the commands table above |

Precedence of the final selection: **built-in defaults < local
`.ai-launch.yaml` < profile (`--profile`) < explicit flags**.

### Examples

```bash
# Run Claude Code with sandbox and memory (safe defaults)
ai-launcher --agent claude

# Only print the command that would run
ai-launcher --agent claude --ssh --docker --dry-run

# Custom mounts and dangerous mode
ai-launcher --agent pi --map /host/data --rw-map /workspace --yolo

# Named ai-memory workstream with project scope
ai-launcher --agent claude --new release-check --project my-app

# Parameter declared in the catalog (Kimi declares "query" and "model")
ai-launcher --agent kimi --param query="refactor the auth module" --param model=k2

# Save the merged selection as a reusable profile
ai-launcher --agent claude --ssh --param model=opus --save-profile review

# Later: load the profile and override only what you need
ai-launcher --profile review --docker

# Add a harness without recompiling the launcher
ai-launcher --add "My Harness" --path /opt/tools/runner --command runner
```

### TUI

`ai-launcher` with no arguments opens the TUI with five sections: **Agent**
(the first row is always "Continue last session"), **Permissions**,
**Mounts**, **Options** (fixed toggles + one row per parameter declared by
the agent), and **Profiles** (section 5, visible when at least one profile is
saved).

| Key | Action |
| --- | --- |
| `Tab` / `Shift+Tab`, `1`–`5` | Cycle / jump between sections |
| `↑/↓` or `j/k` | Move within the current section |
| `Space` | Toggle permission/option, mount mode, or load profile |
| `/` | Open the mount browser (`→/l` enters, `←/h` goes up, `Tab` toggles ro/rw) |
| `Backspace` | Remove the selected mount |
| `Enter` | Select agent, edit parameter, or execute |
| `d` / `Ctrl+D` | Dry-run (shows the command without leaving the TUI) |
| `Ctrl+S` | Save the selection to `.ai-launch.yaml` |
| `Ctrl+P` | Save the selection as a named profile in the global config |
| `?` | Help |
| `q` / `Esc` / `Ctrl+C` | Quit |

In the mount browser you can also type a path directly — including paths
containing the letters `j`/`k`/`l`/`h`: once typing starts, those letters go
to the input instead of navigating, and the arrow keys stay browser-only
no-ops until the input is submitted or cancelled.

On Windows the Jail toggle and the permissions that depend on it (ssh, gh,
docker, gpu) do not appear in the TUI at all.

## Support matrix

| Platform | ai-jail (sandbox) | ai-memory | Harnesses |
| --- | --- | --- | --- |
| Linux (amd64) | Yes (bubblewrap) | Yes | Yes, sandboxed |
| Linux (arm64) | No official ai-jail asset | Yes | Yes, sandbox depends on ai-jail |
| macOS (arm64) | Yes (sandbox-exec) | Yes | Yes, sandboxed |
| macOS (amd64) | No official ai-jail asset | Yes | Yes, sandbox depends on ai-jail |
| Windows | **No** — the supported path is WSL2 | Yes (`ai-memory-windows-x86_64.zip`) | Yes, **without sandbox** |

ai-jail has no Windows support ("probably never", per the author); on Windows
the launcher disables the jail with an explicit warning and removes the
permissions that depend on it. To run sandboxed from Windows, use WSL2.

## Configuration

| File | Scope | Contents |
| --- | --- | --- |
| `~/.config/ai-launch/config.yaml` | Global (machine) | Catalog of `agents`, `tools`, `permissions`, `default_mounts`, `profiles`, `memory_server_url`, `memory_auth_token` |
| `<project>/.ai-launch.yaml` | Workspace | Selection: `agent`, `permissions`, `mounts`, `options` (includes `jail_flags`, `param_values`, `extra_args`) |
| `~/.config/ai-launch/install-state.json` | Global | Already-installed release tags |
| `~/.config/ai-launch/install.log` | Global | Install log (0600, no tokens) |
| `~/.local/share/ai-launcher/bin/ai-memory` | Global | Managed ai-memory native runner (exported as `AI_MEMORY_NATIVE_BIN`) |

The full schema (fields, defaults, ai-jail v1.15 `jail_flags`) is in
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Security

Layers of defense:

- Sandbox via ai-jail (bubblewrap on Linux, sandbox-exec on macOS) with
  read-only mounts by default and permissions (ssh, gh, docker, gpu) off by
  default.
- Every install is SHA-256 checksum-verified (`.sha256`, `.sha256sum`,
  `checksums.txt`, `SHA256SUMS`, or the release body); `allow_unverified:
  true` exists, but it is an explicit operator choice.
- Self-update (`ai-launcher upgrade`) and the `install.sh` curl installer are
  stricter: the release `checksums.txt` is mandatory and there is no
  `allow_unverified` escape hatch. The managed ai-memory native runner comes
  from the same verified assets, so nothing is downloaded inside the jail.
- `memory_auth_token` is only forwarded via environment variable
  (`AI_MEMORY_AUTH_TOKEN`) to the child process and is **never written to
  `install.log`**.
- Configs are written atomically with 0600 permission.

What it does **NOT** protect against:

- **Environment variables are visible inside the jail** (except in ai-jail
  lockdown mode). Do not trust secrets to the environment of a sandboxed
  session.
- **Windows runs WITHOUT a sandbox** — the harness executes with all the
  user's privileges. For hostile workloads on Windows, use WSL2 or a
  disposable VM.
- **A sandbox is not a VM.** Bubblewrap/sandbox-exec share the host kernel;
  hostile or untrusted workloads call for a disposable VM, not a jail.
- It does not replace token hygiene: anyone with access to the global config
  reads `memory_auth_token`.

## Documentation

| Document | Contents |
| --- | --- |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | System shape: data flow, package map, state, invariants |
| [docs/design-decisions.md](docs/design-decisions.md) | Thematic decisions, trade-offs, and what we are not doing |
| [docs/cicd.md](docs/cicd.md) | GitHub Actions pipeline, SonarCloud, and the release process |
| [docs/test-strategy.md](docs/test-strategy.md) | Test pyramid, coverage gate, and the Gherkin contract suite |
| [AGENTS.md](AGENTS.md) | Instructions for AI agents working in this repository |

## Roadmap

- [x] Interactive TUI with real execution (Enter launches the harness)
- [x] Named profiles and harness-declared parameters
- [x] Installer with mandatory SHA-256 checksum
- [x] Windows as a first-class citizen without jail
- [x] Gherkin contract suite against ai-jail/ai-memory drift
- [x] Automated release pipeline (autotag semver + GoReleaser: archives, `checksums.txt`, SBOM)
- [x] Self-update (`ai-launcher upgrade`) and POSIX curl installer (`install.sh`)
- [ ] Native sandbox on Windows (depends on upstream; unlikely)
- [ ] GUI (not planned; the TUI is the interface)

## License

MIT — see [LICENSE](LICENSE).
