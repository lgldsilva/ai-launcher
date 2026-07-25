# Estratégia de testes

## Modelo de evidência

O launcher tem três contratos separáveis:

| Camada | Evidência | Escopo |
| --- | --- | --- |
| Unitária | `internal/config/config_test.go` e demais testes de pacote | defaults, persistência YAML, fallback de erro e regras puras de comando/permissão |
| Propriedade | `pgregory.net/rapid` nas suítes de `internal/config` e `internal/catalog` | persistência YAML lossless e regras de normalização sob entradas aleatórias |
| Contrato | `test/features/launcher.feature`, executado por `test/gherkin` | composição do argv visível ao usuário, regras de preflight e defaults seguros do config local |
| CLI/TUI | `cmd/ai-launcher/main_test.go` e `internal/tui/tui_test.go` | aliases de flags, parsing de argumentos estilo shell, navegação de seções, edição de mounts, ajuda e atalhos de salvar |

O builder de comandos é intencionalmente puro, então a suíte de contrato
verifica o argv sem iniciar terminal, agente, container ou serviço de rede.
Isso mantém o caminho normal de testes determinístico e seguro para execução
local e em CI.

## Comandos

```bash
make build           # compila bin/ai-launcher a partir do entry point da CLI
make test            # suíte Go completa e determinística
make fmt             # formata todos os fontes Go
make lint            # go vet em todos os pacotes
make test-unit       # testes dos pacotes de lógica (config, catalog, launcher)
make test-property   # testes baseados em propriedades (rapid)
make test-gherkin    # cenários executáveis do contrato do launcher
make test-race       # suíte completa com -race -shuffle=on
make test-coverage   # profile de cobertura atômico + gate de 90% na lógica
make test-mutation   # checagem de mutação opcional; pula explicitamente se ausente
make test-all        # todas as checagens determinísticas, depois a mutação opcional
```

`go test ./...` continua sendo o comando determinístico de base. O projeto usa
um pequeno leitor de Gherkin dentro do repositório em vez de uma dependência
BDD; os arquivos de feature usam `Feature`, `Scenario`, `Given`, `When`,
`Then`, `And` e blocos YAML/argv entre aspas triplas.

## Cobertura e testes de mutação

`test-coverage` usa a instrumentação atômica do Go, imprime a cobertura por
função e impõe um **gate mínimo de 90% de cobertura de linha agregada** nos
pacotes de lógica: `internal/config`, `internal/catalog` e
`internal/launcher`. Só sobrescreva `COVERAGE_MIN` para diagnóstico; CI e
validação de merge usam o gate padrão. A mesma fronteira está em `COVER_PKGS`
no `.ai-standards.env`, usada pelos hooks de commit.

Go não expõe cobertura de branch nativa, então a suíte mira explicitamente os
dois desfechos das decisões de configuração/default; um percentual de branch
não deve ser inferido do total de linhas. `internal/tui` e a execução PTY
ficam fora do gate porque dependem de terminal interativo e processos
spawnados. Eles continuam sujeitos a build, vet, `-race` e checagens de
fumaça/manuais; o contrato puro de montagem de comandos é coberto pelas
suítes unitária e Gherkin.

Testes de mutação são deliberadamente opcionais. Se um desenvolvedor ou uma
imagem dedicada de CI tiver `go-mutesting` instalado, `make test-mutation`
roda contra `internal/config`; caso contrário, reporta um skip explícito e bem
sucedido, e nunca baixa ferramentas. Instalação local única sugerida:

```bash
go install github.com/zimmski/go-mutesting/cmd/go-mutesting@latest
```

Fixe essa ferramenta numa imagem de CI antes de tornar o score de mutação um
gate de merge. Não faça o `go test ./...` normal depender dela.

## Expectativas de regressão conhecidas

A suíte trata uma chave `options.jail` ou `options.memory` omitida como o
default seguro (`true`), preservando valores `false` escritos explicitamente.
Esses são contratos de persistência relevantes para segurança. Se qualquer um
desses testes de regressão falhar, release ou merge devem ser bloqueados até
que a implementação da configuração — não o teste — seja corrigida.
