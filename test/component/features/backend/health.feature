@backend
Feature: Health endpoint
  The Loop API exposes a health endpoint for monitoring.

  Scenario: Health check returns OK
    When I send a GET request to "/api/health"
    Then the response status should be 200
    And the response JSON "status" should be "ok"
