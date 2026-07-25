# CI/CD

CI roda no GitHub Actions (`.github/workflows/ci.yml`). SonarQube roda **só
localmente** contra o servidor do homelab — runners hospedados do GitHub não
alcançam `sonar.raspberrypi.lan`. Jenkins está descomissionado: nunca crie
Jenkinsfile nem webhooks para Jenkins neste repositório.

## Pipeline de CI (ci.yml)

Gatilhos: push em `main` e todo pull request. Todos os jobs rodam em
`ubuntu-latest` com Go definido por `go.mod`.

| Job | O que faz | Falha o build quando |
| --- | --- | --- |
| `test` | `go build`, `go test -race -shuffle=on ./...`, gate de cobertura | Cobertura filtrada < 90% (`COVERAGE_MIN`) |
| `lint` | `gofmt -l`, `go vet`, `golangci-lint` v2.12 | Qualquer formatação/lint pendente |
| `vuln` | `govulncheck` | Vulnerabilidade conhecida alcançável no código |
| `trivy` | Scan de filesystem, severidade `CRITICAL`, `ignore-unfixed` | Vulnerabilidade CRITICAL com correção disponível |
| `sbom` | Gera SBOM CycloneDX e publica como artifact | Falha na geração do SBOM |

O gate de cobertura do job `test` replica `make test-coverage`: mede apenas
`internal/config`, `internal/catalog` e `internal/launcher`, exclui o executor
PTY (`executor.go`) e os `replace_*.go` por plataforma, e compara o total
filtrado com 90%. A fronteira é a mesma do `COVER_PKGS` em `.ai-standards.env`
— detalhes em [test-strategy.md](test-strategy.md).

## Actions fixadas por SHA

Toda action de terceiros é referenciada por **commit SHA**, nunca por tag
mutável, com a versão em comentário ao lado (ex.:
`actions/checkout@3d3c42e… # v7.0.1`). A razão é supply-chain: tags de
`trivy-action` anteriores a 0.35.0 foram force-pushed com malware em março de
2026 (CVE-2026-33634). Ao atualizar uma action, atualize SHA e comentário
juntos.

## SonarQube CE local (efêmero)

Análise Sonar é manual e local, via o script compartilhado
`scripts/sonar/sonar-ephemeral.sh` + `sonar-project.properties`:

```bash
SONAR_TOKEN=... PROJECT_KEY=ai-launcher EPHEMERAL=0 \
  SONAR_HOST_URL=http://sonar.raspberrypi.lan:9000 \
  COVERAGE_FILE=coverage.out \
  scripts/sonar/sonar-ephemeral.sh
```

Pré-requisitos: o servidor do homelab alcançável (`sonar.raspberrypi.lan`) e
`SONAR_TOKEN` exportado. O padrão do script é `EPHEMERAL=1` (projeto
temporário, relatórios exportados e projeto deletado — o modo para PRs);
`EPHEMERAL=0` mantém o projeto permanente (modo main). Rode `make
test-coverage` antes para gerar `coverage.out`. O script aplica pisos locais
de qualidade (fail-closed) porque o quality gate do Sonar CE efêmero pode
voltar vazio. O `sonar.coverage.exclusions` do properties espelha a fronteira
do gate de 90%: `cmd/**`, `internal/tui/**`, `internal/installer/**`,
`test/**`, `executor.go` e `replace_*.go` ficam fora da métrica, não da
análise.

## Release

O workflow `.github/workflows/release.yml` é disparado por tags `v*` (ou
manualmente via `workflow_dispatch` informando a tag) e executa:

1. `make release-local` — build dos 6 binários (`dist/`, linux/darwin/windows
   × amd64/arm64) + geração de `dist/SHA256SUMS` (`release-checksums`);
2. verificação com `sha256sum -c dist/SHA256SUMS`;
3. geração do SBOM CycloneDX (`dist/sbom.cdx.json`, após os checksums para não
   entrar no `SHA256SUMS`);
4. `gh release create` com os 6 binários + `SHA256SUMS` + SBOM como assets e
   notas geradas automaticamente. Reruns via `workflow_dispatch` são
   idempotentes (a release anterior é recriada, a tag é preservada).

Para cortar um release:

```bash
git tag vX.Y.Z
git push --tags   # somente quando houver remoto configurado
```

O mesmo processo roda localmente com `make release-local` (útil para validar
antes de taguear); nesse caso a publicação é manual.
