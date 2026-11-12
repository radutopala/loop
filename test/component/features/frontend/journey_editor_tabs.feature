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

  @editor-live-reload
  Scenario: Changes made outside Loop reload open tabs and the file tree
    Given I set up a test channel via API for git repo "bdd-editor-live-reload"
    And I create a file "a.txt" in the repo with:
      """
      a first version
      """
    And I open the app in a browser
    And I wait for text "bdd-editor-live-reload" to appear

    When I click on "bdd-editor-live-reload" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "[data-testid='layout-tab-Editor']"
    And I open the file "a.txt" in the editor tree
    And the element ".cm-content" should contain text "a first version"

    # Written straight to disk, with no agent tool event: the file watcher
    # reloads the clean tab and marks it
    When I create a file "a.txt" in the repo with:
      """
      a second version
      """
    Then the element ".cm-content" should contain text "a second version"
    And I wait for "[data-testid='editor-tab'][data-path='a.txt'] [data-testid='editor-tab-agent-edited']" to be visible

    # A new file shows up in the expanded tree
    When I create a file "b.txt" in the repo with:
      """
      b
      """
    Then the element "[data-testid='file-tree-panel']" should contain text "b.txt"

    # Unsaved edits are kept: the disk version waits behind the banner
    When I append " mine" to the code editor
    And I create a file "a.txt" in the repo with:
      """
      a third version
      """
    Then I wait for text "This file changed on disk" to appear
    And the element ".cm-content" should contain text "a second version mine"
    When I click on the button with text "Replace"
    Then the element ".cm-content" should contain text "a third version"

    # A deleted file stays open, marked, and Restore writes it back
    When I delete the file "a.txt" in the repo
    Then I wait for "[data-testid='editor-tab'][data-path='a.txt'][data-deleted='true']" to be visible
    And I wait for "[data-testid='editor-deleted-banner']" to be visible
    And the element ".cm-content" should contain text "a third version"
    When I click on the button with text "Restore"
    Then the file "a.txt" in the repo should contain "a third version"
    And I wait for text "This file was deleted on disk" to disappear
    And I wait for "[data-testid='editor-tab'][data-path='a.txt']:not([data-deleted])" to be visible

  @editor-live-reload
  Scenario: Extra workspace roots start collapsed in the file tree
    Given I set up a test channel via API for git repo "bdd-editor-roots-collapsed"
    And I add an extra workspace root via API
    And I open the app in a browser
    And I wait for text "bdd-editor-roots-collapsed" to appear

    When I click on "bdd-editor-roots-collapsed" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "[data-testid='layout-tab-Editor']"
    Then I wait for "[data-testid='file-tree-root'][data-root='0'][aria-expanded='true']" to be visible
    And I wait for "[data-testid='file-tree-root'][data-root='1'][aria-expanded='false']" to be visible
    When I click on "[data-testid='file-tree-root'][data-root='1']"
    Then I wait for "[data-testid='file-tree-root'][data-root='1'][aria-expanded='true']" to be visible

  @editor-compare
  Scenario: Compare shows unsaved changes against the file on disk, change by change
    Given I set up a test channel via API for git repo "bdd-editor-compare"
    And I create a file "c.txt" in the repo with:
      """
      alpha
      beta
      gamma
      delta
      epsilon
      """
    And I open the app in a browser
    And I wait for text "bdd-editor-compare" to appear
    When I click on "bdd-editor-compare" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "[data-testid='layout-tab-Editor']"
    And I open the file "c.txt" in the editor tree
    And the element ".cm-content" should contain text "epsilon"
    And I append " mine" to the code editor
    And I create a file "c.txt" in the repo with:
      """
      ALPHA
      beta
      gamma
      delta
      epsilon
      """
    Then I wait for text "This file changed on disk" to appear

    # Compare marks each change, with the disk text above the editor's
    When I click on "[data-testid='disk-change-compare']"
    Then I wait for ".cm-deletedChunk" to be visible
    And the element "[data-testid='disk-change-compare']" should contain text "Hide diff"
    And the element ".cm-deletedChunk" should contain text "ALPHA"

    # Taking the disk side of one change, then keeping the result
    When I click on "[data-testid='compare-reject']"
    Then the element ".cm-content" should contain text "ALPHA"
    And I wait for "[data-testid='disk-change-banner']" to be visible
    And I wait for ".cm-deletedChunk" to be visible
    When I click on "[data-testid='disk-change-keep']"
    Then the element "[data-testid='disk-change-banner']" should not exist
    And the element ".cm-deletedChunk" should not exist
    And the element ".cm-content" should contain text "ALPHA"
    And the element ".cm-content" should contain text "epsilon mine"

    # Saving while comparing writes the merged text and ends the comparison
    When I append " again" to the code editor
    And I create a file "c.txt" in the repo with:
      """
      ALPHA
      beta
      GAMMA
      delta
      epsilon
      """
    Then I wait for text "This file changed on disk" to appear
    When I click on "[data-testid='disk-change-compare']"
    Then I wait for ".cm-deletedChunk" to be visible
    When I click on "[data-testid='compare-reject']"
    And I save the editor
    Then the file "c.txt" in the repo should contain "epsilon mine again"
    And the file "c.txt" in the repo should contain "GAMMA"
    And the element "[data-testid='disk-change-banner']" should not exist
    And the element ".cm-deletedChunk" should not exist

  @editor-tab-view
  Scenario: A tab opens where it was left, across tabs and channels
    Given I set up a test channel via API for git repo "bdd-editor-view-other"
    And I set up a test channel via API for git repo "bdd-editor-view"
    And I create a file "long.txt" in the repo with 400 numbered lines
    And I create a file "short.txt" in the repo with:
      """
      short
      """
    And I open the app in a browser
    And I wait for text "bdd-editor-view" to appear
    When I click on "bdd-editor-view" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "[data-testid='layout-tab-Editor']"
    And I open the file "long.txt" in the editor tree
    And I double-click on "[data-testid='editor-tab'][data-path='long.txt']"
    And I click the code editor line "line 3"
    And I scroll the code editor until "line 200" is the top line

    # Another tab and back
    When I open the file "short.txt" in the editor tree
    Then the element ".cm-content" should contain text "short"
    When I click on "[data-testid='editor-tab'][data-path='long.txt']"
    Then the code editor's top line should be "line 200"
    And the code editor cursor should be on the line "line 3"

    # Another channel and back
    When I click on "bdd-editor-view-other" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "bdd-editor-view" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "[data-testid='layout-tab-Editor']"
    Then the code editor's top line should be "line 200"
    And the code editor cursor should be on the line "line 3"

  @editor-tab-view
  Scenario: The active tab stays in the tab strip's view
    Given I set up a test channel via API for git repo "bdd-editor-strip-other"
    And I set up a test channel via API for git repo "bdd-editor-strip"
    And I create a file "a-rather-long-file-name-01.txt" in the repo with 3 numbered lines
    And I create a file "a-rather-long-file-name-02.txt" in the repo with 3 numbered lines
    And I create a file "a-rather-long-file-name-03.txt" in the repo with 3 numbered lines
    And I create a file "a-rather-long-file-name-04.txt" in the repo with 3 numbered lines
    And I create a file "a-rather-long-file-name-05.txt" in the repo with 3 numbered lines
    And I create a file "a-rather-long-file-name-06.txt" in the repo with 3 numbered lines
    And I create a file "a-rather-long-file-name-07.txt" in the repo with 3 numbered lines
    And I create a file "a-rather-long-file-name-08.txt" in the repo with 3 numbered lines
    And I create a file "a-rather-long-file-name-09.txt" in the repo with 3 numbered lines
    And I create a file "a-rather-long-file-name-10.txt" in the repo with 3 numbered lines
    And I open the app in a browser
    And I wait for text "bdd-editor-strip" to appear
    When I click on "bdd-editor-strip" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "[data-testid='layout-tab-Editor']"
    And I open the file "a-rather-long-file-name-01.txt" in the editor tree
    And I double-click on "[data-testid='editor-tab'][data-path='a-rather-long-file-name-01.txt']"
    And I open the file "a-rather-long-file-name-02.txt" in the editor tree
    And I double-click on "[data-testid='editor-tab'][data-path='a-rather-long-file-name-02.txt']"
    And I open the file "a-rather-long-file-name-03.txt" in the editor tree
    And I double-click on "[data-testid='editor-tab'][data-path='a-rather-long-file-name-03.txt']"
    And I open the file "a-rather-long-file-name-04.txt" in the editor tree
    And I double-click on "[data-testid='editor-tab'][data-path='a-rather-long-file-name-04.txt']"
    And I open the file "a-rather-long-file-name-05.txt" in the editor tree
    And I double-click on "[data-testid='editor-tab'][data-path='a-rather-long-file-name-05.txt']"
    And I open the file "a-rather-long-file-name-06.txt" in the editor tree
    And I double-click on "[data-testid='editor-tab'][data-path='a-rather-long-file-name-06.txt']"
    And I open the file "a-rather-long-file-name-07.txt" in the editor tree
    And I double-click on "[data-testid='editor-tab'][data-path='a-rather-long-file-name-07.txt']"
    And I open the file "a-rather-long-file-name-08.txt" in the editor tree
    And I double-click on "[data-testid='editor-tab'][data-path='a-rather-long-file-name-08.txt']"
    And I open the file "a-rather-long-file-name-09.txt" in the editor tree
    And I double-click on "[data-testid='editor-tab'][data-path='a-rather-long-file-name-09.txt']"
    And I open the file "a-rather-long-file-name-10.txt" in the editor tree
    And I double-click on "[data-testid='editor-tab'][data-path='a-rather-long-file-name-10.txt']"
    Then the editor tabs overflow their strip
    And the editor tab "a-rather-long-file-name-10.txt" should be in the strip's view

    # Coming back to the channel shows the active tab, not the first ones
    When I scroll the editor tab strip to its start
    And I click on "bdd-editor-strip-other" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "bdd-editor-strip" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "[data-testid='layout-tab-Editor']"
    Then the editor tab "a-rather-long-file-name-10.txt" should be in the strip's view

    # Picking a tab past the edge from the tree brings it into view
    When I open the file "a-rather-long-file-name-01.txt" in the editor tree
    Then the editor tab "a-rather-long-file-name-01.txt" should be in the strip's view

  @editor-tab-view
  Scenario: A preview tab stays a preview tab across channel switches
    Given I set up a test channel via API for git repo "bdd-editor-preview-other"
    And I set up a test channel via API for git repo "bdd-editor-preview"
    And I create a file "pinned.txt" in the repo with 3 numbered lines
    And I create a file "peek.txt" in the repo with 3 numbered lines
    And I create a file "next.txt" in the repo with 3 numbered lines
    And I open the app in a browser
    And I wait for text "bdd-editor-preview" to appear
    When I click on "bdd-editor-preview" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "[data-testid='layout-tab-Editor']"
    And I open the file "pinned.txt" in the editor tree
    And I double-click on "[data-testid='editor-tab'][data-path='pinned.txt']"
    And I open the file "peek.txt" in the editor tree
    Then I wait for "[data-testid='editor-tab'][data-path='peek.txt'][data-preview][data-active]" to be visible

    When I click on "bdd-editor-preview-other" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "bdd-editor-preview" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "[data-testid='layout-tab-Editor']"
    Then I wait for "[data-testid='editor-tab'][data-path='peek.txt'][data-preview][data-active]" to be visible
    And the element "[data-testid='editor-tab'][data-path='pinned.txt'][data-preview]" should not exist

    # Opening another file still replaces the restored preview tab
    When I open the file "next.txt" in the editor tree
    Then I wait for "[data-testid='editor-tab'][data-path='next.txt'][data-preview]" to be visible
    And the element "[data-testid='editor-tab'][data-path='peek.txt']" should not exist
