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
the modeling of the ai-jail v1.15/v1.16 flags (`--lockdown`, `--private-home`,
`--no-*` toggles for default-on capabilities) only exists because the CLI
changed under us.

**How.** Explicit contract: the Gherkin suite in
`test/features/launcher.feature` (run by `test/gherkin`) locks the exact argv
composition — `ai-jail → ai-memory run → harness` order, `--no-*` forms,
workstream/workspace/project scope. The contract asserts what ai-launcher
**emits**, never what upstream **accepts**: a new upstream release that
renames a flag installs cleanly and CI stays green. That gap is closed by
version pinning, not by the contract alone — `config.MinAIJailVersion`
(`1.16.0`) and `config.MinAIMemoryVersion` (`1.19.0`) declare the supported
floor in exactly one place, and `ai-launcher --doctor` probes
`ai-jail --version` / `ai-memory --version` (5s timeout), reporting
`ai-jail-version-too-old` / `ai-memory-version-too-old` when the installed
binary is below it. Report, never block: the user may knowingly run ahead. The
probe is a separate command rather than a pre-flight check because
`Validator.Validate` runs on every launch and inside the TUI update loop —
forking two children there buys ~300 ms of latency and a frozen UI for a
diagnostic nobody asked for. The jail flags live in a declarative structure
(tri-state `config.JailFlags`) that mirrors ai-jail's own auto / on / off
model, so absorbing a new ai-jail version is adding a row to a table, not
rewriting `if`s — and the process for a bump is: review the upstream
changelog, update the constants and the Gherkin contract in the same commit.

**Trade-offs.** We depend on the third-party CLI surface and the release
asset formats (e.g. ai-jail publishes only linux-x86_64 and macos-aarch64).
In exchange, we never carry sandbox code of our own.

**Scope boundary.** `jail_flags` exposes only ai-jail *launch capabilities* —
the toggles and mounts that shape the sandbox a launched agent runs in. The
setup and diagnostic verbs stay out on purpose: `--init` / `--bootstrap`
(create config and exit), `--clean` (discard the project `.ai-jail` for one
run), `--dry-run`, `--verbose`, `--help`, `--version`. They are not ways to
configure a launch; wiring them through the selection model would either
contradict it (`--clean` fighting `hide_config`) or add noise no launch needs.
Operators who want them can run ai-jail directly. Absorbing a new ai-jail
release therefore starts by sorting its new flags into "launch capability"
(add a `jail_flags` row) versus "setup/diagnostic verb" (leave out).

## Data-driven per-harness parameters

**Decision.** Harness-specific flags are declared in the catalog
(`agents[].params`: `name`, `flag`, `takes_value`) and filled via `--param
name=value` or the parameter row in the TUI — never by per-agent special-case
code.

**Why.** The trigger was Kimi: besides `--model`, it accepts `--prompt`
(initial prompt). Encoding that in Go would mean rebuilding the launcher for
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
`internal/catalog`, `internal/launcher`, and `internal/container` (excluding
the PTY executor and the `replace_*.go` files). `internal/tui` and
`internal/installer` stay out of the denominator.

**Why.** Line coverage in code coupled to an interactive terminal and to
spawned processes measures theater, not safety — forcing 90% there would
generate fragile, fake tests. What really cannot break is the pure logic:
config persistence, safe defaults, argv composition.

**How.** `make test-coverage` (and the CI `test` job) filter the profile with
`awk` and fail below 90% on the filtered total; the CI job calls
`make test-coverage` instead of repeating it. The `COVERAGE_EXCLUDE` regex in
`.ai-standards.env` states the same boundary for the commit hooks, the
`sonar.coverage.exclusions` for SonarCloud, and the CI `sonar` job still
duplicates the `-coverpkg` list — four statements of one boundary,
which have to be changed together. TUI, installer, and executor are covered by
`go test -race -shuffle=on ./...` (race-only) and by the Gherkin contract
suite.

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

## The child inherits the environment, minus the AI_MEMORY_* we own

**Decision.** The launched process inherits the parent environment as-is. The
launcher only takes ownership of the variables it configures — today
`AI_MEMORY_SERVER_URL`, `AI_MEMORY_AUTH_TOKEN` and `AI_MEMORY_NATIVE_BIN` —
and for those, *unconfigured means unset*: `upsertEnv` removes the inherited
entry rather than forwarding it.

**Why the removal matters.** Returning early on an empty value looked harmless
and was not. A stale `AI_MEMORY_AUTH_TOKEN`, or an `AI_MEMORY_SERVER_URL`
exported by direnv, a `.envrc` or a wrapper script, would reach the sandboxed
agent and point it at somebody else's memory server — with the launcher
reporting nothing, because from its point of view it had configured nothing.

