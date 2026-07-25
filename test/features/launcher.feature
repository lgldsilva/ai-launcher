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
