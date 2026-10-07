@frontend @slow @editortabs
Feature: Editor Tabs Journey
  Open editor tabs follow the agent's edits: a file the agent rewrites is
  reloaded and its tab marked until the user looks at it. Tabs with the same
  file name show enough of their folder to tell them apart, and a tab's
  context menu copies its path.

  Scenario: Agent edits reload open tabs and mark them until viewed
    Given I set up a test channel via API for git repo "bdd-editor-agent-edits"
    And I create a file "a.txt" in the repo with:
      """
      a first version
      """
    And I create a file "b.txt" in the repo with:
      """
      b first version
      """
    And I open the app in a browser
    And I wait for text "bdd-editor-agent-edits" to appear

    When I click on "bdd-editor-agent-edits" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "[data-testid='layout-tab-Editor']"
    # Pin a.txt so opening b.txt adds a tab instead of replacing it
    And I open the file "a.txt" in the editor tree
    And I double-click on "[data-testid='editor-tab'][data-path='a.txt']"
    And I open the file "b.txt" in the editor tree
    And the element ".cm-content" should contain text "b first version"

    # The agent rewrites both files: Edit/Write announce the path on tool.use
    # and the editor re-reads it on the matching successful tool.result
    And I create a file "a.txt" in the repo with:
      """
      a second version
      """
    And I create a file "b.txt" in the repo with:
      """
      b second version
      """
    And I inject a "tool.use" event for the channel with data:
      """
      {"tool_use_id":"bdd-edit-a","tool_name":"Edit","input":"{repo_path}/a.txt"}
      """
    And I inject a "tool.result" event for the channel with data:
      """
      {"tool_use_id":"bdd-edit-a","output":"ok","is_error":false}
      """
    And I inject a "tool.use" event for the channel with data:
      """
      {"tool_use_id":"bdd-write-b","tool_name":"Write","input":"{repo_path}/b.txt"}
      """
    And I inject a "tool.result" event for the channel with data:
      """
      {"tool_use_id":"bdd-write-b","output":"ok","is_error":false}
      """

    # The active tab reloads in place; both tabs carry the agent-edited dot
    Then the element ".cm-content" should contain text "b second version"
    And I wait for "[data-testid='editor-tab'][data-path='a.txt'] [data-testid='editor-tab-agent-edited']" to be visible
    And I wait for "[data-testid='editor-tab'][data-path='b.txt'] [data-testid='editor-tab-agent-edited']" to be visible

    # Clicking the active tab acknowledges it; switching to a tab does too
    When I click on "[data-testid='editor-tab'][data-path='b.txt']"
    Then the element "[data-testid='editor-tab'][data-path='b.txt'] [data-testid='editor-tab-agent-edited']" should not exist
    When I click on "[data-testid='editor-tab'][data-path='a.txt']"
    Then the element ".cm-content" should contain text "a second version"
    And the element "[data-testid='editor-tab'][data-path='a.txt'] [data-testid='editor-tab-agent-edited']" should not exist

  Scenario: A failed agent edit leaves the tab alone
    Given I set up a test channel via API for git repo "bdd-editor-agent-edit-failed"
    And I create a file "a.txt" in the repo with:
      """
      a first version
      """
    And I open the app in a browser
    And I wait for text "bdd-editor-agent-edit-failed" to appear

    When I click on "bdd-editor-agent-edit-failed" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "[data-testid='layout-tab-Editor']"
    And I open the file "a.txt" in the editor tree
    And the element ".cm-content" should contain text "a first version"
    And I inject a "tool.use" event for the channel with data:
      """
      {"tool_use_id":"bdd-edit-fail","tool_name":"Edit","input":"{repo_path}/a.txt"}
      """
    And I inject a "tool.result" event for the channel with data:
      """
      {"tool_use_id":"bdd-edit-fail","output":"old_string not found","is_error":true}
      """
    Then the element "[data-testid='editor-tab-agent-edited']" should not exist

  Scenario: Same-named tabs show their folders, and a tab's menu copies its path
    Given I set up a test channel via API for git repo "bdd-editor-tab-labels"
    And I create a file "web/src/index.ts" in the repo with:
      """
      export const side = "web";
      """
    And I create a file "server/src/index.ts" in the repo with:
      """
      export const side = "server";
      """
    And I open the app in a browser
    And I wait for text "bdd-editor-tab-labels" to appear

    When I click on "bdd-editor-tab-labels" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "[data-testid='layout-tab-Editor']"
    # Tree clicks match by text, so only one folder is expanded at a time
    And I open the file "server" in the editor tree
    And I open the file "src" in the editor tree
    And I open the file "index.ts" in the editor tree
    And I double-click on "[data-testid='editor-tab'][data-path='server/src/index.ts']"
    And I open the file "server" in the editor tree
    And I open the file "web" in the editor tree
    And I open the file "src" in the editor tree
    And I open the file "index.ts" in the editor tree
    And I wait for "[data-testid='editor-tab'][data-path='web/src/index.ts']" to be visible

    # Both are "index.ts"; the parent folder alone ("src") wouldn't tell them apart
    Then the element "[data-testid='editor-tab'][data-path='web/src/index.ts']" should contain text "web/src"
    And the element "[data-testid='editor-tab'][data-path='server/src/index.ts']" should contain text "server/src"

    When I record clipboard writes
    And I right-click on "[data-testid='editor-tab'][data-path='server/src/index.ts']"
    And I click on "Copy relative path" in the context menu
    Then the clipboard should hold "server/src/index.ts"
    When I right-click on "[data-testid='editor-tab'][data-path='server/src/index.ts']"
    And I click on "Copy absolute path" in the context menu
    Then the clipboard should hold "{repo_path}/server/src/index.ts"
