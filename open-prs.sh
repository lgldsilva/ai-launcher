#!/usr/bin/env bash
# Opens the 2026-07-30 review stack, rebased onto main after #19.
# Requires `gh auth status` to pass.
set -uo pipefail
cd "$(git rev-parse --show-toplevel)"

echo '==> pushing (each PR base must exist on the remote first)'
for b in \
  fix/ci-secrets-context \
  chore/hygiene-and-policy-docs \
  fix/trust-record-migration \
  fix/profile-permission-trust \
  feat/aijail-116-docker \
  fix/gpu-docker-dependency \
  feat/portable-default-mounts \
  feat/memory-surface \
  ; do
  git push -u --force-with-lease origin "$b" || exit 1
done

echo '==> creating pull requests (a duplicate is skipped, not fatal)'

gh pr create --base main --head fix/ci-secrets-context \
  --title "fix(ci): restore the workflow by testing a hoisted predicate, not secrets" \
  --body "$(cat <<'PRBODY'
> **Not compiled, not tested.** Written in an environment with no Go toolchain.
> Structural soundness (brace depth, top-level declarations, struct alignment,
> import order) was verified programmatically; correctness was not. CI is the
> first real check. Background: `docs/handoff-2026-07-30.md`.
>
> Merge bottom-up — this stack is chained. `fix/ci-secrets-context` first: main's
> workflow file is currently invalid and no job runs until it lands.

## Commits

### fix(ci): restore the workflow by testing a hoisted predicate, not secrets

#19 scoped SONAR_TOKEN to the scanner step and rewrote the guards from
env.SONAR_TOKEN to secrets.SONAR_TOKEN. `secrets` is not an available
context in `steps.if`, and GitHub validates the entire workflow file
before it runs anything — so the file became unrunnable and every job
went with it: test, lint, dist, vuln, trivy, sbom. The error reads
"Invalid workflow file ... #L1", which points nowhere near the five lines
that caused it.

The intent of #19 was right and is kept: the token must not sit in job
env where the coverage step, and therefore same-repo PR test code, could
read it. What moves to job env is the *predicate* — SONAR_ENABLED, a
boolean — which `secrets` may legally produce there. The token still
appears exactly once, in the env block of the scanner step.

A grep in the lint job now fails on `if:` conditions that reference
secrets. This cost a full CI outage on main and the parse error names
line 1, so the cheapest possible check pays for itself the first time
someone reaches for the obvious-looking expression again.

### fix(security): trust gate, env strip, CI pins, harden, PTY lifecycle (#19)

* fix(security): refuse untrusted local capability grants

Unsaved .ai-launch.yaml could silently enable docker/ssh/gh, mount
sensitive host paths, weaken jail_flags, or set yolo/extra_args.
enforceLocalConfigTrust now requires explicit CLI flags (or a
launcher-saved hash) for those fields, and DeniedMount blocks system
trees and container sockets from file-supplied mounts.

Closes #12 (wave 1).

* fix(security): strip AI_MEMORY env when memory off; bind trust to path

F4: always remove launcher-owned AI_MEMORY_* from the child environment
before optionally re-adding configured values, so a disabled memory path
cannot inherit a parent bearer token.

F5: trusted local configs record canonical path + content hash. Identical
bytes in another checkout no longer inherit trust; legacy bare hashes are
ignored.

Closes #13 (wave 2).

* fix(security): pin CI scanner and quality tools; scope SONAR_TOKEN

F8: keep SONAR_TOKEN off the job env and the coverage step so same-repo
PR test code cannot read it; inject only into the scanner step.

F12: pin sonarqube-scanner@5.0.0 instead of unversioned npx -y.

F15: pin golangci-lint@v2.12.2, gosec@v2.28.0, govulncheck@v1.6.0 in the
Makefile (aligned with CI golangci-lint v2.12).

Closes #16 (wave 5).

* fix(security): bound local YAML, sanitize terminal, harden auto-mount

F7: reject workspace-local configs above 256 KiB before full decode.
F10/F11: SanitizeDisplay escapes C0/C1 controls in dry-run/banner argv
and TUI mount names / preview.
F13: auto-mount denylist refuses high-risk system descendants and
docker/podman sockets while keeping benign data paths mountable.

Closes #14 (wave 3).

* fix(security): kill PTY process group on cancel; always reap child

F9: on context cancel, close the PTY master and SIGKILL the child's
process group (creack/pty Setsid leader) so a descendant holding the
slave cannot pin io.Copy forever.

