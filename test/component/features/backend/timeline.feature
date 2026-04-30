@backend
Feature: Timeline endpoint
  GET /api/channels/{id}/timeline returns chat messages and agent events
  interleaved by chain_position with cursor pagination. Validates the
  endpoint's contract: happy-path empty response, the non-negative cursor
  parsers, the limit parser, and the explicit-zero-cursor first-page shortcut.

  Scenario: Empty channel returns an empty timeline page
    Given a channel exists for directory "/tmp/bdd-timeline-empty"
    When I send a GET request to "/api/channels/{channel_id}/timeline"
    Then the response status should be 200
    And the response JSON "items" should be "[]"
    And the response JSON "next_cursor" should be "<nil>"

  Scenario: Timeline rejects a negative cursor_position
    Given a channel exists for directory "/tmp/bdd-timeline-bad-pos"
    When I send a GET request to "/api/channels/{channel_id}/timeline?cursor_position=-1"
    Then the response status should be 400
    And the response should contain "invalid cursor_position"

  Scenario: Timeline rejects a negative cursor_id
    Given a channel exists for directory "/tmp/bdd-timeline-bad-id"
    When I send a GET request to "/api/channels/{channel_id}/timeline?cursor_id=-5"
    Then the response status should be 400
    And the response should contain "invalid cursor_id"

  Scenario: Timeline rejects a zero limit
    Given a channel exists for directory "/tmp/bdd-timeline-bad-limit"
    When I send a GET request to "/api/channels/{channel_id}/timeline?limit=0"
    Then the response status should be 400
    And the response should contain "invalid limit"

  Scenario: Timeline rejects a non-numeric limit
    Given a channel exists for directory "/tmp/bdd-timeline-non-numeric"
    When I send a GET request to "/api/channels/{channel_id}/timeline?limit=abc"
    Then the response status should be 400
    And the response should contain "invalid limit"

  Scenario: Timeline accepts an explicit zero cursor as the first-page shortcut
    Given a channel exists for directory "/tmp/bdd-timeline-zero-cursor"
    When I send a GET request to "/api/channels/{channel_id}/timeline?cursor_position=0&cursor_id=0"
    Then the response status should be 200
    And the response JSON "items" should be "[]"
