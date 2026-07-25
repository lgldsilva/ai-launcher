# ai-launcher

Launcher TUI/CLI que orquestra CLIs de IA (Claude Code, Codex, Kimi, Crush, OpenCode, Gemini etc.) através de sandbox e camada de memória (Go, bubbletea, ai-jail + ai-memory).

> O ai-launcher é um "super facilitador": ele não reimplementa nada. Compõe duas
> ferramentas de terceiros — [`ai-jail`](https://github.com/akitaonrails/ai-jail)
> (wrapper de sandbox: bubblewrap no Linux, sandbox-exec no macOS) e
> [`ai-memory`](https://github.com/akitaonrails/ai-memory) (servidor MCP + hooks +
> workstreams gerenciadas) — e monta o comando canônico de lançamento com
> permissões, mounts e opções declaradas por você.

## Como funciona

```
┌──────────────┐     ┌─────────────┐     ┌──────────────┐     ┌────────────────┐     ┌──────────────────┐
│ usuário      │────►│ ai-launcher │────►│ ai-jail      │────►│ ai-memory run  │────►│ harness          │
│ (TUI ou CLI) │     │ (orquestra) │     │ (sandbox)    │     │ (memória)      │     │ (claude, codex…) │
└──────────────┘     └─────────────┘     └──────────────┘     └────────────────┘     └──────────────────┘
                          │                                          │
                          ▼                                          ▼
              ~/.config/ai-launch/                     servidor ai-memory
              config.yaml · profiles                   (AI_MEMORY_SERVER_URL)
```

A cadeia canônica de lançamento é sempre:

```
ai-jail [jail flags] ai-memory run [wrapper flags] <harness> [args nativos]
```

Cada camada é opcional (exceto o harness): sem jail o comando começa em
`ai-memory run`; sem memória o harness é executado diretamente. No Windows o
ai-jail não existe — o launcher desativa o sandbox com aviso e executa o resto.

## Instalação

Pré-requisito: Go 1.24+ (definido em `go.mod`).

### go install

```bash
go install github.com/lgldsilva/ai-launcher/cmd/ai-launcher@latest
```

### A partir do código-fonte

```bash
git clone https://github.com/lgldsilva/ai-launcher.git
cd ai-launcher
make build          # gera bin/ai-launcher
make build-release  # binários Linux/macOS/Windows (amd64/arm64) em dist/
```

### Ferramentas gerenciadas (ai-jail, ai-memory, harnesses)

O launcher instala as ferramentas que ele orquestra a partir das releases do
GitHub, com verificação de checksum SHA-256 obrigatória:

```bash
ai-launcher --install                     # tudo que tem receita no catálogo
ai-launcher --install --agent "Kilo Code" # apenas um alvo
ai-launcher --upgrade                     # força reinstalação pela release mais recente
```

No Windows o ai-jail é pulado automaticamente e o ai-memory é instalado pelo
asset nativo `ai-memory-windows-x86_64.zip`. O estado das instalações fica em
`~/.config/ai-launch/install-state.json` e o log em `install.log` (mesmo
diretório, sem tokens).

## Uso

Sem argumentos, `ai-launcher` abre a TUI interativa. Com qualquer flag, opera
em modo CLI e executa de verdade (não é um gerador de comando — use `--dry-run`
para apenas imprimir o argv).

### Comandos e modos

| Comando | Efeito |
| --- | --- |
| `ai-launcher` | Abre a TUI (agentes, permissões, mounts, opções, perfis) |
| `ai-launcher --agent claude [flags]` | Monta o argv e executa o harness |
| `ai-launcher --continue` | Retoma a sessão mais recente do checkout (`ai-memory run` sem harness) |
| `ai-launcher --install [--agent X]` / `--upgrade` | Instala/atualiza ferramentas via releases do GitHub |
| `ai-launcher --add Nome --path /caminho [--command cmd] [--description txt]` | Adiciona/atualiza um harness no catálogo global |
| `ai-launcher --list-profiles` / `--delete-profile N` | Lista ou remove perfis salvos no config global |
| `ai-launcher --save-profile N [flags]` | Salva a seleção mesclada como perfil e sai sem executar |
| `ai-launcher --save` (ou `--save-only`) | Grava a seleção em `.ai-launch.yaml` local e sai |
| `ai-launcher help` | Mostra o uso das flags |

### Opções

| Flag | Efeito |
| --- | --- |
| `--agent <cmd>` | Seleciona o agente (claude, codex, opencode, kimi, …) |
| `--ssh` / `--gh` / `--docker` / `--gpu` | Habilita permissões dentro do jail (gpu exige docker) |
| `--no-jail` / `--sandbox` | Desativa / ativa explicitamente o ai-jail |
| `--memory` / `--no-memory` | Ativa / desativa a camada ai-memory |
| `--yolo` / `--no-yolo` | Passa (ou não) a flag de modo perigoso do agente |
| `--new <nome>` / `--workstream <nome>` | Cria / retoma uma workstream do ai-memory |
| `--workspace <nome>` / `--project <nome>` | Escopo repassado ao `ai-memory run` |
| `--continue` | Continua a última sessão do ai-memory deste checkout |
| `--mount <path>[:ro\|:rw]` / `--map` | Mount somente-leitura (padrão ro; `--map` é alias) |
| `--rw-map <path>` | Mount de leitura e escrita |
| `--param <nome=valor>` | Define parâmetro declarado no catálogo do agente (repetível) |
| `--extra-args "<args>"` / `--args` | Argumentos extras repassados ao harness (`--args` é alias) |
| `--profile <nome>` | Carrega um perfil salvo como base da seleção |
| `--config <path>` / `--local-config <path>` | Caminhos alternativos do config global / local |
| `--dry-run` | Imprime o comando gerado sem executar |
| `--save`, `--save-only`, `--save-profile`, `--list-profiles`, `--delete-profile`, `--install`, `--upgrade`, `--add`, `--path`, `--command`, `--description` | Ver tabela de comandos acima |

Precedência da seleção final: **padrões embutidos < `.ai-launch.yaml` local <
perfil (`--profile`) < flags explícitas**.

### Exemplos

```bash
# Executa o Claude Code com sandbox e memória (padrões seguros)
ai-launcher --agent claude

# Só imprime o comando que seria executado
ai-launcher --agent claude --ssh --docker --dry-run

# Montagens personalizadas e modo perigoso
ai-launcher --agent pi --map /host/data --rw-map /workspace --yolo

# Workstream nomeada do ai-memory com escopo de projeto
ai-launcher --agent claude --new release-check --project meu-app

# Parâmetro declarado no catálogo (o Kimi declara "query" e "model")
ai-launcher --agent kimi --param query="refatore o módulo auth" --param model=k2

# Salva a seleção mesclada como perfil reutilizável
ai-launcher --agent claude --ssh --param model=opus --save-profile review

# Depois: carrega o perfil e sobrescreve só o que precisar
ai-launcher --profile review --docker

# Adiciona um harness sem recompilar o launcher
ai-launcher --add "Meu Harness" --path /opt/tools/runner --command runner
```

### TUI

`ai-launcher` sem argumentos abre a TUI com cinco seções: **Agente** (a
primeira linha é sempre "Continuar última sessão"), **Permissões**, **Mounts**,
**Opções** (toggles fixos + uma linha por parâmetro declarado pelo agente) e
**Perfis** (seção 5, visível quando existe ao menos um perfil salvo).

| Tecla | Ação |
| --- | --- |
| `Tab` / `Shift+Tab`, `1`–`5` | Alternar / saltar entre seções |
| `↑/↓` ou `j/k` | Navegar na seção atual |
| `Space` | Alternar permissão/opção, modo do mount ou carregar perfil |
| `/` | Abrir o navegador de mounts (`→/l` entra, `←/h` sobe, `Tab` alterna ro/rw) |
| `Backspace` | Remover o mount selecionado |
| `Enter` | Selecionar agente, editar parâmetro ou executar |
| `d` / `Ctrl+D` | Dry-run (mostra o comando sem sair da TUI) |
| `Ctrl+S` | Salvar a seleção em `.ai-launch.yaml` |
| `Ctrl+P` | Salvar a seleção como perfil nomeado no config global |
| `?` | Ajuda |
| `q` / `Esc` / `Ctrl+C` | Sair |

No Windows o toggle de Jail e as permissões que dependem dele (ssh, gh,
docker, gpu) nem aparecem na TUI.

## Matriz de suporte

| Plataforma | ai-jail (sandbox) | ai-memory | Harnesses |
| --- | --- | --- | --- |
| Linux (amd64) | Sim (bubblewrap) | Sim | Sim, sandboxed |
| Linux (arm64) | Sem asset oficial do ai-jail | Sim | Sim, sandbox depende do ai-jail |
| macOS (arm64) | Sim (sandbox-exec) | Sim | Sim, sandboxed |
| macOS (amd64) | Sem asset oficial do ai-jail | Sim | Sim, sandbox depende do ai-jail |
| Windows | **Não** — o caminho é WSL2 | Sim (`ai-memory-windows-x86_64.zip`) | Sim, **sem sandbox** |

O ai-jail não tem suporte a Windows ("probably never", segundo o autor); no
Windows o launcher desativa o jail com aviso explícito e remove as permissões
que dependem dele. Para rodar com sandbox a partir do Windows, use WSL2.

## Configuração

| Arquivo | Escopo | Conteúdo |
| --- | --- | --- |
| `~/.config/ai-launch/config.yaml` | Global (máquina) | Catálogo de `agents`, `tools`, `permissions`, `default_mounts`, `profiles`, `memory_server_url`, `memory_auth_token` |
| `<projeto>/.ai-launch.yaml` | Workspace | Seleção: `agent`, `permissions`, `mounts`, `options` (inclui `jail_flags`, `param_values`, `extra_args`) |
| `~/.config/ai-launch/install-state.json` | Global | Tags de release já instaladas |
| `~/.config/ai-launch/install.log` | Global | Log das instalações (0600, sem tokens) |

O esquema completo (campos, defaults, `jail_flags` do ai-jail v1.15) está em
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Segurança

Camadas de defesa:

- Sandbox via ai-jail (bubblewrap no Linux, sandbox-exec no macOS) com mounts
  somente-leitura por padrão e permissões (ssh, gh, docker, gpu) desligadas por
  padrão.
- Instalações sempre verificadas por checksum SHA-256 (`.sha256`,
  `.sha256sum`, `checksums.txt`, `SHA256SUMS` ou corpo da release);
  `allow_unverified: true` existe, mas é escolha explícita do operador.
- `memory_auth_token` só é repassado por variável de ambiente
  (`AI_MEMORY_AUTH_TOKEN`) ao processo filho e **nunca é escrito no
  `install.log`**.
- Configs gravados atomicamente com permissão 0600.

O que **NÃO** protege:

- **Variáveis de ambiente são visíveis dentro do jail** (exceto em modo
  lockdown do ai-jail). Não confie segredos ao ambiente de uma sessão sandboxed.
- **Windows roda SEM sandbox** — o harness executa com todos os privilégios do
  usuário. Para workloads hostis no Windows, use WSL2 ou uma VM descartável.
- **Sandbox não é VM.** Bubblewrap/sandbox-exec compartilham o kernel do host;
  workloads hostis ou não confiáveis pedem uma VM descartável, não um jail.
- Não substitui a higiene de tokens: quem tem acesso ao config global lê
  `memory_auth_token`.

## Documentação

| Documento | Conteúdo |
| --- | --- |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | Forma do sistema: fluxo de dados, mapa de pacotes, estado, invariantes |
| [docs/design-decisions.md](docs/design-decisions.md) | Decisões temáticas, trade-offs e o que não estamos fazendo |
| [docs/cicd.md](docs/cicd.md) | Pipeline GitHub Actions, Sonar CE local e processo de release |
| [docs/test-strategy.md](docs/test-strategy.md) | Pirâmide de testes, gate de cobertura e suíte de contrato Gherkin |
| [AGENTS.md](AGENTS.md) | Instruções para agentes de IA trabalhando neste repositório |

## Roadmap

- [x] TUI interativa com execução real (Enter lança o harness)
- [x] Perfis nomeados e parâmetros declarados por harness
- [x] Instalador com checksum SHA-256 obrigatório
- [x] Windows como cidadão de primeira classe sem jail
- [x] Suíte de contrato Gherkin contra drift do ai-jail/ai-memory
- [ ] Workflow de release automatizado (`release.yml`) — hoje o release é local
- [ ] Sandbox nativo no Windows (depende de upstream; improvável)
- [ ] GUI (não planejada; TUI é a interface)

## Licença

MIT — veja [LICENSE](LICENSE).
