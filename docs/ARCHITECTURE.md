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
| `internal/config` | Versioned schema (`2.1`, reads `2.0`/`1`/`1.0`) of the global and local configs; safe defaults; profiles; tri-state `JailFlags`; atomic 0600 saves |
| `internal/catalog` | Resolves agents against the PATH (`path` > command > aliases) and normalizes dependencies between permissions |
| `internal/launcher` | `Build` (pure argv), `Validator` (preflight with stable codes), `ConstrainToPlatform` (Windows without jail), PTY executor and per-platform `exec`; exports `AI_MEMORY_NATIVE_BIN` when the managed runner exists |
| `internal/container` | Docker backend (pure, no daemon I/O): runtime abstraction (Docker, Podman, nerdctl), stack catalog and Dockerfile generation, content-hashed image tags, resource/port/network validation, dependency and service catalogs, Compose model and rendering, and the `docker run` argv builder |
| `internal/installer` | GitHub Releases client: per-platform asset selection, mandatory SHA-256 verification, tar.gz/zip extraction, `install-state.json` |
| `internal/selfupdate` | `ai-launcher upgrade`: resolves the latest tag, downloads the GoReleaser archive, verifies it strictly against `checksums.txt` (missing/mismatch is a hard error), atomically replaces the running executable |
| `internal/tui` | Bubbletea frontend: 5 sections (Agent, Permissions, Mounts, Options, Profiles); all durable state lives in `launcher.LaunchConfig` |
| `test/gherkin` + `test/features` | Executable contract suite (in-repo Gherkin reader, no BDD dependency) over the argv composition and preflight rules |

## State and storage

| File | Format | Written by |
| --- | --- | --- |
| `~/.config/ai-launch/config.yaml` | YAML (schema `2.1`) | `--add`, `--save-profile`, `--delete-profile`, manual edits |
| `<project>/.ai-launcher/config.yaml` | YAML (schema `2.1`) | `--save`, `Ctrl+S` in the TUI |
| `<project>/.ai-launch.yaml` | YAML (legacy) | read-only fallback; renamed to `.ai-launch.yaml.bak` on the first save that migrates it |
| `<project>/.ai-launcher/Dockerfile` | generated Dockerfile | docker backend materialization (`generate`, launch) |
| `<project>/.ai-launcher/install-config.yaml` | YAML (minimal catalog) | docker backend materialization |
| `<project>/.ai-launcher/docker-compose.yaml` | YAML | docker backend with services (`generate`, `compose up`, launch) |
| `<project>/.ai-launcher/.compose-approval.json` | JSON (0600) | the artifact review flow (accepted custom files, hash-bound) |
| `~/.config/ai-launch/install-state.json` | JSON | `internal/installer` (installed release tags) |
| `~/.config/ai-launch/install.log` | text (0600) | `internal/cmd` (never logs tokens) |
| `~/.local/share/ai-launcher/bin/ai-memory` | binary | `internal/cmd` (managed ai-memory native runner, exported as `AI_MEMORY_NATIVE_BIN`) |

All config saves are atomic (temporary file + `rename`) with 0600 permission.
The workspace file moved from `<project>/.ai-launch.yaml` to
`<project>/.ai-launcher/config.yaml` with the Docker backend: a legacy file is
still read, and the first save writes the directory layout and renames the
legacy file to `.ai-launch.yaml.bak` (`internal/config/local_storage.go`).
Because trust records are path-bound (invariant 2b), the migration invalidates
the record the legacy file had; the same save re-records trust for the new
path. The `.ai-launcher/` artifacts are derived files: they are regenerated
from the selection, and the generated `.ai-launcher/.gitignore` keeps the
temporary ones (build context, image cache, the copied launcher binary, the
approval record, service data) out of version control.

## Cross-cutting invariants

1. **Canonical chain order**: `ai-jail [jail flags] ai-memory run [wrapper
   flags] <harness> [native args]`, or `docker run [flags] <image> <harness>
   [native args]` when the docker backend replaces the jail. No code path
   builds a different order; the Gherkin suite locks regressions out.
2. **Selection precedence**: built-in defaults < local
   `.ai-launcher/config.yaml` < profile < explicit flags. Profiles only
   replace the blocks they define.