F14: every post-start return path — including PTY copy failures —
kills when needed and cmd.Wait()s so no orphan is left running.

Closes #15 (wave 4).

* style: gofmt main and trust tests
### ci: configure Dependabot updates (#11)


### fix: make ai-memory endpoint configurable (#10)

* fix(config): keep profiles inside the trust boundary and honor safe defaults

Two defects in the profiles feature, both letting a launch drop the sandbox
or refuse for the wrong reason.

Trust boundary: localTrust was derived from the local config *after* the
profile had been layered onto it, so a profile's own `jail: false` — stored in
the global config, which ARCHITECTURE invariant 2b lists as trusted — looked
like a repository file lowering the sandbox. `--profile` was refused outright,
and the error blamed a .ai-launch.yaml that had said nothing of the sort. The
snapshot now comes from the file as loaded, and only for the blocks the file
still owns: localTrustFrom mirrors applyProfile's conditions, because a value
a trusted source replaced never reaches the argv.

Safe defaults: invariant 6 ("omitted jail and memory mean true") was applied in
LoadLocal, so a profile's options block decoded straight into Go's zero values.
A hand-written profile naming only `yolo` launched with the sandbox AND memory
off, through both the CLI and the TUI. The rule moves into
Options.UnmarshalYAML, where it holds for every options block in the schema;
LoadLocal keeps only the "no options block at all" case.

Every existing profile test passed --no-jail, which is exactly the flag that
disarmed the first defect — the fixture was masking it. Dropped where the
profile owns the options block, documented where it is genuinely required.

* fix(launcher): deny auto-mounting other users' home trees

The home-symlink auto-mount denylist covered system trees but not the trees
holding other accounts' data: /home and /Users were both absent. A single
hidden symlink resolving to either handed the sandboxed agent read-write
access to every user account on the machine — worse than any entry the list
did carry, and the path is not an operator decision: it fires on whatever the
home directory happens to contain.

Comparison runs against the EvalSymlinks-resolved target, so the macOS
spellings had to be listed alongside the logical ones (/home is a firmlink to
/System/Volumes/Data/home). Also adds the remaining mount-point roots
(/media, /mnt, /run, /srv) and /nix, /snap.

The list is now grouped by reason, because the warning shows it and calling
/Users "the system directory" misdescribes exactly the case that matters most.
Only the trees themselves are denied; paths beneath them stay auto-mountable,
which is what keeps /Volumes/MSD512 working.

Tested against deniedAutoMount directly rather than through the filesystem:
the interesting targets are not all present on every platform, and a
symlink-driven test would silently skip the entries that matter.

* fix(tui): keep the home symlink auto-mounts across profile and jail toggles

ARCHITECTURE invariant 9 maps every home dotfile symlink target that escapes
$HOME, because ai-jail recreates the symlink inside the sandbox without its
target. The launcher merged those once, in applyJailAutoDetection, before the
TUI opened — and two TUI actions then walked out of that state:

- loadProfile replaced launch.Mounts wholesale, so loading any profile with a
  mounts block dropped them and ~/.cache dangled inside the jail again;
- applyJailAutoDetection only runs when the jail is already on, so a --no-jail
  launch whose operator re-enabled Jail in the Options section never got them
  at all.

Both were silent: the argv simply came out short, with no warning.

The Model now keeps the detected targets aside from launch.Mounts and
re-merges them after either action. Detection happens once in NewModel — a
$HOME scan plus one EvalSymlinks per dotfile, never inside the event loop —
and MergeAutoMounts already skips targets a configured mount covers. With the
jail off nothing dangles, so nothing is added.

* fix(install): bound the install network calls and drop dead code

Timeouts. installer.New held http.DefaultClient, which has no deadline and is
shared process-wide, so it could not be given one without changing every other
caller's. internal/cmd then passed context.Background() to every step. A
release host that accepts the connection and never answers froze --install
until the operator killed it — while internal/selfupdate, fetching the same
artifacts from the same host, has always carried one. The Installer now owns
its client with a request deadline, and each install step and ai-memory child
runs under an explicit context.

The ai-memory exec also sets WaitDelay: killing the child is not enough on its
own, because Wait blocks until the stdout pipes close and a grandchild that
outlives its parent keeps the write end open.

