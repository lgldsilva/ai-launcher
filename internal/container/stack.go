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
// optional second block installing the CLI helpers the coding agents
// depend on (linters, language servers) and exposing them on PATH. Both are
// emitted verbatim in image order, so they must be deterministic text. Layer runs as root (apt and
// toolchain managers need system paths); Helpers runs as the least-privilege
// user, so it must only write under the shared home or user-owned toolchain
// dirs and must clean any package-manager cache it populates.
//
// DependencyIDs names the portable dependency mounts selected automatically
// with this stack. The IDs are resolved by the cross-platform dependency
// catalog, which knows the correct host path and the Linux container target.
// CacheDirs remains for compatibility with the old same-path mount helper;
// new launch paths must use DependencyIDs.
type Stack struct {
	ID            string
	Name          string
	Layer         string
	Helpers       string
	DependencyIDs []string
	CacheDirs     []string
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
		Layer: aptInstallPrefix +
			"    golang-go \\\n" +
			aptCleanupSuffix,
		// go clean drops the module/build cache the installs just populated
		// (~1.3 GB) from the layer; the compiled helpers stay in ~/go/bin,
		// which joins PATH so agents can invoke them by name.
		Helpers: "RUN go install golang.org/x/tools/gopls@latest \\\n" +
			" && go install golang.org/x/tools/cmd/goimports@latest \\\n" +
			" && go clean -modcache -cache\n" +
			"ENV PATH=\"/home/ai-launcher/go/bin:${PATH}\"",
		DependencyIDs: []string{"go.module-cache", "go.build-cache"},
		CacheDirs:     []string{"go", ".cache/go-build"},
	},
	{
		ID:   "python",
		Name: "Python",
		Layer: aptInstallPrefix +
			"    python3 python3-pip python3-venv \\\n" +
			aptCleanupSuffix,
		// --user keeps the install inside the shared home (helpers run as the
		// least-privilege user, so /usr/local is not writable); the pip cache
		// is purged so it does not bloat the layer.
		Helpers:       "RUN pip3 install --break-system-packages --user ruff pipx && pip3 cache purge",
		DependencyIDs: []string{"python.pip-cache"},
		CacheDirs:     []string{".cache/pip", ".local/lib/python3.12"},
	},
	{
		ID:   "rust",
		Name: "Rust",
		Layer: aptInstallPrefix +
			"    rustc cargo \\\n" +
			aptCleanupSuffix,
		// The registry cache the install just populated is build-only weight;
		// the cargo-binstall binary stays in ~/.cargo/bin.
		Helpers:       "RUN cargo install cargo-binstall --locked && rm -rf \"$HOME/.cargo/registry\"",
		DependencyIDs: []string{"rust.cargo-registry", "rust.cargo-git"},
		CacheDirs:     []string{".cargo", ".rustup"},
	},
	{
		ID:   "java",
		Name: "Java",
		// JDK via SDKMAN (like nvm for Node): agents and build tools may need
		// specific JDK versions, and sdkman manages them instead of pinning
		// whatever Ubuntu ships. The LTS (21) is installed and java/javac are
		// symlinked into /usr/local/bin so non-login shells find them. SDKMAN
		// is bash-only (source, [[ ]]), so the layer runs under /bin/bash.
		// HOME is pinned to /root so the installer's rc-file edits do not
		// leave root-owned files in the shared home; /opt/sdkman itself stays
		// root-owned and only needs to be world-traversable at run time.
		Layer: "RUN /bin/bash -o pipefail -c 'export HOME=/root SDKMAN_DIR=\"/opt/sdkman\" && curl -fsSL \"https://get.sdkman.io\" | bash && \\\n" +
			" . \"$SDKMAN_DIR/bin/sdkman-init.sh\" && \\\n" +
			" sdk install java 21-tem < /dev/null && \\\n" +
			" java_bin=\"$(ls -d \"$SDKMAN_DIR\"/candidates/java/21* | head -1)/bin\" && \\\n" +
			" ln -sf \"$java_bin/java\" /usr/local/bin/java && \\\n" +
			" ln -sf \"$java_bin/javac\" /usr/local/bin/javac && \\\n" +
			" java -version'",
		DependencyIDs: []string{"java.sdkman", "java.maven-repository"},
		CacheDirs:     []string{".sdkman", ".m2"},
	},
	{
		ID:   "maven",
		Name: "Maven",
		Layer: aptInstallPrefix +
			"    maven \\\n" +
			aptCleanupSuffix,
		DependencyIDs: []string{"java.maven-repository"},
		CacheDirs:     []string{".m2"},
	},
	{
		ID:   "gradle",
		Name: "Gradle",
		Layer: aptInstallPrefix +
			"    gradle \\\n" +
			aptCleanupSuffix,
		DependencyIDs: []string{"gradle.cache", "gradle.wrapper"},
		CacheDirs:     []string{".gradle"},
	},
	{
		ID:   "node",
		Name: "Node",
		Layer: "RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \\\n" +
			" && apt-get install -y --no-install-recommends nodejs \\\n" +
			" && rm -rf /var/lib/apt/lists/*",
		Helpers:       "RUN npm install -g typescript typescript-language-server && npm cache clean --force",
		DependencyIDs: []string{"node.nvm", "node.npm-cache"},
		CacheDirs:     []string{".nvm", ".npm", ".cache/node"},
	},
	{
		ID:   "cpp",
		Name: "C/C++",
		Layer: aptInstallPrefix +
			"    build-essential cmake clang gdb \\\n" +
			aptCleanupSuffix,
		DependencyIDs: []string{"cpp.ccache"},
		CacheDirs:     []string{".cache/ccache"},
	},
	{
		ID:   "neovim",
		Name: "Neovim",
		Layer: aptInstallPrefix +
			"    neovim \\\n" +
			aptCleanupSuffix,
		DependencyIDs: []string{"editor.neovim-config", "editor.neovim-data", "editor.neovim-state", "editor.neovim-cache"},
	},
	{
		// Plain zsh for operators who prefer it as their interactive shell;
		// no oh-my-zsh framework and no config mounts, so nothing to depend on.
		ID:   "zsh",
		Name: "zsh",
		Layer: aptInstallPrefix +
			"    zsh \\\n" +
			aptCleanupSuffix,
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
// Node is deliberately not part of this common profile; Dockerfile selects
// NodeProfile only when the node stack or an agent that declares NeedsNode is
// selected. The docker CLI is likewise excluded: it is only useful when the
// launch grants the docker permission, so Dockerfile emits DockerCLIProfile
// for those launches instead of charging every image for it.
const DevProfile = `# Runtime essentials: all official agent installers need
# bash + curl; agents and their tooling need git, ssh, unzip, jq, ripgrep.
RUN apt-get update && apt-get install -y --no-install-recommends \
    git openssh-client ca-certificates curl \
    build-essential pkg-config \
    jq yq ripgrep fd-find less vim-tiny tmux \
    unzip zip xz-utils \
 && ln -s /usr/bin/fdfind /usr/bin/fd \
 && rm -rf /var/lib/apt/lists/*

ENV PATH="/home/ai-launcher/.local/bin:${PATH}"`

// DockerCLIProfile installs the docker client for launches granted the docker
// permission (which bind-mounts the host daemon socket at run time). Kept out
// of DevProfile so images without the permission do not carry a client that
// has no daemon to talk to.
const DockerCLIProfile = `# Docker CLI for docker-permission launches; the daemon is the host's,
# reached through the socket mounted by the run argv.
RUN apt-get update && apt-get install -y --no-install-recommends \
    docker.io \
 && rm -rf /var/lib/apt/lists/*`

// NodeProfile is emitted only when the selected stack/agent needs Node. Keep
// it separate from DevProfile so Go/Python/Rust-only images do not download
// Node, create /opt/nvm, or chown an unrelated toolchain directory.
//
// The layer runs as root before any USER switch. /opt/nvm stays root-owned
// (run-time only reads node through the /usr/local/bin symlinks), while the
// npm global prefix is handed to the least-privilege user in the same RUN —
// npm helpers and npm-distributed agents install there after the USER switch,
// so the ownership must be right at creation time rather than via a final
// chown -R pass that would duplicate the tree into a new layer. The prefix
// config is written with HOME=/root for the same reason: no root-owned files
// in the shared home.
const NodeProfile = `# Node LTS via nvm (agent installers require a recent Node; distro Node is
# too old). nvm is shell-only, so node/npm/npx are symlinked into
# /usr/local/bin. npm's global prefix is pinned to a fixed dir
# (/usr/local/lib/nvm-bin) that joins PATH, so npm install -g CLIs land
# runnable without shell interpolation in ENV (H3): ENV cannot expand
# $(...), so the install target must be a constant path.
# nvm is installed under /opt so a host UID mapped at runtime can traverse it.
RUN mkdir -p /opt/nvm \
 && export NVM_DIR="/opt/nvm" \
 && PROFILE=/dev/null bash -c 'curl -fsSL https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.3/install.sh | bash' \
 && . "$NVM_DIR/nvm.sh" \
 && nvm install --lts \
 && node_bin="$(nvm which default)" \
 && ln -sf "$node_bin" /usr/local/bin/node \
 && ln -sf "$(dirname "$node_bin")/npm" /usr/local/bin/npm \
 && ln -sf "$(dirname "$node_bin")/npx" /usr/local/bin/npx \
 && mkdir -p /usr/local/lib/nvm-bin \
 && HOME=/root npm config set prefix /usr/local/lib/nvm-bin \
 && chown -R ai-launcher:ai-launcher /usr/local/lib/nvm-bin \
 && node --version && npm --version

ENV PATH="/home/ai-launcher/.local/bin:/usr/local/lib/nvm-bin/bin:${PATH}"
ENV NVM_DIR="/opt/nvm"
ENV NPM_CONFIG_PREFIX="/usr/local/lib/nvm-bin"`

// ContainerUser is the named least-privilege user used when no host UID/GID
// override is supplied. Launches that map the host UID/GID still use the same
// accessible home and /opt toolchain paths.
const ContainerUser = launcherBinaryName

// ContainerHome is intentionally outside /root: Docker launches map the host
// user into the image, so agent binaries and configuration must be traversable
// by an arbitrary non-root UID.
const ContainerHome = "/home/ai-launcher"

// ContainerUserSetup creates the runtime account before the toolchain layers
// that need it (NodeProfile hands the npm global prefix to this user) and
// before stack helpers and agent installers switch to it. The home chown is
// cheap here because the directory was just created empty; everything written
// into it later is written BY this user, so no recursive chown pass is needed
// at the end of the build.
const ContainerUserSetup = `# Least-privilege runtime account. Docker may override
# the numeric identity at launch, so all shared toolchains live outside /root.
RUN groupadd --system ai-launcher \
 && useradd --system --gid ai-launcher --create-home --home-dir /home/ai-launcher --shell /bin/bash ai-launcher \
 && chown -R ai-launcher:ai-launcher /home/ai-launcher
ENV HOME="/home/ai-launcher"`

// ErrNoStacks is returned when the Dockerfile is requested with an empty
// stack selection: an agent box with no toolchain is a footgun, not a build.
var ErrNoStacks = errors.New("at least one stack is required")

// ErrNoAgents is returned when the Dockerfile is requested with no agents:
// the whole point of the image is to host the coding agents.
var ErrNoAgents = errors.New("at least one agent is required")