2b. **The local config is untrusted input**: `.ai-launcher/config.yaml` ships
   with the repository, so `enforceLocalConfigTrust` refuses one that selects
   an unresolvable agent, disables the sandbox while the global catalog
   defaults it on, switches the sandbox to the docker backend without consent,
   or declares a relative mount or a filesystem root. Global config,
   profiles and command-line flags are trusted; the boundary is around the
   workspace file. The exception is provenance: when the launcher saves the
   file itself (Ctrl+S / `--save`), it records the file's canonical path and
   SHA-256 in the trusted global config (`trusted_local_configs`), and the same
   file with the same bytes is honored like operator input. Any later edit
   changes the hash and the boundary applies again — a cloned repository cannot
   forge the record. The path is part of the record on purpose: a hash alone
   proves the bytes, not the file, so a clone carrying an identical
   `.ai-launcher/config.yaml` would otherwise inherit the trust its author
   recorded. Schema 2.0 stored bare hashes; those rows are still read (so
   upgrading does not discard the rest of the global config) but never grant
   trust, and one `--save` rewrites them in the path-bound form.
   The same path binding is what makes the layout migration safe: moving the
   selection from the legacy `.ai-launch.yaml` to `.ai-launcher/config.yaml`
   invalidates the record the old path held, and the save that performs the
   migration immediately re-records trust for the new path, so a saved
   workspace stays saved and an unsaved one never gains trust by being moved.
   The check reads the workspace file **as loaded, before a profile is layered
   on**, and only for the blocks the file still owns (`localTrustFrom` mirrors
   the conditions in `applyProfile`). Deriving it from the merged selection
   attributed a trusted profile's `jail: false` to `.ai-launcher/config.yaml`
   and refused the launch; conversely, a value a profile replaced never
   reaches the argv, so there is nothing left to refuse.
3. **Atomic 0600 saves** in the global and local configs (`internal/config`).
4. **Mandatory checksum on installs**: without a verifiable checksum the
   install fails, unless an explicit `allow_unverified: true` in the recipe.
   A recipe that publishes release assets always installs from them; the
   unverified `source_url` path is reserved for recipes with no assets.
   Sources are tried in descending order of strength: the GitHub API's own
   asset digest, then a checksum asset, then the **release body** text. That
   last one is weaker than it looks and is deliberately last — release notes
   are mutable markdown, editable without re-uploading any asset, so it is a
   convenience for upstreams that publish no checksum file, not a guarantee.
   Every source is matched by filename (`checksumFor` refuses a bare hash), so
   a body quoting the hash of a *different* asset never satisfies verification.
   Self-update deliberately does not share this ladder — see invariant 5.
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
   The rule belongs to `Options.UnmarshalYAML`, so it holds for **every**
   `options:` block in the schema — the workspace file and a global-config
   profile alike. Applying it only in `LoadLocal` left profiles decoding into
   Go's zero values, and a profile naming just `yolo` launched with the sandbox
   off. A profile with no `options:` block at all keeps a nil pointer, which is
   how it leaves the toggles to the workspace file.
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
   configured mount. A denylist refuses three kinds of target: the filesystem
   root and system trees (`/`, `/etc`, `/usr`, `/var`, `/System`, `/private/*`,
   `/nix`, `/snap`, …), the trees holding **other accounts' data** (`/home`,
   `/Users`, and the macOS firmlink spellings under `/System/Volumes/Data`),
   and the mount-point roots that aggregate every attached volume (`/Volumes`,
   `/media`, `/mnt`, `/run`, `/srv`). A refused target is never mounted and is
   reported by name as a pre-flight warning.
   The merge is re-applied whenever the mount list can change underneath it:
   the TUI keeps the detected targets aside from `launch.Mounts` and re-merges
   them after loading a profile (which replaces the list wholesale) and after
   the Jail toggle turns the sandbox on (which the launcher never prepared
   mounts for). Both paths used to emit an argv with the symlinks dangling
   again, silently.
   Only the tree **itself** is denied — paths beneath it stay auto-mountable,
   which is what keeps the operator's own project volume (`/Volumes/MSD512`)
   and home working. Comparison happens against the *resolved* target, so the
   platform-specific spellings must be listed alongside the logical ones:
   on macOS `/home` is a firmlink to `/System/Volumes/Data/home`, and `/etc`
   and `/var` are symlinks into `/private`.
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
| `version` | string | Schema (`2.1`; also reads `2.0`, `1`, `1.0`) |
| `memory_server_url` | string | ai-memory server; configure this or `AI_MEMORY_SERVER_URL` for the deployment |
| `memory_auth_token` | string | Bearer token, forwarded via env only |
| `agents[]` | list | `name`, `command`, `aliases`, `path`, `supports_memory`, `supports_yolo`, `yolo_flag`, `params[]` (`name`/`flag`/`takes_value`), `release` (GitHub recipe), `memory` (MCP/hooks adapter), `source_url` |
| `tools[]` | list | Auxiliary tool recipes (ai-jail, ai-memory) |
| `permissions[]` | list | `id`, `name`, `default`, `locked`, `requires`, `platforms` (empty = all platforms) |
| `default_mounts[]` | list | Mounts suggested when neither the local config/profile nor `--mount`/`--map`/`--rw-map` define any; read-write by default, with the same optional `:ro`/`:rw` suffix as `--mount`. There are no built-in candidates: `DefaultMountCandidates` returns nothing on every platform, because a default mount is a read-write hole in the sandbox and the launcher has no business guessing one. Only paths that exist on the host are applied, so one config can serve several machines. Pre-selected in the TUI mount manager (removable) |
| `container_dependencies` | map | Trusted machine-wide dependency policy: `policy` (`safe`/`none`) plus per-ID `overrides` with platform-aware `source`/`sources`, Linux `target`, `mode`, and explicit `enabled`/`allow_incompatible` controls |
| `profiles{}` | map | Named selection snapshots (`agent`, `permissions`, `mounts`, `options`) |
| `recent_agents[]` | list | Most-recently-used agent commands (newest first); the TUI lists installed agents in this order |

