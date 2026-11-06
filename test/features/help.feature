Feature: Launcher help command
  The `help` positional prints the flag usage and exits without launching.

  Scenario: Shows the flag usage without launching
    Given the launcher binary built from this tree
    When the launcher is invoked with the arguments
      """
      help
      """
    Then the exit code is 0
    And the standard output is empty
    And the standard error starts with "Usage of ai-launcher:"
    And the usage lists the flag "agent"
    And the usage lists the flag "no-jail"
    And the usage lists the flag "dry-run"

  Scenario: Help intercepts a launch that selected an agent
    Given the launcher binary built from this tree
    When the launcher is invoked with the arguments
      """
      --agent
      claude
      help
      """
    Then the exit code is 0
    And the standard output is empty
    And the standard error starts with "Usage of ai-launcher:"
