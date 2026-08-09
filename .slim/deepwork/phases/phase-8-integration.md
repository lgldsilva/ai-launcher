# Phase 8 — Integração final + docs + validação

## Goal

Fechar todas as fases com documentação completa, contrato Gherkin estendido,
bateria de flavors com compose, e validação empírica end-to-end (agente +
infra + recursos num compose real).

## Prerequisites

- Phase 1 a 7 concluídas
- Todas as funcionalidades implementadas e testadas individualmente

## Context for a cold-start agent

Esta é a fase de fechamento. As fases 1-7 implementaram: validação empírica
de config dirs, runtime abstraction, `.ai-launcher/` diretório, catálogo de
serviços, recursos/ports/rede, geração de docker-compose, e TUI completa.

Agora precisamos:
1. Estender o contrato Gherkin (detector de drift) para cobrir os novos argv
2. Atualizar a documentação completa (ARCHITECTURE, design-decisions, README)
3. Atualizar o CHANGELOG
4. Fazer a validação empírica final (compose real com agente + postgres + redis)
5. Garantir que os gates cobrem os novos pacotes (services.go, compose.go)

O projeto exige: Conventional Commits, coverage 90% nos pacotes de lógica,
Gherkin como detector de drift, CHANGELOG no mesmo merge.

## Requirements

### 8.1 Gherkin estendido

Adicionar cenários em `test/features/launcher.feature`:

- Cenário: docker run com `--memory` e `--cpus` (Phase 5)
- Cenário: docker run com `-p 3000:3000` (Phase 5)
- Cenário: docker run com `--network host` (Phase 5)
- Cenário: docker run com `--container-runtime podman` (Phase 2)
- Cenário: compose YAML gerado com serviços (Phase 6)
- Cenário: compose YAML com volumes e networks (Phase 6)

O leitor Gherkin (`test/gherkin/gherkin_test.go`) precisa de campos novos em
`launchSpec`: `memory`, `cpus`, `ports`, `network`, `services`, `runtime`.

**Validação**: `make test-gherkin` passa com os novos cenários.

### 8.2 Gates atualizados

Os novos pacotes `internal/container/services.go` e `compose.go` entram no
gate de 90% de coverage. Atualizar os 4 arquivos juntos (regra do AGENTS.md):
- `.ai-standards.env` (COVERAGE_EXCLUDE regex — não os exclui)
- `Makefile` (COVERAGE_PACKAGES + coverpkg + test-unit)
- `sonar-project.properties` (sonar.coverage.exclusions)
- `.github/workflows/ci.yml` (coverpkg duplicado)

**Validação**: `make test-coverage` mede `internal/container` (incluindo
services.go e compose.go) e passa ≥ 90%.

### 8.3 Docs: ARCHITECTURE

Atualizar `docs/ARCHITECTURE.md`:

- **Runtime abstraction**: novo componente, diagrama da cadeia com runtime
- **docker-compose**: o que muda quando há serviços de infra
- **Services catalog**: como o catálogo modela a infra
- **`.ai-launcher/` directory**: estrutura do diretório, artefatos
- **Resources**: limites, portas, rede
- **Reading order**: atualizada para incluir os novos pacotes

**Validação**: `ARCHITECTURE.md` menciona runtime, compose, services, `.ai-launcher/`.

### 8.4 Docs: design-decisions

Atualizar `docs/design-decisions.md` com novas decisões:

- **Why `.ai-launcher/` is a directory**: artifacts need a home; versionable;
  declarative environment. Cite the bug: Dockerfile generated in temp was
  uninspectable; compose.yaml had nowhere to live.
- **Why runtime abstraction**: podman is daemonless/rootless; nerdctl for k8s
  nodes. Cite: hardcoded "docker" blocked podman users.
- **Why compose when services exist**: single-container can't run infra alongside;
  compose gives DNS + networking + volumes. Cite: `docker run` can't do
  `mongo:27017` DNS resolution.
- **Why rw config dirs (shared login)**: each agent owns its config; login made
  inside persists. Cite: ro mounts lost container logins.