Dead code, per the pre-merge check in the standards. Removed the Executor
interface (no implementation reference, no consumer), PTYExecutor.Run (existed
only to satisfy it; production calls RunWithEnv), CheckUpstreamVersions (built
[]Issue for a pre-flight warning that was never wired up — --doctor calls
UpstreamReport directly), and the exported ExternalVolumeCwd plus its private
forwarding wrapper. Its tests were ported onto UpstreamReport rather than
deleted: the version-comparison behavior they cover is real, it was only the
wrapper that nothing called.

--add hardcoded supports_memory: true, so registering an agent whose command
is not on ai-memory's fixed harness list produced memory-harness-unsupported on
its first launch — an error about a decision --add had made. It now follows
config.SupportsMemoryRunHarness.

* test(config): cover the trust boundary's failure paths

The provenance record is what lets a launch honor an operator's own saved
selection instead of refusing it as repo-supplied input, and it was the least
tested code in the package: RecordTrustedLocalConfig sat at 76.5%, SaveLocal at
77.8%, writeGlobalAtomically at 73.7%. The uncovered half was entirely error
handling — exactly where a silent failure turns into a launch the operator
cannot explain.

Adds the paths that matter: a global config that cannot be parsed or read is
not overwritten with a stub; a save that fails midway leaves the previous file
intact rather than truncated; the history cap keeps the newest hashes, which
are the ones matching files still on disk; non-string entries in a hand-edited
trusted list are skipped, not carried over.

Also covers allow-tcp-ports-without-lockdown (40% -> 100%, each arm of a
condition that is the only thing telling an operator their ports are inert),
Validator.WithPermissions, and MemoryRunHarnesses' defensive copy.

Gate packages: 92.3% -> 93.8%. No production code changed.

* docs(architecture): record the checksum source ladder for installs

Invariant 4 said "mandatory checksum" without naming where the checksum may
come from. It has three sources of descending strength, and the weakest — the
release body — is easy to mistake for a guarantee: release notes are mutable
markdown, editable without re-uploading any asset. Documents the order, why
the body is last, and that every source is matched by filename so a body
quoting another asset's hash cannot satisfy verification.

No behavior change: restricting the body as a source would break recipes whose
upstreams publish no checksum file, and it sits inside the same trust boundary
as the release assets themselves.

* fix: make memory endpoint configurable
PRBODY
  )" || echo "   (skipped fix/ci-secrets-context — already open?)"

gh pr create --base main --head chore/hygiene-and-policy-docs \
  --title "chore: add SECURITY.md and CHANGELOG.md, correct stale doc claims" \
  --body "$(cat <<'PRBODY'
> **Not compiled, not tested.** Written in an environment with no Go toolchain.
> Structural soundness (brace depth, top-level declarations, struct alignment,
> import order) was verified programmatically; correctness was not. CI is the
> first real check. Background: `docs/handoff-2026-07-30.md`.
>
> Merge bottom-up — this stack is chained. `fix/ci-secrets-context` first: main's
> workflow file is currently invalid and no job runs until it lands.

## Commits

### chore: add SECURITY.md and CHANGELOG.md, correct stale doc claims

A tool that ships signed releases, self-updates, and describes itself as a
security boundary had no disclosure policy and no place to record a
breaking change. Both gaps show up the moment one lands.

SECURITY.md draws the line this project actually owns: the launcher is
responsible for the argv it builds, the trust boundary around
.ai-launch.yaml, and the verification on every install path. Containment
failures inside ai-jail and session handling inside ai-memory belong
upstream, and the table says which is which so a report does not bounce.
It also restates the non-goals — a sandbox is not a VM, environment
variables are visible inside the jail, Windows has no sandbox, --docker
means what it says — because those are the reports that come back as
"working as intended".

CHANGELOG.md deliberately does not duplicate GoReleaser, which already
generates grouped per-release notes from the commit log. It carries the
part a commit log cannot: breaking changes and the migration each one
asks of the reader.

Two stale claims fixed while here. The README promised Go 1.24+ while
go.mod pins 1.25.0. AGENTS.md listed three excluded packages where
COVERAGE_EXCLUDE excludes seven, and named the prose instead of the regex
as the boundary — I8 in the audit plan exists to keep exactly these in
sync, so the drift had already been paid for once.

.gitignore picks up .claude/ (the .idea/ entry it belongs next to is
already there) and the slim worktree entries the local tooling writes.

### fix(security): trust gate, env strip, CI pins, harden, PTY lifecycle (#19)

* fix(security): refuse untrusted local capability grants

