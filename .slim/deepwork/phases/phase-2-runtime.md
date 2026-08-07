# Phase 2 — Abstração de runtime (docker / podman / nerdctl)

## Goal

O container backend não assume mais "docker" hardcoded. Suporta múltiplos
runtimes OCI (docker, podman, nerdctl) com auto-detecção e configuração
explícita. O operador pode usar podman (rootless, daemonless) em vez de docker.

## Prerequisites

- Phase 1 concluída (config dirs validados empiricamente)
- `internal/container/run.go` com `BuildRunCommand` hardcoded em "docker"
- `internal/container/build.go` com `EnsureImage` hardcoded em "docker"
- `cmd/ai-launcher/main.go` com `dockerRunner` hardcoded em "docker"

## Context for a cold-start agent

O `ai-launcher` gera argv `docker run ...` para lançar agentes em containers.
Atualmente o binário "docker" é hardcoded em todos os pontos de emissão de
argv e de execução. O operador pode preferir:

- **Docker**: daemon-based, padrão atual, Docker Desktop (macOS/Windows)
- **Podman**: daemonless, rootless por padrão, CLI quase idêntica ao docker.
  Host gateway é `host.containers.internal` (não `host.docker.internal`).
  Compose via `podman compose` (built-in no podman 4+).
- **nerdctl**: CLI sobre containerd, compatível com docker CLI. Usado em k8s
  nodes e ambientes minimalistas.

As diferenças-chave entre runtimes que afetam o ai-launcher:

| Aspecto | Docker | Podman | nerdctl |
|---|---|---|---|
| CLI binary | `docker` | `podman` | `nerdctl` |
| Host gateway name | `host.docker.internal` | `host.containers.internal` | `host.docker.internal` |
| `--add-host` syntax | `host.docker.internal:host-gateway` | `host.containers.internal:host-gateway` | igual ao docker |
| Socket | `/var/run/docker.sock` | rootless: sem socket; root: `/run/podman/podman.sock` | `/run/containerd/containerd.sock` |
| Compose | `docker compose` | `podman compose` | `nerdctl compose` |
| Preflight | `docker info` | `podman info` | `nerdctl info` |

O projeto exige Conventional Commits, coverage 90% em `internal/container`.

## Requirements

### 2.1 Runtime interface

Criar `internal/container/runtime.go` com:

```go
type Runtime interface {
    Name() string           // "docker", "podman", "nerdctl"
    Command() string        // o binário CLI
    HostGateway() string    // "host.docker.internal" ou "host.containers.internal"
    ComposeCommand() []string // ["docker","compose"] ou ["podman","compose"]
    SocketPath() string     // path do socket de controle
    Info() error            // preflight: runtime info (daemon vivo?)
}
```

**Validação**: `go vet ./internal/container/` passa; a interface tem todos os métodos.

### 2.2 Implementações

Três implementações concretas:

- `DockerRuntime`: `Command()="docker"`, `HostGateway()="host.docker.internal"`, `SocketPath()="/var/run/docker.sock"`
- `PodmanRuntime`: `Command()="podman"`, `HostGateway()="host.containers.internal"`, `SocketPath()=""` (rootless não tem socket)
- `NerdctlRuntime`: `Command()="nerdctl"`, `HostGateway()="host.docker.internal"`, `SocketPath()="/run/containerd/containerd.sock"`

Cada uma implementa `Info()` chamando `<command> info` e retornando erro se o exit code não for 0.

**Validação**: Testes unitários verificam os valores de cada implementação.

### 2.3 Auto-detecção

`DetectRuntime()` testa `exec.LookPath` para cada runtime em ordem de
preferência: docker → podman → nerdctl. Retorna o primeiro encontrado.
Aceita uma preferência explícita (`"podman"`) para pular a auto-detecção.

**Validação**: Teste com `LookPath` stub mostra a prioridade correta.

### 2.4 Refatorar BuildRunCommand

