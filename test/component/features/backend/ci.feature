@backend
Feature: CI watching
  Loop watches the checks of a thread's PR/MR. A failure on a commit the
  thread's agent didn't make lights the thread's ci pill, and the owner can
  arm "merge when ready". The forge is a fake gh or glab on the daemon's PATH.

  Scenario Outline: A failure on a commit that isn't the agent's needs the owner
    Given I set up a test channel via API for git repo "bdd-ci-fail-<cli>"
    And the channel's repo has origin "<origin>"
    And <cli> reports <label> with a failing "lint" check

    When I send a GET request to "/api/channels/{channel_id}/ci"
    Then the response status should be 200
    And the response JSON "present" should be "true"
    And the response JSON "ci.kind" should be "<kind>"
    And the response JSON "ci.checks.failed" should be "1"
    And the response JSON "ci.failed.0.name" should be "lint"
    And the response JSON "ci.ci_attention" should be "failed"
    And the response JSON "ci.merge_method" should be "squash"
    And the channel list should soon show the current channel's "ci_attention" as "failed"

    Examples:
      | cli  | origin                                                | label | kind |
      | gh   | https://github.com/example/repo.git                   | PR #7 | pr   |
      | glab | https://gitlab.example.com/example/group/project.git  | MR !7 | mr   |

  Scenario Outline: Merge when ready arms the forge's auto-merge and cancels it
    Given I set up a test channel via API for git repo "bdd-ci-merge-<cli>"
    And the channel's repo has origin "<origin>"
    And <cli> reports <label> with a failing "lint" check

    When I send a POST request to "/api/channels/{channel_id}/pr/merge" with body:
      """
      {"when_ready": true}
      """
    Then the response status should be 204
    And <cli> should have been called with "<arm>"
    And a GET to "/api/channels/{channel_id}/ci" should soon have JSON "ci.merge_armed" equal to "squash"

    When I send a DELETE request to "/api/channels/{channel_id}/pr/merge"
    Then the response status should be 204
    And <cli> should have been called with "<cancel>"
    And a GET to "/api/channels/{channel_id}/ci" should soon have JSON "ci.merge_armed" equal to ""

    Examples:
      | cli  | origin                                               | label | arm                                                                                                  | cancel                                                                                          |
      | gh   | https://github.com/example/repo.git                  | PR #7 | pr merge 7 --auto --squash                                                                           | pr merge 7 --disable-auto                                                                       |
      | glab | https://gitlab.example.com/example/group/project.git | MR !7 | api --method PUT projects/example%2Fgroup%2Fproject/merge_requests/7/merge -F squash=true -F auto_merge=true | api --method POST projects/example%2Fgroup%2Fproject/merge_requests/7/cancel_merge_when_pipeline_succeeds |

  Scenario Outline: The CI tab explains the failure, re-runs it and tails its log
    Given I set up a test channel via API for git repo "bdd-ci-tab-<cli>"
    And the channel's repo has origin "<origin>"
    And <cli> reports <label> with a failing "lint" check

    When I send a GET request to "/api/channels/{channel_id}/ci"
    Then the response status should be 200
    And the response JSON "ci.reason" should be "not_agent_commit"
    And the response JSON "ci.failed.0.id" should be "<check>"

    When I send a POST request to "/api/channels/{channel_id}/ci/rerun" with body:
      """
      {}
      """
    Then the response status should be 204
    And <cli> should have been called with "<rerun>"

    When I send a GET request to "/api/channels/{channel_id}/ci/log?check=<check>"
    Then the response status should be 200
    And the response should contain "missing comma"

    When I send a GET request to "/api/channels/{channel_id}/ci/log?check=404"
    Then the response status should be 404

    Examples:
      | cli  | origin                                               | label | check | rerun                                                                    |
      | gh   | https://github.com/example/repo.git                  | PR #7 | 123   | run rerun 9 --failed                                                     |
      | glab | https://gitlab.example.com/example/group/project.git | MR !7 | 981   | api --method POST projects/example%2Fgroup%2Fproject/jobs/981/retry |

  Scenario: A branch without a PR has no CI
    Given I set up a test channel via API for git repo "bdd-ci-none"

    When I send a GET request to "/api/channels/{channel_id}/ci"
    Then the response status should be 200
    And the response JSON "present" should be "false"

    When I send a POST request to "/api/channels/{channel_id}/ci/fix" with body:
      """
      {}
      """
    Then the response status should be 404
