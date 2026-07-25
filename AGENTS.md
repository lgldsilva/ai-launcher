# AGENTS.md — instruções para agentes de IA neste repositório

## O que é este projeto

Launcher TUI/CLI em Go que orquestra CLIs de IA compostos por duas ferramentas
de terceiros: `ai-jail` (sandbox) e `ai-memory` (memória/sessões). A cadeia
canônica é `ai-jail [jail flags] ai-memory run [wrapper flags] <harness>
[args nativos]`. Detalhes: `docs/ARCHITECTURE.md`.

## Build e testes

| Comando | Uso |
| --- | --- |
| `make build` | Compila `bin/ai-launcher` |
| `make test` | Suíte Go determinística (`go test ./...`) |
| `make test-all` | Porta completa: unit + property + gherkin + race + coverage + lint + sec + mutation |
| `make test-coverage` | Gate de 90% de linha nos pacotes de lógica |
| `make test-gherkin` | Suíte de contrato (`test/features/launcher.feature`) |
| `make lint` / `make lint-full` | `go vet` / golangci-lint |

**Gate de cobertura**: mede apenas `internal/config`, `internal/catalog` e
`internal/launcher` (excluindo `executor.go` e `replace_*.go`); o mínimo é 90%
(`COVERAGE_MIN`). `internal/tui`, `internal/installer` e o executor PTY ficam
**fora do denominador** — são cobertos por `go test -race -shuffle=on ./...`
(race-only). A mesma fronteira está em `COVER_PKGS` no `.ai-standards.env`,
lida pelos hooks de commit. Não mova pacotes de UI/execução para dentro do
gate.

## Git e hooks (ai-standards)

- **Conventional Commits** obrigatórios (`feat:`, `fix:`, `docs:`, `test:`,
  `refactor:`…), verificados por hook.
- **Nunca commite nem dê push direto em `main`.** Crie branch a partir de um
  `main` atualizado.
- **Nunca** defina `AI_STANDARDS_SKIP` ou variante sem autorização humana
  explícita — os hooks aplicam cobertura e qualidade automaticamente.

## Contrato com upstream (ai-jail / ai-memory)

A suíte Gherkin (`test/features/`, reader próprio em `test/gherkin/`) é o
detector de drift das CLIs de terceiros: ela trava a composição exata do argv
(ordem dos wrappers, toggles `--no-*` do ai-jail v1.15, escopo do
`ai-memory run`). Se um teste de contrato falhar depois de uma mudança, **o
código está errado, não o teste** — a menos que o upstream tenha mudado, caso
em que o contrato é atualizado junto com o código, no mesmo commit.

## Ferramentas de qualidade

golangci-lint, gosec e govulncheck rodam via `go run ...@latest` usando as
variáveis do Makefile (`GOLANGCI_LINT`, `GOSEC`, `GOVULNCHECK`) — não é
preciso instalar nada no sistema; sobrescreva a variável com o binário
instalado para acelerar (ex.: `make lint-full GOLANGCI_LINT=golangci-lint`).
Config do lint: `.golangci.yml` (errcheck, gocognit, gosec, govet, revive,
staticcheck…).

## Sonar e CI

- SonarQube CE é **local-only** (`scripts/sonar/sonar-ephemeral.sh` +
  `sonar-project.properties`); runners do GitHub não alcançam o homelab.
  Requer `SONAR_TOKEN` e o servidor `sonar.raspberrypi.lan` acessível.
- CI é GitHub Actions (`.github/workflows/ci.yml`), com actions fixadas por
  SHA. **Jenkins está descomissionado — nunca** crie Jenkinsfile ou webhook.
- Ver `docs/cicd.md` para o pipeline completo e o processo de release.

## Convenções de documentação

- **Português** em toda a documentação (README, docs/, este arquivo).
- Diagramas **ASCII box-and-arrow apenas** — nunca mermaid.
- Arquivos em `docs/` em **kebab-case**; âncoras em CAPS: `README.md`,
  `docs/ARCHITECTURE.md`.
- Tabelas para dados estruturados (flags, config, matrizes); sem sumário (TOC);
  sem emoji decorativo; `Licença` é sempre o último H2 do README.
- Toda flag/opção documentada precisa existir no código — verifique em
  `cmd/ai-launcher/main.go` e `internal/config/config.go` antes de documentar.
