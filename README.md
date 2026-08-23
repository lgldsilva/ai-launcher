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

`ai-memory run` accepts a fixed harness list — `claude`, `codex`, `opencode`,
`pi`, `crush`, `omp`, `kimi`, `grok` — and rejects anything else. A catalog
agent outside that list either declares `memory.run_harness` to map onto one of
them (as `oc` does onto `opencode`) or declares `supports_memory: false`;
pre-flight fails with `memory-harness-unsupported` rather than letting the
rejection surface as an opaque error inside the sandbox. `--no-memory` runs any
harness under the jail alone.

When the managed ai-memory native binary exists
(`~/.local/share/ai-launcher/bin/ai-memory`, provisioned by `--install` /
`--upgrade` from the verified release assets), the launcher exports
`AI_MEMORY_NATIVE_BIN` into the launched process so the memory wrapper uses it
directly — nothing is downloaded inside the jail.

## Screenshots

Terminal captures below are **sanitized** (generic paths, no hostnames or
tokens). On your machine, `--dry-run` prints the real argv for your mounts and
PATH. The TUI frame matches the current select ≠ run flow (`Space`/`Enter`
select, **`r`** runs).

### Interactive TUI

```text
ai-launcher

Agent
  (installed only · most recently used first · Space/Enter select · r RUN)
  Selected: Pi (pi)
  [ ] Continue last session  (ai-memory run)
  [●] Pi                 (pi) · last used
  [ ] Claude Code        (claude) · ready
  [ ] Codex              (codex) · ready
  …

Permissions
  [◆] Jail / Sandbox
  [ ] SSH access
  [ ] GitHub CLI
  [ ] Docker socket
  [ ] GPU passthrough

Mounts
  Tips
    a  or  +  or  /     add a folder
    Space                toggle read-only ↔ read-write
    Backspace            remove highlighted mount
    r                    RUN the selected agent
  /Volumes/Data              (read-write)   # from your default_mounts
  /Volumes/Data/Projetos     (read-write)   # or added with `a` in this panel

Options
  [✓] Jail / Sandbox
  [✓] ai-memory
  [ ] New workstream
  [ ] --yolo
  [ ] --fresh

Preview: ai-jail --no-docker --rw-map /Volumes/Data --rw-map /Volumes/Data/Projetos \
  ai-memory run pi --executable /usr/local/bin/pi
```

Typical sequence: `↑/↓` highlight → `Space`/`Enter` **select** → (optional
Permissions/Mounts/Options) → **`r`** RUN. Pre-flight runs *inside* the TUI;
on failure the UI stays open with the error. After a successful `r`, stderr
shows a short start banner and the child runs under a PTY (raw mode so nested
TUIs such as `oc` still get arrow keys).

### Dry-run (CLI)

Use `--dry-run` to print argv and exit without executing. Captures below use
placeholder paths; your output will list your real mounts and executables.

```bash
# Full chain: jail + ai-memory + harness. The directory holding the resolved
# binary is mapped read-only so --executable is reachable inside the jail.
ai-launcher --agent pi --dry-run
# ai-jail --exec --no-docker --map /usr/local/bin --rw-map /Volumes/Data --rw-map /Volumes/Data/Projetos \
#   ai-memory run pi --executable /usr/local/bin/pi

# Linux-style mounts (from a default_mounts entry that exists on this host)
# ai-jail --exec --no-docker --map /usr/local/bin --rw-map /storage --rw-map /storage/Projetos \
#   ai-memory run pi --executable /usr/local/bin/pi

# OpenCode Presets (catalog command "oc") maps to ai-memory harness "opencode"
ai-launcher --agent oc --dry-run
# ai-jail --exec --no-docker --map ~/.local/bin --rw-map … ai-memory run opencode --executable ~/.local/bin/oc

# Continue last managed session (no harness name)
ai-launcher --continue --dry-run
# ai-jail --exec --no-docker --rw-map … ai-memory run

# Jail only (no memory layer)
ai-launcher --agent pi --no-memory --dry-run
# ai-jail --exec --no-docker --map /usr/local/bin --rw-map … /usr/local/bin/pi

# Memory only (no jail)
ai-launcher --agent claude --no-jail --dry-run
# ai-memory run claude

# Permissions are opt-in (Docker off by default — prefer enabling only when needed)
ai-launcher --agent claude --ssh --gh --dry-run
# ai-jail --exec --no-docker --ssh --rw-map …/.config/gh --map ~/.local/bin ai-memory run claude --executable ~/.local/bin/claude
```

### Docker container backend

Instead of ai-jail, the agent can run inside a Docker container built from the
selected toolchain stacks. Enable it with `--docker-backend` plus one or more
`--stack` flags. The backend auto-detects Docker, Podman, or nerdctl, while
`--container-runtime` selects one explicitly. Selecting infrastructure
services switches the lifecycle from one `docker run` container to a
materialized `docker-compose.yaml`, giving the agent service DNS, persistent
per-service data, and health-checked dependencies.

