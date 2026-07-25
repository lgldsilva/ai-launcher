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
      --ssh
      --rw-map
      /home/tester/.config/gh
      --docker
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

  Scenario: Uses a relative GitHub CLI map when home is unavailable
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
      --rw-map
      .config/gh
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
      gpu-without-docker
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
          flag: --query
          takes_value: true
      param_values:
        query: explain this
      """
    When the launch command is built
    Then the command equals
      """
      kimi
      --query
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

  Scenario: Emits negative forms for disabled default-on jail toggles
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
      --no-gpu
      --no-landlock
      --no-seccomp
      --no-rlimits
      --no-status-bar
      claude
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
