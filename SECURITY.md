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

Security fixes land on `main` and ship in the next tagged release. Only the
latest release is supported — the binary self-updates (`ai-launcher upgrade`),
so staying current is one command.

Upstream floors are pinned in `internal/config` and reported by
`ai-launcher --doctor`:

| Tool | Minimum | Why |
| --- | --- | --- |
| `ai-jail` | 1.16.0 | Up to 1.15.x the Docker socket was bind-mounted read-write on sight, which is host root ([issue #88](https://github.com/akitaonrails/ai-jail/issues/88)) |
| `ai-memory` | 1.19.0 | Managed-workstream and harness surface the launcher composes against |

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