Local config (`.ai-launcher/config.yaml`; a legacy `.ai-launch.yaml` is still
read and is migrated to the new path on the first save): `agent`,
`permissions{}`, `mounts[]`
(`path`/`mode`), and `options`: `jail`, `memory`, `yolo`, `fresh`,
`new_workstream`, `workstream`, `workspace`, `project`, `jail_flags`,
`extra_args`, `param_values`, and the Docker backend keys `docker`, `stacks`,
`services`, `container_runtime`, `container_memory`, `container_cpus`,
`container_pids`, `container_ports`, `container_network`, and
`container_context`, `container_host_gateway`, `container_network_internal`,
`container_network_allowed_domains`,
`container_environment`,
`container_service_ports`, and
`container_dependencies`, and `container_tmux` (`enabled`, `config`,
`local_config`, `oh_my_tmux_dir`, `additional_paths`).
`container_host_gateway` is optional and defaults
to true for backward compatibility; set it to false to prevent host-network
reachability and loopback MCP rewriting. `container_network_internal` is
optional and defaults to false (open egress); set it to true to mark the
generated Compose network `internal: true`, blocking ALL outbound traffic
from every Compose service including the agent's own API calls —
Compose-only (see "The internal Compose network is all-or-nothing in v1" in
design-decisions.md), and only meaningful with at least one service
selected. `container_network_allowed_domains` is an optional list of
domains; when set alongside `container_network_internal: true`, a squid
proxy is injected on a second `<network>-egress` bridge network instead of
blocking all outbound traffic, restricting the agent to just those domains
(see "The v2 egress-allowlist proxy" in design-decisions.md).

### Docker container backend

`options.docker: true` (or `--docker-backend`) replaces the ai-jail prefix
with `docker run ... <image> <harness> [native args]`. When memory is enabled,
the in-container command is `ai-memory run <harness> [native args]`. The image is built
from the selected toolchain stacks, the agent, and selected auxiliary CLIs. Its
tag is a content hash of the canonical selection (`ai-launcher-box:<sha12>`), so an identical
selection reuses the cached image. See `internal/container` for the pure
logic (runtime abstraction, Dockerfile generation, tag hashing, resources,
services, Compose, and argv composition) — it is measured by the same 90%
coverage gate as the other logic packages.

The container substitutes the jail, never composes with it: enabling docker
disables the jail (the TUI toggle and the CLI both enforce this). The
canonical chain therefore becomes `docker run [flags] <image> <harness>
[native args]` in docker mode. The generated image installs `ai-memory` when
the selected harness declares memory support and verifies the wrapper before
the image layer succeeds. The host's managed native runner is not mounted
into a Linux image: a macOS or Windows runner could have the wrong executable
format, so the image owns its Linux runner.

On launch the launcher materializes a build context (generated Dockerfile,
a minimal install config naming the selected agents, and a cross-compiled
Linux copy of the launcher itself) and runs the selected runtime's build
command, streaming the
daemon output. The in-image install step runs `ai-launcher --install`
against the minimal config, so the host installer's checksum verification
and asset selection run inside the build with GOOS=linux — nothing is
reimplemented in shell (design C1). The image is tagged by the content hash
of the selection, so an identical selection reuses the cached image without
rebuilding.