`BuildRunCommand` recebe um `Runtime` (ou lê de `RunConfig.Runtime`) e usa
`runtime.Command()` em vez de hardcoded `"docker"`. O argv passa a ser
`[<runtime>, "run", ...]`.

**Validação**: `TestBuildRunCommandMinimal` com podman gera `["podman", "run", ...]`.

### 2.5 Refatorar --add-host

O `--add-host` usa `runtime.HostGateway()` em vez de fixo
`host.docker.internal:host-gateway`. A constante `hostGatewayArg` vira
derivada do runtime.

**Validação**: Teste com podman gera `host.containers.internal:host-gateway`.

### 2.6 Refatorar EnsureImage

`EnsureImage` e `dockerRunner` no CLI usam `runtime.Command()` em vez de
hardcoded `"docker"`. O `docker image inspect` vira `<runtime> image inspect`.

**Validação**: Teste de `EnsureImage` com runtime mock usa o comando certo.

### 2.7 Config e flag CLI

- Config: `options.container_runtime: auto|docker|podman|nerdctl` (default: `auto`)
- Flag CLI: `--container-runtime <name>`
- Persistência: save/profile propagam o campo

**Validação**: `ai-launcher --container-runtime podman --dry-run` gera argv com `podman`.

### 2.8 TUI mostra runtime detectado

Na seção Container (ou Options), uma linha mostra o runtime ativo:
`Runtime: docker (auto-detected)` ou `Runtime: podman (--container-runtime)`.

**Validação**: `TestContainerView` inclui o runtime na view.

### 2.9 Overlay rewrite usa runtime gateway

O `RewriteLocalhost` continua reescrevendo para `host.docker.internal`, mas
o overlay mount e o `-e` passam a usar o nome correto do runtime. Ou seja:
o `RewriteLocalhost` recebe o nome do gateway como parâmetro.

**Validação**: Teste de overlay com podman gera `host.containers.internal`.

## Files to create/modify

- `internal/container/runtime.go` (novo) — interface + 3 implementações + DetectRuntime
- `internal/container/runtime_test.go` (novo) — testes unitários
- `internal/container/run.go` — usar Runtime em vez de hardcoded "docker"
- `internal/container/run_test.go` — atualizar para suportar runtime
- `internal/container/build.go` — EnsureImage usa Runtime
- `internal/container/build_test.go` — atualizar
- `internal/container/overlay.go` — RewriteLocalhost parametrizado
- `internal/container/rewrite.go` — aceita gateway name
- `cmd/ai-launcher/main.go` — dockerRunner usa Runtime; flag --container-runtime
- `internal/config/config.go` — Options.ContainerRuntime
- `internal/tui/tui.go` — mostrar runtime na seção Container

## Design decisions (already made)

- **Priority order**: docker → podman → nerdctl (docker é o mais comum;
  podman é o principal alternativo; nerdctl é niche).
- **`auto` default**: detecta automaticamente; o operador pode forçar com a flag.
- **Socket**: o `--docker` permission (montar o socket) só faz sentido para
  docker; para podman, o equivalente é o socket rootless (se existir). Por ora
  a permissão docker monta o socket do runtime detectado.

## Validation criteria (acceptance gate)

- [ ] `make test` passa
- [ ] `make test-coverage` ≥ 90%
- [ ] `make lint-full` — 0 issues
- [ ] `make sec-static` — 0 issues
- [ ] Teste `TestRuntimeDetect` valida a prioridade
- [ ] Teste `TestBuildRunCommandPodman` gera argv com `podman` e `host.containers.internal`
- [ ] `ai-launcher --container-runtime podman --dry-run` mostra `podman` no argv
- [ ] TUI mostra o runtime detectado

## Out of scope

- Implementar compose multi-runtime (Phase 6)
- Suporte a rootless podman especificamente (documentar como limitação)
- Buildkit/buildah como alternativa ao `docker build`
