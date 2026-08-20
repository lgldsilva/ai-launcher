Feature: Launcher command contract
  The launcher turns a saved selection into an argv without executing it.

  Scenario: Runs an agent directly when integrations are disabled
    Given a launch configuration
      """
      agent: codex
      jail: false
      memory: false
      args: [--model, gpt-5]
      """
    When the launch command is built
    Then the command equals
      """
      codex
      --model
      gpt-5
      """

  Scenario: Wraps a sandboxed memory-enabled agent with selected capabilities
    Given a launch configuration
      """
      agent: claude
      home: /home/tester
      jail: true
      memory: true
      new_workstream: release-check
      permissions:
        ssh: true
        gh: true
        docker: true
        gpu: true
      mounts:
        - path: /reference
          mode: read-only
        - path: /workspace
          mode: rw
      yolo: true
      args: [--resume, latest]
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --docker
      --no-network
      --ssh
      --rw-map
      /home/tester/.config/gh
      --gpu
      --map
      /reference
      --rw-map
      /workspace
      ai-memory
      run
      --new
      release-check
      claude
      --yolo
      --resume
      latest
      """

  Scenario: Omits the GitHub CLI map when home is unavailable
    Given a launch configuration
      """
      agent: codex
      jail: true
      memory: false
      clear_home: true
      permissions:
        gh: true
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      codex
      """

  Scenario: Skips the GitHub CLI map when a configured mount already covers it
    Given a launch configuration
      """
      agent: codex
      jail: true
      memory: false
      home: /home/tester
      permissions:
        gh: true
      mounts:
        - path: /home/tester/.config
          mode: rw
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      --rw-map
      /home/tester/.config
      codex
      """

  Scenario: Rejects an empty agent command
    Given a launch configuration
      """
      jail: false
      memory: false
      """
    When the launch command is built
    Then command construction fails with "agent command is required"

  Scenario: Reports incompatible preflight permissions without external commands
    Given a validation configuration
      """
      agent: claude
      jail: false
      memory: true
      permissions:
        gpu: true
      missing_commands: [ai-memory]
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      memory-not-found
      permission-without-jail
      """

  Scenario: Rejects GitHub CLI permission without a jail
    Given a validation configuration
      """
      agent: codex
      jail: false
      memory: false
      permissions:
        gh: true
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      permission-without-jail
      """

  Scenario: Keeps omitted safety options enabled in a local configuration
    Given a local configuration
      """
      options:
        yolo: true
      """
    When the local configuration is loaded
    Then option "jail" is true
    And option "memory" is true
    And option "yolo" is true

  Scenario: Passes declared harness parameters to the agent
    Given a launch configuration
      """
      agent: kimi
      jail: false
      memory: false
      params:
        - name: model
          flag: --model
          takes_value: true
        - name: query
          flag: --prompt
          takes_value: true
      param_values:
        query: explain this
      """
    When the launch command is built
    Then the command equals
      """
      kimi
      --prompt
      explain this
      """

  Scenario: Uses the harness yolo flag and model parameter without memory
    Given a launch configuration
      """
      agent: claude
      jail: false
      memory: false
      yolo: true
      yolo_flag: --dangerously-skip-permissions
      params:
        - name: model
          flag: --model
          takes_value: true
      param_values:
        model: sonnet
      """
    When the launch command is built
    Then the command equals
      """
      claude
      --model
      sonnet
      --dangerously-skip-permissions
      """

  Scenario: Keeps extra args pass-through for a harness without declared params
    Given a launch configuration
      """
      agent: goose
      jail: false
      memory: false
      param_values:
        query: ignored
      args: [--verbose]
      """
    When the launch command is built
    Then the command equals
      """
      goose
      --verbose
      """

  Scenario: Round-trips a saved profile through the global configuration
    Given a global configuration
      """
      agents:
        - name: Claude Code
          command: claude
      profiles:
        review:
          agent: claude
          mounts:
            - path: /reference
              mode: read-only
          options:
            jail: true
            memory: true
            yolo: true
            param_values:
              model: sonnet
      """
    When the global configuration is saved and loaded
    Then profile "review" has agent "claude"
    And profile "review" option "jail" is true
    And profile "review" option "memory" is true
    And profile "review" option "yolo" is true
    And profile "review" param "model" is "sonnet"

  Scenario: Composes the canonical chain with jail flags in ai-jail order
    Given a launch configuration
      """
      agent: claude
      jail: true
      jail_exec: true
      memory: true
      workspace: acme
      project: billing
      jail_flags:
        lockdown: true
        mask: [/etc/secrets, /home/tester/.gnupg]
        browser: hard
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --exec
      --no-docker
      --no-network
      --lockdown
      --mask
      /etc/secrets
      --mask
      /home/tester/.gnupg
      --browser=hard
      ai-memory
      run
      --workspace
      acme
      --project
      billing
      claude
      """

  Scenario: Emits negative forms for explicitly disabled jail toggles
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: false
      jail_flags:
        landlock: false
        seccomp: false
        rlimits: false
        status_bar: false
        gpu: false
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      --no-gpu
      --no-landlock
      --no-seccomp
      --no-rlimits
      --no-status-bar
      claude
      """

  Scenario: Emits positive forms for jail toggles forced on
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: false
      jail_flags:
        gpu: true
        display: true
        mise: true
        worktree: true
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      --gpu
      --display
      --mise
      --worktree
      claude
      """

  Scenario: Forces the auto-detected passthroughs off from jail flags
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: false
      jail_flags:
        display: false
        mise: false
        worktree: false
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      --no-display
      --no-mise
      --no-worktree
      claude
      """

  Scenario: An explicit jail flag wins over the permission for the same capability
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: false
      permissions:
        gpu: true
        tailscale: true
        display: true
      jail_flags:
        gpu: false
        tailscale: false
        display: false
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      --no-tailscale
      --no-gpu
      --no-display
      claude
      """

  # ai-jail <= 1.15.x bind-mounted an existing /var/run/docker.sock read-write
  # with no flag and no warning, and that socket is root on the host. Leaving
  # the capability unset is therefore not a safe default for this one flag, so
  # the launcher always states it. Every other jail scenario above asserts the
  # --no-docker default; these two lock the ways an operator turns it on.
  # ai-jail 1.18 forwards a minimal allowlist, so anything the sandbox needs
  # must be named. The bare --env form keeps values out of the argv.
  # The order of this chain is a contract with TWO tools. ai-jail 1.18 parses
  # `ai-memory run <harness>` to decide agent-state mounts, accepts only five
  # value options before the harness, and bails out on anything else starting
  # with "-". So --executable comes before the harness and --fresh after it.
  Scenario: Keeps the ai-memory run chain parseable by ai-jail
    Given a launch configuration
      """
      agent: claude
      executable: /opt/agents/claude
      jail: false
      memory: true
      fresh: true
      workstream: release-1
      """
    When the launch command is built
    Then the command equals
      """
      ai-memory
      run
      --workstream
      release-1
      --executable
      /opt/agents/claude
      claude
      --fresh
      """

  Scenario: Forwards the launcher-owned environment by name
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: false
      jail_env: [AI_MEMORY_AUTH_TOKEN, ANTHROPIC_API_KEY]
      permissions:
        jail: true
        network: true
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --network
      --env
      AI_MEMORY_AUTH_TOKEN
      --env
      ANTHROPIC_API_KEY
      claude
      """

  Scenario: The network permission is the opt-in for outbound access
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: false
      permissions:
        jail: true
        network: true
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --network
      claude
      """

  # ai-jail 1.18 made network opt-in; the launcher states the decision either
  # way, so an offline sandbox is expressible instead of accidental.
  Scenario: Emits the negative network form when the permission is off
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: false
      permissions:
        jail: true
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      claude
      """

  Scenario: The docker permission is the opt-in for the socket
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: false
      permissions:
        docker: true
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --docker
      --no-network
      claude
      """

  Scenario: An explicit docker jail flag wins over the permission
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: false
      permissions:
        docker: true
      jail_flags:
        docker: false
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      claude
      """

  Scenario: Emits --no-hide-config when the project config mask is disabled
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: false
      jail_flags:
        hide_config: false
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      --no-hide-config
      claude
      """

  Scenario: Emits --no-save-config when automatic config writes are disabled
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: false
      jail_flags:
        save_config: false
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      --no-save-config
      claude
      """

  Scenario: Orders the save-config toggle after hide-config
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: false
      jail_flags:
        hide_config: true
        save_config: true
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      --hide-config
      --save-config
      claude
      """

  Scenario: Emits --fresh as an ai-memory run wrapper flag
    Given a launch configuration
      """
      agent: claude
      jail: false
      memory: true
      fresh: true
      workstream: release-1
      """
    When the launch command is built
    Then the command equals
      """
      ai-memory
      run
      --workstream
      release-1
      claude
      --fresh
      """

  Scenario: Rejects --fresh together with --continue
    Given a validation configuration
      """
      agent: claude
      goos: linux
      jail: false
      memory: true
      continue: true
      fresh: true
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      fresh-with-continue
      """

  Scenario: Resumes a named workstream through ai-memory run
    Given a launch configuration
      """
      agent: codex
      jail: false
      memory: true
      workstream: release-1
      """
    When the launch command is built
    Then the command equals
      """
      ai-memory
      run
      --workstream
      release-1
      codex
      """

  Scenario: Continues the most recent session without naming a harness
    Given a launch configuration
      """
      continue: true
      jail: true
      jail_exec: true
      memory: true
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --exec
      --no-docker
      --no-network
      ai-memory
      run
      """

  Scenario: Drops the jail and jail-only permissions on Windows
    Given a launch configuration
      """
      agent: codex
      goos: windows
      jail: true
      memory: true
      permissions:
        ssh: true
      """
    When the launch command is built
    Then the command equals
      """
      ai-memory
      run
      codex
      """

  Scenario: Warns instead of failing when Windows requests the jail
    Given a validation configuration
      """
      agent: codex
      goos: windows
      jail: true
      memory: false
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      jail-unsupported-windows
      """

  Scenario: Warns when jail options are set with the jail disabled
    Given a validation configuration
      """
      agent: codex
      jail: false
      memory: false
      jail_flags:
        lockdown: true
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      jail-options-without-jail
      """

  Scenario: Ships only the published upstream release assets
    Given the built-in global defaults
    Then tool "ai-jail" assets equal
      """
      darwin-arm64
      linux-amd64
      """
    And tool "ai-memory" assets equal
      """
      darwin-amd64
      darwin-arm64
      linux-amd64
      linux-arm64
      windows-amd64
      """

  Scenario: Maps the ai-jail v1.16 passthrough permissions to argv
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: false
      permissions:
        display: true
        pictures: true
        tailscale: true
        systemd-user: true
        mise: true
        worktree: true
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      --display
      --pictures
      --tailscale
      --systemd-user
      --mise
      --worktree
      claude
      """

  Scenario: Warns when an enabled permission is unsupported on the platform
    Given a validation configuration
      """
      agent: claude
      goos: darwin
      jail: true
      memory: false
      permissions:
        systemd-user: true
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      unsupported-platform
      """

  Scenario: Emits mask and deny-path exceptions, hidden dotdirs and status bar style
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: false
      jail_flags:
        mask_exceptions: [/etc/secrets/public, /srv/shared]
        deny_path_exceptions: [/var/cache]
        hide_dotdirs: [.aws, .gnupg]
        status_bar_style: dark
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      --status-bar=dark
      --mask-except
      /etc/secrets/public
      --mask-except
      /srv/shared
      --deny-path-except
      /var/cache
      --hide-dotdir
      .aws
      --hide-dotdir
      .gnupg
      claude
      """

  Scenario: A status bar style suppresses the boolean status bar forms
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: false
      jail_flags:
        status_bar: false
        status_bar_style: pastel
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      --status-bar=pastel
      claude
      """

  # Through ai-jail 1.17.x --allow-tcp-port was lockdown-only and silently
  # ignored otherwise. From 1.18.0 it fails closed and aborts the launch, so
  # the launcher refuses first, with its own message.
  Scenario: Refuses a launch carrying allow_tcp_ports
    Given a validation configuration
      """
      agent: claude
      goos: linux
      jail: true
      memory: false
      jail_flags:
        allow_tcp_ports: [8080]
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      allow-tcp-ports-unsupported
      """

  Scenario: Warns when the internal network blocks the agent
    Given a validation configuration
      """
      agent: claude
      goos: linux
      memory: false
      docker: true
      stacks: [go]
      services: [redis]
      project_dir: /w
      internal_network: true
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      internal-network-blocks-agent
      """

  Scenario: Rejects when the internal network has no Compose services to apply to
    Given a validation configuration
      """
      agent: claude
      goos: linux
      memory: false
      docker: true
      stacks: [go]
      project_dir: /w
      internal_network: true
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      internal-network-requires-compose
      """

  Scenario: Rejects a harness ai-memory run does not accept
    Given a validation configuration
      """
      agent: gemini
      goos: linux
      jail: false
      memory: true
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      memory-harness-unsupported
      """

  Scenario: Accepts a harness from the ai-memory run list
    Given a validation configuration
      """
      agent: opencode
      goos: linux
      jail: false
      memory: true
      permissions:
        gh: true
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      permission-without-jail
      """

  # The unrelated permission issue is deliberate: an empty expectation would
  # also pass if preflight never ran, so the scenario asserts a known issue is
  # present while memory-harness-unsupported is absent.
  Scenario: Refuses an ai-jail below the supported floor in preflight
    Given a validation configuration
      """
      agent: claude
      goos: linux
      jail: true
      memory: false
      jail_version: 1.14.2
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      jail-version-too-old
      """

  Scenario: Warns about an ai-jail above the validated range
    Given a validation configuration
      """
      agent: claude
      goos: linux
      jail: true
      memory: false
      jail_version: 1.19.0
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      jail-version-untested
      """

  Scenario: Accepts the Kiro harness ai-memory gained in 1.24
    Given a validation configuration
      """
      agent: kiro-cli
      goos: linux
      jail: false
      memory: true
      permissions:
        gh: true
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      permission-without-jail
      """

  Scenario: Remaps a wrapper command to the ai-memory harness name
    Given a launch configuration
      """
      agent: oc
      run_harness: opencode
      jail: false
      memory: true
      """
    When the launch command is built
    Then the command equals
      """
      ai-memory
      run
      opencode
      """

  Scenario: Mounts the executable directory read-only inside the jail
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: true
      executable: /opt/tools/bin/claude
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      --map
      /opt/tools/bin
      ai-memory
      run
      --executable
      /opt/tools/bin/claude
      claude
      """

  Scenario: Emits private home, overlay maps, deny paths, tcp ports and claude dir
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: false
      jail_flags:
        lockdown: true
        private_home: true
        overlay_maps: [/data]
        deny_paths: [/proc/kcore]
        allow_tcp_ports: [8080]
        claude_dir: /home/tester/.claude
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      --lockdown
      --private-home
      --overlay-map
      /data
      --deny-path
      /proc/kcore
      --allow-tcp-port
      8080
      --claude-dir
      /home/tester/.claude
      claude
      """

  Scenario: Emits the soft browser profile
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: false
      jail_flags:
        browser: soft
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      --browser=soft
      claude
      """

  Scenario: Emits the negative form for browser off
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: false
      jail_flags:
        browser: off
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      --no-browser
      claude
      """

  Scenario: Continues the most recent session keeping workstream and yolo
    Given a launch configuration
      """
      continue: true
      jail: false
      memory: true
      workstream: sprint-3
      yolo: true
      """
    When the launch command is built
    Then the command equals
      """
      ai-memory
      run
      --workstream
      sprint-3
      --yolo
      """

  Scenario: Exposes registered worktrees outside the current project to Docker
    Given a launch configuration
      """
      agent: claude
      docker: true
      memory: false
      project_dir: /work/project
      stacks: [go]
      worktree_mounts:
        - /outside/feature
        - /work/project/nested
      """
    When the launch command is built
    Then the command equals
      """
      docker
      run
      --rm
      -it
      --cap-drop=ALL
      --security-opt=no-new-privileges:true
      -w
      /work/project
      -v
      /work/project:/work/project
      -v
      /outside/feature:/outside/feature
      --add-host=host.docker.internal:host-gateway
      ai-launcher-box:000000000000
      claude
      """

  Scenario: Produces no issues for a plain --no-jail launch
    Given a validation configuration
      """
      agent: claude
      goos: linux
      jail: false
      memory: false
      jail_exec: true
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      """

  Scenario: Warns when a read-only mount downgrades an enabled permission
    Given a validation configuration
      """
      agent: claude
      goos: linux
      jail: true
      memory: false
      home: /home/tester
      permissions:
        gh: true
      mounts:
        - path: /home/tester/.config
          mode: ro
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      mount-not-found
      permission-mount-downgraded
      """

  Scenario: Warns when a permission mount is omitted without a home
    Given a validation configuration
      """
      agent: claude
      goos: linux
      jail: true
      memory: false
      clear_home: true
      permissions:
        gh: true
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      permission-mount-without-home
      """

  Scenario: Runs an agent in a docker container with the same-path project mount
    Given a launch configuration
      """
      agent: claude
      home: /home/tester
      docker: true
      stacks: [go, python]
      project_dir: /home/tester/proj
      """
    When the launch command is built
    Then the command equals
      """
      docker
      run
      --rm
      -it
      --cap-drop=ALL
      --security-opt=no-new-privileges:true
      -e
      HOME=/home/tester
      -w
      /home/tester/proj
      -v
      /home/tester/proj:/home/tester/proj
      -v
      /home/tester/.claude:/home/tester/.claude
      -v
      /home/tester/.claude.json:/home/tester/.claude.json
      -v
      /home/tester/.claude/projects:/home/tester/.claude/projects
      -v
      /home/tester/.cache/go-build:/home/ai-launcher/.cache/go-build:rw
      -v
      /home/tester/go/pkg/mod:/home/ai-launcher/go/pkg/mod:rw
      -v
      /home/tester/.cache/pip:/home/ai-launcher/.cache/pip:rw
      -e
      GOCACHE=/home/ai-launcher/.cache/go-build
      -e
      GOMODCACHE=/home/ai-launcher/go/pkg/mod
      -e
      PIP_CACHE_DIR=/home/ai-launcher/.cache/pip
      --add-host=host.docker.internal:host-gateway
      ai-launcher-box:000000000000
      claude
      """

  Scenario: Maps docker permissions to read-only credential mounts
    Given a launch configuration
      """
      agent: codex
      home: /home/tester
      docker: true
      stacks: [rust]
      project_dir: /w
      permissions:
        ssh: true
        gh: true
        docker: true
      docker_socket_group: 20
      """
    When the launch command is built
    Then the command equals
      """
      docker
      run
      --rm
      -it
      --cap-drop=ALL
      --security-opt=no-new-privileges:true
      --group-add
      20
      -e
      HOME=/home/tester
      -w
      /w
      -v
      /w:/w
      -v
      /home/tester/.codex:/home/tester/.codex
      -v
      /home/tester/.ssh:/home/tester/.ssh:ro
      -v
      /home/tester/.config/gh:/home/tester/.config/gh:ro
      -v
      /var/run/docker.sock:/var/run/docker.sock
      -v
      /home/tester/.cargo/git:/home/ai-launcher/.cargo/git:rw
      -v
      /home/tester/.cargo/registry:/home/ai-launcher/.cargo/registry:rw
      -e
      CARGO_HOME=/home/ai-launcher/.cargo
      --add-host=host.docker.internal:host-gateway
      ai-launcher-box:000000000000
      codex
      """

  Scenario: Reports a missing docker CLI in preflight
    Given a validation configuration
      """
      agent: claude
      goos: linux
      docker: true
      stacks: [go]
      project_dir: /w
      missing_commands: [docker]
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      docker-not-found
      """

  Scenario: Reports an invalid docker image selection in preflight
    Given a validation configuration
      """
      agent: claude
      goos: linux
      docker: true
      stacks: [cobol]
      project_dir: /w
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      docker-selection-invalid
      """

  Scenario: An ai-memory scope name is never the container project directory
    Given a launch configuration
      """
      agent: claude
      home: /home/tester
      docker: true
      stacks: [go]
      workspace: acme
      project: billing
      project_dir: /work/checkout
      """
    When the launch command is built
    Then the command equals
      """
      docker
      run
      --rm
      -it
      --cap-drop=ALL
      --security-opt=no-new-privileges:true
      -e
      HOME=/home/tester
      -w
      /work/checkout
      -v
      /work/checkout:/work/checkout
      -v
      /home/tester/.claude:/home/tester/.claude
      -v
      /home/tester/.claude.json:/home/tester/.claude.json
      -v
      /home/tester/.claude/projects:/home/tester/.claude/projects
      -v
      /home/tester/.cache/go-build:/home/ai-launcher/.cache/go-build:rw
      -v
      /home/tester/go/pkg/mod:/home/ai-launcher/go/pkg/mod:rw
      -e
      GOCACHE=/home/ai-launcher/.cache/go-build
      -e
      GOMODCACHE=/home/ai-launcher/go/pkg/mod
      --add-host=host.docker.internal:host-gateway
      ai-launcher-box:000000000000
      claude
      """

  Scenario: Reports a relative container project directory in preflight
    Given a validation configuration
      """
      agent: claude
      goos: linux
      docker: true
      stacks: [go]
      project_dir: meu-time
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      docker-project-dir-not-absolute
      """

  Scenario: Runs docker with resource limits, published ports, and a network
    Given a launch configuration
      """
      agent: claude
      docker: true
      stacks: [go]
      project_dir: /work
      container_memory: 4g
      cpus: "2.0"
      pids: 512
      ports: [3000:3000]
      network: bridge
      """
    When the launch command is built
    Then the command equals
      """
      docker
      run
      --rm
      -it
      --memory
      4g
      --cpus
      2.0
      --pids-limit
      512
      -p
      3000:3000
      --network
      bridge
      --cap-drop=ALL
      --security-opt=no-new-privileges:true
      -w
      /work
      -v
      /work:/work
      --add-host=host.docker.internal:host-gateway
      ai-launcher-box:000000000000
      claude
      """

  Scenario: Runs docker with the host network
    Given a launch configuration
      """
      agent: claude
      docker: true
      stacks: [go]
      project_dir: /work
      network: host
      """
    When the launch command is built
    Then the command equals
      """
      docker
      run
      --rm
      -it
      --network
      host
      --cap-drop=ALL
      --security-opt=no-new-privileges:true
      -w
      /work
      -v
      /work:/work
      --add-host=host.docker.internal:host-gateway
      ai-launcher-box:000000000000
      claude
      """

  Scenario: Selects Podman as the configured container runtime
    Given a launch configuration
      """
      agent: claude
      docker: true
      runtime: podman
      stacks: [go]
      project_dir: /work
      """
    When the launch command is built
    Then the command equals
      """
      podman
      run
      --rm
      -it
      --cap-drop=ALL
      --security-opt=no-new-privileges:true
      -w
      /work
      -v
      /work:/work
      --add-host=host.containers.internal:host-gateway
      ai-launcher-box:000000000000
      claude
      """

  Scenario: Renders Compose YAML with selected infrastructure services
    Given a launch configuration
      """
      agent: claude
      docker: true
      stacks: [go]
      services: [postgres, redis]
      project_dir: /work
      """
    When the launch command is built
    Then the Compose YAML contains
      """
      services:
      agent:
      postgres:
      redis:
      """

  Scenario: Keeps native harness parameters when Docker also enables memory
    Given a launch configuration
      """
      agent: opencode
      docker: true
      memory: true
      yolo: true
      yolo_flag: --auto
      params:
        - name: model
          flag: --model
          takes_value: true
      param_values:
        model: qwen-token-plan
      stacks: [go]
      services: [redis]
      project_dir: /work
      """
    When the launch command is built
    Then the Compose YAML contains
      """
      command:
      - ai-memory
      - run
      - opencode
      - --model
      - qwen-token-plan
      - --yolo
      """

  Scenario: Renders Compose YAML with networks and project data mounts
    Given a launch configuration
      """
      agent: claude
      docker: true
      stacks: [go]
      services: [postgres]
      project_dir: /work
      """
    When the launch command is built
    Then the Compose YAML contains
      """
      networks:
      ai-launcher:
      /work/.ai-launcher/data/postgres:/var/lib/postgresql
      """

  Scenario: Renders Compose YAML with an internal network
    Given a launch configuration
      """
      agent: claude
      docker: true
      stacks: [go]
      services: [redis]
      project_dir: /work
      internal_network: true
      """
    When the launch command is built
    Then the Compose YAML contains
      """
      internal: true
      """

  Scenario: Renders Compose YAML with an egress-allowlist proxy
    Given a launch configuration
      """
      agent: claude
      docker: true
      stacks: [go]
      services: [redis]
      project_dir: /work
      internal_network: true
      allowed_domains: [api.anthropic.com]
      """
    When the launch command is built
    Then the Compose YAML contains
      """
      egress-proxy
      ai-launcher-egress
      HTTP_PROXY
      """

  Scenario: Warns when allowed domains restrict rather than block the agent
    Given a validation configuration
      """
      agent: claude
      goos: linux
      memory: false
      docker: true
      stacks: [go]
      services: [redis]
      project_dir: /w
      internal_network: true
      allowed_domains: [api.anthropic.com]
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      internal-network-restricts-agent
      """

  Scenario: Rejects when allowed domains are configured without the internal network
    Given a validation configuration
      """
      agent: claude
      goos: linux
      memory: false
      docker: true
      stacks: [go]
      services: [redis]
      project_dir: /w
      allowed_domains: [api.anthropic.com]
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      container-network-allowed-domains-without-internal-network
      """
