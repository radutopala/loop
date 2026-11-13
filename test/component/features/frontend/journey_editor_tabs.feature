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

    # Hovering a tab shows its full path
    When I rest the pointer on the element "[data-testid='editor-tab'][data-path='server/src/index.ts']"
    Then I wait for "[data-testid='hover-tip']" to be visible
    And the element "[data-testid='hover-tip']" should contain text "{repo_path}/server/src/index.ts"

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

  @editor-tab-menu
  Scenario: A tab's menu closes tabs, keeping the ones with unsaved changes
    Given I set up a test channel via API for git repo "bdd-editor-tab-close"
    And I create a file "a.txt" in the repo with 3 numbered lines
    And I create a file "b.txt" in the repo with 3 numbered lines
    And I create a file "c.txt" in the repo with 3 numbered lines
    And I create a file "d.txt" in the repo with 3 numbered lines
    And I open the app in a browser
    And I wait for text "bdd-editor-tab-close" to appear
    When I click on "bdd-editor-tab-close" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "[data-testid='layout-tab-Editor']"
    And I open the file "a.txt" in the editor tree
    And I double-click on "[data-testid='editor-tab'][data-path='a.txt']"
    And I open the file "b.txt" in the editor tree
    And I double-click on "[data-testid='editor-tab'][data-path='b.txt']"
    And I open the file "c.txt" in the editor tree
    And I double-click on "[data-testid='editor-tab'][data-path='c.txt']"
    And I open the file "d.txt" in the editor tree
    And I double-click on "[data-testid='editor-tab'][data-path='d.txt']"
    And I click on "[data-testid='editor-tab'][data-path='b.txt']"
    And I append " mine" to the code editor

    When I right-click on "[data-testid='editor-tab'][data-path='b.txt']"
    And I click on "Close to the right" in the context menu
    Then I wait up to "5s" for "[data-testid='editor-tab'][data-path='d.txt']" to disappear
    And the element "[data-testid='editor-tab'][data-path='c.txt']" should not exist
    And I wait for "[data-testid='editor-tab'][data-path='a.txt']" to be visible

    # The tab with unsaved changes stays open
    When I open the file "c.txt" in the editor tree
    And I double-click on "[data-testid='editor-tab'][data-path='c.txt']"
    And I right-click on "[data-testid='editor-tab'][data-path='c.txt']"
    And I click on "Close others" in the context menu
    Then I wait up to "5s" for "[data-testid='editor-tab'][data-path='a.txt']" to disappear
    And I wait for "[data-testid='editor-tab'][data-path='b.txt']" to be visible
    And I wait for "[data-testid='editor-tab'][data-path='c.txt'][data-active]" to be visible

    When I right-click on "[data-testid='editor-tab'][data-path='c.txt']"
    And I click on "Close saved" in the context menu
    Then I wait up to "5s" for "[data-testid='editor-tab'][data-path='c.txt']" to disappear
    And I wait for "[data-testid='editor-tab'][data-path='b.txt'][data-active]" to be visible

    When I right-click on "[data-testid='editor-tab'][data-path='b.txt']"
    And I click on "Close" in the context menu
    Then I wait up to "5s" for "[data-testid='editor-tabs']" to disappear

  @editor-tab-menu
  Scenario: A tab's menu pins and unpins the preview tab and reveals its file in the tree
    Given I set up a test channel via API for git repo "bdd-editor-tab-pin"
    And I create a file "one.txt" in the repo with 3 numbered lines
    And I create a file "two.txt" in the repo with 3 numbered lines
    And I create a file "deep/nest/three.txt" in the repo with 3 numbered lines
    And I open the app in a browser
    And I wait for text "bdd-editor-tab-pin" to appear
    When I click on "bdd-editor-tab-pin" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "[data-testid='layout-tab-Editor']"

    When I open the file "one.txt" in the editor tree
    And I right-click on "[data-testid='editor-tab'][data-path='one.txt'][data-preview]"
    And I click on "Pin tab" in the context menu
    Then I wait up to "5s" for "[data-testid='editor-tab'][data-path='one.txt'][data-preview]" to disappear
    And I wait for "[data-testid='editor-tab'][data-path='one.txt']" to be visible

    # Unpinned, the next file opened replaces it
    When I right-click on "[data-testid='editor-tab'][data-path='one.txt']"
    And I click on "Unpin tab" in the context menu
    Then I wait for "[data-testid='editor-tab'][data-path='one.txt'][data-preview]" to be visible
    When I open the file "two.txt" in the editor tree
    Then I wait for "[data-testid='editor-tab'][data-path='two.txt'][data-preview]" to be visible
    And I wait up to "5s" for "[data-testid='editor-tab'][data-path='one.txt']" to disappear

    # Reveal in tree opens the folders the file is in
    When I open the file "deep" in the editor tree
    And I open the file "nest" in the editor tree
    And I open the file "three.txt" in the editor tree
    And I double-click on "[data-testid='editor-tab'][data-path='deep/nest/three.txt']"
    And I open the file "deep" in the editor tree
    Then I wait up to "5s" for "[data-testid='file-tree-entry'][data-path='0:deep/nest/three.txt']" to disappear
    When I right-click on "[data-testid='editor-tab'][data-path='deep/nest/three.txt']"
    And I click on "Reveal in tree" in the context menu
    Then I wait for "[data-testid='file-tree-entry'][data-path='0:deep/nest/three.txt']" to be visible

    # Following the active file is on by default: a tab switch reveals it
    When I open the file "deep" in the editor tree
    And I open the file "two.txt" in the editor tree
    Then I wait for "[data-testid='file-tree-auto-reveal'][aria-pressed='true'][data-follow-source='default']" to be visible
    And the element "[data-testid='file-tree-entry'][data-path='0:deep/nest/three.txt']" should not exist
    When I click on "[data-testid='editor-tab'][data-path='deep/nest/three.txt']"
    Then I wait for "[data-testid='file-tree-entry'][data-path='0:deep/nest/three.txt']" to be visible

    # Turned off, for this channel only, the tree stays where it is
    When I click on "[data-testid='file-tree-auto-reveal']"
    Then I wait for "[data-testid='file-tree-auto-reveal'][aria-pressed='false'][data-follow-source='channel']" to be visible
    When I open the file "deep" in the editor tree
    And I click on "[data-testid='editor-tab'][data-path='two.txt']"
    And I click on "[data-testid='editor-tab'][data-path='deep/nest/three.txt']"
    Then the element "[data-testid='file-tree-entry'][data-path='0:deep/nest/three.txt']" should not exist

    # Turned on again, the channel follows the config once more
    When I click on "[data-testid='file-tree-auto-reveal']"
    Then I wait for "[data-testid='file-tree-auto-reveal'][aria-pressed='true'][data-follow-source='default']" to be visible

  @editor-follow-config
  Scenario: The project config turns following the active file off
    Given I set up a test channel via API for git repo "bdd-editor-follow-config"
    And I create a file "deep/four.txt" in the repo with 3 numbered lines
    And I save the project config via API with:
      """
      {"editor": {"follow_active_file": false}}
      """
    And I open the app in a browser
    And I wait for text "bdd-editor-follow-config" to appear
    When I click on "bdd-editor-follow-config" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "[data-testid='layout-tab-Editor']"
    Then I wait for "[data-testid='file-tree-auto-reveal'][aria-pressed='false'][data-follow-source='project']" to be visible

  @editor-content-search
  Scenario: Searching file contents lists the matching lines by file and opens one at its line
    Given I set up a test channel via API for git repo "bdd-editor-grep"
    And I create a file "a.txt" in the repo with:
      """
      alpha
      a needle here
      """
    And I create a file "sub/b.txt" in the repo with:
      """
      first
      second
      the Needle there
      """
    And I create a file "c.txt" in the repo with:
      """
      nothing to see
      """
    And I open the app in a browser
    And I wait for text "bdd-editor-grep" to appear
    When I click on "bdd-editor-grep" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "[data-testid='layout-tab-Editor']"
    And I wait for "[data-testid='file-tree-root']" to be visible

    When I click on "[data-testid='file-tree-search-contents']"
    And I type "needle" into "[data-testid='file-tree-search']"
    Then I wait for "[data-testid='content-search-file'][data-path='0:sub/b.txt']" to be visible
    And I wait for "[data-testid='content-search-file'][data-path='0:a.txt']" to be visible
    And the element "[data-testid='content-search-file'][data-path='0:c.txt']" should not exist
    And the element "[data-testid='content-search-summary']" should contain text "2 matches in 2 files"
    And the element "[data-testid='file-tree-root']" should not exist

    # Match case leaves out the capitalised one
    When I click on "[data-testid='file-tree-search-case']"
    Then I wait up to "5s" for "[data-testid='content-search-file'][data-path='0:sub/b.txt']" to disappear
    And the element "[data-testid='content-search-summary']" should contain text "1 match in 1 file"
    When I click on "[data-testid='file-tree-search-case']"
    Then I wait for "[data-testid='content-search-file'][data-path='0:sub/b.txt']" to be visible

    # A match opens its file with the cursor on its line
    When I click on "[data-testid='content-search-file'][data-path='0:sub/b.txt'] [data-testid='content-search-match'][data-line='3']"
    Then I wait for "[data-testid='editor-tab'][data-path='sub/b.txt']" to be visible
    And the code editor cursor should be on the line "the Needle there"

    # A regex that doesn't compile says why
    When I click on "[data-testid='file-tree-search-regex']"
    And I type "(" into "[data-testid='file-tree-search']"
    Then I wait for "[data-testid='content-search-error']" to be visible
    And the element "[data-testid='content-search-error']" should contain text "invalid regex"

    # Back to the name search, the tree shows again once the query is cleared
    When I click on "[data-testid='file-tree-search-contents']"
    Then the element "[data-testid='content-search-error']" should not exist
    And the element "[data-testid='file-tree-search-regex']" should not exist

  @editor-head-diff
  Scenario: The diff against HEAD shows every change to the file, and reverts one
    Given I set up a test channel via API for git repo "bdd-editor-head-diff"
    And I create a file "h.txt" in the repo with:
      """
      one
      two
      three
      """
    And I commit all changes in the repo with message "add h.txt"
    And I create a file "h.txt" in the repo with:
      """
      ONE
      two
      three
      """
    And I create a file "new.txt" in the repo with:
      """
      fresh
      """
    And I open the app in a browser
    And I wait for text "bdd-editor-head-diff" to appear
    When I click on "bdd-editor-head-diff" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "[data-testid='layout-tab-Editor']"
    And I open the file "h.txt" in the editor tree
    And the element ".cm-content" should contain text "ONE"
    And I append " mine" to the code editor

    # Both the saved change and the unsaved one show against HEAD
    When I click on "[data-testid='editor-diff-head']"
    Then I wait for "[data-testid='head-diff-banner']" to be visible
    And I wait for ".cm-deletedChunk" to be visible
    And the element ".cm-deletedChunk" should contain text "one"
    And I wait for "[data-testid='editor-diff-head'][aria-pressed='true']" to be visible

    # Reverting the first change brings HEAD's line back; the unsaved one is left
    When I click on "[data-testid='compare-reject']"
    Then the element ".cm-content" should not contain text "ONE"
    And the element ".cm-content" should contain text "three mine"
    And I wait for ".cm-deletedChunk" to be visible
    And the element ".cm-deletedChunk" should contain text "three"

    # Hiding the diff keeps the edits
    When I click on "[data-testid='head-diff-hide']"
    Then the element "[data-testid='head-diff-banner']" should not exist
    And the element ".cm-deletedChunk" should not exist
    And the element ".cm-content" should contain text "three mine"

    # A file HEAD doesn't have says so
    When I open the file "new.txt" in the editor tree
    And the element ".cm-content" should contain text "fresh"
    And I click on "[data-testid='editor-diff-head']"
    Then I wait for "[data-testid='editor-notice']" to be visible
    And the element "[data-testid='editor-notice']" should contain text "isn't in HEAD"
    And the element ".cm-deletedChunk" should not exist

  @editor-format-on-save
  Scenario: Saving formats the file with the project's formatter, and says when it can't
    Given I set up a test channel via API for git repo "bdd-editor-format"
    And I create a file "f.up" in the repo with:
      """
      hello
      """
    And I create a file "g.bad" in the repo with:
      """
      keep me
      """
    And I save the project config via API with:
      """
      {"editor": {"format_on_save": true, "formatters": [{"glob": "*.up", "command": "tr a-z A-Z"}, {"glob": "*.bad", "command": "echo broken >&2; exit 3"}]}}
      """
    And I open the app in a browser
    And I wait for text "bdd-editor-format" to appear
    When I click on "bdd-editor-format" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "[data-testid='layout-tab-Editor']"

    When I open the file "f.up" in the editor tree
    And the element ".cm-content" should contain text "hello"
    And I append " world" to the code editor
    And I save the editor
    Then the file "f.up" in the repo should contain "HELLO WORLD"
    And the element ".cm-content" should contain text "HELLO WORLD"
    And the element "[data-testid='editor-notice']" should not exist

    When I open the file "g.bad" in the editor tree
    And the element ".cm-content" should contain text "keep me"
    And I append " too" to the code editor
    And I save the editor
    Then I wait for "[data-testid='editor-notice']" to be visible
    And the element "[data-testid='editor-notice']" should contain text "Saved without formatting"
    And the element "[data-testid='editor-notice']" should contain text "broken"
    And the file "g.bad" in the repo should contain "keep me too"

  @editor-save-cleanups
  Scenario: The project's editor settings indent, clean up a save, and hide paths from search
    Given I set up a test channel via API for git repo "bdd-editor-cleanups"
    And I create a file "t.txt" in the repo with:
      """
      hello
      """
    And I create a file "x.skip" in the repo with:
      """
      hello
      """
    And I save the project config via API with:
      """
      {"editor": {"tab_size": 2, "insert_spaces": false, "trim_trailing_whitespace": true, "insert_final_newline": true, "search_exclude": ["*.skip"]}}
      """
    And I open the app in a browser
    And I wait for text "bdd-editor-cleanups" to appear
    When I click on "bdd-editor-cleanups" in the sidebar
    And I wait for "textarea" to be visible
    And I click on "[data-testid='layout-tab-Editor']"

    # Indentation follows the config
    When I open the file "t.txt" in the editor tree
    Then I wait for ".cm-content[style*='tab-size: 2']" to be visible

    # Trailing whitespace goes and a final newline is added on save
    When I append " world   " to the code editor
    And I save the editor
    Then the file "t.txt" in the repo should be exactly "hello world\n"

    # Excluded files are left out of the content search
    When I click on "[data-testid='file-tree-search-contents']"
    And I type "hello" into "[data-testid='file-tree-search']"
    Then I wait for "[data-testid='content-search-summary']" to be visible
    And the element "[data-testid='content-search-summary']" should contain text "1 match in 1 file"