- **Why version-pinned service images**: determinism. Cite: `latest` tag drift.

**Validação**: `design-decisions.md` tem as 5 novas decisões.

### 8.5 Docs: README

Atualizar `README.md`:

- **Docker container backend** section atualizada com: compose, services,
  resources, runtime, `.ai-launcher/` directory
- **Quick start** com `ai-launcher --docker-backend --service postgres up`
- **Examples**: agent + postgres + redis via compose

**Validação**: README menciona compose, services, runtime, ports.

### 8.6 CHANGELOG

Entry completa em `## [Unreleased]`:

- Added: runtime abstraction (docker/podman/nerdctl)
- Added: `.ai-launcher/` directory with materialized Dockerfile + compose
- Added: infrastructure catalog (~40 services)
- Added: docker-compose.yaml generation
- Added: resource limits (memory, CPU, PIDs)
- Added: port exposure and network selection
- Added: TUI with services picker, resource editor, compose preview

**Validação**: CHANGELOG tem todas as entries.

### 8.7 Validação empírica final

Teste `TestComposeEndToEnd` (guardado por `AI_LAUNCHER_DOCKER_TESTS=1`):

1. `ai-launcher generate --service postgres --service redis` gera
   `.ai-launcher/docker-compose.yaml`
2. `<runtime> compose up -d` sobe agente + postgres + redis
3. O agente alcança `postgres:5432` e `redis:6379` (DNS do compose)
4. O host alcança `localhost:5432` e `localhost:6379` (portas publicadas)
5. `<runtime> compose down` derruba tudo

**Validação**: `AI_LAUNCHER_DOCKER_TESTS=1 go test ./internal/container/ -run TestComposeEndToEnd -v` passa.

### 8.8 Bateria de flavors com compose

Adicionar à bateria existente (`flavor_matrix_test.go`):

- Cenário: agente + postgres (compose up, agente alcança `postgres:5432`)
- Cenário: agente + redis (compose up, agente alcança `redis:6379`)
- Cenário: agente + postgres + redis (multi-serviço)

**Validação**: Os cenários passam com build real.

## Files to create/modify

- `test/features/launcher.feature` — novos cenários
- `test/gherkin/gherkin_test.go` — campos novos em launchSpec
- `.ai-standards.env`, `Makefile`, `sonar-project.properties`, `.github/workflows/ci.yml` — gates
- `docs/ARCHITECTURE.md` — atualização completa
- `docs/design-decisions.md` — novas decisões
- `README.md` — seção docker atualizada
- `CHANGELOG.md` — entries completas
- `internal/container/flavor_matrix_test.go` — cenários compose

## Design decisions (already made)

- **Gherkin é o drift detector**: cenários novos travam a composição; se o
  código mudar, o teste falha primeiro (regra do projeto).
- **Docs no mesmo merge**: CHANGELOG e docs accompany o código (regra do projeto).
- **Validação empírica com compose real**: o teste end-to-end prova que o
  agente alcança os serviços de infra pelo DNS do compose.

## Validation criteria (acceptance gate)

- [ ] `make test-all` passa (gate completo)
- [ ] `make test-coverage` ≥ 90%
- [ ] `make lint-full` — 0 issues
- [ ] `make sec-static` — 0 issues
- [ ] `make test-gherkin` passa com novos cenários
- [ ] `docs/ARCHITECTURE.md` atualizado
- [ ] `docs/design-decisions.md` tem as 5 novas decisões
- [ ] `README.md` atualizado
- [ ] `CHANGELOG.md` tem todas as entries
- [ ] `AI_LAUNCHER_DOCKER_TESTS=1 go test ./internal/container/ -run TestComposeEndToEnd -v` passa
- [ ] Branch pronta para PR

## Out of scope

- Performance benchmarks
- Stress tests (múltiplos agentes + múltiplos serviços simultaneamente)
- Migration tooling para usuários existentes (documentar path manual)
- Supabase/Airflow stacks completas (many microservices cada)
