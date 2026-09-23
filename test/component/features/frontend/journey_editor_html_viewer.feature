@frontend @slow @html
Feature: Editor HTML Viewer Journey
  Opening an HTML file in the editor renders it in a sandboxed frame, with
  relative assets resolved against the file's directory, and a switch to
  flip between the rendered page, a split view and the source.

  Scenario: An HTML file opens rendered and can be switched to its source
    Given I set up a test channel via API for git repo "bdd-editor-html"
    And I create a file "site/css/style.css" in the repo with:
      """
      h1 { color: rgb(1, 2, 3); }
      """
    And I create a file "site/index.html" in the repo with:
      """
      <!doctype html>
      <html>
      <head><link rel="stylesheet" href="css/style.css"></head>
      <body>
      <h1 id="title">Hello HTML</h1>
      <script>
      addEventListener("load", function () {
        parent.postMessage("color:" + getComputedStyle(document.getElementById("title")).color, "*");
      });
      </script>
      </body>
      </html>
      """
    And I open the app in a browser
    And I wait for text "bdd-editor-html" to appear
    And I record messages posted by frames

    When I click on "bdd-editor-html" in the sidebar
    And I wait for "textarea" to be visible

    And I click on "[data-testid='layout-tab-Editor']"
    And I open the file "site" in the editor tree
    And I open the file "index.html" in the editor tree

    # Opens rendered: scripts run and the relative stylesheet is applied
    Then I wait for "[data-testid='html-preview']" to be visible
    And I wait for a frame message "color:rgb(1, 2, 3)"

    # Source shows the markup and hides the frame
    When I click on "[data-testid='preview-mode-editor']"
    Then I wait for ".cm-content" to be visible
    And the element ".cm-content" should contain text "Hello HTML"
    And the element "[data-testid='html-preview']" should not exist

    # Split shows both side by side
    When I click on "[data-testid='preview-mode-both']"
    Then I wait for "[data-testid='html-preview']" to be visible
    And the element ".cm-content" should be visible
