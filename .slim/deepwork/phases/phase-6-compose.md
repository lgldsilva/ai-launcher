# Phase 6 — docker-compose.yaml generation

## Goal

Gerar `docker-compose.yaml` a partir da seleção completa (agente + stacks +
serviços de infra + recursos + rede). O ai-launcher passa a gerenciar o
environment multi-container: `ai-launcher compose up/down/logs` sobe o agente
+ bancos/filas/caches numa rede compartilhada.

## Prerequisites

- Phase 1 concluída (config dirs validados)
- Phase 2 concluída (runtime abstraction — compose command varia)
- Phase 3 concluída (`.ai-launcher/` diretório — o compose.yaml mora lá)
- Phase 4 concluída (catálogo de serviços)
- Phase 5 concluída (recursos + ports + rede)

## Context for a cold-start agent

Hoje o ai-launcher gera `docker run ...` (single container). Quando o operador
seleciona serviços de infra (ex: postgres + redis), o `docker run` puro não
sobe esses serviços junto. A evolução é gerar um `docker-compose.yaml` que:

1. Define o serviço do **agente** (build from Dockerfile, com limites e ports)
2. Define os **serviços de infra** selecionados (postgres, redis, etc.)
3. Coloca todos na **mesma rede** (DNS por nome: o agente alcança `postgres:5432`)
4. Define **volumes nomeados** para persistência
5. Exporta **variáveis de ambiente** de conexão para o agente

O compose é materializado em `.ai-launcher/docker-compose.yaml` (Phase 3) e
versionado com o projeto. O operador pode editá-lo; `ai-launcher generate`
regenera a partir da seleção.

O projeto exige Conventional Commits, coverage 90% em `internal/container`.

## Requirements

### 6.1 ComposeFile struct e RenderCompose

Criar `internal/container/compose.go`:

```go
type ComposeFile struct {
    Version   string                    // "3.9"
    Services  map[string]ComposeService // "agent", "postgres", "redis"
    Networks  map[string]ComposeNetwork
    Volumes   map[string]ComposeVolume
}

type ComposeService struct {
    Build       string            // "." (Dockerfile dir) — só para o agente
    Image       string            // "postgres:18" — para serviços de infra
    Ports       []string          // ["5432:5432"]
    Volumes     []string          // ["pg-data:/var/lib/postgresql/data"]
    Environment map[string]string // {"POSTGRES_PASSWORD": "dev"}
    Networks    []string          // ["ai-launcher"]
    DependsOn   []string          // ["postgres"] — agente depende de infra
    MemLimit    string            // "4g"
    CPUs        string            // "2.0"
    Restart     string            // "no"
    Healthcheck map[string]any    // {"test": ["CMD", "pg_isready"]}
}
```

`RenderCompose()` retorna o YAML como string (determinístico, keys ordenadas).

**Validação**: `TestRenderCompose` gera YAML válido para agente + 2 serviços.

### 6.2 Serviço do agente no compose

O serviço do agente:
- `build: .` (usa o Dockerfile materializado em `.ai-launcher/`)
- `volumes`: projeto rw + config dirs rw + cache dirs rw (mesmos do docker run)
- `environment`: HOME, AI_MEMORY_* (reescritas), DATABASE_URL, etc.
- `depends_on`: todos os serviços de infra selecionados
- `mem_limit`, `cpus`: limites do Phase 5
- `ports`: portas expostas do Phase 5
- `networks`: rede compartilhada do compose

**Validação**: O serviço do agente no YAML tem build + volumes + network.

### 6.3 Serviços de infra no compose

Para cada serviço selecionado (do catálogo da Phase 4):
- `image: <version-pinned>`
- `ports: ["<internal>:<internal>"]`
- `volumes: ["<volume-name>:<container-path>"]`
- `environment: <service defaults>`
- `networks: ["ai-launcher"]`
- `healthcheck: <service healthcheck>`

**Validação**: Cada serviço de infra tem image + ports + volume + network.

### 6.4 Rede compartilhada

Todos os serviços (agente + infra) estão na rede `ai-launcher` (bridge).
O agente alcança os serviços pelo nome DNS: `postgres:5432`, `redis:6379`,
`mongo:27017`.

