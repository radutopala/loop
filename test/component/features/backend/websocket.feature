@backend @websocket
Feature: WebSocket event stream
  The events WebSocket streams real-time updates to clients.

  Scenario: Connect and disconnect from events WebSocket
    When I connect to the events WebSocket
    Then the WebSocket connection should be established
    When I close the WebSocket connection