```bash
ai-launcher --agent claude --docker-backend --stack go --stack python --dry-run
# docker run --rm -i -w /path/to/project \
#   -v /path/to/project:/path/to/project \
#   -v ~/.claude:~/.claude:ro -v ~/.claude.json:~/.claude.json:ro \
#   --add-host=host.docker.internal:host-gateway \
#   ai-launcher-box:<sha12> claude
```

Quick start with PostgreSQL and Redis:

```bash
ai-launcher --docker-backend --agent claude --stack go \
  --service postgres --service redis generate
ai-launcher --docker-backend compose up
```

The generated project-local `.ai-launcher/` directory is the inspectable
source of truth. It contains `Dockerfile`, `install-config.yaml`, the
cross-compiled Linux launcher when a release agent needs it, and
`docker-compose.yaml` when services are selected. `generate` and `compose up`
rebuild these derived files from the current selection. If the existing
`docker-compose.yaml` differs from the new rendering, ai-launcher shows a
line-by-line diff in the TUI and asks whether to keep the current file or
replace it. Keeping it preserves manual ports, environment entries, or extra
services; replacing applies the current launcher YAML. The decision is hashed
in `.compose-approval.json`, so the same accepted customization is not
questioned again until either the file or the generated selection changes.

For CLI and automation, use `--compose-update=keep` or
`--compose-update=replace`. The default is `prompt`; a non-interactive command
with a conflict stops and prints the diff instead of overwriting it.
`compose down`, `logs`, and `ps` consume the existing materialized file and ask
you to run `generate` when it is missing. Put durable selection changes in the
launcher YAML; use the Compose review when you intentionally need a custom
port or an additional container.
The generated `Dockerfile` and `install-config.yaml` remain derived files and
are refreshed without this Compose customization step.

To use another Docker daemon/context, pass `--container-context` or save
`options.container_context` in the workspace config. The value is applied to
Docker info, image inspection/build, `docker run`, and Compose commands;
empty means the Docker CLI's current context. In the TUI, edit the Docker
context resource and use Up/Down to select a context returned by Docker; the
empty `(current)` entry leaves the host's current context unchanged. Manual
entry remains available if the daemon cannot list contexts. The CLI equivalent
is `docker context ls`.

The TUI also exposes Container → Runtime. It lists `auto`, Docker, Podman, and
nerdctl with local PATH availability; selecting Podman or nerdctl clears an
incompatible Docker context before launch.

Loopback ai-memory and MCP endpoints are rewritten to the host gateway without
mounting the host filesystem. The gateway is on by default; turn it off with
`options.container_host_gateway: false`, the `--no-container-host-gateway` flag
(the reverse is `--container-host-gateway`), or the Container → Host gateway
toggle in the TUI, when the container must not reach host TCP services. For a
narrower MCP boundary, put an allowlisted HTTP/SSE MCP proxy on one dedicated
host port and point the agent at that port; do not use `--network host` or
mount the Docker socket.

When `ai-launcher` is started from a terminal with Compose services selected,
it runs the agent through `docker compose run --rm --service-ports agent` so
the terminal belongs to the agent instead of showing dependency logs. The
dependencies are stopped automatically when that interactive session exits;
service data is preserved in `.ai-launcher/data/<service>` and is not removed
by the launcher's automatic `compose down`. Generated service data is a host
bind mount, so even `compose down --volumes` does not erase it.

The image is built from the selection (stacks + agent + auxiliary tools) and tagged by a
content hash, so an identical selection reuses the cached image — the build
runs only once. The images are large by design (multi-GB: the toolchain
stacks, the agent installers, and the shared development profile), and the
content-hash tag is what keeps that cost a one-time event per selection rather
than a per-launch one. On the first launch the launcher cross-compiles a Linux copy
of itself, materializes the build context, and runs `docker build` (the
installer runs inside the build with its checksum verification intact;
script agents install via their official `curl | bash` recipe). The base
image carries Node LTS via nvm only when the Node stack or an agent that
declares a Node prerequisite is selected, and the Java stack installs JDK 21
via SDKMAN. A Go/Python/Rust-only image does not create `/opt/nvm` or
`/usr/local/lib/nvm-bin`. Each stack shares its host toolchain/cache directories (nvm, sdkman,
cargo, go-build, m2...) read-write with the container, so downloads are
reused on both sides. Portable package and build caches are resolved from the
host platform and mounted at Linux container paths; native toolchain managers
such as nvm and SDKMAN are shared automatically only on a compatible Linux
host. The development profile also includes Git, SSH tooling, `jq`, `yq`,
`ripgrep`, `fd`, archive tools, and the Docker CLI. When the trusted catalog
contains `semidx`, the image installs its checksum-verified CLI on `PATH`, so
agent MCP configurations can use the stdio command `semidx mcp`; the launcher
mounts only `~/.config/semidx` and `~/.cache/semidx`, never the host's complete
home or config tree. The project and each selected agent's credential/history
directories are mounted read-write at identical paths — log in on the host
or inside the container once and the session persists for both. Host-only
credentials such as SSH and GitHub remain read-only. Services listening on
the host (the ai-memory server, MCP servers) stay reachable via
`host.docker.internal`; config files that store MCP URLs are copied,
rewritten, and mounted over the originals so the host files are never
modified. Set `AI_LAUNCHER_NO_REWRITE=1` to disable the localhost rewrite.
The generated image runs the agent as the non-root `ai-launcher` user. It also
contains the Docker CLI; selecting the Docker permission mounts the host
control socket and adds its group to the container process, allowing commands
such as `docker build` and `docker run` to use the host daemon. This is
Docker-outside-of-Docker, not a daemon started inside the image, and socket
access remains effectively host-root access.

