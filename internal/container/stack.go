// Package container builds the Docker backend for ai-launcher: a
// deterministic Dockerfile from a stack/agent selection, a content-hashed
// image tag so identical selections reuse the cached image, and the docker
// run argv that replaces the ai-jail prefix when sandbox=docker.
//
// The package is deliberately pure: no I/O, no exec, no docker daemon calls.
// The argv builder returns []string exactly like launcher.Build so dry-run,
// table tests, and the Gherkin contract all share one behavior. Docker daemon
// interaction (docker info preflight, docker build, docker image inspect)
// lives in the caller, mirroring how launcher keeps ai-jail execution in the
// executor.
package container

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Stack describes one selectable development toolchain. Layer is the exact
// Dockerfile body (a RUN block) that installs the toolchain; Helpers is an
// optional second RUN block installing the CLI helpers the coding agents
// depend on (linters, language servers). Both are emitted verbatim in image
// order, so they must be deterministic text.
//
// CacheDirs lists the host directories (relative to $HOME) shared with the
// container when the stack is selected: toolchain managers (nvm, sdkman),
// package caches (npm, cargo, go-build, m2) and SDK installs. They are
// mounted read-write at the same path, so downloads on one side are reused
// by the other and disk stays lean — the image does not re-download what the
// host already has (and vice versa).
type Stack struct {
	ID        string
	Name      string
	Layer     string
	Helpers   string
	CacheDirs []string
}

// Stacks is the built-in toolchain catalog. Order matters: it is the display
// order in the TUI and the default order of the FROM block layers.
//
// Ubuntu 24.04 (noble) ships go 1.22, python 3.12, rustc/cargo 1.75, openjdk-21
// and gradle 8.x via apt; maven 3.8. Node comes from NodeSource (noble ships
// 18, too old for modern language servers). The "more complete image" request
// is satisfied by the dev profile below (git, openssh-client, build-essential,
// curl, jq, ripgrep, fd, unzip) rather than the minimal ubuntu base.
var Stacks = []Stack{
	{
		ID:   "go",
		Name: "Go",
		Layer: "RUN apt-get update && apt-get install -y --no-install-recommends \\\n" +
			"    golang-go \\\n" +
			" && rm -rf /var/lib/apt/lists/*",
		Helpers: "RUN go install golang.org/x/tools/gopls@latest \\\n" +
			" && go install golang.org/x/tools/cmd/goimports@latest",
		CacheDirs: []string{"go", ".cache/go-build"},
	},
	{
		ID:   "python",
		Name: "Python",
		Layer: "RUN apt-get update && apt-get install -y --no-install-recommends \\\n" +
			"    python3 python3-pip python3-venv \\\n" +
			" && rm -rf /var/lib/apt/lists/*",
		Helpers:   "RUN pip3 install --break-system-packages ruff pipx",
		CacheDirs: []string{".cache/pip", ".local/lib/python3.12"},
	},
	{
		ID:   "rust",
		Name: "Rust",
		Layer: "RUN apt-get update && apt-get install -y --no-install-recommends \\\n" +
			"    rustc cargo \\\n" +
			" && rm -rf /var/lib/apt/lists/*",
		Helpers:   "RUN cargo install cargo-binstall --locked || true",
		CacheDirs: []string{".cargo", ".rustup"},
	},
	{
		ID:   "java",
		Name: "Java",
		// JDK via SDKMAN (like nvm for Node): agents and build tools may need
		// specific JDK versions, and sdkman manages them instead of pinning
		// whatever Ubuntu ships. The LTS (21) is installed and java/javac are
		// symlinked into /usr/local/bin so non-login shells find them. SDKMAN
		// is bash-only (source, [[ ]]), so the layer runs under /bin/bash.
		Layer: "RUN /bin/bash -c 'curl -fsSL \"https://get.sdkman.io\" | bash && \\\n" +
			" export SDKMAN_DIR=\"$HOME/.sdkman\" && . \"$SDKMAN_DIR/bin/sdkman-init.sh\" && \\\n" +
			" sdk install java 21-tem < /dev/null && \\\n" +
			" java_bin=\"$(ls -d $HOME/.sdkman/candidates/java/21* | head -1)/bin\" && \\\n" +
			" ln -sf \"$java_bin/java\" /usr/local/bin/java && \\\n" +
			" ln -sf \"$java_bin/javac\" /usr/local/bin/javac && \\\n" +
			" java -version'",
		CacheDirs: []string{".sdkman", ".m2"},
	},
	{
		ID:   "maven",
		Name: "Maven",
		Layer: "RUN apt-get update && apt-get install -y --no-install-recommends \\\n" +
			"    maven \\\n" +
			" && rm -rf /var/lib/apt/lists/*",
		CacheDirs: []string{".m2"},
	},
	{
		ID:   "gradle",
		Name: "Gradle",
		Layer: "RUN apt-get update && apt-get install -y --no-install-recommends \\\n" +
			"    gradle \\\n" +
			" && rm -rf /var/lib/apt/lists/*",
		CacheDirs: []string{".gradle"},
	},
	{
		ID:   "node",
		Name: "Node",
		Layer: "RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \\\n" +
			" && apt-get install -y --no-install-recommends nodejs \\\n" +
			" && rm -rf /var/lib/apt/lists/*",
		Helpers:   "RUN npm install -g typescript typescript-language-server",
		CacheDirs: []string{".nvm", ".npm", ".cache/node"},
	},
	{
		ID:   "cpp",
		Name: "C/C++",
		Layer: "RUN apt-get update && apt-get install -y --no-install-recommends \\\n" +
			"    build-essential cmake clang gdb \\\n" +
			" && rm -rf /var/lib/apt/lists/*",
		CacheDirs: []string{".cache/ccache"},
	},
}

