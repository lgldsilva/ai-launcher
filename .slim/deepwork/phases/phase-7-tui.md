# Phase 7 — TUI overhaul completa

## Goal

A TUI reflete TODAS as opções do docker backend: stacks, serviços de infra,
recursos (memória/CPU/PIDs), portas expostas, runtime, rede, e o painel de
config compartilhada. O preview mostra argv `docker run` ou `docker-compose.yaml`
dependendo do contexto.

## Prerequisites

- Phase 1 a 6 concluídas
- `internal/tui/tui.go` com a seção Container atual (stacks + shared config)

## Context for a cold-start agent

A TUI do ai-launcher é bubbletea (Go). Hoje tem seções:
0. Agent
1. Permissions
2. Mounts
3. Options (jail/memory/yolo/docker toggle)
4. Container (condicional: stacks + shared config)
5. Profiles (condicional)

Cada seção tem seu próprio `*View(b *strings.Builder)` e suas teclas. A
navegação é por Tab/Shift-Tab e números 1-6. O estado durável vive em
`m.launch` (LaunchConfig) para que CLI e TUI compartilhem o mesmo builder.

O projeto exige Conventional Commits. A TUI está **fora do gate de coverage**
(race-only), mas navegação e rendering são testados.

## Requirements

### 7.1 Seção Container expandida

A seção Container (condicional, docker ativo) agora contém, nesta ordem:

1. **Stacks** (existente): checkboxes `[✓] Go  [ ] Python  [ ] Rust ...`
2. **Shared config** (existente): painel "Shared config (rw)" com dirs do agente
3. **Resources** (novo): campos editáveis
   - `Memory: 4g` (Enter edita; vazio = sem limite)
   - `CPU: 2.0` (Enter edita; vazio = sem limite)
   - `PIDs: 512` (Enter edita; vazio = sem limite)
4. **Ports** (novo): lista editável
   - `Ports: 3000:3000, 8080:8080` (Enter edita — CSV)
5. **Network** (novo): campo editável
   - `Network: bridge` (Enter edita; vazio = default)
6. **Runtime** (novo, só leitura): linha informativa
   - `Runtime: docker (auto-detected)`

**Validação**: `TestContainerViewExpanded` renderiza todas as sub-seções.

### 7.2 Seção Services (nova, condicional)

Nova seção (aparece quando docker ativo), entre Container e Profiles:

```
Services
  Databases (SQL)
    [ ] PostgreSQL    :5432
    [ ] MySQL         :3306
  Databases (NoSQL)
    [✓] MongoDB       27017:27017
    [✓] Redis         :6379
  Message Queues
    [ ] Kafka         :9092
  ...
```

- Agrupado por categoria (`ServicesByCategory()`)
- Cada linha: `[✓/ ] Nome   :porta(s)`
- `Space` toggle; `↑/↓` navega; `Enter` também toggle
- A seção é rolável se muitas categorias

**Validação**: `TestServicesView` renderiza as categorias e serviços.

### 7.3 Preview dinâmico

A tecla `d` (dry-run preview) agora mostra:
- **Sem serviços de infra**: argv `docker run ...` (como hoje)
- **Com serviços de infra**: preview do `docker-compose.yaml` gerado (YAML)

O preview sempre mostra o que seria executado/compose-up.

**Validação**: `TestPreviewWithServices` mostra YAML quando há serviços.

### 7.4 Profile persistence

Todos os novos campos (services, memory, cpu, pids, ports, network, runtime)
persistem no profile (Ctrl+P) e no `.ai-launcher/config.yaml` (Ctrl+S).

**Validação**: `TestProfilePersistsAll` salva e recarrega com todos os campos.

### 7.5 Seção hint atualizada

Cada seção tem sua hint na barra inferior:
- Container: `Stacks · Space toggle · Enter edit resources/ports · Tab next`
- Services: `Space add/remove · Enter edit published ports · Tab next`
- Options: atualizada para incluir `Container (docker)` toggle

**Validação**: Cada `sectionHint()` retorna a string correta.

### 7.6 Navegação com seções dinâmicas

As seções Container e Services são condicionais (só existem com docker ativo).
A seção Profiles é condicional (só com profiles salvos). A navegação (Tab,
números) usa `sectionCount()` que já é dinâmico. Os índices calculados
(`containerIndex()`, `profilesIndex()`) precisam de um novo `servicesIndex()`.

**Validação**: `TestSectionCountWithAllSections` verifica a contagem com todas
as seções ativas.

### 7.7 Ajuda (?) atualizada

O texto de ajuda lista as novas seções e teclas.

**Validação**: `TestHelpText` menciona Services, resources, ports.

## Files to create/modify

- `internal/tui/tui.go` — expandir containerView; nova servicesView; preview dinâmico; hints; help
- `internal/tui/container_test.go` — testes dos campos de recurso e ports
- `internal/tui/services_test.go` (novo) — testes da seção Services
- `internal/tui/tui_test.go` — atualizar testes de navegação

## Design decisions (already made)

- **Fields editáveis via textInput**: o mesmo mecanismo de `startParamInput`
  (textInputActive) serve para editar memory/cpu/ports/network. Enter abre o
  input, Enter confirma, Esc cancela.
- **Services como seção própria**: não cabe dentro de Container (Container já
  tem stacks + config + resources). Services é uma seção separada com seu
  próprio cursor.
- **Preview dinâmico**: o preview escolhe argv vs YAML baseado na presença de
  serviços de infra (mesma regra que Phase 6 usa para docker run vs compose).

## Validation criteria (acceptance gate)

- [ ] `make test` passa (race-only para TUI)
- [ ] `make lint-full` — 0 issues
- [ ] `TestContainerViewExpanded` renderiza stacks + config + resources + ports
- [ ] `TestServicesView` renderiza serviços por categoria
- [ ] `TestPreviewWithServices` mostra YAML quando há serviços
- [ ] `TestProfilePersistsAll` salva/recarrega com todos os campos
- [ ] TUI navegável com todas as seções ativas (Container + Services + Profiles)

## Out of scope

- Edição inline de valores (ex: slider de memória)
- Drag-and-drop de serviços
- Reordenação de serviços
- Filtro/busca de serviços (futura feature se o catálogo crescer muito)
