# Phase 3 — `.ai-launcher/` diretório + materialização de artefatos

## Goal

Migrar de `.ai-launch.yaml` (arquivo único) para `.ai-launcher/` (diretório)
que controla tudo: config, Dockerfile gerado, docker-compose.yaml (fase 6),
install-config.yaml, e o contexto de build. O ai-launcher passa a **gerenciar
o environment**, não só gerar argv.

## Prerequisites

- Phase 1 concluída
- Phase 2 concluída (runtime abstraction — o Dockerfile materializado precisa
  saber qual runtime usar)
- `.ai-launch.yaml` existe como arquivo único hoje (lido/escrito por
  `config.LoadLocal`/`config.SaveLocal`)

## Context for a cold-start agent

O `ai-launcher` hoje lê a seleção de launch de `.ai-launch.yaml` (arquivo na
raiz do projeto). Quando o backend docker está ativo, o Dockerfile e o
install-config.yaml são gerados em diretórios temporários e descartados após o
build. Isso significa:

1. Os artefatos não são versionáveis nem inspecionáveis
2. Não há lugar para o `docker-compose.yaml` (fase 6) morar
3. O operador não consegue editar/customizar o Dockerfile gerado
4. Um rebuild sempre regenera tudo do zero

A evolução natural é um **diretório** `.ai-launcher/` na raiz do projeto que
funciona como o "environment declarativo" — versionado com o projeto, com
artefatos materializados que podem ser inspecionados, editados e regenerados.

O projeto exige Conventional Commits, coverage 90% em `internal/config` e
`internal/container`.

## Requirements

### 3.1 Leitura com fallback

`config.LoadLocal` tenta `.ai-launcher/config.yaml` primeiro. Se não existe,
fallback para `.ai-launch.yaml` (backward compat, sem breaking change).

**Validação**: Teste que lê config novo e config antigo, ambos funcionam.

### 3.2 Escrita migra para o novo formato

`config.SaveLocal` sempre escreve em `.ai-launcher/config.yaml`. Se
`.ai-launch.yaml` antigo existe, copia para `.ai-launcher/config.yaml` (migra)
e renomeia o antigo para `.ai-launch.yaml.bak` com um warning no stderr.

**Validação**: Teste que simula a migração: cria `.ai-launch.yaml`, salva,
verifica que `.ai-launcher/config.yaml` existe e `.ai-launch.yaml.bak` também.

### 3.3 Materializar Dockerfile

Quando o backend docker está ativo, `ai-launcher generate` (ou implicitamente
no `--save`) materializa `.ai-launcher/Dockerfile` a partir da seleção atual
(stacks + agentes). O operador pode inspecionar e editar o Dockerfile; o
`ai-launcher generate` regenera (sobrescreve) a partir da seleção.

**Validação**: `ai-launcher generate` cria `.ai-launcher/Dockerfile` com o
conteúdo esperado (FROM, dev profile, stacks, agentes).

### 3.4 Materializar install-config.yaml

`.ai-launcher/install-config.yaml` — o config mínimo que o `--install` usa
dentro do build. Materializado junto com o Dockerfile.

**Validação**: O arquivo existe e contém os agentes selecionados.

### 3.5 .gitignore dentro do diretório

`.ai-launcher/.gitignore` ignora artefatos temporários:
```
# build context temporário
.context/
# cache de imagem
.image-cache/
```

**Validação**: O `.gitignore` existe e ignora os paths corretos.

### 3.6 Trust gate

O trust gate continua usando o hash do arquivo de config. Mas agora há dois
arquivos para hashear: `.ai-launcher/config.yaml` (a seleção) e
`.ai-launcher/Dockerfile` (o artefato gerado). O trust registra o hash do
`config.yaml` (a fonte de verdade); o `Dockerfile` é derivado e regenerável.

Se um `.ai-launcher/Dockerfile` for symlinked (controle de checkout), o trust
gate recusa (mesma proteção que `.ai-jail` symlinked hoje).

**Validação**: Teste que um config não-salvo é recusado; teste que um
Dockerfile symlinked é recusado.

### 3.7 ResolveConfigPaths atualizado

A função que resolve os paths do local config (`resolveConfigPaths` no CLI)
procura `.ai-launcher/config.yaml` primeiro. A flag `--local-config` continua
funcionando (aponta para um path explícito, seja arquivo ou diretório).

**Validação**: `ai-launcher --local-config .ai-launcher/config.yaml` funciona.

### 3.8 Comando `generate`

Novo subcomando `ai-launcher generate` que:
1. Lê a seleção atual (flags + config)
2. Gera `.ai-launcher/Dockerfile` e `.ai-launcher/install-config.yaml`
3. Não executa build nem launch — só materializa os artefatos
4. Imprime o que foi gerado

**Validação**: `ai-launcher generate` cria os arquivos sem rodar o agente.

## Files to create/modify

- `internal/config/config.go` — `LoadLocal`/`SaveLocal` suportam diretório; `LocalConfigDir()` helper
- `internal/config/config_test.go` — testes de migração e fallback
- `cmd/ai-launcher/main.go` — `resolveConfigPaths` procura `.ai-launcher/`; subcomando `generate`
- `cmd/ai-launcher/trust_test.go` — teste de trust com Dockerfile symlinked
- `internal/container/build.go` — `PrepareBuildContext` pode escrever em `.ai-launcher/` em vez de temp

## Design decisions (already made)

- **Migração automática sem breaking**: o `.ai-launch.yaml` antigo continua
  sendo lido; o primeiro save migra para `.ai-launcher/config.yaml`.
- **Dockerfile editável**: o operador pode editar o Dockerfile materializado;
  `ai-launcher generate` sobrescreve. Se o operador quer preservar edits, usa
  um `.ai-launcher/Dockerfile.custom` (futura feature).
- **Versionável**: o `.ai-launcher/` é versionado com o projeto. O
  `.ai-launcher/.gitignore` ignora só artefatos temporários, não o config nem
  o Dockerfile.
- **Trust**: hash do `config.yaml` (fonte de verdade). O Dockerfile é derivado.

## Validation criteria (acceptance gate)

- [ ] `make test` passa
- [ ] `make test-coverage` ≥ 90%
- [ ] `make lint-full` — 0 issues
- [ ] Teste `TestLocalConfigMigration` valida migração automática
- [ ] Teste `TestLocalConfigFallback` valida leitura do `.ai-launch.yaml` antigo
- [ ] `ai-launcher generate` cria `.ai-launcher/Dockerfile` e `.ai-launcher/install-config.yaml`
- [ ] Trust gate recusa Dockerfile symlinked

## Out of scope

- docker-compose.yaml materializado (Phase 6)
- Edição custom do Dockerfile (futura feature)
- Migração reversa (de diretório para arquivo)
