# Decisões de design

Decisões temáticas, não ADRs numerados. Cada uma segue **Decisão → Por quê →
Como → Trade-offs**, ancorada no incidente concreto que a forçou.

## Orquestrar ai-jail/ai-memory upstream em vez de reimplementar

**Decisão.** O launcher nunca forka nem embute sandbox ou memória: ele invoca os
binários de terceiros (`akitaonrails/ai-jail`, `akitaonrails/ai-memory`).

**Por quê.** As duas ferramentas têm cadência de release rápida e autor ativo;
reimplementar bubblewrap/sandbox-exec ou o protocolo MCP do ai-memory seria um
fork eternamente atrás. O preço do acoplamento é o drift de CLI — e ele é
real: a modelagem dos flags do ai-jail v1.15 (`--lockdown`, `--private-home`,
toggles `--no-*` para capacidades default-on) só existe porque a CLI mudou sob
nós.

**Como.** Contrato explícito: a suíte Gherkin em `test/features/launcher.feature`
(executada por `test/gherkin`) trava a composição exata do argv — ordem
`ai-jail → ai-memory run → harness`, formas `--no-*`, escopo de
workstream/workspace/project. Se o upstream mudar, o contrato quebra no CI,
não na máquina do usuário. Os flags do jail vivem em uma estrutura declarativa
(`config.JailFlags` tri-state), então absorver uma nova versão do ai-jail é
adicionar uma linha numa tabela, não reescrever `if`s.

**Trade-offs.** Dependemos da superfície de CLI de terceiros e do formato dos
assets de release (ex.: ai-jail publica só linux-x86_64 e macos-aarch64). Em
troca, nunca carregamos código de sandbox próprio.

## Parâmetros por harness orientados a dados

**Decisão.** Flags específicos de cada harness são declarados no catálogo
(`agents[].params`: `name`, `flag`, `takes_value`) e preenchidos por
`--param nome=valor` ou pela linha do parâmetro na TUI — nunca por código
especial por agente.

**Por quê.** O gatilho foi o Kimi: além de `--model`, ele aceita `--query`
(query inicial). Codificar isso em Go significaria rebuild do launcher para
cada flag novo de qualquer harness — insustentável com ~20 agentes no catálogo.

**Como.** `launcher.Build` emite os parâmetros declarados na ordem de
declaração; nomes não declarados são ignorados no argv e reportados pelo
validador (`param-not-declared`). A TUI renderiza uma linha de texto por
parâmetro declarado do agente selecionado.

**Trade-offs.** O catálogo pode ficar desatualizado em relação ao harness
(mesmo problema de drift do item anterior, mesma mitigação: dados, não
código). `--extra-args` continua existindo como válvula de escape para
qualquer coisa não declarada.

## Windows como cidadão de primeira classe, sem jail

**Decisão.** Windows recebe binário nativo e fluxo completo, mas o jail é
desativado automaticamente — com aviso, nunca com erro.

**Por quê.** O ai-jail "provavelmente nunca" vai suportar Windows (bubblewrap
e sandbox-exec são mecanismos Unix). Bloquear o launcher no Windows por causa
disso expulsaria o usuário; esconder a ausência do sandbox seria desonesto.

**Como.** `launcher.ConstrainToPlatform` força `UseJail=false` no Windows e
desliga toda permissão que requer jail (ssh, gh, docker, gpu), emitindo o
aviso `jail-unsupported-windows`. A TUI esconde o toggle de Jail e filtra as
permissões dependentes. O instalador pula o ai-jail e instala o ai-memory pelo
asset nativo `ai-memory-windows-x86_64.zip`. O caminho recomendado para
sandbox no Windows é WSL2.

**Trade-offs.** No Windows o harness roda com todos os privilégios do usuário
— isso está dito explicitamente na seção de segurança do README. Aceitamos
essa assimetria porque a alternativa (sem Windows) é pior.

## Checksum SHA-256 obrigatório em instalações

**Decisão.** Toda instalação via release do GitHub exige checksum verificável;
sem ele, a instalação falha.

**Por quê.** O launcher baixa e executa binários de terceiros em nome do
usuário — é exatamente o ponto onde um supply-chain attack entra. Tags de
release e assets são mutáveis; "baixou, rodou" sem verificação é inaceitável
numa ferramenta cujo propósito é segurança.

