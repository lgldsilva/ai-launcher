# Evaluation — Unmerged changes vs `main` (2026-08-07)

**Project:** ai-launcher (Go TUI/CLI launcher)
**Baseline:** `origin/main` against unmerged refs
**Date:** 2026-08-07

## 1. Executive summary

There are **three real blocks of unmerged work** and **five stale branches**
(already absorbed by squash):

| Block | Kind | Size | Merge pending |
|---|---|---|---|
| **Docker Container Mode (committed)** | Large feature, Phase 0 | 52 files, **+7,255 lines**, 16 commits | not pushed (local worktree only) |
| **Docker Container Mode (uncommitted)** | Phases 2/4/5/6 in progress | 39 files modified (**+4,742/−546**) + 25 untracked | **uncommitted, not pushed** |
| **Cursor credential-store fix** | macOS bug fix | 1 commit, +151 lines | lost on a stale branch |

The Docker Container Mode is a well-structured, well-documented feature with
good cohesion and SOLID adherence, but it lives **nowhere remote** — a
data-loss risk. Beyond the 16 committed commits, the worktree carries a
**second, uncommitted wave of work** (services, resources, runtime, compose
and socket files) that implements phases 2, 4, 5 and 6 of the plan but has
not been committed or pushed. The Cursor fix is small, ready and isolated,
but was **forgotten on a stale branch**. The rest is reference hygiene.

**Validation run (2026-08-07):** linters and tests are all green; the only
gate concern is that `internal/container` alone measures **88.4%**, below the
**90%** standard (see §5).

## 2. Branch inventory (vs `origin/main`)

### 2.1 Local branch / worktree

| Ref | Commits ahead of `main` | State |
|---|---|---|
| `feature/docker-container-mode` (worktree `.slim/worktrees/docker-container-mode`) | 16 | local-only, `ahead 14 / behind 1`, **no remote tracking** |

### 2.2 Remote branches