Mounts are same-path for project and agent state: the project is mounted
read-write at its own path, and
the agent config directories (per-agent map in `internal/container`,
platform-aware) are mounted read-write at identical paths — the shared-login
model. Each agent only sees its own config dirs, so a login made inside a
container persists to the host and to every other container sharing the same
host paths; sessions written by the container are visible on the host too.
The permission→mount mapping from the jail is reused: `--ssh` and `--gh`
mount their config dirs read-only (host-only credentials, not per-agent),
`--docker` mounts the control socket (explicit opt-in; write access there is
root on the host, and the launcher's `DeniedMount` gate refuses the socket
from untrusted configs). The generated image includes the Docker CLI and runs
the agent as `ai-launcher`, outside /root; when the socket is mounted, its
numeric group is added as a supplemental group so the non-root process can
use it. Every generated service is hardened with `cap_drop: [ALL]` and
`security_opt: [no-new-privileges:true]`, bringing the docker backend closer
to the seccomp/landlock baseline the ai-jail path gets from its sandbox. The
agent (which already runs as a non-root user) needs no capabilities handed
back; the egress proxy and each infrastructure service start as root and drop
privileges, so they get exactly the capabilities that drop requires handed
back via `cap_add` — `SETGID SETUID` for the squid proxy and
`CHOWN SETGID SETUID` for catalog services (which also chown their data
directory on first start), each set pinned by a behavioral integration test.
The dangerous capabilities (SYS_ADMIN, NET_ADMIN, SYS_PTRACE, …) stay dropped.
No Docker daemon is started
inside the agent image. The shared
development profile includes Git, SSH tooling, `jq`, `yq`, `ripgrep`, `fd`,
archive tools, and the Docker CLI.

The default trusted tool catalog includes `semidx`, whose release archive is
downloaded and checksum-verified during the image build. It is available on
`PATH` for agent MCP configurations through `semidx mcp` (stdio). Only
`~/.config/semidx` and `~/.cache/semidx` are mounted when the tool is selected;
the launcher never mounts the host's complete home or config tree for this
integration.

Dependency mounts use a separate cross-platform catalog. Existing portable
package/build caches are selected automatically for the chosen stacks: Go
module/build cache, Cargo registry/git, pip, npm, Maven, Gradle, ccache, and
the package homes for NuGet, Bundler, Composer, Elixir Mix/Hex, and Dart/Flutter
Pub. The host path is resolved for `linux`, `darwin`, or `windows`, while the
container receives a stable Linux target and the corresponding environment
variable. Native managers (`.nvm` and `.sdkman`) are selected automatically
under `/opt/nvm` and `/opt/sdkman` only on Linux; a non-Linux host requires an explicit override with
`allow_incompatible: true`. Missing automatic paths are reported and skipped;
missing explicit sources, invalid targets, and invalid modes fail before the
runtime is called. Local dependency settings are subject to the same trust
boundary as other workspace options, while global settings and saved profiles
are trusted.

Example:

```yaml
container_dependencies:
  policy: safe
  overrides:
    node.npm-cache:
      sources:
        darwin: ~/.npm
        windows: '%LOCALAPPDATA%/npm-cache'
      mode: rw
    java.maven-repository:
      source: ~/.m2/repository
      mode: rw
    gradle.cache:
      source: ~/.gradle/caches
      mode: rw
```

The runtime abstraction in `internal/container/runtime.go` keeps this policy
independent of the command line: auto-detection tries Docker, Podman, then
nerdctl; an explicit `container_runtime` never silently falls back. Each
runtime supplies its command, Compose prefix, host gateway, and socket
policy. This lets `podman compose` and `nerdctl compose` use the same
declarative model without duplicating the launcher.

When `services` is empty, the launcher executes the single-container path.
When one or more catalog services are selected, `launcher.BuildCompose` emits
an agent service plus the infrastructure services on one named bridge network.
Service IDs are DNS names (`postgres`, `redis`, and so on), catalog ports are
published for host tools, and each catalog data target is bind-mounted under
`.ai-launcher/data/<service>` so state survives container removal outside the
Docker volume store. The agent gets conservative connection URLs through the
same network. Port collisions,
invalid healthchecks, unknown dependencies, and unsafe volume mappings are
rejected before the runtime is invoked.

