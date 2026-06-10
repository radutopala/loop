@frontend @slow @gate-approval
Feature: Per-source gate approval routing
  When the backend emits gate.approval_requested with a `source` field,
  the FE renders the ApprovalCard on exactly one surface: ChatMessages
  for source="chat" (or empty, for back-compat with older proxies), or
  the matching Terminal pane overlay for source="terminal:<leafId>".
  Concurrent gates from different surfaces are tracked in a per-source
  map so they coexist; resolving one leaves the other untouched.

  Background:
    Given I set up a test channel via API for directory "/tmp/bdd-gate-approval"
    And I open the app in a browser
    And I wait for text "bdd-gate-approval" to appear
    And I click on "bdd-gate-approval" in the sidebar
    And I wait for "textarea" to be visible

  Scenario: Chat-sourced gate renders the ApprovalCard in chat
    When I inject a gate.approval_requested event with req_id "gate-chat-A", source "chat", and target "/tmp/bdd-gate-chat-A.txt"
    Then I wait for text "/tmp/bdd-gate-chat-A.txt" to appear
    And the page should contain text "Allow once"
    And the page should contain text "Allow for session"
    And the page should contain text "Deny with prompt"

  Scenario: Missing source defaults to chat (back-compat)
    # Older agentgate builds and the non-Linux stub omit `source` from the
    # event. useChatStateStore treats empty/missing as "chat" so the card
    # still appears in the chat region rather than vanishing.
    When I inject a gate.approval_requested event with req_id "gate-chat-B", source "", and target "/tmp/bdd-gate-default-chat.txt"
    Then I wait for text "/tmp/bdd-gate-default-chat.txt" to appear

  Scenario: Terminal-sourced gate renders inside the matching pane
    # "newest-docker-agent" resolves to the just-added pane's real leaf id —
    # the counter's start value depends on which features ran earlier in the
    # suite, so the first added pane is not always docker-agent-1.
    When I add a "Docker Agent" panel
    And I inject a gate.approval_requested event with req_id "gate-term-A", source "terminal:newest-docker-agent", and target "/tmp/bdd-gate-term-A.txt"
    Then I wait for text "/tmp/bdd-gate-term-A.txt" to appear
    And the page should contain text "Deny with prompt"

  Scenario: Gate for a non-existent terminal pane renders nowhere
    # source="terminal:agent-99" doesn't match any pane (the layout has none
    # of that id) and is not "chat" — so neither ChatMessages nor any
    # Terminal pane should mount an ApprovalCard for it.
    When I inject a gate.approval_requested event with req_id "gate-orphan", source "terminal:agent-99", and target "/tmp/bdd-gate-orphan.txt"
    And I wait "300ms"
    Then the page should not contain text "/tmp/bdd-gate-orphan.txt"

  Scenario: Chat and terminal gates coexist on different surfaces
    # The key regression this feature fixes: a terminal-sourced gate no longer
    # evicts a chat-sourced one (singleton state was the previous bug).
    When I add a "Docker Agent" panel
    And I inject a gate.approval_requested event with req_id "gate-chat-C", source "chat", and target "/tmp/bdd-gate-parallel-chat.txt"
    And I inject a gate.approval_requested event with req_id "gate-term-C", source "terminal:newest-docker-agent", and target "/tmp/bdd-gate-parallel-term.txt"
    Then I wait for text "/tmp/bdd-gate-parallel-chat.txt" to appear
    And I wait for text "/tmp/bdd-gate-parallel-term.txt" to appear

  Scenario: Resolving the chat gate leaves the terminal gate intact
    When I add a "Docker Agent" panel
    And I inject a gate.approval_requested event with req_id "gate-chat-D", source "chat", and target "/tmp/bdd-gate-resolve-chat.txt"
    And I inject a gate.approval_requested event with req_id "gate-term-D", source "terminal:newest-docker-agent", and target "/tmp/bdd-gate-resolve-term.txt"
    And I wait for text "/tmp/bdd-gate-resolve-chat.txt" to appear
    And I wait for text "/tmp/bdd-gate-resolve-term.txt" to appear
    When I inject a gate.approval_resolved event with req_id "gate-chat-D"
    Then I wait for text "/tmp/bdd-gate-resolve-chat.txt" to disappear
    And the page should contain text "/tmp/bdd-gate-resolve-term.txt"