Unsaved .ai-launch.yaml could silently enable docker/ssh/gh, mount
sensitive host paths, weaken jail_flags, or set yolo/extra_args.
enforceLocalConfigTrust now requires explicit CLI flags (or a
launcher-saved hash) for those fields, and DeniedMount blocks system
trees and container sockets from file-supplied mounts.

Closes #12 (wave 1).

* fix(security): strip AI_MEMORY env when memory off; bind trust to path

F4: always remove launcher-owned AI_MEMORY_* from the child environment
before optionally re-adding configured values, so a disabled memory path
cannot inherit a parent bearer token.

F5: trusted local configs record canonical path + content hash. Identical
bytes in another checkout no longer inherit trust; legacy bare hashes are
ignored.

Closes #13 (wave 2).

* fix(security): pin CI scanner and quality tools; scope SONAR_TOKEN

F8: keep SONAR_TOKEN off the job env and the coverage step so same-repo
PR test code cannot read it; inject only into the scanner step.

F12: pin sonarqube-scanner@5.0.0 instead of unversioned npx -y.

F15: pin golangci-lint@v2.12.2, gosec@v2.28.0, govulncheck@v1.6.0 in the
Makefile (aligned with CI golangci-lint v2.12).

Closes #16 (wave 5).

* fix(security): bound local YAML, sanitize terminal, harden auto-mount

F7: reject workspace-local configs above 256 KiB before full decode.
F10/F11: SanitizeDisplay escapes C0/C1 controls in dry-run/banner argv
and TUI mount names / preview.
F13: auto-mount denylist refuses high-risk system descendants and
docker/podman sockets while keeping benign data paths mountable.

Closes #14 (wave 3).

* fix(security): kill PTY process group on cancel; always reap child

F9: on context cancel, close the PTY master and SIGKILL the child's
process group (creack/pty Setsid leader) so a descendant holding the
slave cannot pin io.Copy forever.

F14: every post-start return path — including PTY copy failures —
kills when needed and cmd.Wait()s so no orphan is left running.

Closes #15 (wave 4).

* style: gofmt main and trust tests
### ci: configure Dependabot updates (#11)


### fix: make ai-memory endpoint configurable (#10)

* fix(config): keep profiles inside the trust boundary and honor safe defaults

Two defects in the profiles feature, both letting a launch drop the sandbox
or refuse for the wrong reason.

Trust boundary: localTrust was derived from the local config *after* the
profile had been layered onto it, so a profile's own `jail: false` — stored in
the global config, which ARCHITECTURE invariant 2b lists as trusted — looked
like a repository file lowering the sandbox. `--profile` was refused outright,
and the error blamed a .ai-launch.yaml that had said nothing of the sort. The
snapshot now comes from the file as loaded, and only for the blocks the file
still owns: localTrustFrom mirrors applyProfile's conditions, because a value
a trusted source replaced never reaches the argv.

Safe defaults: invariant 6 ("omitted jail and memory mean true") was applied in
LoadLocal, so a profile's options block decoded straight into Go's zero values.
A hand-written profile naming only `yolo` launched with the sandbox AND memory
off, through both the CLI and the TUI. The rule moves into
Options.UnmarshalYAML, where it holds for every options block in the schema;
LoadLocal keeps only the "no options block at all" case.

Every existing profile test passed --no-jail, which is exactly the flag that
disarmed the first defect — the fixture was masking it. Dropped where the
profile owns the options block, documented where it is genuinely required.

* fix(launcher): deny auto-mounting other users' home trees

The home-symlink auto-mount denylist covered system trees but not the trees
holding other accounts' data: /home and /Users were both absent. A single
hidden symlink resolving to either handed the sandboxed agent read-write
access to every user account on the machine — worse than any entry the list
did carry, and the path is not an operator decision: it fires on whatever the
home directory happens to contain.

Comparison runs against the EvalSymlinks-resolved target, so the macOS
spellings had to be listed alongside the logical ones (/home is a firmlink to
/System/Volumes/Data/home). Also adds the remaining mount-point roots
(/media, /mnt, /run, /srv) and /nix, /snap.

The list is now grouped by reason, because the warning shows it and calling
/Users "the system directory" misdescribes exactly the case that matters most.
Only the trees themselves are denied; paths beneath them stay auto-mountable,
which is what keeps /Volumes/MSD512 working.

