# Índice — Fases do Docker Container Mode

> Este índice é o ponto de entrada para qualquer agente (com ou sem memória
> de sessão) que precise implementar ou dar continuidade ao docker container
> mode do ai-launcher. Cada fase é um documento autocontido: goal, pré-requisitos,
> contexto para agente frio, requisitos numerados com validação, arquivos a
> criar/modificar, decisões de design já tomadas, e gate de aceitação.

## Branch

```
repo:     /Volumes/MSD512/Projetos/ai-launcher
worktree: .slim/worktrees/docker-container-mode/
branch:   feature/docker-container-mode
base:     origin/main
```

## Regras do projeto (leia antes de começar)

- **Conventional Commits** (`feat:`, `fix:`, `docs:`, `test:`, `refactor:`)
- **Nunca commit/push em `main`**
- **1 preocupação por PR**
- **Coverage gate 90%** em `internal/{config,catalog,launcher,container}` —
  os 4 arquivos mudam juntos: `.ai-standards.env`, `Makefile`,
  `sonar-project.properties`, `.github/workflows/ci.yml`
- **Gherkin** (`test/features/`) é o detector de drift — se um teste falha,
  o código está errado, não o teste
- **CHANGELOG** no mesmo merge do código
- **Commits após as 18h** (preferência do operador)

## Fases (em ordem de dependência)

| # | Fase | Arquivo | Pré-requisitos | Status |
|---|---|---|---|---|
| 0 | Docker backend base | `docker-container-mode.md` | — | ✅ Entregue (13 commits) |
| 1 | Validação empírica config dirs | `phase-1-config-dirs.md` | Phase 0 | ✅ Entregue (worktree, gates verdes) |
| 2 | Runtime abstraction | `phase-2-runtime.md` | Phase 1 | ✅ Entregue (worktree, gates verdes) |
| 3 | `.ai-launcher/` diretório | `phase-3-dot-launcher.md` | Phase 1, 2 | ✅ Entregue (worktree, gates verdes) |
| 4 | Catálogo de serviços | `phase-4-services.md` | Phase 1 | ✅ Entregue (worktree, gates verdes) |
| 5 | Recursos + ports + rede | `phase-5-resources.md` | Phase 1, 2 | ✅ Entregue (worktree, gates verdes) |
| 6 | docker-compose generation | `phase-6-compose.md` | Phase 3, 4, 5 | ✅ Entregue (worktree, gates verdes) |
| 7 | TUI overhaul | `phase-7-tui.md` | Phase 1-6 | ✅ Entregue (Oracle PASS 97/100, gates verdes) |
| 8 | Integração + docs | `phase-8-integration.md` | Phase 1-7 | ✅ Entregue (gates verdes; Oracle externo sem retorno) |

## Diagrama de dependências

```
Phase 0 (entregue) ─────┬──► Phase 1 (config dirs) ──┬──► Phase 3 (.ai-launcher/) ──┐
                        │                            │                              ├──► Phase 6 (compose) ──┐
                        │                            ├──► Phase 4 (services) ───────┤                       │
                        │                            │                              │                       ├──► Phase 7 (TUI) ──► Phase 8 (docs)
                        │                            └──► Phase 5 (resources) ──────┘                       │
                        │                                                                                   │
                        └──► Phase 2 (runtime) ───────────────────────────────────────────────────────────┘
```

## Decisões confirmadas pelo operador

1. **rw config dirs**: cada agente monta SÓ seus dirs, read-write (login compartilhado)
2. **Runtime priority**: docker → podman → nerdctl (auto-detecção nesta ordem)
3. **Compose automático**: quando há serviços de infra, usa compose; senão, docker run
4. **`.ai-launcher/` migration**: automática no primeiro save (com backup)
5. **Catálogo**: ~40+ serviços cobrindo SQL, NoSQL, Vector, TS, Search, Queue, Cache, Storage, Monitoring, Auth, Workflow, DevTools
6. **Portas do agente**: por-perfil (cada profile define suas portas)
7. **Commits após 18h**

## Como continuar (para um agente frio)

1. Leia `AGENTS.md` (regras do projeto)
2. Leia `docs/ARCHITECTURE.md` (arquitetura atual)
3. Leia este índice (`phases/README.md`)
4. Leia a fase que vai implementar
5. Verifique o status: `cd .slim/worktrees/docker-container-mode && git log --oneline origin/main..HEAD`
6. Implemente seguindo os requisitos numerados da fase
7. Rode o gate: `make test-all`
8. Commit com Conventional Commits após as 18h
9. Marque a fase como entregue neste índice
