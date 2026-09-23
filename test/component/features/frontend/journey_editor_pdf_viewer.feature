@frontend @slow @pdf
Feature: Editor PDF Viewer Journey
  Opening a PDF in the editor renders its pages with pdf.js, with a page
  indicator and a selectable text layer, instead of a "Binary file" notice.

  Scenario: A PDF opens as rendered pages with selectable text
    Given I set up a test channel via API for git repo "bdd-editor-pdf"
    And I create a 2-page PDF "guide.pdf" reading "Hello PDF page" in the repo
    And I open the app in a browser
    And I wait for text "bdd-editor-pdf" to appear

    When I click on "bdd-editor-pdf" in the sidebar
    And I wait for "textarea" to be visible

    And I click on "[data-testid='layout-tab-Editor']"
    And I open the file "guide.pdf" in the editor tree

    # Both pages are laid out and the first one's text layer is rendered
    Then I wait for "[data-testid='pdf-toolbar']" to be visible
    And I wait for text "Page 1 / 2" to appear
    And I wait for text "Hello PDF page 1" to appear
    And the element "[data-pdf-page='2']" should be visible
    And the page should not contain text "Binary file"