Tested against deniedAutoMount directly rather than through the filesystem:
the interesting targets are not all present on every platform, and a
symlink-driven test would silently skip the entries that matter.

* fix(tui): keep the home symlink auto-mounts across profile and jail toggles

ARCHITECTURE invariant 9 maps every home dotfile symlink target that escapes
$HOME, because ai-jail recreates the symlink inside the sandbox without its
target. The launcher merged those once, in applyJailAutoDetection, before the
TUI opened — and two TUI actions then walked out of that state:

- loadProfile replaced launch.Mounts wholesale, so loading any profile with a
  mounts block dropped them and ~/.cache dangled inside the jail again;
- applyJailAutoDetection only runs when the jail is already on, so a --no-jail
  launch whose operator re-enabled Jail in the Options section never got them
  at all.

Both were silent: the argv simply came out short, with no warning.

The Model now keeps the detected targets aside from launch.Mounts and
re-merges them after either action. Detection happens once in NewModel — a
$HOME scan plus one EvalSymlinks per dotfile, never inside the event loop —
and MergeAutoMounts already skips targets a configured mount covers. With the
jail off nothing dangles, so nothing is added.

* fix(install): bound the install network calls and drop dead code

Timeouts. installer.New held http.DefaultClient, which has no deadline and is
shared process-wide, so it could not be given one without changing every other
caller's. internal/cmd then passed context.Background() to every step. A
release host that accepts the connection and never answers froze --install
until the operator killed it — while internal/selfupdate, fetching the same
artifacts from the same host, has always carried one. The Installer now owns
its client with a request deadline, and each install step and ai-memory child
runs under an explicit context.

The ai-memory exec also sets WaitDelay: killing the child is not enough on its
own, because Wait blocks until the stdout pipes close and a grandchild that
outlives its parent keeps the write end open.

Dead code, per the pre-merge check in the standards. Removed the Executor
interface (no implementation reference, no consumer), PTYExecutor.Run (existed
only to satisfy it; production calls RunWithEnv), CheckUpstreamVersions (built
[]Issue for a pre-flight warning that was never wired up — --doctor calls
UpstreamReport directly), and the exported ExternalVolumeCwd plus its private
forwarding wrapper. Its tests were ported onto UpstreamReport rather than
deleted: the version-comparison behavior they cover is real, it was only the
wrapper that nothing called.

--add hardcoded supports_memory: true, so registering an agent whose command
is not on ai-memory's fixed harness list produced memory-harness-unsupported on
its first launch — an error about a decision --add had made. It now follows
config.SupportsMemoryRunHarness.

* test(config): cover the trust boundary's failure paths

The provenance record is what lets a launch honor an operator's own saved
selection instead of refusing it as repo-supplied input, and it was the least
tested code in the package: RecordTrustedLocalConfig sat at 76.5%, SaveLocal at
77.8%, writeGlobalAtomically at 73.7%. The uncovered half was entirely error
handling — exactly where a silent failure turns into a launch the operator
cannot explain.

Adds the paths that matter: a global config that cannot be parsed or read is
not overwritten with a stub; a save that fails midway leaves the previous file
intact rather than truncated; the history cap keeps the newest hashes, which
are the ones matching files still on disk; non-string entries in a hand-edited
trusted list are skipped, not carried over.

Also covers allow-tcp-ports-without-lockdown (40% -> 100%, each arm of a
condition that is the only thing telling an operator their ports are inert),
Validator.WithPermissions, and MemoryRunHarnesses' defensive copy.

Gate packages: 92.3% -> 93.8%. No production code changed.

* docs(architecture): record the checksum source ladder for installs

Invariant 4 said "mandatory checksum" without naming where the checksum may
come from. It has three sources of descending strength, and the weakest — the
release body — is easy to mistake for a guarantee: release notes are mutable
markdown, editable without re-uploading any asset. Documents the order, why
the body is last, and that every source is matched by filename so a body
quoting another asset's hash cannot satisfy verification.

No behavior change: restricting the body as a source would break recipes whose
upstreams publish no checksum file, and it sits inside the same trust boundary
as the release assets themselves.

* fix: make memory endpoint configurable
PRBODY
  )" || echo "   (skipped chore/hygiene-and-policy-docs — already open?)"

gh pr create --base fix/ci-secrets-context --head fix/trust-record-migration \
  --title "fix(config): keep schema 2.0 global configs loadable after path binding" \
  --body "$(cat <<'PRBODY'