The `.ai-launcher/` directory is project-local and inspectable. It contains
the generated `Dockerfile`, `install-config.yaml`, `.gitignore`, the Linux
launcher binary when a release installer needs it, and
`docker-compose.yaml` when services are selected. Before replacing an existing
artifact, the launcher compares the current bytes with the deterministic
rendering from the selection and shows a diff. The TUI offers Keep or Replace;
CLI callers can use `--compose-update=keep|replace`. A small hash record in
`.compose-approval.json` remembers an accepted custom artifact and is
invalidated when either the file or the generated selection changes; the
record covers every generated artifact (`Dockerfile`, `install-config.yaml`,
`.gitignore`, and `docker-compose.yaml`), not just the Compose file. Thus
`compose up`, `down`, `logs`, and `ps` continue to use the exact reviewed
materialized file, while `generate` remains a reviewable update operation.

Container resources are opt-in: memory, CPU, and PIDs limits map directly to
the runtime; `container_ports` maps host-to-container ports; and
`container_service_ports` replaces the published mappings for selected Compose
services without changing their internal service ports. `container_network`
selects a named network. A host network cannot be combined with published
ports, and Compose services require a bridge network. Collisions are rejected
unless the affected services are explicitly remapped.
`container_network_internal: true` marks that network `internal: true`
(Compose-only, all-or-nothing — see design-decisions.md), blocking every
outbound route including the agent's own API calls; pre-flight warns when a
service is selected and rejects the launch when none is (the toggle is a
no-op without a Compose network). `container_network_allowed_domains` softens that: a
squid proxy on a second, non-internal `<network>-egress` network is injected
instead, and only the listed domains (plus subdomains) are reachable — the
same "narrow proxy instead of an open door" pattern the host-gateway section
below uses for inbound MCP reachability, applied outbound (see "The v2
egress-allowlist proxy" in design-decisions.md).

`container_context` selects a Docker context without changing the process
environment. The launcher places it before `run`, `build`, image inspection,
`info`, and `compose`, so all commands use the same daemon. An empty value uses
Docker's current context; non-Docker runtimes reject the setting instead of
guessing a different connection mechanism. The TUI lists contexts from
`docker context ls` when the context editor opens, offers `(current)` as the
empty choice, and still accepts a manually typed name if listing fails.

The selected daemon must be local. All container mounts are same-path bind
mounts of host paths (project directory, home dotfiles, the ai-memory binary,
Compose service data), which only resolve correctly when the daemon runs on
the same machine. After the runtime preflight succeeds, the launcher resolves
the effective endpoint — `DOCKER_HOST` first (it overrides contexts in the
Docker CLI), otherwise `docker context inspect` on the selected or current
context — and rejects `ssh://` endpoints, `tcp://` endpoints to non-loopback
hosts, and any other non-local scheme before building or mounting. `unix://`,
`npipe://`, empty endpoints, and loopback `tcp://` (rootless Docker, colima)
are local. The check fails closed: when the endpoint cannot be determined,
the launch is refused with a message explaining how to select a local
endpoint, because a refused launch beats silently wrong mounts.

An interactive Docker launch may also set `container_tmux.enabled`. The
generated development profile installs tmux and wraps the in-container agent
as `tmux new-session -A -s ai-launcher ...`, leaving the first window attached
to the agent. Host tmux configuration is explicit and read-only: when paths
are omitted, the launcher checks `~/.tmux.conf`, `~/.tmux.conf.local`, and
`~/.tmux`; `config`, `local_config`, and `oh_my_tmux_dir` override those
locations. `additional_paths` covers custom plugins, scripts and sourced
fragments. The mounts preserve the host paths so `HOME`-relative includes and
oh-my-tmux customizations continue to work. The tmux socket and the host home
root are never mounted, and the feature is not applied to non-interactive
launches.

An interactive launch with Compose services uses `compose run --rm agent` so
the agent receives the terminal directly. Compose starts declared dependencies
for that one-shot session; ai-launcher runs `compose down` after the agent
exits, preserving project-local service data while stopping the service
containers and network. Generated service data is a bind mount, so even
`compose down --volumes` does not erase it.

URLs pointing at `localhost`/`127.0.0.1` (the ai-memory server URL and MCP
server configs) are rewritten to `host.docker.internal`, with
`--add-host=host.docker.internal:host-gateway` emitted on Linux so the host
stays reachable; `AI_LAUNCHER_NO_REWRITE=1` disables the rewrite. This is a
network route only: it does not mount `$HOME`, `/`, the Docker socket, or an
arbitrary host directory. `container_host_gateway: false` is the explicit
configuration escape hatch when a project must have no host-network route;
the launcher then keeps loopback URLs unchanged and does not create MCP
overlays, so an unavailable local endpoint fails visibly.

The gateway is not a host-service allowlist: a TCP service reachable on the
host may be reachable from the container. For sensitive MCP deployments,
run a host-side MCP HTTP/SSE proxy bound to a dedicated port, allow only the
required MCP routes there, and point the agent's MCP config at that port. Do
not use `--network host` and do not mount the Docker socket as a shortcut; the
former removes the network boundary and the latter is effectively host-root
access. Config files that store MCP URLs (`~/.claude.json`,
`~/.codex/config.toml`, opencode config, and semidx's
`~/.config/semidx/config.yaml`/`semidx.env`) are handled via overlay: the
launcher copies the file, rewrites the URLs in the copy, and mounts the copy
over the original inside the container, so the host file is never modified
(R7 item 31).

### Permission → jail argv (implementation)

Permissions are **optional auxiliaries**. Pre-flight never requires host tools
such as `gh`, `ssh`, or Docker to be installed; it only enforces dependency
edges among permissions (e.g. every jail-backed permission requires jail).

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

When the `worktree` permission is enabled for the Docker backend, the caller
also runs `git worktree list --porcelain` from the selected workspace and
mounts each existing non-bare worktree root read-write at the same path. This
uses Git's registered metadata only, so a worktree in a sibling directory or a
different volume is included without scanning the host. Stale registrations
are skipped and the discovered paths are printed before launch. The same
mount list is used by `docker run` and generated Compose files; the host jail
backend continues to rely on `ai-jail --worktree`.

`jail_flags` mirrors the ai-jail toggles with tri-state booleans that follow
ai-jail's own model — absent = auto (enabled when the resource exists on the
host), `true` = force on (`--flag`), `false` = force off (`--no-flag`). Forcing
on is a distinct state from leaving unset, so neither form is ever suppressed:
`lockdown`, `private_home`, `tailscale`, `gpu`, `display`, `mise`, `worktree`,
`landlock`, `seccomp`, `rlimits`, `status_bar`, `hide_config`, `save_config`,
`browser` (`hard`/`soft`/`off`), `claude_dir`, `overlay_maps`, `mask`, `mask_exceptions`,
`deny_paths`, `deny_path_exceptions`, `hide_dotdirs`, `allow_tcp_ports`, and
`status_bar_style` (`dark`/`light`/`pastel`, emitted as `--status-bar=STYLE`;
when set it suppresses both boolean `--status-bar` forms). When unset,
`hide_config` is auto-disabled for projects whose `.ai-jail` is a symlink
(bwrap cannot mask a symlink). `save_config` has no such launcher-side
handling — it only forwards ai-jail's write toggle.

