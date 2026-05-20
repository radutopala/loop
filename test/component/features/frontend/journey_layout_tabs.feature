@frontend
Feature: Layout Tabs Journey
  Drag-and-drop reorder of layout tabs in the channel workspace.

  @layout-drag
  Scenario: Drag a layout tab to reorder it within the tab strip
    Given I set up a test channel via API for git repo "bdd-layout-tabs"
    And I open the app in a browser
    And I wait for text "bdd-layout-tabs" to appear

    When I click on "bdd-layout-tabs" in the sidebar
    And I wait for "textarea" to be visible
    Then the layout tabs should be in order "Chat,Editor,Memory,Terminal,Git,Browser Chat,Sessions,Swarm,Canvas,Playground,Kanban,Workflows"

    # Drag "Editor" onto "Chat" — leftward drag lands before the target
    When I drag layout tab "Editor" onto layout tab "Chat"
    Then the layout tabs should be in order "Editor,Chat,Memory,Terminal,Git,Browser Chat,Sessions,Swarm,Canvas,Playground,Kanban,Workflows"

    # Drag "Memory" onto "Workflows" — rightward drag lands after the target
    When I drag layout tab "Memory" onto layout tab "Workflows"
    Then the layout tabs should be in order "Editor,Chat,Terminal,Git,Browser Chat,Sessions,Swarm,Canvas,Playground,Kanban,Workflows,Memory"