**Validação**: Todos os serviços têm `"ai-launcher"` em networks.

### 6.5 Volumes nomeados

Para cada serviço de infra com volume, um volume nomeado é declarado:
```yaml
volumes:
  pg-data:
  redis-data:
  mongo-data:
```

**Validação**: `TestRenderComposeVolumes` verifica os volumes nomeados.

### 6.6 Environment de conexão

O serviço do agente recebe variáveis de ambiente com as conexões dos
serviços de infra. Convenção: `<SERVICE_ID>_URL` ou `<SERVICE_ID>_HOST`:
```
POSTGRES_URL=postgres://postgres:5432/dev
REDIS_URL=redis://redis:6379
MONGO_URL=mongodb://mongo:27017
```

O operador pode customizar via config. Defaults sensatos.

**Validação**: `TestRenderComposeEnv` verifica as URLs de conexão.

### 6.7 Materializar em .ai-launcher/docker-compose.yaml

`ai-launcher generate` (ou implicitamente no `--save` quando há serviços)
materializa o compose em `.ai-launcher/docker-compose.yaml`.

**Validação**: `ai-launcher generate` cria o arquivo.

### 6.8 Comandos compose

Novos subcomandos que delegam para `<runtime> compose`:
- `ai-launcher compose up` — build + up (-d se não-interativo)
- `ai-launcher compose down` — down + remove volumes (-v se flag)
- `ai-launcher compose logs [service]` — logs
- `ai-launcher compose ps` — status

Usa o `Runtime.ComposeCommand()` da Phase 2 (`docker compose` ou
`podman compose`).

**Validação**: Os comandos delegam para o runtime correto.

### 6.9 Compose vs docker run

Quando há serviços de infra selecionados, o launch usa compose
automaticamente. Quando não há serviços, usa `docker run` puro (mais rápido,
sem overhead de compose).

**Validação**: `TestLaunchModeSelection` verifica que serviços → compose,
sem serviços → docker run.

### 6.10 Port exposure do agente no compose

As portas expostas (Phase 5) vão para o serviço do agente no compose:
```yaml
agent:
  ports:
    - "3000:3000"
    - "8080:8080"
```

**Validação**: As portas aparecem no serviço do agente.

## Files to create/modify

- `internal/container/compose.go` (novo) — ComposeFile + RenderCompose
- `internal/container/compose_test.go` (novo) — testes do compose
- `cmd/ai-launcher/main.go` — subcomandos compose up/down/logs/ps
- `internal/tui/tui.go` — preview (`d`) mostra compose YAML quando há serviços

## Design decisions (already made)

- **Compose automático quando há serviços**: o operador não precisa decidir
  entre docker run e compose; a presença de serviços de infra decide.
- **DNS por nome de serviço**: o agente alcança `postgres:5432` (não
  `localhost:5432`) porque está na mesma rede do compose.
- **Host também acessa**: as portas dos serviços são publicadas no host
  (`ports: ["5432:5432"]`), então `localhost:5432` também funciona do host.
- **Volumes nomeados**: persistem entre `compose down` e `compose up` (dados
  não se perdem). `compose down -v` remove.
- **Environment de conexão**: o agente recebe `POSTGRES_URL`, `REDIS_URL`,
  etc. apontando para o nome do serviço no DNS do compose.

## Validation criteria (acceptance gate)

- [ ] `make test` passa
- [ ] `make test-coverage` ≥ 90% (compose.go entra no gate)
- [ ] `make lint-full` — 0 issues
- [ ] `TestRenderCompose` gera YAML válido
- [ ] `TestRenderComposeVolumes` verifica volumes nomeados
- [ ] `TestRenderComposeEnv` verifica URLs de conexão
- [ ] `ai-launcher --service postgres --service redis --dry-run` mostra
      o preview do compose
- [ ] `AI_LAUNCHER_DOCKER_TESTS=1 go test ./internal/container/ -run TestCompose -v`
      sobe agente + postgres + redis e verifica DNS (`postgres:5432` reachable)

## Out of scope

- Clustering/replicação de serviços (single-node)
- Secrets management (senhas hardcoded em env vars por ora)
- TLS entre serviços
- Auto-scaling
- Supabase stack completa (many microservices)