**Como.** `internal/installer` procura o digest em `.sha256`, `.sha256sum`,
`checksums.txt`, `SHA256SUMS` ou no corpo da release, e só extrai depois de
conferir. A válvula `allow_unverified: true` existe para receitas com outra
forma confiável de verificação, mas é uma decisão explícita do operador,
registrada no YAML.

**Trade-offs.** Projetos que não publicam checksum não são instaláveis por
receita (seguem executáveis se já estiverem no PATH). Preferimos isso a
instalar cegamente.

## Gate de 90% de cobertura só nos pacotes de lógica pura

**Decisão.** O gate de 90% mede apenas `internal/config`, `internal/catalog` e
`internal/launcher` (excluindo o executor PTY e os `replace_*.go`).
`internal/tui` e `internal/installer` ficam fora do denominador.

**Por quê.** Cobertura de linha em código acoplado a terminal interativo e a
processos spawnados mede teatro, não segurança — forçar 90% ali geraria testes
frágeis e fingidos. O que realmente não pode quebrar é a lógica pura:
persistência de config, defaults seguros, composição do argv.

**Como.** `make test-coverage` (e o job `test` do CI) filtram o profile com
`awk` e falham abaixo de 90% no total filtrado; `COVER_PKGS` em
`.ai-standards.env` repete a mesma fronteira para os hooks. TUI, instalador e
executor são cobertos por `go test -race -shuffle=on ./...` (race-only) e
pela suíte de contrato Gherkin.

**Trade-offs.** Bugs de interação (teclas, terminal) dependem de race tests e
verificação manual. É a divisão honesta: gate onde a métrica significa algo.

## A TUI executa de verdade

**Decisão.** `Enter` na TUI lança o harness; a TUI não é um gerador de
comando.

**Por quê.** A versão anterior imprimia o argv e saía — o usuário tinha que
copiar e colar, o que anula o propósito de um "launcher" e convidava a erro de
transcrição justamente na parte sensível (ordem dos wrappers).

**Como.** A TUI mantém todo o estado em `launcher.LaunchConfig` e devolve a
configuração confirmada; `main` monta o argv com o mesmo `launcher.Build` da
CLI e executa via `exec` (substituição de processo) ou PTY. Dry-run explícito
(`--dry-run`, `d`/`Ctrl+D` dentro da TUI) é o único caminho que só imprime.

**Trade-offs.** Executar de verdade exige preflight sério — daí o
`launcher.Validator` com códigos de issue estáveis e warnings não-fatais (ex.:
jail no Windows), em vez de fail-fast no primeiro problema.

## O que NÃO estamos fazendo (ainda)

- **Sandbox nativo no Windows.** Depende de upstream (ai-jail "probably
  never"); o caminho suportado é WSL2.
- **GUI.** A TUI é a interface; não há plano de interface gráfica.
- **Sistema de plugins.** Extensibilidade é por dados (catálogo YAML), não por
  código carregável.
- **Registry próprio de harnesses.** O catálogo é local e por máquina; não
  publicamos nem consumimos um registry central.
- **Gerenciadores de pacote.** O instalador fala só com GitHub Releases;
  `npm`/`apt`/`brew` ficam fora por decisão.

## Erros a evitar

- [ ] Não reimplemente funcionalidade do ai-jail/ai-memory — absorva o upstream e atualize o contrato Gherkin.
- [ ] Não adicione `if agente == "x"` no builder — declare um `params:` no catálogo.
- [ ] Não emita flags do jail como string solta — use `config.JailFlags` (tri-state) para respeitar os defaults do ai-jail.
- [ ] Não instale nada sem checksum verificável — não "contorne" com `allow_unverified` no catálogo padrão.
- [ ] Não coloque `internal/tui` ou o executor PTY no denominador do gate de cobertura.
- [ ] Não escreva tokens em logs, argv ou configs locais — token só via env do processo filho.
- [ ] Não mude a ordem canônica `ai-jail → ai-memory run → harness` — se mudar, a suíte de contrato deve falhar primeiro.
- [ ] Não transforme a TUI de volta em gerador de comando — dry-run é o caminho explícito para só imprimir.
- [ ] Não trate o aviso de jail no Windows como erro — degradação com aviso é o comportamento correto.