> **Not compiled, not tested.** Written in an environment with no Go toolchain.
> Structural soundness (brace depth, top-level declarations, struct alignment,
> import order) was verified programmatically; correctness was not. CI is the
> first real check. Background: `docs/handoff-2026-07-30.md`.
>
> Merge bottom-up — this stack is chained. `fix/ci-secrets-context` first: main's
> workflow file is currently invalid and no job runs until it lands.

## Commits

### fix(config): keep schema 2.0 global configs loadable after path binding

Binding trust records to {path, hash} changed the shape of
trusted_local_configs from a list of strings to a list of maps. LoadGlobal
falls back to DefaultGlobal on any parse error, so an operator upgrading
from 2.0 lost far more than their trust records: --add agents, saved
profiles, the MRU list and memory_auth_token all reverted to defaults on
the first launch after the upgrade.

TrustedLocalEntry.UnmarshalYAML now reads the legacy scalar into an entry
with an empty Path. That is read, not honored — LocalConfigTrusted compares
against a canonical absolute path, which is never empty, so a bare hash can
never match. The security property the path binding exists for is intact
(bytes are not a file; a clone must not inherit its author's trust) while
the rest of the document survives the upgrade. The stale rows are dropped
from disk by the next RecordTrustedLocalConfig.

CurrentVersion moves to 2.1 and ValidateVersion reads from LoadableVersions,
so the accepted set and its error message cannot drift apart. Writing stays
one-way by nature: a 2.1 file is not loadable by a pre-2.1 binary, and now
that fails by name instead of as a YAML type error.

README documents the one-time --save, and gains the five refusals the trust
gate added without documentation (sensitive mounts, permissions, yolo,
extra_args, jail_flags) plus the saved-file exemption.
PRBODY
  )" || echo "   (skipped fix/trust-record-migration — already open?)"

gh pr create --base fix/trust-record-migration --head fix/profile-permission-trust \
  --title "refactor(trust): make profileBlocks mirror applyProfile mechanically" \
  --body "$(cat <<'PRBODY'
> **Not compiled, not tested.** Written in an environment with no Go toolchain.
> Structural soundness (brace depth, top-level declarations, struct alignment,
> import order) was verified programmatically; correctness was not. CI is the
> first real check. Background: `docs/handoff-2026-07-30.md`.
>
> Merge bottom-up — this stack is chained. `fix/ci-secrets-context` first: main's
> workflow file is currently invalid and no job runs until it lands.

## Commits

### refactor(trust): make profileBlocks mirror applyProfile mechanically

profileBlocks documents itself as mirroring the conditions in applyProfile
one-for-one, and did not: applyProfile replaces local.Permissions when the
profile declares them, but there was no permissions field, so localTrustFrom
excused the block with an inline `profile == nil || profile.Permissions ==
nil` written next to the assignment instead.

The behaviour was already correct. The structure was not, and it is the
structure that has to survive the next edit: a fifth block added to
config.Profile and applyProfile has no field here, gets no exemption, and
the launcher refuses the run over a value a trusted profile already
discarded. That failure mode has now been shipped twice — once for agent,
once for jail — and both times it presented as "--profile is broken" with
an error blaming a .ai-launch.yaml that had said nothing of the sort.

TestProfileBlocksCoversEveryApplyProfileBlock compares the two structs by
reflection, so the third occurrence fails at compile-time-adjacent distance
rather than in somebody's terminal. The behavioural tests around it lock
the three outcomes: a profile-owned permissions block discards the file's
grant without refusing, a file-owned one is still refused by name, and the
flag the refusal names accepts it.
PRBODY
  )" || echo "   (skipped fix/profile-permission-trust — already open?)"

gh pr create --base fix/profile-permission-trust --head feat/aijail-116-docker \
  --title "feat(jail): state the docker decision explicitly and raise the floor to 1.16.0" \
  --body "$(cat <<'PRBODY'
> **Not compiled, not tested.** Written in an environment with no Go toolchain.
> Structural soundness (brace depth, top-level declarations, struct alignment,
> import order) was verified programmatically; correctness was not. CI is the
> first real check. Background: `docs/handoff-2026-07-30.md`.
>
> Merge bottom-up — this stack is chained. `fix/ci-secrets-context` first: main's
> workflow file is currently invalid and no job runs until it lands.

## Commits

### feat(jail): state the docker decision explicitly and raise the floor to 1.16.0

