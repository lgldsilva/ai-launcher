# Phase 4 — Catálogo de serviços de infra (~40+ serviços)

## Goal

Modelar ~40 serviços de infraestrutura como dados tipados (bancos, filas,
caches, search, storage, monitoring, auth, workflow, dev tools), prontos para
o gerador de docker-compose (Phase 6). Cada serviço declara imagem, portas,
volumes, environment e healthcheck.

## Prerequisites

- Phase 1 concluída
- `internal/container/stack.go` existe como modelo para `Service` (struct
  similar, mas para infra em vez de toolchain)

## Context for a cold-start agent

O `ai-launcher` roda agentes de IA em containers. O operador frequentemente
precisa de serviços de infraestrutura junto: um banco PostgreSQL, um Redis
para cache, um Kafka para streaming, um MinIO para object storage.

Hoje esses serviços seriam rodados manualmente com `docker run` ou
`docker-compose` separado. A evolução é o ai-launcher **gerenciar** esses
serviços como parte do environment, materializando um `docker-compose.yaml`
(Phase 6) que sobe o agente + os serviços selecionados numa rede compartilhada.

Esta fase cria o **catálogo de dados** — a Phase 6 usa esses dados para gerar
o compose. Esta fase NÃO gera compose nem roda containers; só modela os dados.

O projeto exige Conventional Commits, coverage 90% em `internal/container`.

## Requirements

### 4.1 Service struct

Criar `internal/container/services.go` com:

```go
type Service struct {
    ID          string            // "postgres", "mongo"
    Name        string            // "PostgreSQL"
    Category    ServiceCategory   // DatabaseSQL, DatabaseNoSQL, etc.
    Image       string            // "postgres:18"
    Ports       []PortMapping     // [{Internal: 5432, Host: 5432}]
    Volumes     []string          // ["pg-data:/var/lib/postgresql/data"]
    Environment map[string]string // {"POSTGRES_PASSWORD": "dev"}
    DependsOn   []string          // serviços que devem subir antes
    Healthcheck string            // "pg_isready -U postgres"
    Description string            // "PostgreSQL 18 — SQL database"
}
```

### 4.2 ServiceCategory

```go
type ServiceCategory string
const (
    CategoryDatabaseSQL     ServiceCategory = "Databases (SQL)"
    CategoryDatabaseNoSQL   ServiceCategory = "Databases (NoSQL)"
    CategoryDatabaseVector  ServiceCategory = "Databases (Vector)"
    CategoryDatabaseTS      ServiceCategory = "Databases (Time-series)"
    CategorySearch          ServiceCategory = "Search"
    CategoryQueue           ServiceCategory = "Message Queues"
    CategoryCache           ServiceCategory = "Caches"
    CategoryStorage         ServiceCategory = "Object Storage"
    CategoryMonitoring      ServiceCategory = "Monitoring"
    CategoryAuth            ServiceCategory = "Identity / Auth"
    CategoryWorkflow        ServiceCategory = "Workflow / Orchestration"
    CategoryDevTools        ServiceCategory = "Dev Tools"
)
```

### 4.3 Catálogo completo (~40+ serviços)

Cada serviço abaixo declara: image (version-pinned), portas, volume nomeado,
env mínimo. Baseado em catálogos de referência como
`guidomantilla/docker-compose-services` (26 stacks) e
`Romanow/docker-compose-examples`.

**Databases (SQL):**

| ID | Image | Ports | Volume |
|---|---|---|---|
| postgres | postgres:18 | 5432 | pg-data |
| mysql | mysql:8 | 3306 | mysql-data |
| mariadb | mariadb:11 | 3306 | mariadb-data |
| oracle | gvenzl/oracle-free:23-slim | 1521 | oracle-data |
| sqlserver | mcr.microsoft.com/mssql/server:2022-CU16-ubuntu-22.04 | 1433 | mssql-data |

**Databases (NoSQL):**