**What we are deliberately not doing.** There is no allowlist. Everything else
in the parent environment — `GITHUB_TOKEN`, cloud credentials,
`AI_LAUNCHER_UPDATE_TOKEN` — crosses into the jail. An allowlist is the right
long-term answer, but it breaks the agents themselves (they need `PATH`,
`HOME`, `TERM`, proxy settings, language runtime variables, and whatever the
user's shell adds for their toolchain), so it needs a curated list and a way
for the operator to extend it. Until then this is a known, recorded exposure,
not an oversight.

The same applies to `AI_MEMORY_AUTH_TOKEN` itself: env vars are readable inside
the jail, so the agent can read the token (already noted in the README security
section). `ai-memory run --config <PATH>` is an upstream-supported alternative
that would keep it out of the environment entirely; evaluating it is the
natural next step for this entry.

## Catalog boolean flags are trusted, but never silent

**Decision.** A catalog param declared with `takes_value: false` injects its
`flag` verbatim into the argv when any truthy `param_values` entry enables
it. The global catalog (`~/.config/ai-launch/config.yaml`) is the trusted
location — unlike the workspace-local `.ai-launch.yaml`, which is treated as
attacker-supplied input and gated by the trust check — so there is no
allowlist of injectable flags. Instead, pre-flight emits a
`catalog-flag-param` warning naming every catalog-declared boolean flag that
fires, so a hand-edited declaration is visible at launch time.

**Why a warning and not an allowlist.** An allowlist would either duplicate
the catalog (a second list of "approved" flags to keep in sync) or block
legitimate operator-declared flags until a code change ships. The threat is a
config the operator did not notice editing; making the injection visible on
every launch answers it without taking the catalog's extensibility away. The
built-in catalog declares no boolean params, so the warning only fires for
operator-added declarations.

## The docker container backend replaces the jail, never composes with it

**Decision.** `options.docker: true` switches the sandbox from ai-jail to a
docker container built from the selected toolchain stacks. The two backends
are mutually exclusive: enabling docker disables the jail, and the TUI toggle
enforces the same rule. Docker is a *substitute* sandbox, not a layer on top
of the jail.

**Why.** A container already provides the isolation ai-jail exists for
(process, filesystem, network namespace); running the jail inside it would
add a second sandbox over a sandbox, doubling the failure surface without
adding isolation the container does not already have. Keeping them
exclusive also keeps the argv contract simple: one chain, one backend,
locked by the Gherkin suite.

The image does still install `ai-memory` for a memory-enabled harness. That
is a runtime dependency of the selected agent, not a second sandbox. The
host-side `ai-jail` remains outside the image; nested jail execution is not
enabled implicitly because the current catalog has no Linux ARM64 ai-jail
asset and a missing nested runtime would create a false security guarantee.

**Why same-path mounts.** The project is mounted read-write at its own path
(`-v $PWD:$PWD`) rather than at a fixed `/workspace`: ai-memory scopes
projects by absolute path, and agent configs record absolute paths. A
container-internal path that differs from the host path would break session
continuity and memory scoping.

**Why agent config dirs are read-write (shared login).** ai-jail rebuilds
`$HOME` as a tmpfs so a compromised sandbox cannot persist host
credentials — the jail is an isolation boundary. The docker backend is the
*trusted* mode: each agent mounts only its own config directories
read-write, so a login made inside a container persists to the host and to
every other container sharing the same host paths, and the host sees
sessions the container wrote. The agent never sees another agent's config
dirs (each map is per-agent), so there is no cross-agent credential leak.
The risk — a compromised container can mutate its own login — is the same
risk the operator already accepts by running the agent on their code; it is
the explicit trade for the shared-login workflow. Host-only credentials
(`--ssh`, `--gh`) stay read-only.

**Why the trust gate treats docker as a sandbox change.** A workspace-local
`.ai-launch.yaml` switching the sandbox to docker is a security-relevant
change made by the repository, exactly like `jail: false`; it is refused
until the operator passes `--docker-backend` or saves the selection.

**Why installs pin versions (and never `latest`).** The image tag is a
content hash of the selection. Installing `latest` would make the tag lie as
upstream moves — the same selection would produce different images over
time. Pinning the release version in the hashed selection keeps the cache
honest. The checksum-verified installer runs inside the image build itself
(`RUN ai-launcher --install` against a minimal config copied into the
build context), reusing the host installer's verification logic instead of
reimplementing checksum validation in shell.

## The internal Compose network is all-or-nothing in v1

**Decision.** `options.container_network_internal: true` marks the
generated Compose network `internal: true` (`internal/container/compose.go`),
blocking every outbound route for every service on it — including the
agent's own LLM API calls. There is no selective allowlist: it is either
"the container backend has the same open egress it always had" or "nothing
gets out at all." Two pre-flight warnings guard the two ways this surprises
an operator: `internal-network-blocks-agent` fires whenever the toggle is on
and a Compose service is selected (the common case), and
`internal-network-requires-compose` fires when it is on with zero services
selected — the plain `docker run` backend (`BuildRunCommand`) has no Compose
network at all, so the toggle is a silent no-op there.

**Why Compose-only.** `internal: true` is a Compose Spec network-level
attribute; there is no `docker run` equivalent short of a separate
`docker network create --internal` step outside this launcher's argv
building. `BuildCompose` already only runs when at least one infrastructure
service is selected (see "The docker container backend replaces the jail"
above and `internal/launcher/compose.go`'s service-count guard) — a launch
with the container backend but no services goes through the plain `docker
run` path today regardless of this toggle, so the requires-compose warning
is not hypothetical.

**Why the field is persisted separately from the resolved value.**
`LaunchConfig.ContainerNetworkInternal *bool` carries the raw tri-state
operator choice, distinct from the already-resolved
`LaunchConfig.Docker.NetworkInternal bool` that `BuildCompose`/
`BuildRunCommand` actually consume. `ContainerHostGateway` only kept the
resolved form, so `--save`/`--save-profile` silently dropped the operator's
raw choice; that gap is not repeated here on purpose, because losing this
one is a safety regression (the operator believes egress is blocked when the
next launch reopens it) rather than a cosmetic default drifting back.

**Why all-or-nothing instead of a domain allowlist now.** Docker networks
have no domain/IP allowlist primitive — `internal: true` is a network-layer
on/off switch. Selective egress needs a companion dual-homed proxy service
(squid/tinyproxy on both the internal network and a normal bridge with real
internet access, with `HTTP_PROXY`/`HTTPS_PROXY` pushed into the agent
container) — the same "run a narrow proxy instead of opening the door"
philosophy already applied to the *inbound* `host.docker.internal` gateway
case (see ARCHITECTURE.md, "MCP server reachability" / the host-gateway
section: "run a host-side MCP HTTP/SSE proxy... do not use `--network host`
and do not mount the Docker socket"). `AddInfrastructureServiceWithDataDir`
and the catalog `Service`/`ComposeServiceFromCatalog` representation
already used for postgres/redis are the reusable insertion points for that
proxy service — a v2, not shipped here.

## Why `.ai-launcher/` is a directory

**Decision.** Docker artifacts live in a project-local `.ai-launcher/`
directory: Dockerfile, install config, generated Compose, and any Linux
launcher binary needed by the image build.

**Why.** The old temporary Dockerfile was uninspectable after a launch, and a
Compose document had nowhere durable to live. A directory gives the operator
one reviewable, declarative environment and keeps generated files together.

**Trade-off.** The directory is machine-local generated state. Compose is
deterministic from the launcher selection, but an operator may deliberately
keep a reviewed customization such as an extra service or host port. The
launcher records hashes for that decision and asks again when the file or the
generated selection changes; non-interactive callers must choose
`--compose-update=keep|replace` explicitly.

## Why runtime abstraction matters

**Decision.** Docker, Podman, and nerdctl implement one runtime interface with
auto-detection ordered Docker → Podman → nerdctl and explicit selection that
never silently falls back.

**Why.** Hardcoding `docker` blocked Podman users, while Podman supports
daemonless/rootless development and nerdctl is natural on containerd/Kubernetes
nodes. The interface centralizes command, Compose, host-gateway, and socket
policy.

## Why Compose is used when services exist

**Decision.** A launch with infrastructure services uses Compose; a launch
without them stays a single `docker run`/runtime command. Catalog data paths are
mapped to project-local `.ai-launcher/data/<service>` bind mounts.

**Why.** One container cannot run infrastructure alongside the agent with
service DNS, shared networks, health dependencies, and persistent per-service
data. Compose
allows the agent to resolve `postgres:5432` or `redis:6379`; a bare
`docker run` cannot provide that `mongo:27017` DNS topology.

## Why rw config dirs are the shared-login model

**Decision.** Each selected agent mounts only its own configuration/history
directories read-write at the same host/container path. Host-only SSH and
GitHub credential mounts remain read-only.

**Why.** Read-only agent mounts caused logins made in the container to be lost
when it exited. Read-write same-path mounts make login and session state
persist while the per-agent map prevents another agent's credentials from
being exposed.

## Why service images are version-pinned

**Decision.** Catalog service images use explicit versions, never `latest`.

**Why.** `latest` tag drift would make the generated environment change
without a selection change and invalidate the content-hash/cache promise.
Pinned images make Compose output deterministic and make failures reproducible.

## Official vendor installers are recorded with allow_unverified

**Decision.** The mainstream coding agents (claude, codex, opencode, kimi,
agy, pi, omp, cursor, grok, devin) ship a `curl | bash` installer as their
canonical installation method; there is no checksum-verifiable release
alternative for them. The built-in catalog now records those official
installer URLs (`source_url`) with `allow_unverified: true`.

**Why this is not the invariant-4 weakening the project forbids.** The rule
"do not weaken with allow_unverified in the default catalog" exists to stop
silently accepting unverifiable downloads where a verified alternative
exists. For these CLIs the vendor installer *is* the only distribution
channel — the operator already accepts this exact trust model when
installing on the host (the README's `curl | bash`). The URLs are pinned to
vendor domains, never third-party mirrors, so the trust anchor is the vendor's
TLS + their own script. Any agent without an official installer stays
recipe-less and falls back to the host-binary mount.

**Why the base image carries nvm and the Java stack uses SDKMAN.** Several
installers (pi, devin) hard-require a recent Node, and SDKMAN is the only
portable way to pin a JDK LTS across distros. Both are shell-only, so the
Dockerfile symlinks node/npm/npx and java/javac into /usr/local/bin and adds
the nvm node bin dir to PATH. The runtime user is non-root; nvm and SDKMAN are
installed under `/opt/nvm` and `/opt/sdkman` so host UID mapping cannot strand
the executable behind /root.

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
- **`ai-memory install-instructions` in the install flow.** `--install`
  provisions binaries globally; `install-instructions` writes a routing block
  into `CLAUDE.md` / `AGENTS.md` **in the current checkout**. Folding a
  file-mutating step into a binary installer would be a surprise, so it stays
  out until it gets its own flag and its own answer to "which file may it
  touch". Nothing is blocked meanwhile — running the upstream command by hand
  does the same thing.
- **Two upstream questions we handle at the trust boundary.** Both need a live
  upstream runtime for a definitive answer, so the launcher treats them as
  policy decisions rather than pretending they are closed:
  [#17](https://github.com/lgldsilva/ai-launcher/issues/17) — a
  checkout-controlled `.ai-jail` symlink can change what ai-jail reads and
  writes when config masking is disabled. The launcher already emits
  `--no-hide-config` automatically for bwrap compatibility; for an unsaved
  workspace file that is a security decision made by the repository, so the
  trust gate now refuses the launch unless the operator passes
  `--no-local-config` or saves the selection.
  [#18](https://github.com/lgldsilva/ai-launcher/issues/18) — an authenticated
  ai-memory token could in principle resume or mutate a workstream under another
  workspace/project if the launcher forwards repository-selected scope fields.
  The trust gate now treats `options.workspace` and `options.project` like
  `options.yolo` or `options.extra_args`: an unsaved local config setting them
  requires the matching `--workspace` / `--project` CLI flag (or a saved
  selection). This does not prove what the upstream server authorizes, but it
  ensures the operator explicitly agrees with the scope the launcher forwards.
  A live upstream integration test remains the only way to close the question
  fully; until then the boundary records the risk explicitly.

## Mistakes to avoid

- [ ] Do not reimplement ai-jail/ai-memory functionality — absorb upstream and update the Gherkin contract.
- [ ] Do not bump `MinAIJailVersion`/`MinAIMemoryVersion` without reviewing the upstream changelog and updating the Gherkin contract in the same commit.
- [ ] Do not add `if agent == "x"` in the builder — declare a `params:` entry in the catalog.
- [ ] Do not emit jail flags as loose strings — use `config.JailFlags` (tri-state) to respect ai-jail's auto / on / off model.
- [ ] Do not let a permission and a `jail_flags` entry emit the same ai-jail capability — the explicit flag wins, or the argv contradicts itself.
- [ ] Do not exec an upstream binary from `Validator.Validate` — it runs on every launch and inside the TUI update loop.
- [ ] Do not install anything without a verifiable checksum — do not "work around" it with `allow_unverified` in the default catalog.
- [ ] Do not weaken self-update verification — a missing `checksums.txt` is a hard error, never a silent skip.
- [ ] Do not put `internal/tui` or the PTY executor in the coverage gate denominator.
- [ ] Do not write tokens to logs, argv, or local configs — token only via the child process env.
- [ ] Do not change the canonical `ai-jail → ai-memory run → harness` order — if it changes, the contract suite must fail first.
- [ ] Do not let the docker backend and the jail compose — they are mutually exclusive; enabling one must disable the other.
- [ ] Do not install `latest` inside the image — the content-hashed tag would lie; pin the release version in the selection.
- [ ] Do not share another agent's credentials — same-path mounts are read-write only for the selected agent's own config/history directories; host-only SSH/GitHub mounts stay `ro`.
- [ ] Do not turn the TUI back into a command generator — dry-run is the explicit print-only path.
- [ ] Do not treat the Windows jail warning as an error — degradation with a warning is the correct behavior.