Up to ai-jail v1.15.x an existing /var/run/docker.sock was bind-mounted
read-write into the sandbox whenever it existed, with no flag and no
warning. Write access to that socket is root on the host: the agent asks
the daemon to mount / into a container and reads anything, because the
mount happens in a process that lives outside every namespace bwrap
created. bubblewrap, Landlock, seccomp, the tmpfs $HOME and every --mask
are bypassed in one command. That is upstream issue #88, fixed in v1.16.0
by making the passthrough opt-in.

The launcher documented "docker off by default" throughout, and emitted
nothing for it. On a 1.15.x host that sentence was false, and nothing in
the launcher could make it true: JailFlags had no Docker field, so
--no-docker was unreachable from any configuration surface.

So the launcher stops leaving this one to the default. appendDockerDecision
emits exactly one of --docker / --no-docker on every jailed command, with
the same precedence as every other capability: an explicit jail_flags.docker
wins, then the permission toggle, then off. One argv token, the same meaning
on 1.15.x and 1.16.x, and "off by default" becomes a property of the command
this launcher builds rather than of whichever ai-jail happens to be
installed.

docker is deliberately absent from jailToggles and from jailPermissions:
both express "unset means auto", which is the assumption being removed.
JailFlags.Docker joins IsZero so the trust gate keeps seeing a workspace
file that sets it.

MinAIJailVersion moves to 1.16.0 — a security floor rather than a feature
floor. The argv fix covers 1.15.x hosts; the floor is what tells an
operator to stop running a build that had no way to say no.

Every jail argv in the contract suite and the Go tests gains the decision;
two new contract scenarios and internal/launcher/docker_test.go lock the
precedence, the exactly-once property, and the absence of the flag on a
jail-less launch.
PRBODY
  )" || echo "   (skipped feat/aijail-116-docker — already open?)"

gh pr create --base feat/aijail-116-docker --head fix/gpu-docker-dependency \
  --title "fix(config): stop tying the gpu permission to the docker socket" \
  --body "$(cat <<'PRBODY'
> **Not compiled, not tested.** Written in an environment with no Go toolchain.
> Structural soundness (brace depth, top-level declarations, struct alignment,
> import order) was verified programmatically; correctness was not. CI is the
> first real check. Background: `docs/handoff-2026-07-30.md`.
>
> Merge bottom-up — this stack is chained. `fix/ci-secrets-context` first: main's
> workflow file is currently invalid and no job runs until it lands.

## Commits

### fix(config): stop tying the gpu permission to the docker socket

The catalog declared gpu as requiring docker, so enabling GPU passthrough
silently pulled in the Docker socket and pre-flight raised
gpu-without-docker when it did not. ai-jail documents the two as
independent capabilities — its options table lists --gpu / --no-gpu and
--docker / --no-docker separately, and the default-mounts table lists GPU
devices (/dev/dri, /dev/nvidia*) and the Docker socket as separate rows.
Nothing upstream chains them. The edge was this launcher's invention.

Harmless while the socket was mounted on sight anyway. Not harmless now:
with docker correctly opt-in, the dependency meant "I want the GPU"
resolved to "give the agent host root" — the exact trade upstream's own
v1.16.0 guidance tells you not to make.

gpu now requires jail, like every other jail-backed permission.

The CLI normalization test was asserting this product decision as a proxy
for the mechanism it actually covers (a flag going through
NormalizePermissions). It now declares its own dependency in its own
fixture, so removing a real edge cannot break a test about something else.

### docs(readme): list param_values in the untrusted-config table


### fix(security): bring param_values inside the local-config trust boundary

The trust gate refuses a workspace file that sets jail_flags, yolo or
extra_args, and said nothing about param_values — the fourth thing in the
same options block that reaches the argv.

param_values is narrower than extra_args: a value can only land behind a
flag the catalog already declares, so a repository cannot invent an
argument. Narrower is not inert. It chooses the model the agent runs as,
and a param the catalog declares with takes_value: false is a bare flag the
file switches on by name — which pre-flight already warns about as
catalog-flag-param, an admission that these are not neutral. Choosing the
model and the flags of the process about to read the checkout is an
operator decision.

So it takes the same shape as its three siblings: refused when the file
still owns the options block, accepted when --param is passed or the
selection is saved, silent when a profile owns the block and the file's
values never reach the argv.

The message lists every refused param sorted, because Go map order is
random and an error that reshuffles itself between runs is one nobody can
grep for.
PRBODY
  )" || echo "   (skipped fix/gpu-docker-dependency — already open?)"