| ID | Image | Ports | Volume |
|---|---|---|---|
| mongo | mongo:8 | 27017 | mongo-data |
| cassandra | cassandra:5 | 9042 | cassandra-data |
| neo4j | neo4j:5 | 7474,7687 | neo4j-data |
| dynamodb | amazon/dynamodb-local:2.6.0 | 8000 | dynamodb-data |

**Databases (Vector):**

| ID | Image | Ports | Volume |
|---|---|---|---|
| qdrant | qdrant/qdrant:v1.13 | 6333,6334 | qdrant-data |
| weaviate | semitechnologies/weaviate:1.28 | 8080 | weaviate-data |
| milvus | milvusdb/milvus:v2.4 | 19530 | milvus-data |
| chroma | chromadb/chroma:0.6.3 | 8000 | chroma-data |
| pgvector | pgvector/pgvector:pg16 | 5432 | pgvector-data |

**Databases (Time-series):**

| ID | Image | Ports | Volume |
|---|---|---|---|
| influxdb | influxdb:2 | 8086 | influxdb-data |
| timescaledb | timescale/timescaledb:pg16 | 5432 | tsdb-data |
| clickhouse | clickhouse/clickhouse-server:25.3 | 8123,9000 | ch-data |

**Search:**

| ID | Image | Ports | Volume |
|---|---|---|---|
| elasticsearch | elasticsearch:8 | 9200 | es-data |
| opensearch | opensearchproject/opensearch:2 | 9200 | os-data |
| meilisearch | getmeili/meilisearch:v1.12 | 7700 | ms-data |
| typesense | typesense/typesense:0.25 | 8108 | ts-data |

**Message Queues:**

| ID | Image | Ports | Volume |
|---|---|---|---|
| kafka | bitnami/kafka:3.7 | 9092 | kafka-data |
| redpanda | redpandadata/redpanda:v25 | 19092,8080 | redpanda-data |
| rabbitmq | rabbitmq:3.13-management | 5672,15672 | rabbit-data |
| nats | nats:2 | 4222,8222 | nats-data |
| activemq | apache/activemq-classic:5.18 | 61616,8161 | amq-data |
| pulsar | apachepulsar/pulsar:3.3 | 6650,8080 | pulsar-data |

**Caches:**

| ID | Image | Ports | Volume |
|---|---|---|---|
| redis | redis:7 | 6379 | redis-data |
| valkey | valkey/valkey:8 | 6379 | valkey-data |
| dragonfly | docker.dragonflydb.io/dragonflydb/dragonfly:v1.34.1 | 6379 | dragonfly-data |

**Object Storage:**

| ID | Image | Ports | Volume |
|---|---|---|---|
| minio | minio/minio:RELEASE.2025-02-28T09-55-16Z | 9000,9001 | minio-data |
| localstack | localstack/localstack:4 | 4566 | ls-data |

**Monitoring:**

| ID | Image | Ports | Volume |
|---|---|---|---|
| prometheus | prom/prometheus:v3.4.2 | 9090 | prom-data |
| grafana | grafana/grafana:11.6.0 | 3000 | grafana-data |
| jaeger | jaegertracing/all-in-one:1.69.0 | 16686,4317 | jaeger-data |
| loki | grafana/loki:3.4.2 | 3100 | loki-data |
| otel-collector | otel/opentelemetry-collector:0.127.0 | 4317,4318 | — |

**Identity / Auth:**

| ID | Image | Ports | Volume |
|---|---|---|---|
| keycloak | quay.io/keycloak/keycloak:26 | 8080 | keycloak-data |
| authentik | ghcr.io/goauthentik/server:2025.4.1 | 9000,9443 | authentik-data |

**Workflow:**

| ID | Image | Ports | Volume |
|---|---|---|---|
| airflow | apache/airflow:3 | 8080 | airflow-data |
| temporal | temporalio/auto-setup:1.27.2 | 7233,8233 | temporal-data |
| n8n | n8nio/n8n:1.95.3 | 5678 | n8n-data |

**Dev Tools:**

