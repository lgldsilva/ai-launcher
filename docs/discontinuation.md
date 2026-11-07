# Discontinuation

ai-launcher is discontinued. This page records that decision and the
conservative way to move an existing setup onto ai-jail and ai-memory. It
does not delete source, history, tags, or GitHub releases, and it does not
archive the repository.

## Last release

The last release is
[v5.12.1](https://github.com/lgldsilva/ai-launcher/releases/tag/v5.12.1),
commit `a35610d714ae32b89bd343f435ca6745455ba961`, published 2026-11-06.
That release stays downloadable from the
[releases page](https://github.com/lgldsilva/ai-launcher/releases).

At the time of this notice, `main` is one test commit ahead of that tag
(`a064a7c`, help-route coverage only). The launcher behavior described here
is v5.12.1.

## Install ai-jail and ai-memory from upstream

Use each project's own installer. The versions below are the stable releases
this page was checked against on 2026-11-07. If a newer stable tag is on the
releases page, use that tag's own notes. This page was not re-checked against
a later tag.

| Tool | Project | Release checked |
| --- | --- | --- |
| ai-jail | <https://github.com/akitaonrails/ai-jail> | [v3.3.0](https://github.com/akitaonrails/ai-jail/releases/tag/v3.3.0) |
| ai-memory | <https://github.com/akitaonrails/ai-memory> | [v3.4.3](https://github.com/akitaonrails/ai-memory/releases/tag/v3.4.3) |

ai-jail publishes install paths in the v3.3.0 README and on
[aijail.io/download](https://aijail.io/download/): Homebrew
(`brew tap akitaonrails/tap && brew install ai-jail`), the release `.tar.gz`
or `.deb`, and `cargo install --locked ai-jail`. Linux still needs a
root-owned `bwrap`. Windows is not a supported ai-jail target. That README
says to use WSL2 and the Linux backend. That is the sandboxed path. The
launcher's old Windows behavior, running on the host with the sandbox
disabled, is not the migration.

ai-memory's v3.4.3 README installs with the checksum-checked script and then
`ai-memory setup`:

```bash
curl -fsSL https://github.com/akitaonrails/ai-memory/releases/latest/download/install.sh | sh
ai-memory setup
```

`latest` follows whatever release is current. The script text checked for
this page is the
[v3.4.3 `install.sh`](https://github.com/akitaonrails/ai-memory/releases/download/v3.4.3/install.sh).
Read [docs/install.md](https://github.com/akitaonrails/ai-memory/blob/v3.4.3/docs/install.md)
in that repository before running it. The script verifies SHA-256, unpacks
the whole archive under `~/.local/lib/ai-memory`, and links
`~/.local/bin/ai-memory`. The archive includes `hooks/`, so they stay beside
the binary. `ai-memory setup` is the documented next step: data directory,
per-user service, and agent wiring.

Do not use `ai-launcher --install` or `--upgrade` to fetch ai-memory.
v5.12.1 extracts the executable and drops the rest of the archive, so a
later `ai-memory install-hooks` or `ai-memory setup` cannot find `hooks/`.
An open change to that installer is not in v5.12.1, and this page does not
depend on it.

## Keep the sandbox

Configure access in ai-jail, then start the harness under that sandbox.
Documented entry points:

| Command | What the upstream docs say |
| --- | --- |
| `ai-jail configure HARNESS` | Saves the project access policy and does not launch. Needs a terminal. |
| `ai-jail HARNESS` | First interactive run of a known harness reviews access, then launches. The v3.3.0 README example is `ai-jail claude`. |
| `ai-memory run --jail HARNESS` | Asks ai-memory to re-exec under ai-jail. Described in [design-yolo-safety-ai-jail.md](https://github.com/akitaonrails/ai-memory/blob/v3.4.3/docs/design-yolo-safety-ai-jail.md) at v3.4.3. |

Do not pass `--no-jail`. A host-only launch is not the replacement.

`ai-jail --harness HARNESS status --json` is read-only. Upstream documents
`onboarding_complete` and the `onboarding_required` blocker. Use it to see
whether a project still needs `ai-jail configure`. It does not write policy.

On ai-jail 3.3.0, a first interactive run of a known harness opens the
onboarding UI even when the command line already carries access flags. That
was observed with `ai-jail --harness claude` and a non-Claude command, and
the UI was cancelled before it saved anything. It was not repeated with a
real harness, with ai-memory in front of ai-jail, or on macOS.

The v3.4.3 ai-memory binary was not installed or executed for this page, so
the `--jail` re-exec is a documented path, not a path that was run here.

## Re-enter launcher choices by hand

There is no converter from `~/.config/ai-launch/config.yaml`,
`<project>/.ai-launcher/config.yaml`, a legacy `<project>/.ai-launch.yaml`,
or a named launcher profile. Leave those files in place until the same
choices exist upstream. This repository does not delete them.

| Launcher choice | Re-enter as | Care |
| --- | --- | --- |
| SSH, GitHub CLI, GPU, display, Pictures, Tailscale, systemd user bus, mise, Git worktree passthrough | The matching ai-jail onboarding row. The v3.3.0 inventory is [CLI_TUI_PARITY.md](https://github.com/akitaonrails/ai-jail/blob/v3.3.0/docs/CLI_TUI_PARITY.md). | A checked launcher box is not a flag to paste. Read the ai-jail row. Full SSH and the host GitHub token are OPEN authority there. |
| Network access (on by default in the launcher) | The network row in the ai-jail UI | The "For developers" preset leaves unrestricted network closed and allows the known harness API host. Copying a launcher `network: true` into `--network` opens what that preset keeps closed. |
| Allow hosts and allow private hosts | `--allow-host` and `--allow-private-host` | CLI-only in the v3.3.0 parity inventory. The UI covers the harness API host, not an arbitrary list. |
| Extra mounts, read-only or read-write | Directory access in the ai-jail UI | ai-jail still refuses credential roots and the filesystem root. |
| Docker socket permission | "Host container engine" in the ai-jail UI | This is the host daemon socket. It is OPEN authority, and no preset enables it. It is not the launcher container backend below. |
| Sandbox toggle | One of the launch forms in the previous section | Turning the sandbox off is the host-only path. |
| Memory on, continue, workstream, fresh, workspace, project, force-unlock, resume, workstream list, search | `ai-memory run` and the matching ai-memory subcommand. Confirm the flags with `ai-memory run --help` on the version you installed. | v3.4.3's changelog makes `--yolo` the yolo switch and keeps `--true-yolo` as a hidden alias. That was read from the changelog, not executed. |
| `--yolo` | `ai-memory run --yolo` while still using the jail path above | ai-memory documents an offer to re-run a yolo session inside ai-jail. The sandboxed choice is the one that accepts that offer, or an explicit `--jail`. |
| MCP and hooks | `ai-memory setup`, or `install-hooks` / `install-mcp` after the official install | The official archive keeps `hooks/`. The v5.12.1 launcher installer does not. |

An ai-jail project policy is the access policy for one project (or, with the
v3.3.0 global setting `project_strategy = "repo-root"`, for linked worktrees
of one repository). It is not a launcher profile that also selects the
memory layer and the container backend.

## What has no converter and no full replacement

These were not found in the ai-jail 3.3.0 README or CLI/TUI parity
inventory, nor in the ai-memory 3.4.3 README, `docs/features.md`, or
`docs/usage.md`:

- The container backend: `--docker-backend`, toolchain stacks, infrastructure
  services, the generated Dockerfile, `docker-compose.yaml`, and the other
  generated files under `<project>/.ai-launcher/`.
- Launcher profiles that combine an agent, memory, the jail, and that
  container backend.
- The launcher catalog that downloads harness installers.
- `ai-launcher upgrade`.

There is no migration command for them. Someone who still depends on the
container backend can keep the v5.12.1 binary for that workflow only. That
binary reports ai-jail 3.1.0 and newer as outside the range it validated.
This page does not extend that range, and it does not claim the container
backend was run against current upstream.

## What was not executed

No migration was run end to end. No harness was started inside a sandbox.
ai-memory 3.4.3 was not installed. ai-jail 3.3.0 was used for `--version`,
`capabilities --json`, one read-only `status --json` on an empty temporary
project, one `--dry-run`, and one onboarding frame that was cancelled before
save. macOS was not tested.
