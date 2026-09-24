@frontend @components
Feature: Chat Components Journey
  An agent shows a component in the chat by filling a template with its own
  HTML, CSS and JS. The backend composes the document and posts it as an
  agent message; the chat renders it in a sandboxed frame that sizes itself
  to its content, with a stepper over <section data-step> elements.

  Scenario: A component posted through the API renders in the chat
    Given I set up a test channel via API for git repo "bdd-chat-components"
    And I open the app in a browser
    And I wait for text "bdd-chat-components" to appear
    And I record messages posted by frames

    When I click on "bdd-chat-components" in the sidebar
    And I wait for "textarea" to be visible
    And I send a POST request to "/api/components?channel_id={channel_id}" with body:
      """
      {
        "template": "math",
        "title": "Fracții algebrice",
        "html": "<section data-step='Pasul 1'><div style='height:420px'>Primul pas</div></section><section data-step='Pasul 2'><div style='height:420px'>Al doilea pas</div></section>",
        "js": "addEventListener('DOMContentLoaded', () => { const counter = () => document.querySelector('nav.loop-steps span').textContent; parent.postMessage('paper:' + !!document.querySelector('.paper'), '*'); parent.postMessage('steps:' + counter(), '*'); document.querySelector('nav.loop-steps button:last-of-type').click(); parent.postMessage('steps:' + counter(), '*'); let access = 'blocked'; try { access = String(parent.document.title); } catch {} parent.postMessage('parent:' + access, '*'); requestAnimationFrame(() => requestAnimationFrame(() => parent.postMessage('grid:' + (parseFloat(document.documentElement.style.getPropertyValue('--base')) > 0), '*'))); });"
      }
      """
    Then the response status should be 201
    And the response JSON "msg_id" should not be empty

    # The frame shows under its title, and the template's paper wraps the content
    And I wait for "[data-testid='chat-component'][data-template='math']" to be visible
    And the element "[data-testid='chat-component-title']" should contain text "Fracții algebrice"
    And I wait for a frame message "paper:true"

    # The stepper shows one step at a time and moves on
    And I wait for a frame message "steps:1 / 2"
    And I wait for a frame message "steps:2 / 2"

    # The notebook's grid is shifted to the measured baseline, so the writing
    # sits on its lines; the frame has no layout while it parses, so this
    # has to happen once it does
    And I wait for a frame message "grid:true"

    # Scripts run, but can't reach the app
    And I wait for a frame message "parent:blocked"

    # The frame grows to fit its content
    And I wait for "[data-testid='chat-component-frame']" to be at least 400px tall

    # Expanding maximizes the chat pane and shows the component across it;
    # Escape goes back to the chat and restores the pane
    When I click on "[data-testid='chat-component-expand']"
    Then I wait for "[data-testid='chat-component-full'][data-template='math'] iframe" to be visible
    And I wait for "button[title='Restore pane']" to be visible
    When I press Escape
    Then the element "[data-testid='chat-component-full']" should not exist
    And the element "button[title='Restore pane']" should not exist
    And I wait for "[data-testid='chat-component'][data-template='math']" to be visible

    # It's stored in the chat, so it's still there after a reload
    When I open the app in a browser
    And I wait for text "bdd-chat-components" to appear
    And I click on "bdd-chat-components" in the sidebar
    Then I wait for "[data-testid='chat-component'][data-template='math']" to be visible

  Scenario: A canvas gets its size in the chat and across the whole pane
    Given I set up a test channel via API for git repo "bdd-chat-components-canvas"
    And I open the app in a browser
    And I wait for text "bdd-chat-components-canvas" to appear
    And I record messages posted by frames

    When I click on "bdd-chat-components-canvas" in the sidebar
    And I wait for "textarea" to be visible
    And I send a POST request to "/api/components?channel_id={channel_id}" with body:
      """
      {
        "template": "canvas",
        "title": "Plot",
        "js": "const report = () => parent.postMessage('canvas:' + (innerHeight > 500 ? 'large' : 'inline') + ':' + (canvas.clientWidth > 0 && canvas.width === Math.round(canvas.clientWidth * devicePixelRatio)), '*'); canvas.addEventListener('resize', report); report();"
      }
      """
    Then the response status should be 201

    # The overlay's frame has no size while its document parses; the canvas
    # is sized once it does. The script reports at start as well as on
    # resize, as the guide says to draw: a module script runs after parsing,
    # so the first resize can come before it's listening.
    And I wait for "[data-testid='chat-component'][data-template='canvas']" to be visible
    And I wait for a frame message "canvas:inline:true"
    When I click on "[data-testid='chat-component-expand']"
    Then I wait for a frame message "canvas:large:true"

  Scenario: A React component gets React by name and a root to mount on
    Given I set up a test channel via API for git repo "bdd-chat-components-react"
    And I open the app in a browser
    And I wait for text "bdd-chat-components-react" to appear
    And I record messages posted by frames

    When I click on "bdd-chat-components-react" in the sidebar
    And I wait for "textarea" to be visible
    And I send a POST request to "/api/components?channel_id={channel_id}" with body:
      """
      {
        "template": "react",
        "title": "Counter",
        "html": "<p id='below'>under the app</p>",
        "js": "const map = JSON.parse(document.querySelector('script[type=importmap]').textContent).imports; const root = document.getElementById('root'); parent.postMessage('react:' + ['react', 'react-dom/client', 'htm'].every((k) => map[k]) + ':' + !!(root && root.compareDocumentPosition(document.getElementById('below')) & Node.DOCUMENT_POSITION_FOLLOWING), '*');"
      }
      """
    Then the response status should be 201

    # React itself loads from esm.sh, which the tests don't reach, so this
    # checks what the page gives the app: the import map and the root, with
    # the component's own HTML under it
    And I wait for "[data-testid='chat-component'][data-template='react']" to be visible
    And I wait for a frame message "react:true:true"

  Scenario: An unknown template is refused with the ones available
    Given I set up a test channel via API for git repo "bdd-chat-components-unknown"
    When I send a POST request to "/api/components?channel_id={channel_id}" with body:
      """
      {"template": "chart", "html": "<p>x</p>"}
      """
    Then the response status should be 400
    And the response should contain "available: math, canvas, react"

  Scenario: A component fence that never closes stays a code block
    Given I set up a test channel via API for git repo "bdd-chat-components-open"
    And I open the app in a browser
    And I wait for text "bdd-chat-components-open" to appear

    When I click on "bdd-chat-components-open" in the sidebar
    And I wait for "textarea" to be visible
    And I inject a bot message with:
      """
      Half a component

      ```loop-component math Unfinished
      <p>still streaming</p>
      """
    Then I wait for text "Half a component" to appear
    And the element "pre" should contain text "<p>still streaming</p>"
    And the element "[data-testid='chat-component']" should not exist
