# Phase 1 — Validação empírica dos diretórios de config

## Goal

Verificar empiricamente onde cada agentic coding CLI realmente guarda suas
configurações, credenciais e histórico, rodando cada um dentro de um container
real com HOME limpo e capturando os paths criados. Corrigir TODOS os paths no
mapa `agentConfigDirs` que não batem com a realidade.

## Prerequisites

- Branch `feature/docker-container-mode` com 13 commits (docker backend funcional)
- `internal/container/agentmounts.go` com o mapa atual de 23 agentes
- Docker daemon acessível para a validação empírica
- Bateria de flavors existente (`AI_LAUNCHER_DOCKER_TESTS=1`)

## Context for a cold-start agent

O projeto `ai-launcher` (Go, TUI bubbletea) orquestra agentes de IA em
containers Docker. Quando o backend docker está ativo, cada agente precisa de
seus diretórios de config montados no container (same-path, read-write) para
que o login, credenciais e histórico persistam entre host e containers.

O mapa de diretórios está em `internal/container/agentmounts.go`, na variável
`agentConfigDirs`. Ele foi baseado em **pesquisa web**, não em verificação
empírica — uma verificação parcial já mostrou que o `cursor-agent` estava
errado (`~/.config/cursor` não `~/.cursor`). Precisamos validar TODOS.

O projeto exige:
- Conventional Commits
- Coverage gate 90% nos pacotes de lógica
- `make test-all` é o gate completo
- A bateria de flavors (`AI_LAUNCHER_DOCKER_TESTS=1`) faz builds reais

## Requirements

### 1.1 Construir imagem de verificação

Criar um teste temporário que constrói uma imagem Docker com TODOS os agentes
instaláveis (script + npm). Usar `|| true` em cada `RUN` para que um agente
quebrado não pare o build inteiro. A imagem deve ter `node`/`npm` disponíveis
(via nvm no DevProfile).

**Validação**: `docker images | grep verify` mostra a imagem construída.

### 1.2 Rodar cada agente e capturar paths

Para cada agente na imagem de verificação:
1. Montar um diretório temporário vazio como `HOME`
2. Rodar o agente com `--version` (não-interativo) e com first-run simulado
   (`echo no | timeout 15 <agente>` para agents que pedem login)
3. Capturar `find $HOME -type d` e `find $HOME -type f` após cada execução
4. Registrar os paths criados

**Validação**: O output mostra os paths reais para cada agente.

### 1.3 Corrigir paths errados no agentConfigDirs

Comparar os paths empíricos com o mapa declarado em `agentConfigDirs`.
Corrigir TODOS os que não batem. Documentar cada correção com um comentário
`// empirically verified: <agente> writes <path>`.

**Paths já corrigidos**: `cursor-agent` (`~/.config/cursor` não `~/.cursor`).

**Paths a verificar (prioridade alta — instaláveis)**:
- claude: declarado `~/.claude`, `~/.claude.json`, `~/.claude/projects`
- codex: declarado `~/.codex`
- opencode: declarado `~/.config/opencode`, `~/.local/share/opencode`
- kimi: declarado `~/.kimi-code`
- agy: declarado `~/.config/antigravity`, `~/.gemini/antigravity`
- pi: declarado `~/.pi`, `~/.config/pi`
- crush: declarado `~/.config/crush`
- omp: declarado `~/.omp`
- cursor-agent: declarado `~/.config/cursor` (corrigido)
- grok: declarado `~/.grok`
- devin: declarado `~/.devin`
- gemini: declarado `~/.gemini`
- qwen: declarado `~/.qwen`, `~/.config/qwen`
- openclaw: declarado `~/.openclaw`
- cline: declarado `~/.cline`, `~/.config/cline`

**Paths a verificar (host-binary fallback — menor prioridade)**:
- mimo, zero, oc, aider, goose, kiro-cli, hermes, kilo

**Validação**: `grep 'empirically verified' internal/container/agentmounts.go`
mostra comentários para TODOS os agentes instaláveis.

### 1.4 Adicionar teste empírico permanente

Criar `TestEmpiricalConfigDirs` na bateria de flavors que:
1. Para cada agente instalável, roda no container com HOME limpo
2. Verifica que os paths declarados em `agentConfigDirs` são criados pelo agente
3. Falha se um path declarado NÃO existe ou se um path criado NÃO está declarado

Guardado por `AI_LAUNCHER_DOCKER_TESTS=1`.

**Validação**: `AI_LAUNCHER_DOCKER_TESTS=1 go test ./internal/container/ -run TestEmpiricalConfigDirs -v` passa.

### 1.5 Documentar fonte de cada path

Cada entry em `agentConfigDirs` ganha um comentário de fonte:
- `// empirically verified: <data>` — validado em container real
- `// documented: <url>` — baseado em documentação oficial
- `// guessed: needs verification` — não verificado

**Validação**: `grep -c 'empirically verified\|documented\|guessed' internal/container/agentmounts.go` == número de entries.

## Files to create/modify

- `internal/container/agentmounts.go` — corrigir paths, adicionar comentários de fonte
- `internal/container/agentmounts_test.go` — atualizar testes para paths corrigidos
- `internal/container/flavors_test.go` — adicionar `TestEmpiricalConfigDirs`

## Design decisions (already made)

- **Same-path rw**: cada agente monta SÓ seus próprios dirs, read-write (modelo de login compartilhado). Decisão do operador.
- **Platform variants**: paths podem diferir entre macOS/Linux/Windows. O campo `Platforms []string` já existe no `ConfigDir`.
- **Cursor corrigido**: empiricamente verificado que cursor-agent usa `~/.config/cursor` (não `~/.cursor`) no Linux/macOS.

## Validation criteria (acceptance gate)

- [ ] `make test` passa (todos os testes determinísticos)
- [ ] `make test-coverage` ≥ 90%
- [ ] `make lint-full` — 0 issues
- [ ] `make sec-static` — 0 issues
- [ ] `AI_LAUNCHER_DOCKER_TESTS=1 go test ./internal/container/ -run TestEmpiricalConfigDirs -v` passa
- [ ] `grep -c 'empirically verified' internal/container/agentmounts.go` ≥ 15 (todos os instaláveis)
- [ ] Cada entry de agente instalável tem comentário de fonte

## Out of scope

- Adicionar novos agentes ao catálogo (só validar os existentes)
- Mudar o modelo rw/ro (já decidido: rw)
- Implementar runtime abstraction (Phase 2)
- Implementar `.ai-launcher/` diretório (Phase 3)