Resource limits and published ports are opt-in:

```bash
ai-launcher --docker-backend --agent claude --stack go \
  --container-memory 4g --cpus 2.0 --pids 512 \
  --publish 3000:3000 --network bridge --dry-run
```

For an agent plus PostgreSQL and Redis, the generated Compose network resolves
`postgres:5432` and `redis:6379` from the agent. Their default host ports are
published for host tools; choose different ports in `container_service_ports`
when a local service already occupies 5432 or 6379. The override replaces the
published mappings for that service while preserving its internal Compose
port:

```yaml
options:
  container_service_ports:
    postgres:
      - host: 15432
        internal: 5432
    wiremock:
      - host: 18080
        internal: 8080
```

The CLI equivalent is repeatable: `--service-port postgres=15432:5432`.
In the TUI, select a service and press Enter to edit its published mappings;
an empty value keeps the service available only on the Compose network.

**Warning:** published service ports bind on all host interfaces
(`0.0.0.0`), and the catalog services ship development-default credentials
(`dev`/`dev`, `admin`/`admin`, and similar). Anyone on the LAN can reach a
published service while the Compose session is up — keep it on a trusted
network, publish only what host tools need, or firewall the ports.

Each catalog service keeps its own declared container data target and maps it
to `.ai-launcher/data/<service>` when Compose is generated. PostgreSQL uses
`.ai-launcher/data/postgres`, Redis uses `.ai-launcher/data/redis`, and
WireMock uses `.ai-launcher/data/wiremock` for `/home/wiremock`. Services
without a persistent data path are not given a fake volume. The TUI shows the
catalog target and the project data directory.

Persistence is service-specific, including the runtime flags that make the
mounted directory effective: DynamoDB Local uses `-sharedDb -dbPath ./data`,
NATS uses JetStream with `-js -sd /data`, Jaeger all-in-one uses its Badger
backend under `/badger`, and Mailpit stores its SQLite database at
`/data/mailpit.db`. NGINX, Traefik, and the OpenTelemetry Collector remain
stateless in their default catalog configuration; their configuration,
certificates, or file-storage extensions must be enabled explicitly before a
project data directory is appropriate.

For browser-based editing, select the `code-server` service. It publishes
port 8080, mounts the project at `/home/coder/project`, and persists its
configuration and extensions under `.ai-launcher/data/code-server`. The image
keeps password authentication enabled by default; use SSH forwarding or a
TLS/authenticated reverse proxy before exposing it outside a trusted network.
For terminal editing, select the `Neovim` stack. It installs `nvim` and
shares the platform-specific Neovim configuration, data, state, and cache
directories when they exist. SSH or the optional container tmux session is the
transport for that terminal workflow. An optional `zsh` stack installs zsh
alongside the default bash for terminals that prefer it.

In the TUI, toggle `Container (docker)` in Options to switch the backend
from the jail (they are mutually exclusive), then pick stacks in the new
Container section.

For a multi-window interactive session, enable `Container tmux` in the TUI or
set `options.container_tmux.enabled: true`. The image includes tmux and starts
the agent in the `ai-launcher` session, so normal `Ctrl-b c` and pane keys
remain available. Existing host customizations are mounted read-only: by
default `~/.tmux.conf`, `~/.tmux.conf.local`, and `~/.tmux` when they exist.
Use explicit paths when the host uses another layout:

```yaml
options:
  docker: true
  container_tmux:
    enabled: true
    config: ~/.config/tmux/tmux.conf
    local_config: ~/.config/tmux/tmux.conf.local
    oh_my_tmux_dir: ~/.config/oh-my-tmux
    additional_paths:
      - ~/.tmux/plugins
```

Only those files/directories are shared, always as `ro`; the tmux socket, the
host `$HOME` root, and host credentials outside the normal agent mounts are
not transported into the container. Use `additional_paths` for plugins, scripts
or sourced fragments referenced by the custom configuration. The setting applies
to interactive Docker and Compose paths, not to non-interactive or dry-run
processes.