gh pr create --base fix/gpu-docker-dependency --head feat/portable-default-mounts \
  --title "feat(config): stop shipping built-in default mounts" \
  --body "$(cat <<'PRBODY'
> **Not compiled, not tested.** Written in an environment with no Go toolchain.
> Structural soundness (brace depth, top-level declarations, struct alignment,
> import order) was verified programmatically; correctness was not. CI is the
> first real check. Background: `docs/handoff-2026-07-30.md`.
>
> Merge bottom-up — this stack is chained. `fix/ci-secrets-context` first: main's
> workflow file is currently invalid and no job runs until it lands.

## Commits

### docs(readme): show the docker decision in the sample argv

The --no-docker token added by appendDockerDecision reaches every jailed
command, including the TUI preview line and all seven --dry-run examples.
Leaving the samples on the old shape would have them disagree with what a
reader gets on their first run, which for the one flag the change exists
to make visible is worse than a stale doc.

### feat(config): stop shipping built-in default mounts

DefaultMountCandidates returned this author's own volumes — /storage,
/storage/Projetos and /storage/cache on Linux, /Volumes/MSD512 and
/Volumes/MSD512/Projetos on macOS — compiled into a tool other people
install from a public repository.

ExistingPaths is why nobody noticed: a candidate that is not on the host
is dropped, so on the machine this was written for the feature worked, and
on every other machine it silently did nothing while looking configured.
The README documented the paths as "built-in candidates", which made a
personal layout read as a product decision.

The failure mode that matters is the other one. A default mount is a
read-write hole in the sandbox, and these are guesses. /storage is a
plausible enough name that someone else has one — and if they do, an agent
gets write access to a volume nobody asked about, on a launch where the
operator specified no mounts at all. That is the opposite of what the rest
of this codebase spends its time enforcing.

The function stays, and stays goos-shaped, so the callers and the tests
keep saying out loud that no platform has a built-in default. Operators
declare their own in default_mounts, which already existed, is already
documented as the override, and is already filtered by ExistingPaths — so
one config can still cover several machines. ai-jail mounts the current
project read-write on its own, so the common case needs no mount at all.
PRBODY
  )" || echo "   (skipped feat/portable-default-mounts — already open?)"

gh pr create --base feat/portable-default-mounts --head feat/memory-surface \
  --title "feat(memory): read the workstream ledger back with --workstream-search" \
  --body "$(cat <<'PRBODY'
> **Not compiled, not tested.** Written in an environment with no Go toolchain.
> Structural soundness (brace depth, top-level declarations, struct alignment,
> import order) was verified programmatically; correctness was not. CI is the
> first real check. Background: `docs/handoff-2026-07-30.md`.
>
> Merge bottom-up — this stack is chained. `fix/ci-secrets-context` first: main's
> workflow file is currently invalid and no job runs until it lands.

## Commits

### feat(memory): read the workstream ledger back with --workstream-search

The launcher creates workstreams (--new) and resumes them (--workstream),
so it owned the write side of the ledger and offered no way to read it.
That gap is not cosmetic: the context ai-memory hands the next harness is
a size-limited recent delta by design, and an old decision — the approach
that was abandoned, the bug that only showed up with two workers — lives
in the searchable ledger instead. Needing a second tool to reach it is
most of the reason to have a command that knows about workstreams at all.

--limit and --json are forwarded only when given, because unset means
"ai-memory decides" and sending a zero would not say that.

Deliberately not jail-wrapped, unlike a launch. This is a read-only HTTP
query in the same class as --doctor: no harness runs, nothing touches the
checkout, and a sandbox would only stop the answer reaching the terminal.
The server URL and bearer token come from the trusted global config
through launcher.Environment, so a search reads the same ledger the
launches write to rather than defaulting to loopback.

Not included: `ai-memory install-instructions`. It belongs to the same
upstream surface, but it writes the routing block into CLAUDE.md /
AGENTS.md in the current checkout, and folding a file-mutating step into
--install (which provisions binaries globally) is the kind of surprise
this repository spends its time avoiding. It wants its own opt-in flag and
its own decision about which file it may touch.
PRBODY
  )" || echo "   (skipped feat/memory-surface — already open?)"

echo
echo '==> merge order: fix/ci-secrets-context, then chore/hygiene-and-policy-docs,'
echo '    then the chain in listed order. Merging out of order rebases the rest.'