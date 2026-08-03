# Implementation plan — 2026-07-30 review

> **Historical document. Kept as written; the plan is not edited.**
>
> Committed on 2026-08-02 together with `review-2026-07-30.md` and
> `handoff-2026-07-30.md`. Outcome of every PR below is recorded at the bottom.

> **Status, 2026-07-30: implemented, unverified.** Every PR below except PR6
> exists as a commit on the branch stack described in
> `docs/handoff-2026-07-30.md`. None of it was compiled or tested — the
> environment had no Go toolchain. Read the handoff before the plan.
>
> | PR | Branch | State |
> | --- | --- | --- |
> | 1 | `feature/security-trust-gate-hardening` + `fix/trust-record-migration` | done; the migration question was resolved by reading legacy records without honoring them |
> | 2 | `fix/profile-permission-trust` | done, but smaller than planned — the branch already fixed T1/T2; what shipped is the structural guard |
> | 3 | `feat/aijail-116-docker` | done, option (a): `--docker` / `--no-docker` on every jailed command |
> | 4 | `fix/gpu-docker-dependency` | done; upstream README confirms the two are independent capabilities |
> | 5 | `fix/param-values-trust` | done |
> | 6 | — | **open**; needs one command run against the installed `ai-memory`, see the handoff |
> | 7 | `feat/memory-surface` | partial: `--workstream-search` shipped, `install-instructions` deliberately not |
> | 8 + 10 | `chore/hygiene-and-policy-docs` | done |
> | 9 | `feat/portable-default-mounts` | done, option 1 |


Companion to `docs/review-2026-07-30.md`. Scope: every finding in that review.
Delivery: small sequential PRs, each green on CI on its own, conventional
commits, never straight to `main` (AGENTS.md).

Sizing is relative: **S** = one file plus its test, **M** = a package plus
contract/doc updates, **L** = cross-cutting or needs an upstream decision first.

---

## Step 0 — Local cleanup and a known-good baseline

Not a PR. Nothing below is trustworthy until the tree is clean and the suite is
green.

```bash
cd /Volumes/MSD512/Projetos/ai-launcher

# 1. discard the botched hand-copy (B1-B4)
git checkout -- cmd/ai-launcher/main.go
rm -f sensitive_paths.go sensitive_paths_test.go

# 2. the internal/launcher copies are the same file, already committed on the
#    branch — drop the untracked duplicates too
rm -f internal/launcher/sensitive_paths.go internal/launcher/sensitive_paths_test.go

# 3. the .gitignore / .ignore edits are from the slim tooling, keep or drop as
#    you prefer, but decide now rather than carrying them across every PR
git diff .gitignore .ignore

# 4. move to the branch that already has this work done properly
git fetch origin
git checkout feature/security-trust-gate-hardening
git status   # must be clean
```