Dependency directories are configurable in the trusted global config or in a
saved workspace/profile selection. The default `safe` policy shares only
existing portable caches for the selected stacks; a source that does not exist
is reported and skipped. Toolchain directories are platform-aware, and a
macOS/Windows host does not bind-mount a native Linux toolchain into the Linux
image unless `allow_incompatible: true` is explicitly set.

```yaml
# ~/.config/ai-launch/config.yaml
container_dependencies:
  policy: safe
  overrides:
    node.nvm:
      enabled: true
      sources:
        linux: ~/.nvm
        darwin: ~/.nvm
      target: /opt/nvm
      mode: rw
    java.sdkman:
      enabled: true
      source: ~/.sdkman
      target: /opt/sdkman
    java.maven-repository:
      mode: rw
    gradle.cache:
      mode: rw
    rust.cargo-registry:
      mode: ro
```

The built-in IDs cover Go module/build caches, Cargo registry/git, pip, nvm,
npm, SDKMAN, Maven, Gradle, ccache, NuGet, Bundler, Composer, Elixir Mix/Hex,
and Dart/Flutter Pub. Use `policy: none` to disable automatic selection, or
set `enabled: true` for an individual ID. `source` overrides the current host
path; `sources` selects a path by `linux`, `darwin`, or `windows`; `target`
must be an absolute Linux path; and `mode` is `ro` or `rw`.
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

Optional env: `AI_LAUNCHER_UPDATE_TOKEN` or `GITHUB_TOKEN` (Bearer) so the
script pulls assets through the GitHub assets API — needed only for private
forks/mirrors. The public repo installs without any token.

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

Prerequisite: Go 1.25+ (the toolchain declared in `go.mod`).

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
| `ai-launcher --workstream-search "query" [--workstream-id ID] [--limit N] [--json]` | Searches the ai-memory workstream ledger and exits (read-only; not sandboxed) |
| `ai-launcher --save-profile N [flags]` | Saves the merged selection as a profile and exits without launching |
| `ai-launcher --save` (or `--save-only`) | Writes the selection to the local `.ai-launcher/config.yaml` and exits |
| `ai-launcher [docker flags] generate` | Materializes the `.ai-launcher/` artifacts (Dockerfile, install-config.yaml, `docker-compose.yaml`) from the current selection without launching |
| `ai-launcher [docker flags] compose up\|down\|logs\|ps [args]` | Runs the runtime's Compose subcommand against the materialized `.ai-launcher/docker-compose.yaml` |
| `ai-launcher --version` | Prints the binary version (release, commit, build date) and exits |
| `ai-launcher help` | Shows flag usage |

The positional `upgrade` (self-update of the launcher binary) is unrelated to
the `--upgrade` flag (reinstall of the third-party tools).

### Options

