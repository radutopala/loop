@backend @pprof
Feature: pprof listener
  The daemon serves its Go pprof profiles on a listener of their own while
  the pprof config block turns it on, without a restart.

  Scenario: Turning pprof on and off starts and stops its listener
    When I set the global pprof config to:
      """
      {"enabled": true, "addr": "127.0.0.1:16060"}
      """
    Then the daemon should soon serve pprof on "127.0.0.1:16060"

    # A new address moves the listener.
    When I set the global pprof config to:
      """
      {"enabled": true, "addr": "127.0.0.1:16061"}
      """
    Then the daemon should soon serve pprof on "127.0.0.1:16061"
    And nothing should soon listen on "127.0.0.1:16060"

    When I set the global pprof config to:
      """
      {"enabled": false, "addr": "127.0.0.1:16061"}
      """
    Then nothing should soon listen on "127.0.0.1:16061"
    And I remove the global pprof config