Good news that removes a step from the original plan: the branch is **already
based on `bde6ea3` (#11)** and is **0 commits behind `origin/main`**. No rebase
is needed.

**Gate — run the full suite before touching anything:**

```bash
make test-all
```

Two `rapid` failure artifacts dated 2026-07-26 sit in
`internal/config/testdata/rapid/` (`TestPropertyProfileRoundTripPreservesAllFields`,
`TestPropertySaveLoadPreservesConfiguredFields`). rapid replays a recorded
failure on the next run, so one of two things is true:

- they still fail → **fix them first**, everything below is built on sand;
- they were fixed and the files are stale → `rm -rf internal/config/testdata/rapid/`
  and move on.

Do not skip this. `AI_STANDARDS_SKIP` is off the table without explicit
authorization (AGENTS.md).

---

## PR 1 — `fix(security): trust gate hardening`

**Branch:** `feature/security-trust-gate-hardening` (exists, 6 commits, based on
main) · **Size:** L (already written) · **Blocks:** PR 2

Ship what is already there:

| Commit | What |
| --- | --- |
| `654a578` | refuse untrusted local capability grants (F1/F2/F3/F6 + `sensitive_paths.go`) |
| `daf3f4d` | strip `AI_MEMORY_*` env when memory is off; bind trust to path |
| `22bd902` | pin CI scanners and quality tools; scope `SONAR_TOKEN` |
| `96dea85` | bound local YAML (256 KiB), sanitize terminal output, harden auto-mount |
| `440f200` | kill the PTY process group on cancel; always reap the child |
| `54a0317` | gofmt |

### One thing to settle before opening the PR

`Global.TrustedLocalConfigs` changes from `[]string` to `[]TrustedLocalEntry`
(path + hash), and `parseTrustedLocalEntry` drops what it calls a *"legacy bare
hash"*. `CurrentVersion` stays `"2.0"`.

Consequence for anyone already running the launcher: every local config they
saved with `--save` silently loses its trust record, and the next launch is
refused by the very gate this PR strengthens — with an error naming an opt-in
they already performed. Pick one:

- **(a) Migrate on load.** A bare-string entry becomes `{Path: "", Hash: h}`
  and still matches on hash alone, with a one-time warning that the record will
  be upgraded on next save. Weakest, but nobody is locked out.
- **(b) Drop, and say so.** Keep the current behaviour, bump `CurrentVersion`
  to `"2.1"`, and put it in the release notes as "re-run `--save` once".
- **(c) Drop silently.** What the branch does today. Not defensible for a tool
  that self-updates.

Recommendation: **(b)**. The whole point of the commit is that a bare hash is
not proof; carrying it forward under (a) keeps the hole open. But it has to be
a documented break, not a surprise.

### Checklist

- [ ] Resolve the migration question above
- [ ] Confirm the F1 map uses **bare** flag names (`"ssh"`, not `"--ssh"`) —
      `flagsWasSet` matches `flag.Flag.Name`. The branch is correct; verify it
      survived any conflict resolution
- [ ] `make test-all` green
- [ ] Open PR, squash-merge

---

## PR 2 — `fix(config): honor profile-owned permissions in the trust gate`

**Branch:** `fix/profile-permission-trust` off main after PR 1 · **Size:** S

Closes **T1** and **T2** from the review.

**T1.** `profileBlocks` tracks `agent` / `mounts` / `options` but not
`permissions`, while `applyProfile` does replace `local.Permissions` when the
profile declares them. So `--profile X` with a permissions block refuses the
launch over file values that were already discarded — the exact bug the comment
at `main.go:255-259` says was fixed for `agent` and `jail`.

```go
type profileBlocks struct {
    agent       bool
    mounts      bool
    options     bool
    permissions bool   // NEW
}

func profileOverrides(profile *config.Profile) profileBlocks {
    // ...
    permissions: profile.Permissions != nil,
}

// in localTrustFrom:
if !overrides.permissions {
    trust.rawPermissions = copyPermissions(raw.Permissions)
}
```

**T2.** Make the `extraArgs` check read like its siblings
(`if trust.optionsRaw && len(trust.extraArgs) > 0`). Correct today only by
accident of where the field is populated.

### Checklist

- [ ] `profileBlocks.permissions` + gate `rawPermissions` on it
- [ ] Align the `extraArgs` guard
- [ ] Regression test in `cmd/ai-launcher/profiletrust_test.go`: a profile with
      `permissions: {ssh: true}` over a local file with `permissions: {docker: true}`
      launches without refusal, and the argv carries `--ssh` and not `--docker`
- [ ] Mirror scenario in `test/features/launcher.feature`

---

## PR 3 — `feat(jail): map the ai-jail v1.16 docker capability and raise the floor`

**Branch:** `feat/aijail-116-docker` · **Size:** M · **Highest value in the plan**

Closes **U1**, **U2**, **U4**. This is the one that makes the README's Security
section true again.

Background: ai-jail ≤ 1.15.x mounted `/var/run/docker.sock` read-write
automatically whenever it existed on the host — no flag, no warning. v1.16.0
(2026-07-25, issue #88) made it opt-in. The launcher pins `1.15.0` and has no
way to emit `--no-docker`, so on a 1.15.x host the documented "docker off by
default" is false and the agent has host root.

### Changes

**`internal/config/config.go`**

```go
MinAIJailVersion = "1.16.0"   // was "1.15.0"

type JailFlags struct {
    Lockdown    *bool `yaml:"lockdown,omitempty"`
    PrivateHome *bool `yaml:"private_home,omitempty"`
    Docker      *bool `yaml:"docker,omitempty"`   // NEW
    Tailscale   *bool `yaml:"tailscale,omitempty"`
    // ...
}
```

- Add `f.Docker == nil` to `IsZero()` — miss this and a repo-supplied
  `jail_flags.docker` slips past the F3 trust check.

**`internal/launcher/builder.go`**

- `jailToggles()`: add `{name: "docker", value: flags.Docker}` in the stable
  emission order (after `private-home`, matching the struct).
- `jailPermissions()`: give the docker entry its `override`, like gpu/display/
  tailscale/mise/worktree already have:

```go
{id: "docker", flag: "--docker", override: func(f config.JailFlags) *bool { return f.Docker }},
```

**Version-aware pre-flight (the part that actually protects 1.15.x users).**
`Validator.Validate` deliberately never execs upstream — keep it that way. Two
options, pick one:

- **(a)** Always emit `--no-docker` when the docker permission is off and no
  `jail_flags.docker` is set. Explicit beats auto. Costs one argv token and
  works on both 1.15.x and 1.16.x. **Recommended** — it is the only fix that
  closes the hole without a version probe.
- **(b)** Leave it unset (ai-jail auto) and rely on the `--doctor` floor bump
  to nag. Cheaper, but a user who never runs `--doctor` stays exposed.

(a) changes the argv, so every affected Gherkin scenario moves in this PR.

### Documentation, same commit (AGENTS.md contract rule)

- `README.md`: `jail_flags` table gains `docker`; the Permissions table note on
  Docker mentions the v1.16.0 default flip; the Security section stops
  overclaiming for older ai-jail; `--doctor` floor reads `≥ 1.16.0`.
- `AGENTS.md:43`: "ai-jail v1.15 `--no-*` toggles" → v1.16.
- `docs/ARCHITECTURE.md`, `docs/design-decisions.md`: same version references.
- `test/features/launcher.feature`: the four toggle scenarios ("Emits negative
  forms…", "Emits positive forms…", "Forces the auto-detected passthroughs
  off…", "An explicit jail flag wins over the permission…") plus a new
  scenario asserting docker is forced off by default under option (a).

### Checklist

- [ ] `JailFlags.Docker` + `IsZero()` + `jailToggles()` + permission `override`
- [ ] Decide (a) vs (b); if (a), update all affected argv scenarios
- [ ] `MinAIJailVersion = "1.16.0"`, `--doctor` output test
- [ ] README / AGENTS.md / ARCHITECTURE / design-decisions in the same commit
- [ ] `make test-gherkin` green

---

## PR 4 — `fix(config): decouple gpu from the docker permission`

**Branch:** `fix/gpu-docker-dependency` · **Size:** S · **Needs verification first**

`config.go:562` declares `{ID: "gpu", Requires: []string{"docker"}}`, and
`builder.go:848` raises `gpu-without-docker`. In ai-jail, `--gpu` and
`--docker` are independent capabilities. Post-v1.16.0 the effect is that
"I want GPU passthrough" implies "give the agent host root" — against upstream's
own guidance.

**Verify before coding:** run `ai-jail --gpu` on a GPU host without `--docker`
and check the upstream README capability table. If GPU passthrough genuinely
needs the container runtime for some paths, keep the dependency and record the
reason in `docs/design-decisions.md` instead — that also closes the finding.

If it is launcher-invented:

- [ ] Drop `Requires: []string{"docker"}` from the gpu permission
- [ ] Remove the `gpu-without-docker` issue and its test
- [ ] README permissions table: drop "Requires Docker permission"
- [ ] Gherkin: update "Reports incompatible preflight permissions…"

---

## PR 5 — `fix(security): bring param_values inside the trust boundary`

**Branch:** `fix/param-values-trust` · **Size:** S

Closes **T3**. `Options.ParamValues` is file-supplied and reaches the argv via
`appendDeclaredParams`. F3 refuses `jail_flags`, F6 refuses `yolo` and
`extra_args`, nothing refuses `param_values`. The blast radius is smaller
(values only fill catalog-declared flags), but the asymmetry is unexplained.

Two acceptable outcomes — either closes it:

- **Check it.** Refuse file-supplied `param_values` without `--param`, matching
  the F6 shape.
- **Document it.** Record in `docs/design-decisions.md` why declared-flag
  values are considered safe where free-form `extra_args` is not.

Recommendation: check it. `--param model=<x>` can select a model, and a catalog
param with `takes_value: false` already gets a `catalog-flag-param` warning —
which is an admission that these are not fully inert.

---

## PR 6 — `fix(memory): align MemoryRunHarnesses with the installed ai-memory`

**Branch:** `fix/memory-harness-list` · **Size:** S · **Needs verification first**

`config.MemoryRunHarnesses()` declares eight: `claude, codex, opencode, pi,
crush, omp, kimi, grok`. Upstream's documented managed-mode adapters are six:
Claude Code, Codex, OpenCode, Pi, Crush, OMP. The author explicitly separates
"has MCP + a hook" (Kimi Code, Grok Build CLI, Devin, Zero) from "has an
`ai-memory run` native-session adapter".

**Verify:**

```bash
ai-memory --version
ai-memory run --help                    # the accepted HARNESS list
for h in claude codex opencode pi crush omp kimi grok devin zero; do
  ai-memory run "$h" --help >/dev/null 2>&1 && echo "ok   $h" || echo "FAIL $h"
done
```

Then either trim the list (and set `supports_memory: false` on the affected
catalog entries, so pre-flight fails with `memory-harness-unsupported` instead
of letting the rejection surface inside the jail) or confirm 1.19.0 added them
and leave a comment in `config.go:42` naming the version that did.

---

## PR 7 — `feat(memory): surface workstream-search and install-instructions`

**Branch:** `feat/memory-surface` · **Size:** M

Closes **M2** and **M3**. Pure feature work; do it after the security PRs.

- **M2 — `--workstream-search`.** The launcher owns `--new` and `--workstream`
  but offers no way to read the ledger back. Add
  `ai-launcher --workstream-search "query" [--limit N] [--json]` forwarding to
  `ai-memory workstream-search`, jail-wrapped like every other chain. Decide
  whether it belongs in the TUI or stays CLI-only — CLI-only is the smaller
  first step.
- **M3 — `install-instructions`.** `--install` provisions binaries and runs
  `install-hooks` for `pi`. Upstream now ships `ai-memory install-instructions`,
  which installs the managed Agent Skills that replace the hand-maintained
  routing block in `CLAUDE.md` / `AGENTS.md`. Run it after a managed ai-memory
  install so the protocol stops drifting per repo. Make it opt-out — it writes
  into the user's project files.

---

## PR 8 — `chore: repo hygiene`

**Branch:** `chore/hygiene` · **Size:** S

- **H3** `.gitignore`: add `.claude/` and `.idea/` (both present, untracked).
- **H5** `README.md:177`: "Go 1.24+" → 1.25 (`go.mod` says `go 1.25.0`).
- **H6** `AGENTS.md:22-29`: the prose lists three excluded packages; the
  authoritative `COVERAGE_EXCLUDE` regex also excludes `cmd/`,
  `internal/selfupdate/`, `internal/cmd/`, `test/`. Make the prose match — I8
  in the old plan exists to keep exactly these in sync.

---

## PR 9 — `feat(config): platform-neutral default mounts`

**Branch:** `feat/portable-default-mounts` · **Size:** M · **Behaviour change**

**H7.** `DefaultMountCandidates` hardcodes `/Volumes/MSD512` and
`/storage/Projetos` — one developer's volumes — as the built-in defaults of a
public tool. `ExistingPaths` keeps it harmless, but for everyone else the
feature is dead on arrival.

Options, cheapest first:

1. Ship **no** built-in defaults; on first run with no mounts configured, the
   TUI suggests `$PWD` and offers the add-folder panel. CLI users already pass
   `--mount`.
2. Derive candidates: `$PWD`, `$HOME/Projects`, `$HOME/Projetos`, `$HOME/src`,
   plus the existing paths as a documented fallback.
3. Keep the hardcoded list but move it to the shipped `config.yaml` template
   rather than Go source, so it is visibly a default and not a law.

Recommendation: **1**, with **3** as the migration for the current author's
machine. Note that `default_mounts` is already overridable in
`~/.config/ai-launch/config.yaml`, so nobody loses anything they configured.

---

## PR 10 — `docs: add SECURITY.md and CHANGELOG.md`

**Branch:** `docs/security-changelog` · **Size:** S

**H9.** A security-positioned tool that ships signed releases and self-updates
has neither a disclosure policy nor a changelog. Minimum viable:

- `SECURITY.md`: supported versions, how to report (private advisory), expected
  response window, and the upstream boundary — a bug in ai-jail's sandboxing
  goes to akitaonrails, a bug in the argv the launcher composes goes here.
- `CHANGELOG.md`: Keep a Changelog format, seeded from the tags GoReleaser has
  already cut. Wire it into `.goreleaser.yaml` release notes.

---

## Ongoing — branch and worktree cleanup

**H1 / H2.** Not a PR, but it is what let B1–B4 happen: the same code lives in
four places.

| Branch | State | Action |
| --- | --- | --- |
| `chore/dependabot` | merged as #11 | delete local + remote |
| `feature/security-trust-gate-hardening` | 6 ahead, 0 behind | → PR 1, then delete |
| `fix/profile-trust-and-automount-denylist` | 6 ahead, 3 behind | superseded by PR 1? diff and confirm, then delete |
| `omos/public-endpoint-config` | 7 ahead | `8cc998c` looks superseded by #10; confirm and delete |
| `fix/audit-phase-3` | 37 ahead, 5 behind | audit: is any of this unlanded? Most reads as already in main via #7. Cherry-pick what survives, then delete |

Then prune `.slim/worktrees/` down to at most one active worktree.

---

## Sequencing

```
Step 0 ─ local cleanup + make test-all green
   │
   ├─ PR 1  trust gate hardening            [L, exists]
   │     │
   │     └─ PR 2  profile-owned permissions [S]
   │
   ├─ PR 3  ai-jail v1.16 docker + floor    [M]  ← highest value
   │     │
   │     └─ PR 4  gpu/docker decoupling     [S, verify first]
   │
   ├─ PR 5  param_values trust              [S]
   ├─ PR 6  memory harness list             [S, verify first]
   ├─ PR 8  hygiene                         [S]
   │
   └─ after the security work lands:
      PR 7  workstream-search + instructions [M]
      PR 9  portable default mounts          [M]
      PR 10 SECURITY.md + CHANGELOG.md       [S]
      branch/worktree cleanup
```

PR 1→2 and PR 3→4 are ordered because they touch the same code. Everything else
is independent and can go in any order, or in parallel if you want to open
several at once.

If you only do three things: **Step 0**, **PR 1**, **PR 3**.

---

## Verification gates

Per PR:

```bash
make test          # deterministic suite
make lint          # go vet
make test-gherkin  # when argv changed
```

Before merge:

```bash
make test-all      # unit + property + gherkin + race + coverage + lint + sec + mutation
```

Coverage gate is 90% on `internal/config`, `internal/catalog`,
`internal/launcher`. PRs 2, 3, 4, 5 and 6 all land inside that boundary, so each
needs its tests in the same commit.

Manual smoke after PR 3, on a host with Docker installed:

```bash
ai-launcher --doctor                          # must flag ai-jail < 1.16.0
ai-launcher --agent claude --dry-run          # must NOT carry --docker (and must carry --no-docker under option (a))
ai-launcher --agent claude --docker --dry-run # must carry --docker
```

---

## Outcome — 2026-08-02

What actually shipped, verified against `main` at `4c3688b`:

| PR | Landed as | Note |
| --- | --- | --- |
| 1 | #19 (v0.2.9) | with issues #12–#16 as tracking |
| 2, 3, 4, 5, 7, 9 | #22 (v0.3.0) | **all six squashed into one PR** titled `feat(memory): …`, against this plan's "small sequential PRs, each green on CI on its own" |
| 6 | — | resolved without a code change: ai-memory 1.21.0 accepts all eight harnesses |
| 8 + 10 | #20 | merged three minutes *after* the v0.3.0 tag, so v0.3.0's release notes carry the schema break with no changelog |

Two things this plan asked for and did not get:

- **Step 0's `make test-all` before merging.** The suite is green today, but the
  argv rewrites in PR 3 and the new surface in PR 7 were merged without the
  manual smoke this plan lists. PR 7 shipped broken as a result: upstream also
  requires `--workstream-id`, which the stub-based tests could not reveal.
- **One PR per concern.** #22 buried the highest-value security change (the
  1.16 floor and the docker argv) inside a commit titled `feat(memory)`, where
  neither the log nor the generated release notes show it.