| Ref | Commits ahead of `main` | Verdict |
|---|---|---|
| `origin/fix/memory-yolo-unsupported` | 3: `7a05418` (real), `2234cac`, `35a84ef` | 2 already squashed (#29, #31); `7a05418` **unmerged** |
| `origin/feature/tui-parity` | `35a84ef` | Already squashed (#29) — **stale** |
| `origin/fix/agent-catalog-parameters` | `7b37b3e` | Already squashed (#34) — **stale** |
| `origin/fix/sonar-remediation` | `31114e5` | Already squashed (#32) — **stale** |
| `origin/ci/sonar-pinned-scan-action` | — | Already merged (#30) — **stale** |
| `origin/security/ai-jail-symlink-and-memory-scope` | 8 (dev history) | Already squashed (#35) — **stale** |

> Note: local `main` is **9 commits behind** `origin/main` (PRs #29–#36).
> A `git pull` is recommended first.

## 3. Docker Container Mode — structural evaluation

### 3.1 What it is (Phase 0)

An alternate backend that runs the agent in a Docker container instead of
`ai-jail`, behind `--docker-backend`. It adds: a consent trust gate, stack
selection (go, python, rust, java, maven, gradle, node, cpp), in-image agent
install (checksum-verified release / gated script / npm / host bind), MCP
overlays `localhost→host.docker.internal`, same-path credential/cache mounts,
content-hashed image tags, and a Gherkin contract.

### 3.2 Package structure — committed tip vs working tree

**Committed tip (`92ff812`): 11 production files, ~2,339 lines.**

| File | Responsibility | Prod lines |
|---|---|---|
| `selection.go` | Image selection normalization/validation | 165 |
| `stack.go` | Stack catalog | 212 |
| `build.go` | Image build argv | 153 |
| `run.go` | `docker run` argv | 242 |
| `dockerfile.go` | Dockerfile generation | 106 |
| `overlay.go` / `rewrite.go` | MCP overlays | 91 / 59 |
| `agentmounts.go` | Credential/agent mounts | 273 |
| `image.go` | Image tag/hash | 72 |
| `version.go` | Agent version comparison | 155 |
| `installconfig.go` | Install configuration | 127 |

**Working tree (current): 17 production files, ~3,674 lines + ~3,843 test
lines.** The uncommitted wave adds six new files corresponding to plan phases
2, 4, 5 and 6:

| New file (uncommitted) | Plan phase | Responsibility | Prod lines |
|---|---|---|---|
| `runtime.go` | Phase 2 | runtime detection (docker→podman→nerdctl) | 202 |
| `services.go` | Phase 4 | services catalog | 277 |
| `resources.go` | Phase 5 | resources/ports/network | 191 |
| `compose.go` | Phase 6 | docker-compose generation | 507 |
| `dependencies.go` | — | service dependency graph | 443 |
| `socket_unix.go` / `socket_other.go` | — | docker socket handling | 34 |

Also uncommitted: `internal/launcher/compose.go`, `internal/config/`
`{dependencies,runtime,services,localdir}.go`, and TUI container plumbing.

### 3.3 Cohesion

**Assessment: HIGH.** The package is single-domain (container backend) with
files separated by responsibility. Exemplary design documentation (invariants
R1–R9, decisions C1/C2, gotcha M5). No container responsibility is scattered
outside the package beyond the integration point in `cmd/ai-launcher/main.go`.

Notes:
- `main.go` grew **+356 lines** of docker orchestration
  (`dockerRunConfigFromOptions`, trust gate, TTY, env). Acceptable for a CLI
  handler, but it is the highest-coupling point.
- **Duplicated concept of "required agent mounts"**: `AgentRequiredMounts`
  (in `internal/launcher/builder.go`) vs `AgentMounts`
  (`internal/container/agentmounts.go`). Risk of divergence/per-platform
  drift.
- Mixing "pure argv building" with "host checks" (`ExistsOnHost`, `os.Stat`)
  in the same package — mitigated by dependency injection, but worth
  revisiting in phases 1–8.

### 3.4 SOLID

| Principle | Assessment | Evidence |
|---|---|---|
| **S**RP | ✅ Strong | `BuildRunCommand` is a pure function (no I/O), one responsibility per constructor |
| **O**CP | ✅ Good | `InstallKind` (release/script/npm/host) extensible via enum; `Validate()` per kind |
| **L**SP | ✅ N/A | Composition/structs, no inheritance |
| **I**SP | ⚠️ Partial | `RunConfig` is wide (many required fields); a broad DTO, acceptable today |
| **D**IP | ✅ Good | `runtimeGOOS` (indirect var), `ExistsOnHost` injected as a param, `AgentMounts(cfg, cmds, ExistsOnHost)` — testable without a real FS |

### 3.5 Code quality

- **Exemplary testability**: `BuildRunCommand` is pure → table tests + Gherkin
  contract without I/O.
- **Edge-case handling**: mounts only emitted when the host path exists
  (`docker` refuses a `-v` with a missing source, M5); the image tag hashes
  content (script/npm/setup) to invalidate cache.
- **Runtime bugs fixed in the final review** (GLM 5.2): explicit HOME,
  overlays before the image arg, params/yolo reaching the agent — all
  re-validated with real docker builds.

## 4. Cursor fix — `7a05418`

**Small, isolated, ready and forgotten.** For `cursor-agent` on macOS with
jail active: sets `AGENT_CLI_CREDENTIAL_STORE=file` (Cursor's wrapper reaches
for the macOS keychain, inaccessible inside the jail) and mounts `~/.cursor`
rw when present. Preserves any user-supplied value. Tested on real macOS.

- **3 files** (`main.go`, `builder.go`, `builder_test.go`), **+151/-1 lines**.
- **Low impact, real value**, fully covered (`runtimeGOOS` indirection).
- **Risk**: not in `origin/main` nor any live branch — only on the stale
  `origin/fix/memory-yolo-unsupported`. Could be lost.

## 5. Sonar

| Item | Status |
|---|---|
| Branch `fix/sonar-remediation` | Merged (#32) — baseline SonarCloud findings addressed |
| Coverage gate extended to `internal/container` | Consistent across all 4 points (`.ai-standards.env`, `Makefile`, `sonar-project.properties`, `ci.yml`) |
| Docker code (7,255+ committed + ~5,300 uncommitted new lines) on SonarCloud | **Never analyzed** — findings surface only on merge + `sonar` CI job |
| `.scannerwork/report-task.txt` | **Stale** — points to `sonar.internal.lgldsilva.com.br` (decommissioned homelab SonarQube); today it is SonarCloud. Cleanup pending |

## 5.1 Coverage and quality gates (measured 2026-08-07)

Ran the real gates on both the local `main` and the docker worktree:

| Gate | `main` | docker worktree | Result |
|---|---|---|---|
| `make test-coverage` (blended) | **94.8%** PASS | **90.9%** PASS | ✅ |
| `internal/container` alone | n/a | **88.4%** (1105/1250) | ⚠️ **below 90%** |
| `go test ./...` | — | all ok | ✅ |
| `go test -race -shuffle=on ./...` | — | all ok | ✅ |
| `go vet` / golangci-lint | — | clean / **0 issues** | ✅ |
| gosec | — | **0 issues** (38 `#nosec` validated) | ✅ |
| govulncheck | — | **no vulnerabilities** | ✅ |

**Key finding:** the blended gate passes only because `config`/`catalog`/
`launcher` lift the average. `internal/container` itself sits at **88.4%**,
below the project's own 90% logic-package standard. Per-file weak spots:

| File | Coverage |
|---|---|
| `build.go` (image build path) | **68.8%** |
| `socket_unix.go` | **72.7%** |
| `version.go` | **83.3%** |
| `runtime.go` | **84.8%** |
| `resources.go` | **86.1%** |
| `run.go` | **88.1%** |
| `dependencies.go` | 91.0% |
| `compose.go` | 92.2% |
| remaining 10 files | ≥ 93.9% |

## 6. What is PENDING (docker phase plan)

Phases documented in `.slim/deepwork/phases/`. **Phase 0 is committed; phases
2, 4, 5 and 6 are now implemented in the working tree but NOT committed**
(`runtime.go`, `services.go`, `resources.go`, `compose.go`, `dependencies.go`,
`socket_unix.go`). The plan document still marks them pending — it has not
caught up with the working tree:

| Phase | Description | Status 2026-08-07 |
|---|---|---|
| 0 | Docker backend base | ✅ committed (16 commits) |
| 1 | Empirical validation of config dirs | ⬜ pending |
| 2 | Runtime abstraction (docker→podman→nerdctl) | 🟡 in working tree, uncommitted |
| 3 | `~/.ai-launcher/` directory (backup migration) | ⬜ pending |
| 4 | Services catalog (~40+ infra) | 🟡 in working tree, uncommitted |
| 5 | Resources + ports + network (per-profile) | 🟡 in working tree, uncommitted |
| 6 | `docker-compose` generation | 🟡 in working tree, uncommitted |
| 7 | TUI overhaul | 🟡 partial (TUI container plumbing uncommitted) |
| 8 | Final integration + docs | ⬜ pending |

**Confirmed decisions:** rw only on each agent's config dirs; runtime priority
docker→podman→nerdctl; automatic compose when infra services exist; ports per
profile; commits after 18h.

## 7. Risks and recommendations

| Severity | Item | Recommendation |
|---|---|---|
| 🔴 **High** | Docker feature **not pushed** (local worktree only) — data-loss risk | Immediately push to a remote branch; split into one-concern PRs (AGENTS.md) |
| 🔴 **High** | **~5,300 lines uncommitted** (phases 2/4/5/6) in the worktree working tree — highest data-loss risk | Commit the worktree state (or at least `git stash`/push) before anything else |
| 🟠 **Medium** | `internal/container` at **88.4% < 90%** gate (blended passes by luck) | Add tests for `build.go`, `socket_unix.go`, `version.go`, `runtime.go`, `resources.go` before the container package is treated as done |
| 🟠 **Medium** | Cursor fix `7a05418` "lost" | Cherry-pick to a fresh branch from `origin/main`, run gate, open PR |
| 🟡 **Low** | 5 stale remote branches | Delete local/remote refs |
| 🟡 **Low** | Stale `.scannerwork/` (homelab SonarQube) | Remove; do not recreate (AGENTS.md) |
| 🟡 **Low** | Local `main` 9 commits behind `origin/main` | `git pull` |
| 🟡 **Note** | Duplication `AgentRequiredMounts` vs `AgentMounts` | Unify during phases 1–8 review |
| 🟡 **Note** | Wide `RunConfig` | Acceptable; consider grouping into sub-structs if it grows |

## 8. Conclusion

The **Docker Container Mode** feature is technically solid (high cohesion,
good SOLID adherence, excellent testability, strong design docs) and in good
shape to start integrating — but the immediate **operational risk is that it
is not pushed anywhere**. The **Cursor fix** is a small, mature item that
should already have shipped. The rest is branch hygiene and stale config.

**Top-priority action:** push/back up the Docker Container Mode and merge the
Cursor fix. Then clean up stale refs and `.scannerwork/`.