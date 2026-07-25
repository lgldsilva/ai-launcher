# ai-launcher

Projeto de um launcher TUI para agentes de IA (Claude Code, Codex, OpenCode, pi, etc.) baseado no plano do Oracle.

## Visão geral

Este projeto implementa um launcher interativo de interface de usuário por teclado (TUI) que permite aos usuários selecionar agentes de IA, configurar permissões e montagens, e executar comandos de forma segura usando `ai-jail` e `ai-memory`.

## Arquitetura

### Configuração global (`~/.config/ai-launch/config.yaml`)

Catálogo de agentes e permissões disponíveis:

```yaml
version: "2.0"

agents:
  - name: "Claude Code"
    command: "claude"
    supports_memory: true
    supports_yolo: true
    description: "Anthropic's Claude Code"
    memory:
      client: "claude-code"
      agent: "claude-code"
      install_mcp: true
      install_hooks: true

  - name: "Codex"
    command: "codex"
    aliases: ["codex-cli"]
    supports_memory: false
    supports_yolo: false
    description: "OpenAI's Codex CLI"

  # Receita opcional para instalar/atualizar pelo GitHub.
  - name: "Meu Harness"
    command: "meu-harness"
    release:
      repository: "acme/meu-harness"
      assets:
        linux-amd64: "meu-harness-linux-amd64.tar.gz"
        linux-arm64: "meu-harness-linux-arm64.tar.gz"
        darwin-amd64: "meu-harness-darwin-amd64.zip"
        darwin-arm64: "meu-harness-darwin-arm64.zip"
      binary: "meu-harness"
      checksum_asset: "checksums.txt"

  # O catálogo padrão também inclui aliases/variantes de:
  # kimi/kimi-cli, kilo/kilocode, mimo/mimocode, agy/antigravity,
  # pi, omp, cursor-agent/cursor, gemini, qwen, kiro-cli/kiro,
  # openclaw, hermes, cline, crush e oc.
  # Entradas adicionais podem ser incluídas neste arquivo.

permissions:
  - id: "jail"
    name: "Jail / Sandbox"
    default: true
    locked: true
    requires: []

  - id: "ssh"
    name: "SSH access"
    default: false
    requires: ["jail"]

  - id: "docker"
    name: "Docker socket"
    default: false
    requires: ["jail"]

default_mounts:
  - /storage
  - /storage/Projetos
  - /storage/cache

tools:
  - name: "ai-jail"
    command: "ai-jail"
    release:
      repository: "akitaonrails/ai-jail"
      assets:
        linux-amd64: "ai-jail-linux-x86_64.tar.gz"
        linux-arm64: "ai-jail-linux-aarch64.tar.gz"
        darwin-arm64: "ai-jail-macos-aarch64.tar.gz"
      binary: "ai-jail"
      checksum_asset: "checksums.txt"
  - name: "ai-memory"
    command: "ai-memory"
    source_url: "https://raw.githubusercontent.com/akitaonrails/ai-memory/main/bin/ai-memory"
```

As chaves de plataforma são `linux-amd64`, `linux-arm64`, `darwin-amd64` e
`darwin-arm64`. Um asset pode usar `*` como glob. Por segurança, o launcher
exige checksum (`.sha256`, `.sha256sum`, `checksums.txt`, `SHA256SUMS` ou o
corpo da release); use `allow_unverified: true` somente quando houver outra
forma confiável de verificar a integridade.

### Configuração local (`<projeto>/.ai-launch.yaml`)

Seleções do usuário para um diretório específico:

```yaml
version: "2.0"
agent: "claude"
permissions:
  ssh: true
  docker: true
mounts:
  - path: "/home/lgldsilva/Projetos/app"
    mode: "read-only"
  - path: "/home/lgldsilva/.config/ai-standards"
    mode: "read-only"
options:
  memory: true
  yolo: false
  extra_args: "--model sonnet-4.20250514"
```

## Requisitos de instalação

```bash
# Clone o repositório
git clone https://github.com/seu-usuario/ai-launcher.git
cd ai-launcher

# Instale dependências Go
make deps  # ou go mod tidy

# Construa o binário
make build  # ou go build -o ai-launcher ./cmd/ai-launcher

# Teste local
make test  # ou go test ./...

# Builds portáveis (Linux, macOS e Windows; amd64/arm64)
make build-release
```

## Uso

### Modo não-interativo (CLI flags)

```bash
# Executa Claude Code com SSH e Docker, sem iniciar o agente
ai-launcher --agent claude --ssh --docker --dry-run

# Executa com montagem personalizada
ai-launcher --agent pi --map /host/data --rw-map /workspace --yolo

# Cria uma workstream nomeada no ai-memory
ai-launcher --agent claude --new release-check --dry-run

# Salva seleção sem executar
ai-launcher --save-only --agent codex --ssh

# Adiciona um harness sem recompilar o launcher
ai-launcher --add Xpto --path /opt/xpto/bin/xpto

# Define um comando diferente do nome do arquivo
ai-launcher --add "Meu Harness" --path /opt/tools/runner --command runner
```

