@frontend @slow
Feature: Files Panel Journey
  End-to-end journey verifying the standalone Files panel renders the
  workspace tree, exposes its toolbar, and lists workspace files.

  Scenario: Files panel can be added to a channel and lists workspace files
    Given I set up a test channel via API for git repo "bdd-files-panel"
    And I open the app in a browser
    And I wait for text "bdd-files-panel" to appear
    And I record requests matching "/files\?"
    When I click on "bdd-files-panel" in the sidebar
    Then I wait for "textarea" to be visible

    # Without a Files panel in the layout, the chat doesn't list the workspace
    And no recorded request should be sent

    # Add the standalone Files panel
    When I add a "Files" panel
    Then the element "[data-testid='file-tree-panel']" should be visible
    And I wait for text "WORKSPACE" to appear

    # Refresh and new-file buttons are exposed by the Files panel toolbar
    And the element "button[aria-label='Refresh files']" should be visible
    And the element "button[aria-label='New file']" should be visible

    # The workspace root is auto-expanded; the seeded README.md is visible
    Then I wait for text "README.md" to appear

    # Refreshing the tree keeps README.md visible
    When I click on "button[aria-label='Refresh files']"
    Then the page should contain text "README.md"

  @files-tree-actions
  Scenario: Toolbar buttons explain themselves on hover
    Given I set up a test channel via API for git repo "bdd-files-tips"
    And I open the app in a browser
    And I wait for text "bdd-files-tips" to appear
    When I click on "bdd-files-tips" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "[data-testid='layout-tab-Editor']"
    Then I wait for text "README.md" to appear
    When I rest the pointer on the element "button[aria-label='Refresh files']"
    Then I wait for "[data-testid='hover-tip']" to be visible
    And the element "[data-testid='hover-tip']" should contain text "Refresh files"
    When I rest the pointer on the element "button[aria-label='New file']"
    Then the element "[data-testid='hover-tip']" should contain text "New file"

  @files-tree-actions
  Scenario: Searching finds files and directories in the workspace
    Given I set up a test channel via API for git repo "bdd-files-search"
    And I create a file "docs/guide/notes.md" in the repo with:
      """
      # Notes
      """
    And I open the app in a browser
    And I wait for text "bdd-files-search" to appear
    When I click on "bdd-files-search" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "[data-testid='layout-tab-Editor']"
    Then I wait for text "README.md" to appear

    # A file result opens in the editor
    When I type "notes" into "[data-testid='file-tree-search']"
    Then I wait for "[data-testid='file-search-result'][data-path='0:docs/guide/notes.md']" to be visible
    When I click on "[data-testid='file-search-result'][data-path='0:docs/guide/notes.md']"
    Then I wait for "[data-testid='editor-tab'][data-path='docs/guide/notes.md']" to be visible

    # A directory result closes the search onto that directory, expanded
    When I clear and type "guide" into "[data-testid='file-tree-search']"
    Then I wait for "[data-testid='file-search-result'][data-path='0:docs/guide']" to be visible
    When I click on "[data-testid='file-search-result'][data-path='0:docs/guide']"
    Then I wait for "[data-testid='file-tree-entry'][data-path='0:docs/guide/notes.md']" to be visible
    And the element "[data-testid='file-search-result']" should not exist

    # Nothing to show, and Escape brings the tree back
    When I type "zzqqxx" into "[data-testid='file-tree-search']"
    Then I wait for text "Nothing matches" to appear
    When I press Escape
    Then I wait for text "README.md" to appear

  @files-tree-actions
  Scenario: Renaming a file from the context menu moves its open tab
    Given I set up a test channel via API for git repo "bdd-files-rename"
    And I create a file "old-name.txt" in the repo with:
      """
      keep me
      """
    And I open the app in a browser
    And I wait for text "bdd-files-rename" to appear
    When I click on "bdd-files-rename" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "[data-testid='layout-tab-Editor']"
    Then I wait for "[data-testid='file-tree-entry'][data-path='0:old-name.txt']" to be visible
    When I double-click on "[data-testid='file-tree-entry'][data-path='0:old-name.txt']"
    Then I wait for "[data-testid='editor-tab'][data-path='old-name.txt']" to be visible
    When I right-click on "[data-testid='file-tree-entry'][data-path='0:old-name.txt']"
    And I click on "Rename" in the context menu
    Then I wait for "[data-testid='file-tree-rename']" to be visible
    When I clear and type "new-name.txt" into "[data-testid='file-tree-rename']"
    And I press Enter
    Then I wait for "[data-testid='file-tree-entry'][data-path='0:new-name.txt']" to be visible
    And I wait for "[data-testid='editor-tab'][data-path='new-name.txt']" to be visible
    And the file "new-name.txt" in the repo should contain "keep me"
    And the file "old-name.txt" in the repo should not exist

    # Renaming onto an existing name fails inline and keeps the file
    When I right-click on "[data-testid='file-tree-entry'][data-path='0:new-name.txt']"
    And I click on "Rename" in the context menu
    And I clear and type "README.md" into "[data-testid='file-tree-rename']"
    And I press Enter
    Then I wait for text "README.md already exists" to appear
    And the file "new-name.txt" in the repo should exist

  @files-tree-actions
  Scenario: Deleting from the context menu asks first
    Given I set up a test channel via API for git repo "bdd-files-delete"
    And I create a file "doomed.txt" in the repo with:
      """
      bye
      """
    And I open the app in a browser
    And I wait for text "bdd-files-delete" to appear
    When I click on "bdd-files-delete" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "[data-testid='layout-tab-Editor']"
    Then I wait for "[data-testid='file-tree-entry'][data-path='0:doomed.txt']" to be visible

    # No keeps the file
    When I right-click on "[data-testid='file-tree-entry'][data-path='0:doomed.txt']"
    And I click on "Delete" in the context menu
    Then I wait for "[data-testid='context-menu-confirm']" to be visible
    And the element "[data-testid='context-menu-confirm']" should contain text "Delete doomed.txt?"
    When I click on "[data-testid='context-menu-confirm'] [data-testid='confirm-no']"
    Then the element "[data-testid='context-menu-confirm']" should not exist
    And the file "doomed.txt" in the repo should exist

    # Yes removes it
    When I click on "Delete" in the context menu
    And I click on "[data-testid='context-menu-confirm'] [data-testid='confirm-yes']"
    Then the file "doomed.txt" in the repo should not exist
    And I wait up to "5s" for "[data-testid='file-tree-entry'][data-path='0:doomed.txt']" to disappear
