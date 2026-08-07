# Phase 5 — Limites de recursos + portas expostas + rede

## Goal

O container do agente pode ser configurado com limites de memória, CPU, PIDs;
portas expostas do container para o host; e seleção de rede (bridge, host,
rede customizada). Isso atende o caso de uso "tenho um dev server rodando
dentro do container na porta 3000 e preciso acessá-lo do host".

## Prerequisites

- Phase 1 concluída
- Phase 2 concluída (runtime abstraction — `--network` syntax varia por runtime)
- `internal/container/run.go` com `RunConfig` e `BuildRunCommand`

## Context for a cold-start agent

Hoje o `ai-launcher` gera `docker run --rm -it --user UID:GID -e HOME=... -w
... -v ... <image> <agent>`. Não há limites de recursos nem portas expostas.
O operador pode precisar de:

- **Memória/CPU limitados**: evitar que o agente (ou um build dentro do
  container) consuma todo o host
- **Portas expostas**: um dev server roda dentro do container (ex: `npm run
  dev` na porta 3000) e o host precisa acessar `localhost:3000`
- **Rede customizada**: alcançar containers em outra rede docker, ou usar
  `--network host` para VPN/redis local

O projeto exige Conventional Commits, coverage 90% em `internal/container`.

## Requirements

### 5.1 RunConfig ganha campos de recurso

```go
type RunConfig struct {
    // ... campos existentes ...
    MemoryLimit  string        // "4g", "512m" → --memory
    CPULimit     string        // "2.0", "2" → --cpus
    PIDsLimit    int64         // 512 → --pids-limit
    ExposedPorts []PortMapping // [{Host: 3000, Internal: 3000}] → -p
    NetworkName  string        // "host", "my-net" → --network
}
```

**Validação**: O struct tem os novos campos.

### 5.2 PortMapping struct

```go
type PortMapping struct {
    Host     int
    Internal int
    Protocol string // "tcp" (default), "udp"
}
```

Renderizado como `-p 3000:3000/tcp` (ou `-p 3000:3000` quando tcp).

**Validação**: Teste que `PortMapping{3000, 3000, "tcp"}.DockerFlag()` retorna
`"3000:3000"`.

### 5.3 BuildRunCommand emite os flags

- `MemoryLimit != ""` → `--memory <value>`
- `CPULimit != ""` → `--cpus <value>`
- `PIDsLimit > 0` → `--pids-limit <value>`
- `len(ExposedPorts) > 0` → `-p <host>:<internal>/<proto>` para cada
- `NetworkName != ""` → `--network <value>`

Flags emitidos ANTES da imagem (são flags do `docker run`, não args do agente).

**Validação**: `TestBuildRunCommandWithResources` verifica cada flag no argv.

### 5.4 Config options

```yaml
options:
  container_memory: "4g"
  container_cpus: "2.0"
  container_pids: 512
  container_ports:
    - host: 3000
      internal: 3000
    - host: 8080
      internal: 8080
  container_network: "bridge"
```

**Validação**: `LoadLocal` lê os campos; `SaveLocal` os escreve.

### 5.5 CLI flags

```
--memory 4g           # limite de memória
--cpus 2.0            # limite de CPU
--pids 512            # limite de PIDs
--publish 3000:3000   # porta exposta (repeatable, formato host:internal)
--publish 8080        # porta exposta (host == internal)
--network host        # nome da rede
```

**Validação**: `ai-launcher --memory 2g --publish 3000:3000 --dry-run` mostra
os flags no argv.

### 5.6 TUI: campos editáveis na seção Container

Na seção Container, após os stacks e shared config, campos editáveis:
- `Memory: 4g` (Enter para editar)
- `CPU: 2.0` (Enter para editar)
- `Ports: 3000:3000, 8080:8080` (Enter para editar — lista CSV)
- `Network: bridge` (Enter para editar)

**Validação**: `TestContainerView` inclui os campos de recurso.

### 5.7 Validação

- Formato de memória: sufixos válidos (`b`, `k`, `m`, `g`) — erro claro se inválido
- Portas: 1-65535; aviso se porta já em uso (best-effort, não bloqueia)
- CPU: número positivo

**Validação**: Teste que formato inválido de memória retorna erro.

## Files to create/modify

- `internal/container/run.go` — RunConfig + BuildRunCommand + PortMapping
- `internal/container/run_test.go` — testes de recursos
- `internal/config/config.go` — Options com campos de recurso
- `cmd/ai-launcher/main.go` — flags CLI + propagação
- `internal/tui/tui.go` — campos editáveis na seção Container
- `internal/tui/container_test.go` — teste da view com recursos

## Design decisions (already made)

- **Default sem limites**: se não configurado, o container roda sem limites
  (mesmo comportamento de hoje). Limites são opt-in.
- **`--network host`**: suportado mas documentado como risco (container
  compartilha a rede do host). No Docker Desktop (macOS/Windows) tem caveats.
- **Portas expostas**: o formato `--publish 3000:3000` segue a convenção docker.
  `--publish 8080` sem `:` significa `8080:8080`.

## Validation criteria (acceptance gate)

- [ ] `make test` passa
- [ ] `make test-coverage` ≥ 90%
- [ ] `make lint-full` — 0 issues
- [ ] `make sec-static` — 0 issues
- [ ] `TestBuildRunCommandWithResources` valida todos os flags
- [ ] `ai-launcher --memory 2g --publish 3000:3000 --dry-run` mostra os flags
- [ ] TUI mostra os campos de recurso

## Out of scope

- docker-compose com recursos por serviço (Phase 6 aplica `mem_limit`/`cpus`
  no serviço do agente do compose)
- GPU passthrough (já existe como permissão `--gpu` no modo jail)
- Limites de I/O (`--device-read-bps`, etc.)
- Detecção automática de portas em uso no host