| ID | Image | Ports | Volume |
|---|---|---|---|
| mailpit | axllent/mailpit:v1.20.5 | 1025,8025 | mailpit-data |
| wiremock | wiremock/wiremock:3.13.2 | 8080 | wiremock-data |
| code-server | codercom/code-server:4.131.0-39 | 8080 | code-server-config,code-server-data |
| nginx | nginx:1-alpine | 80 | — |
| traefik | traefik:v3 | 80,8080 | — |
| caddy | caddy:2 | 80,443 | — |

`code-server` is a browser-based developer service. It receives the selected
project at `/home/coder/project`, keeps its configuration and extensions in
the project `.ai-launcher/data/code-server` tree, and uses the image's default
password authentication. Neovim is intentionally modeled as a selectable
container stack rather than an infrastructure service; its XDG config, data,
state, and cache directories are resolved separately for Linux, macOS, and
Windows.

### 4.4 Funções de acesso

- `Services` — slice com todos os serviços (ordem por categoria)
- `ServicesByCategory()` — agrupa por categoria para a TUI
- `ServiceByID(id string) (Service, bool)`
- `ValidServiceIDs(ids []string) ([]string, error)` — molde `ValidStackIDs`

**Validação**: Testes unitários verificam que todos os IDs são únicos e que
`ValidServiceIDs` rejeita IDs desconhecidos.

### 4.5 Port mapping

```go
type PortMapping struct {
    Internal int    // porta dentro do container
    Host     int    // porta no host (default: mesma do internal)
    Protocol string // "tcp" (default), "udp"
}
```

O `Host` defaulta para `Internal`. O operador pode remapear (ex: postgres
em 15432 no host para não conflitar com postgres local).

### 4.6 CLI flag

`--service <id>` (repeatable) adiciona serviços à seleção.

**Validação**: `ai-launcher --service mongo --service redis --dry-run` mostra
os serviços na seleção.

### 4.7 Config

`options.services: [mongo, redis]` no `.ai-launcher/config.yaml`.
Service host ports can be overridden with `options.container_service_ports`:
`postgres: [{host: 15432, internal: 5432}]`. The CLI form is repeatable:
`--service-port postgres=15432:5432`.

### 4.8 TUI: seção Services

Nova seção na TUI (aparece quando docker ativo), com checkboxes agrupados por
categoria. `Space` adiciona/remove. Mostra portas e volumes de cada serviço.

**Validação**: `TestServicesView` renderiza as categorias e serviços; Enter
edita os published ports do serviço destacado.

## Files to create/modify

- `internal/container/services.go` (novo) — catálogo + funções de acesso
- `internal/container/services_test.go` (novo) — testes do catálogo
- `internal/config/config.go` — `Options.Services []string`
- `cmd/ai-launcher/main.go` — flag `--service`; propagação no LaunchConfig
- `internal/tui/tui.go` — seção Services
- `internal/tui/services_test.go` (novo) — testes da view

## Design decisions (already made)

- **Version-pinned**: cada imagem tem tag específica (não `latest`) para
  determinismo. Atualizações são deliberadas.
- **Portas sem conflito**: cada serviço usa sua porta default; o operador
  pode remapear via config.
- **Volumes nomeados**: `mongo-data`, `pg-data`, etc. — persistência entre
  compose up/down.
- **Categories**: agrupamento lógico para a TUI; reflete como o operador pensa.

## Validation criteria (acceptance gate)

- [x] `make test` passa
- [x] `make test-coverage` ≥ 90% (services.go entra no gate)
- [x] `make lint-full` — 0 issues
- [x] `len(Services) >= 40` (catálogo cobre todas as categorias)
- [x] Todos os IDs são únicos (`TestServiceIDsUnique`)
- [x] `ValidServiceIDs` rejeita IDs desconhecidos
- [x] `ai-launcher --service mongo --dry-run` mostra o serviço

## Out of scope

- Gerar docker-compose.yaml (Phase 6)
- Rodar os containers de serviço (Phase 6)
- Configuração custom de cada serviço (env vars avançadas, replicas, etc.)
- Supabase stack completa (many microservices)