// StackByID returns the stack with the given ID.
func StackByID(id string) (Stack, bool) {
	for _, stack := range Stacks {
		if stack.ID == id {
			return stack, true
		}
	}
	return Stack{}, false
}

// ValidStackIDs returns a canonical (sorted, deduplicated) copy of ids,
// failing on any unknown or empty stack ID. Sorting makes the selection
// order-independent so the image tag does not churn when the user ticks the
// same stacks in a different order.
func ValidStackIDs(ids []string) ([]string, error) {
	seen := make(map[string]struct{}, len(ids))
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := StackByID(id); !ok {
			return nil, fmt.Errorf("unknown stack %q", id)
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	sort.Strings(result)
	return result, nil
}

// BaseImage is the Ubuntu release the generated Dockerfile starts from.
const BaseImage = "ubuntu:24.04"

// DevProfile is the system-tool layer shared by every generated image: the
// "more complete" base the user asked for (git, ssh client, build toolchain,
// jq, ripgrep, fd, unzip, less, vim) plus the PATH for the official agent
// installers, which all drop binaries into ~/.local/bin.
//
// Node LTS arrives via nvm (not the distro package): several agent installers
// (pi, devin) hard-require a recent Node, and nvm pins the LTS line rather
// than whatever Ubuntu happens to ship. node/npm/npx are symlinked into
// /usr/local/bin so non-login shells find them.
const DevProfile = `# Runtime essentials: all official agent installers need
# bash + curl; agents and their tooling need git, ssh, unzip, jq, ripgrep.
RUN apt-get update && apt-get install -y --no-install-recommends \
    git openssh-client ca-certificates curl \
    build-essential pkg-config \
    jq ripgrep fd-find less vim-tiny \
    unzip zip xz-utils \
 && ln -s /usr/bin/fdfind /usr/bin/fd \
 && rm -rf /var/lib/apt/lists/*

# Node LTS via nvm (agent installers require a recent Node; distro Node is
# too old). nvm is shell-only, so symlink node/npm/npx into /usr/local/bin;
# the npm global bin dir (where pi/devin/other npm CLIs land) joins PATH.
RUN curl -fsSL https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.3/install.sh | bash \
 && export NVM_DIR="$HOME/.nvm" && . "$NVM_DIR/nvm.sh" \
 && nvm install --lts \
 && node_bin="$(nvm which default)" \
 && ln -sf "$node_bin" /usr/local/bin/node \
 && ln -sf "$(dirname "$node_bin")/npm" /usr/local/bin/npm \
 && ln -sf "$(dirname "$node_bin")/npx" /usr/local/bin/npx \
 && node --version && npm --version

ENV PATH="/root/.local/bin:$(ls -d /root/.nvm/versions/node/*/bin | head -1):${PATH}"`

// ErrNoStacks is returned when the Dockerfile is requested with an empty
// stack selection: an agent box with no toolchain is a footgun, not a build.
var ErrNoStacks = errors.New("at least one stack is required")

// ErrNoAgents is returned when the Dockerfile is requested with no agents:
// the whole point of the image is to host the coding agents.
var ErrNoAgents = errors.New("at least one agent is required")