## Upstream version compatibility

The launcher composes a specific upstream surface, and that floor is pinned in
two constants in `internal/config`: `MinAIJailVersion` (`1.16.0`) and
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
schema version** (`config.CurrentVersion`, currently `"2.1"`), which versions
the YAML files, not the binary.

## Reading order

For a new contributor, in this sequence:

1. `README.md` — what the user sees.
2. `cmd/ai-launcher/main.go` — flags, precedence, and dispatch.
3. `internal/config/config.go` — schema, defaults, and persistence.
4. `internal/launcher/` — the pure argv composition (the heart), split by
   concern: `build.go` (the `Build` entry point), `argv.go` (the argv passes),
   `jail.go` (permission → ai-jail flags), `args.go` (shell-style argument
   parsing), `environment.go` (wrapper and binary resolution, exported env),
   `worktree.go` (Git worktree discovery), `compose.go` (the Compose model
   builder), and `validation.go` (the pre-flight Validator).
5. `internal/catalog/catalog.go` — agent and permission resolution.
6. `internal/container/` — the Docker backend: runtime abstraction, config-dir
   and cache mounts, Dockerfile generation, content-hashed image tags,
   resources, service catalog, Compose model, and docker run argv builder.
7. `internal/tui/tui.go` — how the TUI reuses the same `LaunchConfig`.
8. `internal/cmd/install.go` + `internal/installer/installer.go` — the
   install flow and checksum verification.
9. `internal/selfupdate/selfupdate.go` — the self-update flow and the atomic
   executable replace.
10. `test/features/launcher.feature` — the executable end-to-end contract.
11. `docs/design-decisions.md` — why it is this way.
