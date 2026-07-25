# Arquitetura

O README é operacional; o formato longo mora em `docs/`. Esta página é o
resumo operacional de "o que é e qual a forma" para quem vai ler o código.

## Propósito

O ai-launcher é um orquestrador de CLIs de IA. Ele não reimplementa sandbox
nem memória: compõe o `ai-jail` (sandbox de terceiros) e o `ai-memory`
(memória/sessões de terceiros) na cadeia canônica de lançamento, dando ao
usuário uma TUI e uma CLI para declarar agente, permissões, mounts e opções —
e um instalador que baixa as ferramentas gerenciadas direto das releases do
GitHub com checksum obrigatório.

## Fluxo de dados

```
┌──────────┐   flags/config    ┌──────────────────┐
│ TUI/CLI  │──────────────────►│ cmd/ai-launcher  │  parsing + precedência
└──────────┘                   └────────┬─────────┘
                                        │ LaunchConfig (puro, sem I/O)
                                        ▼
                               ┌──────────────────┐
                               │ internal/launcher│  Build(argv) + Validator
                               └────────┬─────────┘
                                        │ argv + env
              ┌─────────────────────────┼─────────────────────────┐
              ▼                         ▼                         ▼
       ┌────────────┐          ┌────────────────┐          ┌────────────┐
       │  ai-jail   │─────────►│ ai-memory run  │─────────►│  harness   │
       │ (sandbox)  │          │ (memória/MCP)  │          │ (claude…)  │
       └────────────┘          └────────────────┘          └────────────┘

       internal/cmd ──► internal/installer ──► GitHub Releases (SHA-256)
       (--install)        (download/verify)         ai-jail, ai-memory, harnesses
```

Os dois fluxos (lançar e instalar) compartilham apenas `internal/config` e
`internal/catalog`. `launcher.Build` é deliberadamente puro: dry-run, testes
de tabela e a TUI consomem exatamente o mesmo argv.

## Mapa de componentes

| Pacote | Responsabilidade |
| --- | --- |
| `cmd/ai-launcher` | Entry point: parsing de flags, precedência (padrões < local < perfil < flags), dispatch (TUI, install, add, perfis), execução final via `exec`/PTY |
| `internal/cmd` | Orquestração fora do lançamento: `--install`/`--upgrade`, wiring de MCP/hooks do ai-memory, `--add`; mantém `install.log` (0600, sem tokens) |
| `internal/config` | Esquema versionado (`2.0`) do config global e local; defaults seguros; perfis; `JailFlags` tri-state; saves atômicos 0600 |
| `internal/catalog` | Resolve agentes contra o PATH (`path` > command > aliases) e normaliza dependências entre permissões |
| `internal/launcher` | `Build` (argv puro), `Validator` (preflight com códigos estáveis), `ConstrainToPlatform` (Windows sem jail), executor PTY e `exec` por plataforma |
| `internal/installer` | Cliente de GitHub Releases: seleção de asset por plataforma, verificação SHA-256 obrigatória, extração de tar.gz/zip, `install-state.json` |
| `internal/tui` | Frontend bubbletea: 5 seções (Agente, Permissões, Mounts, Opções, Perfis); todo estado durável vive em `launcher.LaunchConfig` |
| `test/gherkin` + `test/features` | Suíte de contrato executável (reader Gherkin próprio, sem dependência BDD) sobre a composição do argv e regras de preflight |

## Estado e armazenamento

| Arquivo | Formato | Gravado por |
| --- | --- | --- |
| `~/.config/ai-launch/config.yaml` | YAML (schema `2.0`) | `--add`, `--save-profile`, `--delete-profile`, edição manual |
| `<projeto>/.ai-launch.yaml` | YAML (schema `2.0`) | `--save`, `Ctrl+S` na TUI |
| `~/.config/ai-launch/install-state.json` | JSON | `internal/installer` (tags de release instaladas) |
| `~/.config/ai-launch/install.log` | texto (0600) | `internal/cmd` (nunca registra tokens) |

Todos os saves de config são atômicos (arquivo temporário + `rename`) com
permissão 0600.

## Invariantes transversais

1. **Ordem canônica da cadeia**: `ai-jail [jail flags] ai-memory run [wrapper
   flags] <harness> [args nativos]`. Nenhum caminho de código monta outra
   ordem; a suíte Gherkin trava regressões.
2. **Precedência da seleção**: padrões embutidos < `.ai-launch.yaml` local <
   perfil < flags explícitas. Perfis só substituem os blocos que definem.
3. **Saves atômicos 0600** em config global e local (`internal/config`).
4. **Checksum obrigatório em instalações**: sem checksum verificável a
   instalação falha, salvo `allow_unverified: true` explícito na receita.
5. **Defaults seguros**: `jail` e `memory` omitidos valem `true`; `false`
   explícito é preservado (contrato testado — ver docs/test-strategy.md).
6. **Token só por ambiente**: `AI_MEMORY_AUTH_TOKEN` vai no env do processo
   filho e jamais em logs ou argv.

## Configuração (resumo)

Config global (`~/.config/ai-launch/config.yaml`):

| Chave | Tipo | Propósito |
| --- | --- | --- |
| `version` | string | Schema (`2.0`; aceita `1`/`1.0`) |
| `memory_server_url` | string | Servidor ai-memory (default `https://aimemory.raspberrypi.lan`) |
| `memory_auth_token` | string | Bearer token, repassado só por env |
| `agents[]` | lista | `name`, `command`, `aliases`, `path`, `supports_memory`, `supports_yolo`, `yolo_flag`, `params[]` (`name`/`flag`/`takes_value`), `release` (receita GitHub), `memory` (adapter MCP/hooks), `source_url` |
| `tools[]` | lista | Receitas de ferramentas auxiliares (ai-jail, ai-memory) |
| `permissions[]` | lista | `id`, `name`, `default`, `locked`, `requires` |
| `default_mounts[]` | lista | Mounts sugeridos em novas seleções |
| `profiles{}` | mapa | Snapshots nomeados de seleção (`agent`, `permissions`, `mounts`, `options`) |

Config local (`.ai-launch.yaml`): `agent`, `permissions{}`, `mounts[]`
(`path`/`mode`) e `options`: `jail`, `memory`, `yolo`, `new_workstream`,
`workstream`, `workspace`, `project`, `jail_flags`, `extra_args`,
`param_values`.

`jail_flags` espelha os toggles do ai-jail v1.15 com booleans tri-state
(ausente = default do ai-jail): `lockdown`, `private_home`, `tailscale`,
`gpu`, `landlock`, `seccomp`, `rlimits`, `status_bar`, `browser`
(`hard`/`soft`/`off`), `claude_dir`, `overlay_maps`, `mask`, `deny_paths`,
`allow_tcp_ports`.

## Ordem de leitura

Para um contribuidor novo, nesta sequência:

1. `README.md` — o que o usuário vê.
2. `cmd/ai-launcher/main.go` — flags, precedência e dispatch.
3. `internal/config/config.go` — esquema, defaults e persistência.
4. `internal/launcher/builder.go` — a composição pura do argv (o coração).
5. `internal/catalog/catalog.go` — resolução de agentes e permissões.
6. `internal/tui/tui.go` — como a TUI reusa o mesmo `LaunchConfig`.
7. `internal/cmd/install.go` + `internal/installer/installer.go` — o fluxo de
   instalação e a verificação de checksum.
8. `test/features/launcher.feature` — o contrato executável ponta a ponta.
9. `docs/design-decisions.md` — por que é assim.
