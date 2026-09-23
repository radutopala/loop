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

  Scenario: Going back to an HTML tab from a markdown tab renders the page
    Given I set up a test channel via API for git repo "bdd-editor-html-back"
    And I create a file "notes.md" in the repo with:
      """
      # Notes
      """
    And I create a file "page.html" in the repo with:
      """
      <!doctype html>
      <html>
      <body>
      <h1>Back again</h1>
      <script>
      addEventListener("load", function () {
        parent.postMessage("loaded:" + document.querySelector("h1").textContent, "*");
      });
      </script>
      </body>
      </html>
      """
    And I open the app in a browser
    And I wait for text "bdd-editor-html-back" to appear

    When I click on "bdd-editor-html-back" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "[data-testid='layout-tab-Editor']"

    # Pin the HTML tab (an edit promotes it) so it stays open, then move to markdown
    And I open the file "page.html" in the editor tree
    And I click on "[data-testid='preview-mode-editor']"
    And I append " " to the code editor
    And I click on "[data-testid='preview-mode-preview']"
    And I open the file "notes.md" in the editor tree
    And I wait for ".readme-content" to be visible

    # Switching back must load the page, not leave the frame blank
    And I record messages posted by frames
    And I click on "button[title='page.html']"
    Then I wait for "[data-testid='html-preview']" to be visible
    And I wait for a frame message "loaded:Back again"

  Scenario: The first HTML file opened loads in a fresh frame
    Given I set up a test channel via API for git repo "bdd-editor-html-first"
    And I create a file "output/page.html" in the repo with:
      """
      <!doctype html>
      <html>
      <body>
      <h1>First open</h1>
      <script>
      addEventListener("load", function () {
        parent.postMessage("loaded:" + document.querySelector("h1").textContent, "*");
      });
      </script>
      </body>
      </html>
      """
    And I open the app in a browser
    And I wait for text "bdd-editor-html-first" to appear

    When I click on "bdd-editor-html-first" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "[data-testid='layout-tab-Editor']"
    And I open the file "output" in the editor tree

    # The frame mounts, hidden, while the file is still loading, with an empty document
    And I delay requests matching "*/file\?path=output*" by "1s"
    And I record messages posted by frames
    And I open the file "page.html" in the editor tree
    And I mark the element "[data-testid='html-preview']"

    # The page loads in a new frame, not by handing the empty one a new document:
    # Chromium can drop that update and leave the frame blank
    Then I wait for "[data-testid='html-preview']" to be visible
    And I wait for a frame message "loaded:First open"
    And the element "[data-testid='html-preview']" should not be marked
