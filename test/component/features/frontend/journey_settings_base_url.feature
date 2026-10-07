@frontend @slow @settings-base-url
Feature: Settings API Base URL Journey
  Settings → Authentication sets anthropic_base_url, which routes the
  agents' Claude API calls through a gateway or proxy.

  Scenario: The API base URL is saved to the global config
    Given I open the app in a browser
    Then I wait for text "Settings" to appear

    When I open the settings panel and select "Authentication"
    And I clear and type "https://llm-gateway.example.com" into "input[placeholder='https://api.anthropic.com']"
    And I click button "Save" in the settings panel
    Then a GET to "/api/config" should soon contain "https://llm-gateway.example.com"

    # A fresh load reads it back into the form
    When I open the app in a browser
    And I wait for text "Settings" to appear
    And I open the settings panel and select "Authentication"
    Then the field "input[placeholder='https://api.anthropic.com']" should hold "https://llm-gateway.example.com"

    # Clear it again so other scenarios see the default
    When I clear the "input[placeholder='https://api.anthropic.com']" field
    And I click button "Save" in the settings panel
    Then a GET to "/api/config" should soon not contain "llm-gateway.example.com"
