# CI/CD

CI runs on GitHub Actions (`.github/workflows/ci.yml`). Sonar analysis runs
on **SonarCloud** (sonarcloud.io) as a CI job — the old homelab SonarQube CE
and the vendored `scripts/sonar` were removed; never recreate them. Jenkins
is decommissioned: never create a Jenkinsfile or Jenkins webhooks in this
repository. There is no Gitea workflow either — GitHub is the only forge.

## CI pipeline (ci.yml)

Triggers: push to `main` and every pull request. All jobs run on
`ubuntu-latest` with Go set by `go.mod`.

| Job | What it does | Fails the build when |
| --- | --- | --- |
| `test` | `go build`, `go test -race -shuffle=on ./...`, coverage gate | Filtered coverage < 90% (`COVERAGE_MIN`) |
| `lint` | `gofmt -l`, `go vet`, `golangci-lint` v2.12 | Any formatting/lint issue pending |
| `vuln` | `govulncheck` | Known vulnerability reachable in the code |
| `trivy` | Filesystem scan, severity `CRITICAL`, `ignore-unfixed` | CRITICAL vulnerability with a fix available |
| `sbom` | Generates a CycloneDX SBOM and publishes it as an artifact | SBOM generation fails |
| `sonar` | SonarCloud analysis via `npx sonarqube-scanner`, waits for the quality gate | Quality gate red (`qualitygate.wait=true`) |

The `test` job's coverage gate replicates `make test-coverage`: it measures
only `internal/config`, `internal/catalog`, and `internal/launcher`, excludes
the PTY executor (`executor.go`) and the per-platform `replace_*.go` files,
and compares the filtered total against 90%. The CI job runs
`make test-coverage` rather than repeating the command, so the two can never
report different numbers on the same commit. The commit hooks state the same
boundary as the `COVERAGE_EXCLUDE` regex in `.ai-standards.env` — details in
[test-strategy.md](test-strategy.md).

The `sonar` job is **gated on the `SONAR_TOKEN` secret**: every step skips
cleanly while the secret does not exist, so forks and early setup stay green.
The scanner runs with `-Dsonar.host.url=https://sonarcloud.io` and
`-Dsonar.qualitygate.wait=true`; `sonar-project.properties` at the repo root
is host-agnostic (sources, test inclusions, coverage exclusions aligned with
the 90% gate) and never carries host, org, key, or token — those arrive as
`-D` flags. The npx scanner was chosen deliberately: no third-party action to
pin/audit.

## Required secrets and variables

| Name | Kind | Purpose |
| --- | --- | --- |
| `SONAR_TOKEN` | secret | SonarCloud token for the `sonar` job |
| `SONAR_ORGANIZATION` | variable | SonarCloud organization (default `lgldsilva`) |
| `SONAR_PROJECT_KEY` | variable | SonarCloud project key (default `lgldsilva_ai-launcher`) |
| `RELEASE_TAG_TOKEN` | secret | PAT with `repo` + `workflow` scope used by `autotag.yml` to push release tags |

## SHA-pinned actions

Every third-party action is referenced by **commit SHA**, never by a mutable
tag, with the version in a comment next to it (e.g.
`actions/checkout@3d3c42e… # v7.0.1`). The reason is supply-chain:
`trivy-action` tags prior to 0.35.0 were force-pushed with malware in March
2026 (CVE-2026-33634). When bumping an action, update the SHA and the comment
together.

## Release

Release is fully automated and two-staged:

1. **Auto-tag** (`.github/workflows/autotag.yml`): on every push to `main`
   (and on manual dispatch), it computes the next semantic version with
   [svu](https://github.com/caarlos0/svu) from the Conventional Commits since
   the last tag (`feat` → minor, `fix`/others → patch, `!`/BREAKING CHANGE →
   major). If nothing bump-worthy landed, it tags nothing. When the version
   bumps, it creates and pushes the annotated `v*` tag.
2. **Release** (`.github/workflows/release.yml`): triggered by `v*` tags (or
   manually via `workflow_dispatch` with a tag input, which is idempotent —
   the previous release for that tag is dropped and recreated). It runs
   GoReleaser (`go run github.com/goreleaser/goreleaser/v2@v2.13.0 release --clean`)
   driven by `.goreleaser.yaml`: six binaries (linux/darwin/windows
   × amd64/arm64), archives named
   `ai-launcher_<version>_<os>_<arch>.{tar.gz,zip}` (zip on Windows) with
   README + LICENSE, a SHA-256 `checksums.txt`, and a grouped changelog
   (Features / Fixes / Others; `docs:`/`test:`/`ci:`/`chore:` excluded). A
   CycloneDX SBOM is generated afterwards (so it stays out of
   `checksums.txt`) and attached to the release with `gh release upload
   --clobber`.

**Why `RELEASE_TAG_TOKEN` (a PAT) and not `GITHUB_TOKEN`**: a tag pushed with
the auto-provided Actions token does **not** trigger another workflow — that
is GitHub's loop guard — so `release.yml` would never fire for autotagged
versions. Pushing the tag with a real PAT (`repo` + `workflow` scope) makes
the tag event fire `release.yml`. Until the secret is provisioned, the
autotag job skips cleanly and stays green.

GoReleaser injects the binary version via `-ldflags` (`main.version`,
`main.commit`, `main.date`) — the same variables `make build` injects — which
is what `ai-launcher --version` prints and what `ai-launcher upgrade`
compares against the latest release tag.

To validate the release artifacts locally without publishing:

```bash
go run github.com/goreleaser/goreleaser/v2@v2.13.0 check
go run github.com/goreleaser/goreleaser/v2@v2.13.0 release --snapshot --clean --skip=publish
```

`make release-local` still exists as a minimal local fallback (plain `dist/`
binaries + `SHA256SUMS`), but the canonical check of what `release.yml`
publishes is the GoReleaser snapshot above.
