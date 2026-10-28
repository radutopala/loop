@backend
Feature: PR watching
  Loop watches the checks of a thread's PR/MR. A failure on a commit the
  thread's agent didn't make lights the thread's ci pill, any merge conflict
  its conflict pill and the PR/MR tab only, new unresolved review threads
  notify the owner and light its cmt pill, and the owner can arm "merge when
  ready". The forge is a fake gh or glab on the daemon's PATH.

  Scenario Outline: A failure on a commit that isn't the agent's needs the owner
    Given I set up a test channel via API for git repo "bdd-pr-fail-<cli>"
    And the channel's repo has origin "<origin>"
    And <cli> reports <label> with a failing "lint" check

    When I send a GET request to "/api/channels/{channel_id}/pr/status"
    Then the response status should be 200
    And the response JSON "present" should be "true"
    And the response JSON "pr.kind" should be "<kind>"
    And the response JSON "pr.checks.failed" should be "1"
    And the response JSON "pr.failed.0.name" should be "lint"
    And the response JSON "pr.pr_attention" should be "failed"
    And the response JSON "pr.merge_method" should be "squash"
    And the channel list should soon show the current channel's "pr_attention" as "failed"
    And a GET to "/api/channels/{channel_id}/pr/status" should soon have JSON "pr.unresolved_threads" equal to "1"
    And the channel list should soon show the current channel's "unresolved_threads" as "1"
    And a GET to "/api/notifications?channel_id={channel_id}" should soon have JSON "notifications.0.title" equal to "<label>: lint failed; 1 unresolved review thread"

    Examples:
      | cli  | origin                                                | label | kind |
      | gh   | https://github.com/example/repo.git                   | PR #7 | pr   |
      | glab | https://gitlab.example.com/example/group/project.git  | MR !7 | mr   |

  Scenario Outline: A merge conflict is the owner's to resolve
    Given I set up a test channel via API for git repo "bdd-pr-conflict-<cli>"
    And the channel's repo has origin "<origin>"
    And <cli> reports <label> with a failing "lint" check and a merge conflict

    When I send a GET request to "/api/channels/{channel_id}/pr/status"
    Then the response status should be 200
    And the response JSON "pr.merge_state" should be "conflict"
    And a GET to "/api/channels/{channel_id}/pr/status" should soon have JSON "pr.pr_attention" equal to "conflict"
    And the response JSON "pr.base_ref" should be "main"
    And the channel list should soon show the current channel's "pr_attention" as "conflict"
    And a GET to "/api/notifications?channel_id={channel_id}" should soon have JSON "notifications.0.title" equal to "<label>: 1 unresolved review thread"

    Examples:
      | cli  | origin                                                | label | kind |
      | gh   | https://github.com/example/repo.git                   | PR #7 | pr   |
      | glab | https://gitlab.example.com/example/group/project.git  | MR !7 | mr   |

  Scenario Outline: Merge when ready arms the forge's auto-merge and cancels it
    Given I set up a test channel via API for git repo "bdd-pr-merge-<cli>"
    And the channel's repo has origin "<origin>"
    And <cli> reports <label> with a failing "lint" check

    When I send a POST request to "/api/channels/{channel_id}/pr/merge" with body:
      """
      {"when_ready": true}
      """
    Then the response status should be 204
    And <cli> should have been called with "<arm>"
    And a GET to "/api/channels/{channel_id}/pr/status" should soon have JSON "pr.merge_armed" equal to "squash"

    When I send a DELETE request to "/api/channels/{channel_id}/pr/merge"
    Then the response status should be 204
    And <cli> should have been called with "<cancel>"
    And a GET to "/api/channels/{channel_id}/pr/status" should soon have JSON "pr.merge_armed" equal to ""

    Examples:
      | cli  | origin                                               | label | arm                                                                                                  | cancel                                                                                          |
      | gh   | https://github.com/example/repo.git                  | PR #7 | pr merge 7 --auto --squash                                                                           | pr merge 7 --disable-auto                                                                       |
      | glab | https://gitlab.example.com/example/group/project.git | MR !7 | api --method PUT projects/example%2Fgroup%2Fproject/merge_requests/7/merge -F squash=true -F auto_merge=true | api --method POST projects/example%2Fgroup%2Fproject/merge_requests/7/cancel_merge_when_pipeline_succeeds |

  Scenario Outline: The PR tab explains the failure, re-runs it and tails its log
    Given I set up a test channel via API for git repo "bdd-pr-tab-<cli>"
    And the channel's repo has origin "<origin>"
    And <cli> reports <label> with a failing "lint" check

    When I send a GET request to "/api/channels/{channel_id}/pr/status"
    Then the response status should be 200
    And the response JSON "pr.reason" should be "not_agent_commit"
    And the response JSON "pr.failed.0.id" should be "<check>"

    When I send a POST request to "/api/channels/{channel_id}/pr/rerun" with body:
      """
      {}
      """
    Then the response status should be 204
    And <cli> should have been called with "<rerun>"

    When I send a GET request to "/api/channels/{channel_id}/pr/log?check=<check>"
    Then the response status should be 200
    And the response should contain "missing comma"

    When I send a GET request to "/api/channels/{channel_id}/pr/log?check=404"
    Then the response status should be 404

    Examples:
      | cli  | origin                                               | label | check | rerun                                                                    |
      | gh   | https://github.com/example/repo.git                  | PR #7 | 123   | run rerun 9 --failed                                                     |
      | glab | https://gitlab.example.com/example/group/project.git | MR !7 | 981   | api --method POST projects/example%2Fgroup%2Fproject/jobs/981/retry |

  Scenario: A branch without a PR has no CI
    Given I set up a test channel via API for git repo "bdd-pr-none"

    When I send a GET request to "/api/channels/{channel_id}/pr/status"
    Then the response status should be 200
    And the response JSON "present" should be "false"

    When I send a POST request to "/api/channels/{channel_id}/pr/fix" with body:
      """
      {}
      """
    Then the response status should be 404
