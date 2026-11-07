# Security Policy

## What this project is responsible for

ai-launcher composes two third-party tools:
[`ai-jail`](https://github.com/akitaonrails/ai-jail) (the sandbox) and
[`ai-memory`](https://github.com/akitaonrails/ai-memory) (memory and managed
sessions). It reimplements neither. Knowing which side a problem is on decides
where it gets fixed, so please route reports accordingly:

| Report here | Report upstream |
| --- | --- |
| The launcher builds an argv that grants more than you asked for | The sandbox itself fails to contain a process |
| A configuration surface bypasses the local-config trust boundary | `ai-jail` leaks a path, a socket, or an environment variable it claims to mask |
| An install, `upgrade`, or `install.sh` path accepts an unverified artifact | `ai-memory` mishandles session data or credentials |
| A secret is written to a log, a config file, or a process listing | A harness (Claude Code, Codex, …) misbehaves |

When in doubt, report it here. Misrouted is better than unreported, and we will
forward it.

## Supported versions

ai-launcher is discontinued. [v5.12.1](https://github.com/lgldsilva/ai-launcher/releases/tag/v5.12.1)
is the last release. No further security fix is scheduled in this repository.
A defect in the sandbox belongs in
[ai-jail](https://github.com/akitaonrails/ai-jail). A defect in sessions,
hooks, or the memory server belongs in
[ai-memory](https://github.com/akitaonrails/ai-memory). The migration,
which keeps the sandbox and does not convert `.ai-launcher` config, is
[docs/discontinuation.md](docs/discontinuation.md).

The floors below describe v5.12.1. They are not a promise of a newer
launcher build. `ai-launcher upgrade` is not a path onto a supported
release after v5.12.1.

Upstream floors are pinned in `internal/config` and reported by
`ai-launcher --doctor`:

| Tool | Minimum | Why |
| --- | --- | --- |
| `ai-jail` | 1.20.1 | Up to 1.20.0 on macOS the sandbox could exec a binary it never vetted: the `PATH` entry `execvp` walks was never named in the seatbelt profile, so it ran the first match inside an already-readable prefix — ai-jail granted one build of an agent and started another. Same release fixes DNS and TLS failing under `--network`. Linux is unaffected by both. The earlier 1.16.0 floor still holds underneath: up to 1.15.x the Docker socket was bind-mounted read-write on sight, which is host root ([issue #88](https://github.com/akitaonrails/ai-jail/issues/88)) |
| `ai-memory` | 1.25.0 | Last of the three releases adding a managed harness the catalog offers (`kiro` and `antigravity` in 1.24.0, `command-code` in 1.25.0). Below it, pre-flight passes a harness the wrapper then rejects inside the jail |

Those floors are what the composed argv requires. Published advisories newer
than that floor are a warning, not a second refusal: an install that still
parses the argv keeps launching, and `--doctor` exits 0. Launch pre-flight
prints the same warning from the version it already cached.

| Tool | Recommended | Advisory | Fixed in |
| --- | --- | --- | --- |
| `ai-jail` | 2.13.0 | [GHSA-v2fr-83mr-f9cg](https://github.com/akitaonrails/ai-jail/security/advisories/GHSA-v2fr-83mr-f9cg) (medium) | 2.10.0 |
| `ai-jail` | 2.13.0 | [GHSA-2gq6-9rhj-xrg4](https://github.com/akitaonrails/ai-jail/security/advisories/GHSA-2gq6-9rhj-xrg4) (low: a host rename over a masked path unmasks it on Linux) | 2.11.0 |
| `ai-jail` | 2.13.0 | [GHSA-w976-gw52-hvx2](https://github.com/akitaonrails/ai-jail/security/advisories/GHSA-w976-gw52-hvx2) (high: the 1.22.0 source audit). The advisory does not name a patched version; the 2.8.1 follow-up records that its five findings were fixed | before 2.13.0 |
| `ai-jail` | 2.13.0 | Symlinked-binary file-map regression (a map such as `~/.local/bin/claude` failed the launch after the 2.11.0 mount-order change) | 2.13.0 |
| `ai-memory` | 3.2.10 | [GHSA-9xw8-cg9v-h9gw](https://github.com/akitaonrails/ai-memory/security/advisories/GHSA-9xw8-cg9v-h9gw) (high: request-controlled eval configuration on `POST /admin/auto-improve`, remote command execution) | 3.1.1 |
| `ai-memory` | 3.2.10 | [GHSA-55q2-6fgm-49ww](https://github.com/akitaonrails/ai-memory/security/advisories/GHSA-55q2-6fgm-49ww) (medium: a restricted project opens to every authenticated user when its access mode or grant cannot be read) | 3.2.4 |
| `ai-memory` | 3.2.10 | [GHSA-pphw-hm98-3wh4](https://github.com/akitaonrails/ai-memory/security/advisories/GHSA-pphw-hm98-3wh4) (low: hollow-project cleanup removes a fresh purge tombstone and permits session replay) | 3.2.10 |

`--doctor` reports `ai-jail-version-advisory` and `ai-memory-version-advisory`.
Pre-flight reports `jail-version-advisory` and `memory-version-advisory`. Below
the hard floor the launch is refused for that reason; the advisory detail is
still on `--doctor`.

`--doctor` also probes the launcher-managed ai-memory native binary under
`~/.local/share/ai-launcher/bin` against the same floor and the same
recommended release (`ai-memory-native-advisory`). It is a second install
with its own lifecycle — only `--install` / `--upgrade` moves it — so a current
`ai-memory` on `PATH` says nothing about the copy exported as
`AI_MEMORY_NATIVE_BIN`. A managed runner older than the PATH copy is reported
as `ai-memory-native-behind-path`.

## Reporting a vulnerability

Use GitHub's private reporting: **Security → Advisories → Report a
vulnerability** on this repository. That opens a channel visible only to the
maintainers.

Please do not open a public issue for anything that lets an agent read or write
outside the sandbox it was told to stay in.

Useful in a report, roughly in order of value:

1. What the launcher emitted — the output of `ai-launcher --agent <x> --dry-run`
   is usually the whole story, since the argv is what this project controls.
2. A minimal reproduction: the `.ai-launch.yaml`, the global config (**redact
   `memory_auth_token`**), the flags, and the platform.
3. Versions: `ai-launcher --version` and `ai-launcher --doctor`.
4. What you expected the sandbox to prevent, and what happened instead.

Expect an acknowledgement within a week. We will tell you what we think the
severity is and why, and we would rather be argued out of a wrong call early
than quietly disagree with you.

Coordinated disclosure is welcome; we will credit you in the release notes
unless you would prefer we did not.

## What ai-launcher does not protect against

Documented in full in the README's Security section, and worth repeating here
because these are the reports that come back as "working as intended":

- **A sandbox is not a VM.** bubblewrap and sandbox-exec share the host kernel.
  A kernel escape is a kernel bug, not a launcher bug.
- **Environment variables are visible inside the jail**, except in ai-jail's
  lockdown mode. Do not put secrets in the environment of a sandboxed session.
- **Windows runs without a sandbox.** ai-jail has no Windows build; the
  launcher says so and runs the rest. Use WSL2 for a sandboxed session.
- **Anyone who can read the global config can read `memory_auth_token`.** The
  file is 0600 and the token never reaches a log, but it is at rest on disk.
- **Opting in means opting in.** `--docker` hands the workload the host Docker
  socket, which is root. That is the documented meaning of the flag, and using
  it is not a vulnerability.
