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