`--add` grava o registro em `~/.config/ai-launch/config.yaml` (ou no caminho
passado por `--config`). O arquivo guarda nome, comando e caminho do executável;
o TUI e a CLI passam a detectá-lo automaticamente.

### Instalar e atualizar CLIs

`--install` consulta a API do GitHub em tempo de execução. Ele instala o que
estiver ausente e atualiza um executável quando a tag da release mudou; não
chama `npm`, `apt`, `brew` nem outro gerenciador de pacotes. `--upgrade` força
a reinstalação a partir da release mais recente. Depois de garantir o
`ai-memory`, executa `install-mcp --apply` e `install-hooks --apply` para cada
harness que possui um adapter declarado no catálogo.

```bash
# Verifica todas as receitas configuradas
ai-launcher --install

# Apenas um harness
ai-launcher --install --agent "Meu Harness"

# Reinstala mesmo que a tag já esteja registrada como atual
ai-launcher --upgrade --agent "Meu Harness"
```

O estado fica em `~/.config/ai-launch/install-state.json`. O destino é `path:`
quando informado; caso contrário, `~/.local/bin/<command>`. O launcher não usa
`sudo`: para destinos protegidos, configure um `path` gravável ou faça a
instalação com a política operacional desejada. Entradas sem `release:` seguem
válidas para execução, mas são reportadas como “sem receita GitHub” em vez de
adivinhar um gerenciador de pacotes.

Aliases também são usados na detecção local: se `kilo` não existir, mas
`kilocode` existir, a TUI marca o Kilo como instalado e o launcher executa o
binário encontrado. Harnesses sem adapter conhecido do ai-memory continuam
recebendo o runtime quando `supports_memory: true`, mas não têm hooks/MCP
inventados; adicione o bloco `memory:` específico no YAML quando houver
suporte.

`source_url` existe para wrappers versionados pelo próprio projeto, como
`ai-memory`: o launcher baixa apenas HTTPS e exige um script com shebang; o
runner nativo do wrapper continua seguindo as releases com checksum do
`ai-memory`.

### ai-memory remoto e TLS

O servidor padrão é `https://aimemory.raspberrypi.lan` e pode ser alterado no
YAML global:

```yaml
memory_server_url: "https://aimemory.example"
```

Essa URL é repassada ao processo filho por `AI_MEMORY_SERVER_URL` e também é
gravada nos MCP/hooks instalados. Se o servidor usa uma CA privada do
homelab, essa CA precisa estar instalada como confiável no Linux, macOS e
Windows que executarem os harnesses. Não desative a verificação TLS com
`-k`/`--insecure`; sem a CA confiável o cliente do ai-memory falhará com
`UnknownIssuer` mesmo que o endpoint esteja acessível.

No Windows o launcher continua executando os harnesses normalmente, mas
desativa automaticamente `ai-jail`, que não é compatível com esse sistema.
O `ai-memory` permanece habilitado.

As operações de instalação ficam registradas em
`~/.config/ai-launch/install.log` (ou no diretório de configuração equivalente
do usuário). O log não registra tokens.

### Modo interativo (TUI)

```bash
ai-launcher
```

O TUI compartilha o mesmo builder da CLI e expõe as seções de agente,
permissões, mounts e opções. Navegue usando as teclas:

| Tecla | Ação |
|--------|--------|
| `Tab` ou `1`–`4` | Alternar/saltar entre Agente, Permissões, Mounts e Opções |
| `↑/↓` ou `j/k` | Navegar na seção atual |
| `Space` | Alternar permissão, modo do mount ou opção |
| `/` | Adicionar mount; `Tab` alterna `ro`/`rw` |
| `Backspace` | Remover mount selecionado |
| `Enter` | Selecionar agente ou executar |
| `Ctrl+D` / `d` | Dry-run (exibe comando sem executar) |
| `Ctrl+S` | Salva seleção em `.ai-launch.yaml` local |
| `?` | Abrir ajuda |
| `Ctrl+C` ou `q` | Sai |

## Desenvolvimento

### Testes unitários

```bash
# Execute todos os testes e gates determinísticos
make test-all

# Execute todos os testes com coverage
make test-coverage

# Execute testes específicos
make test-unit
make test-gherkin
```

### Qualidade

```bash
make lint
```

### Build

```bash
make build
```

## Contribuindo

1. Faça um fork do repositório
2. Crie um branch para sua feature (`git checkout -b feature/awesome-feature`)
3. Commit suas alterações (`git commit -a`)
4. Push para o branch (`git push origin feature/awesome-feature`)
5. Abra um Pull Request

## Licença

Este projeto está sob licença MIT. Consulte o arquivo LICENSE para mais detalhes.