| Flag | Effect |
| --- | --- |
| `--agent <cmd>` | Selects the agent (claude, codex, opencode, kimi, …) |
| `--ssh` / `--gh` / `--docker` / `--gpu` | Optional jail helpers (see [Permissions: CLI + config](#permissions-cli--config-what-gets-mounted); none required; all independent of each other) |
| `--display` / `--pictures` / `--tailscale` / `--systemd-user` / `--mise` / `--worktree` | Optional jail passthroughs, all off by default; each one **forces** its capability on (platform support varies — `display` and `systemd-user` are Linux-only) |
| `--doctor` | Prints the installed ai-jail / ai-memory versions against the supported floor and exits |
| `--no-jail` / `--sandbox` | Explicitly disables / enables ai-jail |
| `--memory` / `--no-memory` | Enables / disables the ai-memory layer. A non-boolean value is the container memory limit instead: `--memory 4g` (or `--memory=512m`) is an alias for `--container-memory` |
| `--yolo` / `--no-yolo` | Passes (or not) the agent's dangerous-mode flag |
| `--new <name>` / `--workstream <name>` | Creates / resumes an ai-memory workstream |
| `--workspace <name>` / `--project <name>` | Scope forwarded to `ai-memory run` |
| `--workstream-search <query>` | Queries the ai-memory ledger and exits; `--limit N` and `--json` refine it |
| `--workstream-id <id>` | Which workstream `--workstream-search` reads. Defaults to `$AI_MEMORY_WORKSTREAM_ID`, which ai-memory sets inside the agents it manages — so an agent launched from here needs no id, and a bare shell does. It is an **id**, not the name `--workstream` takes; upstream maps neither to the other |
| `--continue` | Continues the last ai-memory session of this checkout |
| `--fresh` | Starts a new ai-memory session in the current workstream instead of resuming one (mutually exclusive with `--continue`) |
| `--mount <path>[:ro\|:rw]` / `--map` | Read-only mount (ro by default; `--map` is an alias) |
| `--rw-map <path>` | Read-write mount |
| `--param <name=value>` | Sets a parameter declared in the agent catalog (repeatable) |
| `--extra-args "<args>"` / `--args` | Extra arguments forwarded to the harness (`--args` is an alias) |
| `--profile <name>` | Loads a saved profile as the base selection |
| `--config <path>` / `--local-config <path>` | Alternative paths for the global / local config |
| `--no-local-config` | Ignores the workspace config and starts from the built-in defaults |
| `--no-color` / `--high-contrast` | Disables color output / switches to high-contrast colors |
| `--compose-update=prompt\|keep\|replace` | Handles a changed generated Compose file: review interactively, preserve it, or replace it |
| `--dry-run` | Runs pre-flight validation, prints the generated command, and exits without executing. Warnings go to stderr and still exit 0; a fatal issue reports the problem, still prints the argv, and exits non-zero |
| `--version` | Prints the binary version and exits |
| `--save`, `--save-only`, `--save-profile`, `--list-profiles`, `--delete-profile`, `--install`, `--upgrade`, `--add`, `--path`, `--command`, `--description` | See the commands table above |

### Permissions: CLI + config (what gets mounted)

Jail permissions are **optional auxiliaries**. None of them (including GitHub
CLI) is required to run ai-launcher or an agent. Leave every toggle off if you
do not need SSH, `gh`, Docker, or GPU inside the sandbox — pre-flight never
demands that those host tools be installed.

When you **do** opt in, each permission maps to ai-jail flags and/or mounts so
the host tool’s **config/state** stays usable inside the sandbox. All of them
still **require Jail / Sandbox** when enabled (pre-flight fails if Jail is off
and a permission is on). On Windows they are hidden: there is no ai-jail.

| Permission (TUI / flag) | What ai-launcher adds | Platform | Optional on the host | Notes |
| --- | --- | --- | --- | --- |
| **SSH access** / `--ssh` | `ai-jail --ssh` | Linux + macOS | OpenSSH client / agent if you use SSH from agents | Native ai-jail capability (not a free-form mount list) |
| **GitHub CLI** / `--gh` | `--rw-map $HOME/.config/gh` | Linux + macOS | Only if you want `gh` inside the jail: install + `gh auth login` | **Auxiliary only.** Does not install `gh` and does not fail when `gh` is missing. Mounts host config so tokens travel with the tool |
| **Docker socket** / `--docker` | `ai-jail --docker` | Linux + macOS | Docker (or compatible) socket if agents need containers | **Opt-in only.** Socket access is effectively host root — enable only for trusted projects |
| **GPU passthrough** / `--gpu` | `ai-jail --gpu` | Linux + macOS | GPU devices (mainly Linux) if agents need GPU | Independent of Docker — ai-jail exposes `/dev/dri` and `/dev/nvidia*` directly |
| **Display passthrough** / `--display` | `ai-jail --display` | Linux only | X11/Wayland session if agents open GUI windows | Forces X11/Wayland on; ai-jail auto-detects it when left off |
| **Pictures folder** / `--pictures` | `ai-jail --pictures` | Linux + macOS | — | Off by default |
| **Tailscale socket** / `--tailscale` | `ai-jail --tailscale` | Linux + macOS | Tailscale daemon if agents use the tailnet | Off by default |
| **systemd user bus** / `--systemd-user` | `ai-jail --systemd-user` | Linux only | systemd user session | Off by default; hidden on macOS and unsupported there (pre-flight warns `unsupported-platform`) |
| **mise integration** / `--mise` | `ai-jail --mise` | Linux + macOS | mise if agents rely on its shims | Forces mise on; ai-jail auto-detects it when left off |
| **Git worktree passthrough** / `--worktree` | `ai-jail --worktree`; Docker/Compose bind-mounts registered worktree roots | Linux + macOS | — | Docker discovers existing non-bare paths from `git worktree list --porcelain`, including worktrees outside the current project; stale registrations are skipped |

Permissions unsupported on the current platform are hidden in the TUI; if a
hand-edited config enables one anyway, pre-flight reports an
`unsupported-platform` warning instead of failing the launch.

A permission only ever **forces a capability on**. ai-jail treats an unset
capability as *auto* — enabled when the resource exists on the host — so leaving
a permission off means "let ai-jail decide", not "off". To force one **off**,
use the tri-state `jail_flags` block below. When both name the same capability
(`docker`, `gpu`, `display`, `tailscale`, `mise`, `worktree`), the explicit
`jail_flags` value wins and the permission stays silent, so the argv never
contradicts itself.

**Docker is the one exception, and it is deliberate.** The launcher never
leaves the socket to auto mode: every jailed command carries `--docker` or
`--no-docker`, explicitly. Up to ai-jail v1.15.x an existing
`/var/run/docker.sock` was bind-mounted read-write on sight, with no flag and
no warning — and write access to that socket is root on the host, which walks
past bubblewrap, Landlock, seccomp and every mask in one `docker run -v /:/host`
(ai-jail [issue #88](https://github.com/akitaonrails/ai-jail/issues/88),
fixed in v1.16.0). Stating it costs one argv token and makes "off by default"
a property of the command this launcher builds rather than of whichever ai-jail
you happen to have installed.

Model: the sandbox already sees normal system paths for tools (e.g. `/usr`,
Homebrew layout depending on ai-jail). Sensitive **state** is withheld unless
you opt in. Without `--gh`, a host `gh` binary may be visible on PATH but
`~/.config/gh` is not mapped — so auth will not work until you enable the
permission. If you never use `gh`, ignore the toggle entirely.

When you *do* want GitHub CLI inside the agent:

```bash
# Host (only if you use gh): CLI installed and authenticated
gh auth status

# Dry-run: with --gh, argv includes --rw-map …/.config/gh
ai-launcher --agent claude --gh --dry-run
# example:
#   ai-jail --exec --no-docker --rw-map /home/user/.config/gh ai-memory run claude …
```

If `gh` fails inside the agent after you opted in:

1. Confirm Jail is on and GitHub CLI is checked (or `--gh`).
2. Confirm dry-run shows `--rw-map …/.config/gh`.
3. If `gh` lives outside usual system prefixes (e.g. only under `~/bin`), add
   that directory under **Mounts** as well.
4. On native Windows, use WSL2 for jail + permissions; native Windows runs
   without sandbox and without these permission rows.

Extra mounts (project data, caches, second disks) stay under **Mounts** /
`--mount` / `--rw-map` and are independent of the table above.

When no mount is given by flags, local config, or profile, the global
`default_mounts` are suggested (rw by default). **There are no built-in
candidates** — the launcher does not guess a path on your machine. ai-jail
already mounts the current project read-write on its own, so most launches
need no extra mount at all.

Declare your own in `~/.config/ai-launch/config.yaml`; only paths that exist
on the host are applied, so one config can cover several machines:

```yaml
default_mounts:
  - /storage/Projetos       # applied where it exists
  - /Volumes/Data/Projetos  # applied on the other machine
```
With the jail enabled, every home dotfile symlink that resolves outside
`$HOME` (for example `~/.android -> /Volumes/MSD512/.android` or
`~/.cache -> /storage/cache`) is also auto-mounted rw, because ai-jail
recreates those symlinks inside the sandbox without their targets.

Precedence of the final selection: **built-in defaults < local
`.ai-launcher/config.yaml` < profile (`--profile`) < explicit flags**.

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

# Read the ledger back: the delta injected into the next harness is
# size-limited by design, so an old decision lives here. From inside an agent
# this launcher started, AI_MEMORY_WORKSTREAM_ID supplies the workstream
ai-launcher --workstream-search "why did we drop redis"
# From a bare shell, name the workstream yourself
ai-launcher --workstream-search "migration that failed" --workstream-id "$WS" --limit 50 --json

# Parameter declared in the catalog (Kimi's stable key is "query", mapped to --prompt)
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

The fixed toggles of the **Options** section, in display order (parameter
rows declared by the agent's catalog entry follow them):

| Toggle | CLI equivalent | Notes |
| --- | --- | --- |
| `Jail / Sandbox` | `--sandbox` / `--no-jail` | Hidden on Windows, where ai-jail has no build |
| `ai-memory` | `--memory` / `--no-memory` | Wraps the harness in `ai-memory run` |
| `New workstream` | `--new <name>` | Seeds the name `new-workstream`; the value shows on a `workstream:` line under the toggles |
| `--yolo` | `--yolo` / `--no-yolo` | Passes the agent's dangerous-mode flag |
| `--fresh` | `--fresh` | Only shown while `ai-memory` is on — with the memory layer off the flag would toggle a no-op |

| Key | Action |
| --- | --- |
| `Tab` / `Shift+Tab`, `1`–`5` | Cycle / jump between sections |
| `↑/↓` or `j/k` | Move within the current section |
| `Space` | Toggle permission/option, mount mode, or load profile |
| `Space` / `Enter` (Agent) | **Select** the highlighted agent (UI stays open) |
| `r` / `Ctrl+Enter` | **RUN** the selected agent (UI closes only if pre-flight passes) |
| `a` / `+` / `/` | Mounts: open add-folder panel |
| `Backspace` | Remove the selected mount (or edit path while adding) |
| `d` / `Ctrl+D` | Dry-run (preview argv, stay open) |
| `Ctrl+S` | Save the selection to `.ai-launcher/config.yaml` (running with `r` also autosaves, so the next open restores it) |
| `Ctrl+P` | Save the selection as a named profile in the global config |
| `?` | Help (full key list) |
| `q` / `Esc` / `Ctrl+C` | Quit without running |

**Typical sequence**

1. `↑/↓` highlight an agent (e.g. Pi)  
2. `Space` or `Enter` → **select** (`Selected: Pi`, interface stays open)  
3. (optional) `Tab` → Permissions / Mounts / Options  
4. `r` → **RUN** (pre-flight runs inside the TUI; on failure you stay there)

**Add a folder:** `a` → browse → `Enter` to add → `Esc` cancel.

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
| `~/.config/ai-launch/config.yaml` | Global (machine) | Catalog of `agents`, `tools`, `permissions`, `default_mounts`, `container_dependencies`, `recent_agents`, `profiles`, `memory_server_url`, `memory_auth_token` |
| `<project>/.ai-launcher/config.yaml` | Workspace | Selection: `agent`, `permissions`, `mounts`, `options` (includes `jail_flags`, `param_values`, `extra_args`, `container_dependencies`, `container_tmux`); legacy `.ai-launch.yaml` is migrated on save |
| `~/.config/ai-launch/install-state.json` | Global | Already-installed release tags |
| `~/.config/ai-launch/install.log` | Global | Install log (0600, no tokens) |
| `~/.local/share/ai-launcher/bin/ai-memory` | Global | Managed ai-memory native runner (exported as `AI_MEMORY_NATIVE_BIN`) |

### The workspace config is not trusted

`.ai-launcher/config.yaml` travels with the repository, so for any checkout you
did not write yourself it is somebody else's input. In one-shot CLI and
`--dry-run` mode it cannot lower your security posture on its own — the launcher
refuses, naming the explicit opt-in:

| The file tries to | Result |
| --- | --- |
| Select an `agent` the catalog cannot resolve | Refused. Run it with `--agent <name>`, or register it in the **global** catalog with `--add` |
| Set `options.jail: false` while the global catalog defaults the sandbox on | Refused. Pass `--no-jail` to accept it |
| Declare a relative mount, or mount `/` | Refused |
| Mount a sensitive tree (`/etc`, `/usr`, another user's home, a container socket…) | Refused. Pass `--mount <path>` to accept it |
| Enable a `permission` (ssh, gh, docker, gpu…) | Refused. Pass the matching flag (`--ssh`, `--gh`, `--docker`…) to accept it |
| Set `options.yolo: true` | CLI: refused; pass `--yolo`. Bare TUI: shown as the `--yolo` option for review before `r` |
| List `options.extra_args` | Refused. Pass `--args "<args>"` to accept it |
| Set `options.param_values` (model selection, catalog flags) | Refused. Pass `--param name=value` to accept it |
| Set any `options.jail_flags` | Refused. There is no per-flag CLI toggle: save the selection or select a profile |

What you type on the command line stays fully trusted: the boundary is around
the file, not around you. The bare `ai-launcher` invocation is the interactive
consent surface: it opens the TUI so the operator can review and adjust the
effective permissions, mounts, jail/container mode, and visible options before
running. An unresolved agent from the workspace file remains blocked because it
cannot be selected from the catalog.

**The file you saved yourself is yours.** `--save` / `Ctrl+S` records the
file's canonical path and SHA-256 in the global config, and a file matching
both is honored like operator input — no refusals, no flags to repeat. Editing
it changes the hash and the boundary applies again, and a clone carrying
identical bytes at a different path never inherits the record.

> Upgrading from schema 2.0: trust records used to be bare hashes with no path
> bound to them. They are still read — your catalog, profiles and token are not
> touched — but they no longer grant trust. Run `--save` once per workspace you
> had saved before, and the record is rewritten in the path-bound form.

The full schema (fields, defaults, ai-jail `jail_flags`) is in
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md). Docker passthrough is **opt-in**
via the Docker permission / `--docker`, and refused explicitly otherwise — do
not pass the host socket unless you would hand the same workload `sudo`.

The `jail_flags` block of `.ai-launch.yaml` mirrors the ai-jail v1.16 CLI. Its
booleans are tri-state and match ai-jail's own model: unset leaves the
capability in ai-jail's *auto* mode (enabled when the resource exists), while
`true` and `false` force it on or off. Forcing on is a real state, not a
synonym for unset, so both forms are always emitted:

| `jail_flags` key | Type | ai-jail flag emitted |
| --- | --- | --- |
| `lockdown`, `private_home`, `tailscale`, `gpu`, `display`, `mise`, `worktree`, `landlock`, `seccomp`, `rlimits`, `status_bar`, `hide_config`, `save_config` | tri-state bool | `--flag` when `true`, `--no-flag` when `false`, nothing when unset |
| `docker` | tri-state bool | `--docker` when `true`, `--no-docker` when `false`. **Unset is not auto**: with no `jail_flags.docker` and the Docker permission off, the launcher still emits `--no-docker` |
| `status_bar_style` | string (`dark` / `light` / `pastel`) | `--status-bar=STYLE` (suppresses the boolean `status_bar` forms) |
| `browser` | string (`hard` / `soft` / `off`) | `--browser=hard` / `--browser=soft` / `--no-browser` |
| `claude_dir` | string | `--claude-dir <PATH>` |
| `overlay_maps` | list | `--overlay-map <PATH>` per entry |
| `mask` | list | `--mask <PATH>` per entry |
| `mask_exceptions` | list | `--mask-except <PATH>` per entry |
| `deny_paths` | list | `--deny-path <PATH>` per entry |
| `deny_path_exceptions` | list | `--deny-path-except <PATH>` per entry |
| `hide_dotdirs` | list | `--hide-dotdir <NAME>` per entry |
| `allow_tcp_ports` | list of int | `--allow-tcp-port <PORT>` per entry (lockdown only) |

## Security

Layers of defense:

- Sandbox via ai-jail (bubblewrap on Linux, sandbox-exec on macOS) with
  read-only mounts by default and every permission (ssh, gh, docker, gpu,
  display, pictures, tailscale, systemd-user, mise, worktree) off by default.
  Off means "leave ai-jail's own auto-detection alone", never "force off" —
  that is a `jail_flags` decision. **Docker is the exception**: it is forced
  off with `--no-docker` unless you opt in, because auto mode for that one
  capability meant an unannounced host-root escape on ai-jail ≤ 1.15.x.
- `ai-launcher --doctor` pins the supported upstream floor (`ai-jail` ≥ 1.16.0,
  `ai-memory` ≥ 1.19.0): it probes `--version` and reports
  (`ai-jail-version-too-old` / `ai-memory-version-too-old`) when the installed
  binary is older. It is a separate command on purpose — pre-flight validation
  never execs the upstream binaries, so a launch costs no extra processes.
- Every install from a GitHub release asset is SHA-256 checksum-verified
  (`.sha256`, `.sha256sum`, `checksums.txt`, `SHA256SUMS`, or the release
  body); `allow_unverified: true` exists, but it is an explicit operator
  choice. A recipe that publishes release assets always installs from them —
  the unverified `source_url` path (HTTPS scheme and a `#!` shebang, from a
  mutable branch) is only a fallback for recipes with no assets at all.
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

## Ecosystem

ai-launcher is a **companion** to the [ai-jail](https://github.com/akitaonrails/ai-jail)
+ [ai-memory](https://github.com/akitaonrails/ai-memory) stack (Fabio Akita / community).
It does not reimplement those tools; it builds the same chain you would type by hand,
with a TUI, profiles, and a verified installer.

Optional third piece of the daily toolkit (not integrated here — use alongside):
[ai-usagebar](https://github.com/akitaonrails/ai-usagebar) for provider quota / spend.

Background reading (author):
[ai-jail](https://akitaonrails.com/tags/ai-jail/),
[ai-memory](https://akitaonrails.com/tags/ai-memory/).

Documentation and UI strings in this repository are **English**.

## Documentation

| Document | Contents |
| --- | --- |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | System shape: data flow, package map, state, invariants |
| [docs/design-decisions.md](docs/design-decisions.md) | Thematic decisions, trade-offs, and what we are not doing |
| [docs/cicd.md](docs/cicd.md) | GitHub Actions pipeline, SonarCloud, and the release process |
| [docs/test-strategy.md](docs/test-strategy.md) | Test pyramid, coverage gate, and the Gherkin contract suite |
| [docs/remediation-plan.md](docs/remediation-plan.md) | Live tracker: audit findings against the ai-jail / ai-memory composition, their status, and the phased plan to close the open ones |
| [docs/review-2026-07-30.md](docs/review-2026-07-30.md) | Historical: the external review that drove #19–#23, with each finding's outcome |
| [docs/remediation-plan-2026-07-30.md](docs/remediation-plan-2026-07-30.md) | Historical: the PR-by-PR plan built from that review, and what each PR actually became |
| [docs/handoff-2026-07-30.md](docs/handoff-2026-07-30.md) | Historical: what that work left unverified, and how each item was resolved |
| [AGENTS.md](AGENTS.md) | Instructions for AI agents working in this repository |
| [SECURITY.md](SECURITY.md) | Reporting a vulnerability, supported versions, and the ai-jail / ai-memory boundary |
| [CHANGELOG.md](CHANGELOG.md) | Breaking changes and their migrations (per-release notes are on the releases page) |

## Roadmap

- [x] Interactive TUI with real execution (`r` / Ctrl+Enter runs; Space/Enter select)
- [x] Named profiles and harness-declared parameters
- [x] Installer with mandatory SHA-256 checksum
- [x] Full ai-jail v1.16 capability surface (tri-state auto / force-on / force-off)
- [x] `--dry-run` runs pre-flight validation before printing the command
- [x] Windows as a first-class citizen without jail
- [x] Gherkin contract suite against ai-jail/ai-memory drift
- [x] Automated release pipeline (autotag semver + GoReleaser: archives, `checksums.txt`, SBOM)
- [x] Self-update (`ai-launcher upgrade`) and POSIX curl installer (`install.sh`)
- [x] Configurable `default_mounts` (no built-in guesses) + home-symlink auto-mounts
- [x] `ai-memory run` harness remap for wrappers (e.g. catalog `oc` → harness `opencode`)
- [ ] Native sandbox on Windows (depends on upstream; unlikely)
- [ ] GUI (not planned; the TUI is the interface)

## License

MIT — see [LICENSE](LICENSE).
