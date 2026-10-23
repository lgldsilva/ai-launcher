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

  Scenario: Refuses a Linux jail when bubblewrap is missing
    Given a validation configuration
      """
      agent: claude
      goos: linux
      jail: true
      memory: false
      missing_commands: [bwrap]
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      bwrap-not-found
      """

  Scenario: Refuses a Linux jail whose only bwrap ai-jail would not trust
    Given a validation configuration
      """
      agent: claude
      goos: linux
      jail: true
      memory: false
      untrusted_commands: [bwrap]
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      bwrap-not-found
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

  # ai-jail 2.0 filtered egress replaces unrestricted --network. The host
  # list is only emitted when the detected jail is new enough to accept it.
  # With memory on, the server must be https and its host must be listed:
  # the proxy is CONNECT-only and answers plain HTTP with 405.
  Scenario: Emits allow-host and drops unrestricted network
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: false
      jail_version: "2.2.0"
      permissions:
        jail: true
        network: true
      jail_flags:
        allow_hosts:
          - api.anthropic.com
          - github.com
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      --allow-host
      api.anthropic.com
      --allow-host
      github.com
      claude
      """

  Scenario: Refuses allow_hosts on an ai-jail older than 2.0
    Given a validation configuration
      """
      agent: claude
      goos: linux
      jail: true
      memory: false
      jail_version: "1.20.1"
      jail_flags:
        allow_hosts: [api.anthropic.com]
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      allow-host-requires-ai-jail-2
      """

  Scenario: Refuses allow_hosts combined with unrestricted network
    Given a validation configuration
      """
      agent: claude
      goos: linux
      jail: true
      memory: false
      jail_version: "2.2.0"
      jail_flags:
        network: true
        allow_hosts: [api.anthropic.com]
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      allow-host-conflicts-with-network
      """

  Scenario: Refuses allow_hosts when memory would call plain HTTP
    Given a validation configuration
      """
      agent: claude
      goos: linux
      jail: true
      memory: true
      jail_version: "2.2.0"
      memory_server_url: http://127.0.0.1:49374
      jail_flags:
        allow_hosts: [api.anthropic.com]
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      allow-host-memory-needs-https
      """

  Scenario: Refuses allow_hosts that omit the https memory server
    Given a validation configuration
      """
      agent: claude
      goos: linux
      jail: true
      memory: true
      jail_version: "2.2.0"
      memory_server_url: https://aimemory.example
      jail_flags:
        allow_hosts: [api.anthropic.com]
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      allow-host-omits-memory-server
      """

  Scenario: Emits allow-host for an https memory server that is listed
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: true
      jail_version: "2.2.0"
      memory_version: "2.4.0"
      memory_server_url: https://aimemory.example
      permissions:
        network: true
      jail_flags:
        allow_hosts:
          - api.anthropic.com
          - aimemory.example
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      --allow-host
      api.anthropic.com
      --allow-host
      aimemory.example
      ai-memory
      run
      claude
      --no-autowire
      """

  Scenario: Emits --no-autowire after the harness inside the jail
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: true
      jail_version: "2.2.0"
      memory_version: "2.4.0"
      permissions:
        network: true
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --network
      ai-memory
      run
      claude
      --no-autowire
      """

  Scenario: Emits --no-autowire after the harness on ai-memory 2.3+
    Given a launch configuration
      """
      agent: claude
      jail: false
      memory: true
      memory_version: "2.4.0"
      fresh: true
      """
    When the launch command is built
    Then the command equals
      """
      ai-memory
      run
      claude
      --fresh
      --no-autowire
      """

  # ai-jail 2.6.0 turned agent state on by default (harness credential dirs
  # read-write, provider API keys copied from the host), so the same argv
  # built a different sandbox across 2.5.x and 2.6.x. From 2.6.0 the launcher
  # states the decision after the network decision; unset follows the
  # upstream default, which is on.
  Scenario: States the agent-state decision on ai-jail 2.6+
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: false
      jail_version: "2.6.0"
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      --agent-state
      claude
      """

  Scenario: Emits --no-agent-state when agent_state is false
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: false
      jail_version: "2.9.0"
      jail_flags:
        agent_state: false
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      --no-agent-state
      claude
      """

  Scenario: Lockdown forces --no-agent-state
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: false
      jail_version: "2.6.0"
      jail_flags:
        lockdown: true
        agent_state: true
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      --no-agent-state
      --lockdown
      claude
      """

  Scenario: Refuses a configured agent_state below ai-jail 2.6
    Given a validation configuration
      """
      agent: claude
      goos: linux
      jail: true
      memory: false
      jail_version: "2.5.9"
      jail_flags:
        agent_state: true
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      agent-state-requires-ai-jail-2-6
      """

  # The ai-jail 2.5.0 toggle batch. nil leaves ai-jail's own default in
  # charge (toolchains on, kvm and the credential mounts off); a configured
  # value forces its form, and only on a jail new enough to accept the flag.
  Scenario: Emits the ai-jail 2.5 toggles when configured
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: false
      jail_version: "2.9.0"
      jail_flags:
        toolchains: false
        kvm: true
        github: true
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      --agent-state
      --no-toolchains
      --kvm
      --github
      claude
      """

  Scenario: Refuses a 2.5 toggle below ai-jail 2.5
    Given a validation configuration
      """
      agent: claude
      goos: linux
      jail: true
      memory: false
      jail_version: "2.4.9"
      jail_flags:
        kvm: true
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      jail-toggles-require-ai-jail-2-5
      """

  # forward_ports (ai-jail 2.9.0) relays a host loopback port into the
  # sandbox under filtered egress — the same mode allow_hosts selects.
  Scenario: Emits forward ports on ai-jail 2.9
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: false
      jail_version: "2.9.0"
      jail_flags:
        forward_ports: [8080, 3000]
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      --agent-state
      --forward-port
      8080
      --forward-port
      3000
      claude
      """

  Scenario: Refuses forward ports with unrestricted network
    Given a validation configuration
      """
      agent: claude
      goos: linux
      jail: true
      memory: false
      jail_version: "2.9.0"
      jail_flags:
        forward_ports: [8080]
        network: true
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      forward-ports-conflict-with-network
      """

  Scenario: Refuses forward ports below ai-jail 2.9
    Given a validation configuration
      """
      agent: claude
      goos: linux
      jail: true
      memory: false
      jail_version: "2.8.1"
      jail_flags:
        forward_ports: [8080]
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      forward-ports-require-ai-jail-2-9
      """

  # allow_private_hosts (ai-jail 2.11.0) is --allow-host for a self-hosted
  # service on a private network; either list alone selects filtered egress.
  Scenario: Emits private allow hosts after the public ones on ai-jail 2.11
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: false
      jail_version: "2.11.0"
      jail_flags:
        allow_hosts: [api.anthropic.com]
        allow_private_hosts: [git.internal.example]
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      --agent-state
      --allow-host
      api.anthropic.com
      --allow-private-host
      git.internal.example
      claude
      """

  Scenario: Private allow hosts alone select filtered egress
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: false
      jail_version: "2.11.2"
      permissions:
        jail: true
        network: true
      jail_flags:
        allow_private_hosts: [git.internal.example]
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      --agent-state
      --allow-private-host
      git.internal.example
      claude
      """

  Scenario: Refuses private allow hosts below ai-jail 2.11
    Given a validation configuration
      """
      agent: claude
      goos: linux
      jail: true
      memory: false
      jail_version: "2.10.0"
      jail_flags:
        allow_private_hosts: [git.internal.example]
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      allow-private-host-requires-ai-jail-2-11
      """

  Scenario: Refuses private allow hosts combined with unrestricted network
    Given a validation configuration
      """
      agent: claude
      goos: linux
      jail: true
      memory: false
      jail_version: "2.11.0"
      jail_flags:
        allow_private_hosts: [git.internal.example]
        network: true
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      allow-host-conflicts-with-network
      """

  # ai-memory's own run options: --profile and --env-file precede the
  # harness, memory_jail is host-launch only (ai-memory ignores --jail inside
  # ai-jail and a container image has no ai-jail to run).
  Scenario: Emits the ai-memory run options before the harness on a host launch
    Given a launch configuration
      """
      agent: claude
      jail: false
      memory: true
      memory_version: "3.0.0"
      memory_profile: ci
      memory_env_file: /tmp/env
      memory_jail: github,aws
      """
    When the launch command is built
    Then the command equals
      """
      ai-memory
      run
      --profile
      ci
      --env-file
      /tmp/env
      --jail=github,aws
      claude
      --no-autowire
      """

  Scenario: Emits the ai-memory run options through a 2.9 jail with the env file mounted
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: true
      jail_version: "2.9.0"
      memory_version: "3.0.0"
      memory_profile: ci
      memory_env_file: /tmp/env
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      --agent-state
      --map
      /tmp/env
      ai-memory
      run
      --profile
      ci
      --env-file
      /tmp/env
      claude
      --no-autowire
      """

  Scenario: Refuses memory_profile below ai-memory 2.6
    Given a validation configuration
      """
      agent: claude
      goos: linux
      jail: false
      memory: true
      memory_version: "2.5.9"
      memory_profile: ci
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      memory-profile-requires-ai-memory-2-6
      """

  Scenario: Refuses a pre-harness run option through an old jail
    Given a validation configuration
      """
      agent: claude
      goos: linux
      jail: true
      memory: true
      jail_version: "2.2.0"
      memory_version: "3.0.0"
      memory_profile: ci
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      memory-run-flag-requires-ai-jail-2-9
      """

  Scenario: Refuses memory_jail below ai-memory 2.5.2
    Given a validation configuration
      """
      agent: claude
      goos: linux
      jail: false
      memory: true
      memory_version: "2.4.9"
      memory_jail: github
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      memory-jail-requires-ai-memory-2-5-2
      """

  Scenario: Warns when memory_jail meets the launcher jail
    Given a validation configuration
      """
      agent: claude
      goos: linux
      jail: true
      memory: true
      jail_version: "2.9.0"
      memory_version: "3.0.0"
      memory_jail: github
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      memory-jail-with-launcher-jail
      """

  # ai-jail 2.6.2 made --lockdown always force --unshare-net: the same
  # lockdown + network argv flipped from networked to offline. The sentinel
  # issue proves preflight ran on the release before the flip.
  Scenario: Warns when lockdown meets unrestricted network on ai-jail 2.6.2+
    Given a validation configuration
      """
      agent: claude
      goos: linux
      jail: true
      memory: false
      jail_version: "2.9.0"
      jail_flags:
        lockdown: true
        network: true
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      lockdown-forces-offline
      """

  Scenario: Lockdown keeps the network below the unshare flip
    Given a validation configuration
      """
      agent: claude
      goos: linux
      jail: false
      memory: false
      jail_version: "2.6.1"
      jail_flags:
        lockdown: true
        network: true
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      jail-options-without-jail
      """

  # The remaining toggle mirror: x11/host_shm are 1.18 opt-ins (no floor);
  # audio/terminal_passthrough/macos_host_ipc/update_check are floored at the
  # verified 2.9; audit_log at 2.0. secrets ride filtered egress only.
  Scenario: Emits the remaining ai-jail toggles when configured
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: false
      jail_version: "2.9.0"
      jail_flags:
        x11: true
        audio: true
        audit_log: true
        update_check: false
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      --agent-state
      --x11
      --audio
      --no-update-check
      --audit-log
      claude
      """

  Scenario: Emits secrets alongside filtered egress
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: false
      jail_version: "2.9.0"
      jail_flags:
        allow_hosts: [github.com]
        secrets: [GITHUB_TOKEN=github.com]
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      --agent-state
      --secret
      GITHUB_TOKEN=github.com
      --allow-host
      github.com
      claude
      """

  Scenario: Refuses secrets without filtered egress
    Given a validation configuration
      """
      agent: claude
      goos: linux
      jail: true
      memory: false
      jail_version: "2.9.0"
      jail_flags:
        secrets: [GITHUB_TOKEN=github.com]
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      secrets-require-filtered-egress
      """

  Scenario: Refuses a 2.9-verified toggle below its floor
    Given a validation configuration
      """
      agent: claude
      goos: linux
      jail: true
      memory: false
      jail_version: "2.8.1"
      jail_flags:
        audio: true
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      jail-toggles-require-ai-jail-2-9
      """

  Scenario: Emits env files on ai-jail 2.0+
    Given a launch configuration
      """
      agent: claude
      jail: true
      memory: false
      jail_version: "2.9.0"
      jail_flags:
        env_from_files: [/tmp/env]
      """
    When the launch command is built
    Then the command equals
      """
      ai-jail
      --no-docker
      --no-network
      --agent-state
      --env-from-file
      /tmp/env
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

  # copilot / copilot-cli arrived in ai-memory 3.0.0; the sentinel permission
  # proves preflight ran while the harness checks stay absent.
  Scenario: Accepts copilot on ai-memory 3.0
    Given a validation configuration
      """
      agent: copilot
      goos: linux
      jail: false
      memory: true
      memory_version: "3.0.0"
      permissions:
        gh: true
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      permission-without-jail
      """

  Scenario: Refuses copilot below ai-memory 3.0
    Given a validation configuration
      """
      agent: copilot
      goos: linux
      jail: false
      memory: true
      memory_version: "2.9.9"
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      memory-harness-version
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
      jail_version: "3.0.0"
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      jail-version-untested
      """

  # 2.9.0 is the newest ai-jail the argv was re-validated against; the
  # network conflict is the sentinel that preflight actually ran (an empty
  # expectation would also pass if it never did).
  Scenario: Accepts ai-jail 2.9.0 inside the validated range
    Given a validation configuration
      """
      agent: claude
      goos: linux
      jail: true
      memory: false
      jail_version: "2.9.0"
      jail_flags:
        network: true
        allow_hosts: [api.anthropic.com]
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      allow-host-conflicts-with-network
      """

  # 1.19.1 was inside the supported range until the floor moved to 1.20.1. On
  # macOS every release below that could exec a binary the sandbox never
  # vetted, so this is the one version whose verdict this bump inverts — the
  # scenario exists to make that inversion visible rather than incidental.
  Scenario: Refuses the ai-jail the 1.20.1 security floor left behind
    Given a validation configuration
      """
      agent: claude
      goos: linux
      jail: true
      memory: false
      jail_version: 1.19.1
      """
    When launcher preflight is checked
    Then issue codes equal
      """
      jail-version-too-old
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

  # The catalog spells Antigravity "agy" (its own executable name), while
  # ai-memory names the harness "antigravity" and accepts "agy" as an alias.
  # Pre-flight has to agree with clap on both spellings or it refuses a harness
  # the installed ai-memory would have taken.
  Scenario: Accepts the Antigravity harness ai-memory gained in 1.24
    Given a validation configuration
      """
      agent: agy
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

  # The catalog command is "kimi"; "kimi-cli" is the binary the installer also
  # ships, and ai-memory accepts it as an alias of kimi (since the 1.25 floor).
  # Pre-flight has to agree or PATH resolving kimi-cli refuses a harness the
  # installed ai-memory would have taken.
  Scenario: Accepts the kimi-cli alias of the Kimi harness
    Given a validation configuration
      """
      agent: kimi-cli
      goos: linux
      jail: false
      memory: true
      memory_version: "1.25.0"
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
