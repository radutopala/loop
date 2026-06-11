//go:build component

package component

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/cucumber/godog"
	"github.com/radutopala/loop/internal/browser"
)

// chromeManager is a package-level singleton that manages a single Chrome
// process shared across all frontend scenarios in a test run.
var chromeManager struct {
	mu            sync.Mutex
	allocCtx      context.Context
	allocCancel   context.CancelFunc
	browserCtx    context.Context    // first CDP connection, reused for tab creation in remote mode
	browserCancel context.CancelFunc // cancel for browserCtx
	started       bool
	remote        bool // true when using remote allocator (host browser mode)
}

// ensureChrome starts a headless Chromium process or connects to a host
// browser via CDP, depending on the CHROME_CDP_URL environment variable.
func ensureChrome() error {
	chromeManager.mu.Lock()
	defer chromeManager.mu.Unlock()

	if chromeManager.started {
		return nil
	}

	// Host browser mode: connect to an existing Chrome via CDP WebSocket.
	// We dial once and reuse that connection for all scenario tabs, avoiding
	// repeated permission prompts (same approach as the host browser provider).
	if cdpURL := os.Getenv("CHROME_CDP_URL"); cdpURL != "" {
		wsURL := cdpURL
		if cdpURL == "auto" {
			discovered, err := browser.DiscoverWSEndpoint()
			if err != nil {
				return fmt.Errorf("auto-discovering Chrome CDP endpoint: %w", err)
			}
			wsURL = discovered
		}
		chromeManager.allocCtx, chromeManager.allocCancel = chromedp.NewRemoteAllocator(
			context.Background(), wsURL)
		// Establish the single WS connection; subsequent tabs reuse it.
		chromeManager.browserCtx, chromeManager.browserCancel = chromedp.NewContext(chromeManager.allocCtx)
		if err := chromedp.Run(chromeManager.browserCtx); err != nil {
			chromeManager.browserCancel()
			chromeManager.allocCancel()
			return fmt.Errorf("connecting to host Chrome at %s: %w", wsURL, err)
		}
		chromeManager.remote = true
		chromeManager.started = true
		return nil
	}

	// Default: launch headless Chromium.
	chromeBin := "chromium"
	if _, err := exec.LookPath(chromeBin); err != nil {
		chromeBin = "chromium-browser"
		if _, err := exec.LookPath(chromeBin); err != nil {
			chromeBin = "google-chrome"
			if _, err := exec.LookPath(chromeBin); err != nil {
				return fmt.Errorf("no chromium/chrome binary found in PATH")
			}
		}
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromeBin),
		chromedp.NoSandbox,
		chromedp.DisableGPU,
		chromedp.Flag("headless", "new"),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-software-rasterizer", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.WindowSize(1280, 800),
	)
	if os.Getenv("LOOP_DOCS_CAPTURE") != "" {
		// Render at 2x device pixels so the recorded MP4 matches the screenshots'
		// resolution. CDP screencast captures the backing store but ignores
		// per-page emulation deviceScaleFactor, so the flag is what bumps it.
		opts = append(opts, chromedp.Flag("force-device-scale-factor", "2"))
	}

	chromeManager.allocCtx, chromeManager.allocCancel = chromedp.NewExecAllocator(
		context.Background(), opts...)
	chromeManager.started = true
	return nil
}

// stopChrome shuts down Chrome. Called from TestMain.
func stopChrome() {
	chromeManager.mu.Lock()
	defer chromeManager.mu.Unlock()

	if chromeManager.browserCancel != nil {
		chromeManager.browserCancel()
		chromeManager.browserCancel = nil
	}
	if chromeManager.allocCancel != nil {
		chromeManager.allocCancel()
		chromeManager.allocCancel = nil
	}
	chromeManager.started = false
	chromeManager.remote = false
}

// chromeTab wraps a per-scenario chromedp context (browser tab).
type chromeTab struct {
	ctx    context.Context
	cancel context.CancelFunc
	rec    *screencastRecorder // non-nil while a docs-capture recording is active
}

func (t *chromeTab) close() {
	if t.cancel != nil {
		t.cancel()
	}
}

func registerFrontendSteps(ctx *godog.ScenarioContext, tc *TestContext) {
	// Navigation
	ctx.Step(`^I open the app in a browser$`, tc.openAppInBrowser)
	ctx.Step(`^I navigate to "([^"]*)"$`, tc.navigateTo)

	// DOM interaction — CSS selectors
	ctx.Step(`^I click on "([^"]*)"$`, tc.clickOn)
	ctx.Step(`^I type "([^"]*)" into "([^"]*)"$`, tc.typeInto)
	ctx.Step(`^I type "([^"]*)" into the agent terminal$`, tc.typeIntoAgentTerminal)
	ctx.Step(`^I submit the agent terminal$`, tc.submitAgentTerminal)
	ctx.Step(`^I clear and type "([^"]*)" into "([^"]*)"$`, tc.clearAndTypeInto)
	ctx.Step(`^I wait for "([^"]*)" to be visible$`, tc.waitForVisible)
	ctx.Step(`^I select "([^"]*)" from "([^"]*)"$`, tc.selectFrom)
	ctx.Step(`^I open the file "([^"]*)" in the editor tree$`, tc.openFileInEditorTree)
	ctx.Step(`^I append "([^"]*)" to the code editor$`, tc.appendToCodeEditor)
	ctx.Step(`^I save the editor$`, tc.saveEditor)
	ctx.Step(`^I click "([^"]*)" in the git panel$`, tc.clickInGitPanel)

	// DOM interaction — text-based (scoped to a data-testid region)
	ctx.Step(`^I click on the button with text "([^"]*)"$`, tc.clickButtonWithText)
	ctx.Step(`^I open the global tasks panel$`, tc.openGlobalTasksPanel)
	ctx.Step(`^I click on "([^"]*)" in the sidebar$`, tc.clickInSidebar)
	ctx.Step(`^I click on "([^"]*)" in the tasks panel$`, tc.clickInTasksPanel)
	ctx.Step(`^I click on "([^"]*)" in the global tasks panel$`, tc.clickInGlobalTasksPanel)
	ctx.Step(`^I click on "([^"]*)" in the context menu$`, tc.clickInContextMenu)
	ctx.Step(`^I click on "([^"]*)" in the branch picker$`, tc.clickInBranchPicker)
	ctx.Step(`^I click button "([^"]*)" in the tasks panel$`, tc.clickButtonInTasksPanel)
	ctx.Step(`^I click on "([^"]*)" in the worktrees panel$`, tc.clickInWorktreesPanel)
	ctx.Step(`^I click button "([^"]*)" in the worktrees panel$`, tc.clickButtonInWorktreesPanel)
	ctx.Step(`^I click on "([^"]*)" in the branches panel$`, tc.clickInBranchesPanel)
	ctx.Step(`^I click button "([^"]*)" in the branches panel$`, tc.clickButtonInBranchesPanel)
	ctx.Step(`^I click on "([^"]*)" in the kanban panel$`, tc.clickInKanbanPanel)
	ctx.Step(`^I click button "([^"]*)" in the kanban panel$`, tc.clickButtonInKanbanPanel)
	ctx.Step(`^I click button "([^"]*)" in the git panel$`, tc.clickButtonInGitPanel)
	ctx.Step(`^I open the settings panel and select "([^"]*)"$`, tc.openSettingsPanelSection)
	ctx.Step(`^I open the workflows panel$`, tc.openWorkflowsPanel)
	ctx.Step(`^I click on "([^"]*)" in the workflows panel$`, tc.clickInWorkflowsPanel)
	ctx.Step(`^I click button "([^"]*)" in the workflows panel$`, tc.clickButtonInWorkflowsPanel)
	ctx.Step(`^I click on "([^"]*)" in the workflows split panel$`, tc.clickInWorkflowsSplitPanel)
	ctx.Step(`^I click button "([^"]*)" in the workflows split panel$`, tc.clickButtonInWorkflowsSplitPanel)
	ctx.Step(`^I click on "([^"]*)" in the settings panel$`, tc.clickInSettingsPanel)
	ctx.Step(`^I click button "([^"]*)" in the settings panel$`, tc.clickButtonInSettingsPanel)
	ctx.Step(`^I trigger Run Now for the visible task$`, tc.triggerRunNowForVisibleTask)
	ctx.Step(`^I capture the visible task ID$`, tc.captureVisibleTaskID)
	ctx.Step(`^I click on the element with text "([^"]*)"$`, tc.clickElementWithText)
	ctx.Step(`^I click on the button with title "([^"]*)"$`, tc.clickButtonWithTitle)
	ctx.Step(`^I hover over the element with text "([^"]*)"$`, tc.hoverElementWithText)
	ctx.Step(`^I hover over "([^"]*)" in the sidebar$`, tc.hoverInSidebar)
	ctx.Step(`^I right-click on the element with text "([^"]*)"$`, tc.rightClickElementWithText)
	ctx.Step(`^I right-click on "([^"]*)" in the sidebar$`, tc.rightClickInSidebar)

	// Keyboard
	ctx.Step(`^I press Enter$`, tc.pressEnter)
	ctx.Step(`^I press Escape$`, tc.pressEscape)

	// Wait
	ctx.Step(`^I wait "([^"]*)"$`, tc.waitDuration)

	// Assertions
	ctx.Step(`^the page should contain text "([^"]*)"$`, tc.assertPageContainsText)
	ctx.Step(`^the page should not contain text "([^"]*)"$`, tc.assertPageNotContainsText)
	ctx.Step(`^the element "([^"]*)" should be visible$`, tc.assertElementVisible)
	ctx.Step(`^the element "([^"]*)" should not exist$`, tc.assertElementNotExist)
	ctx.Step(`^the element "([^"]*)" should contain text "([^"]*)"$`, tc.assertElementContainsText)
	ctx.Step(`^I wait for text "([^"]*)" to appear$`, tc.waitForTextToAppear)
	ctx.Step(`^I wait for text "([^"]*)" to disappear$`, tc.waitForTextToDisappear)

	// Extended wait
	ctx.Step(`^I wait up to "([^"]*)" for text "([^"]*)" to appear$`, tc.waitForTextToAppearWithTimeout)
	ctx.Step(`^I wait up to "([^"]*)" for text "([^"]*)" to disappear$`, tc.waitForTextToDisappearWithTimeout)
	ctx.Step(`^I wait up to "([^"]*)" for "([^"]*)" to disappear$`, tc.waitForSelectorToDisappear)
	ctx.Step(`^I wait up to "([^"]*)" for "([^"]*)" to be visible$`, tc.waitForSelectorVisibleWithTimeout)
	ctx.Step(`^I wait up to "([^"]*)" for "([^"]*)" to be visible, best effort$`, tc.waitForSelectorVisibleBestEffort)
	ctx.Step(`^I select the created playground$`, tc.selectCreatedPlayground)

	// Label interaction
	ctx.Step(`^I click on the label with text "([^"]*)"$`, tc.clickLabelWithText)

	// Panel interaction
	ctx.Step(`^I add a "([^"]*)" panel$`, tc.addPanel)
	ctx.Step(`^I open the add-panel menu in the git panel$`, tc.openAddPanelMenuInGitPanel)
	ctx.Step(`^I open the add-panel menu$`, tc.openAddPanelMenuFirst)
	ctx.Step(`^I add the "([^"]*)" panel below in the menu$`, tc.addPanelBelowFromMenu)
	ctx.Step(`^I click the task create button$`, tc.clickTaskCreateButton)

	// Layout tab drag/drop + chat scroll (synthesized via JS — chromedp's
	// real mouse can't deliver an HTML5 DataTransfer payload).
	ctx.Step(`^I drag layout tab "([^"]*)" onto layout tab "([^"]*)"$`, tc.dragLayoutTab)
	ctx.Step(`^the layout tabs should be in order "([^"]*)"$`, tc.assertLayoutTabOrder)
	ctx.Step(`^I scroll the chat messages to bottom$`, tc.scrollChatMessagesToBottom)
	ctx.Step(`^I inject (\d+) bot messages with content "([^"]*)"$`, tc.injectBotMessages)
	ctx.Step(`^I inject a user message with content "([^"]*)"$`, tc.injectUserMessage)

	// Event injection (chromedp dispatches a CustomEvent that the chat store
	// listens for; routes through the same handler as a real WS message).
	ctx.Step(`^I inject an exit_plan event with plan "([^"]*)"$`, tc.injectExitPlanEvent)
	ctx.Step(`^I inject an ask_user event with question "([^"]*)" and options "([^"]*)"$`, tc.injectAskUserEvent)
	ctx.Step(`^I inject a gate\.approval_requested event with req_id "([^"]*)", source "([^"]*)", and target "([^"]*)"$`, tc.injectGateApprovalRequested)
	ctx.Step(`^I inject a gate\.approval_resolved event with req_id "([^"]*)"$`, tc.injectGateApprovalResolved)

	// Documentation capture (no-op unless LOOP_DOCS_CAPTURE is set; see docs-capture make target)
	ctx.Step(`^I capture screenshot "([^"]*)"$`, tc.captureDocScreenshot)
	ctx.Step(`^I start recording$`, tc.startRecording)
	ctx.Step(`^I stop recording "([^"]*)"$`, tc.stopRecording)
	ctx.Step(`^I show caption "([^"]*)"$`, tc.showCaption)
	ctx.Step(`^I show the Loop title card$`, tc.showLoopTitleCard)
	ctx.Step(`^I show the Loop title card and hold$`, tc.showLoopTitleCardHold)
	ctx.Step(`^I show the Loop intro card$`, tc.showLoopIntroCard)
	ctx.Step(`^I fade out the Loop title card$`, tc.fadeOutLoopTitleCard)
	ctx.Step(`^I hide caption$`, tc.hideCaption)
	ctx.Step(`^I show the mouse cursor$`, tc.injectMouseCursor)

	// Debugging
	ctx.Step(`^I take a screenshot$`, tc.takeScreenshot)
	ctx.Step(`^I dump page text$`, tc.dumpPageText)
	ctx.Step(`^I dump visible refs$`, tc.dumpVisibleRefs)
	ctx.Step(`^I dump pane leaves$`, tc.dumpPaneLeaves)
}

// ensureChromeTab initializes a browser tab for the scenario if needed.
func (tc *TestContext) ensureChromeTab() error {
	if tc.chromeTab != nil {
		return nil
	}
	if err := ensureChrome(); err != nil {
		return err
	}
	// In remote mode, create tabs from the browser context to reuse the
	// single WS connection (no new dial / permission prompt per tab).
	parentCtx := chromeManager.allocCtx
	if chromeManager.remote {
		parentCtx = chromeManager.browserCtx
	}
	// Docs-capture scenarios may drive a live agent (container cold-start +
	// Claude latency), so give them a somewhat larger per-scenario budget —
	// but short enough that a hung agent fails fast rather than after ~10m.
	scenarioTimeout := 120 * time.Second
	if os.Getenv("LOOP_DOCS_CAPTURE") != "" {
		// The docs journey is one long scenario (several live agent replies —
		// chat, review-diff, an agent file change + commit with a Git-panel tour,
		// an editor-edit commit, and a chat inside a new worktree — plus a
		// captioned, deliberately-slow tour of every panel) — give it room but
		// still fail a hang inside the go-test timeout.
		scenarioTimeout = 1080 * time.Second
	}
	timeoutCtx, timeoutCancel := context.WithTimeout(parentCtx, scenarioTimeout)
	ctx, cancel := chromedp.NewContext(timeoutCtx)
	tc.chromeTab = &chromeTab{ctx: ctx, cancel: func() { cancel(); timeoutCancel() }}
	return nil
}

// --- Navigation steps ---

func (tc *TestContext) openAppInBrowser() error {
	if err := tc.ensureChromeTab(); err != nil {
		return err
	}
	actions := []chromedp.Action{chromedp.Navigate(tc.AppURL)}
	// Viewport size + device scale factor. Docs-capture renders larger and at
	// 2x DPI so screenshots/GIFs are crisp and panels aren't cramped; normal
	// runs use the launch size (1280x800 @ 1x).
	vw, vh, scale := int64(1280), int64(800), 1.0
	if os.Getenv("LOOP_DOCS_CAPTURE") != "" {
		vw, vh, scale = 1600, 1000, 2.0
	}
	if chromeManager.remote || os.Getenv("LOOP_DOCS_CAPTURE") != "" {
		// Pin a consistent viewport. In host-browser mode also clear stored
		// state so each scenario starts fresh (headless uses isolated contexts).
		actions = append(actions,
			chromedp.ActionFunc(func(ctx context.Context) error {
				return emulation.SetDeviceMetricsOverride(vw, vh, scale, false).Do(ctx)
			}),
		)
		if chromeManager.remote {
			actions = append(actions,
				chromedp.Evaluate(`localStorage.clear(); sessionStorage.clear()`, nil),
				chromedp.Reload(),
			)
		}
	}
	return chromedp.Run(tc.chromeTab.ctx, actions...)
}

func (tc *TestContext) navigateTo(path string) error {
	if err := tc.ensureChromeTab(); err != nil {
		return err
	}
	url := tc.BaseURL + path
	return chromedp.Run(tc.chromeTab.ctx, chromedp.Navigate(url))
}

// --- DOM interaction steps ---

func (tc *TestContext) clickOn(selector string) error {
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.Click(selector, chromedp.ByQuery),
	)
}

func (tc *TestContext) typeInto(text, selector string) error {
	// Use Poll to actively check for the element, avoiding races where
	// chromedp's event-driven WaitVisible misses a briefly-visible element.
	js := fmt.Sprintf(`(() => {
		const el = document.querySelector(%q);
		if (!el) return false;
		const r = el.getBoundingClientRect();
		return r.width > 0 && r.height > 0;
	})()`, selector)
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.Poll(js, nil, chromedp.WithPollingTimeout(15*time.Second)),
		chromedp.SendKeys(selector, text, chromedp.ByQuery),
	)
}

// typeIntoAgentTerminal types text into the Docker Agent pane's interactive
// Claude TUI. xterm.js routes input through an offscreen .xterm-helper-textarea
// (zero-size, so the visibility-gated typeInto can't reach it). We focus it via
// the DOM (a real click), then type with ordinary key events — exactly as a
// user would. Scoped to the docker-agent pane so it never lands in a shell or
// editor xterm sharing the layout.
func (tc *TestContext) typeIntoAgentTerminal(text string) error {
	if err := tc.clickAgentTerminalToFocus(); err != nil {
		return fmt.Errorf("type into agent terminal: %w", err)
	}
	return chromedp.Run(tc.chromeTab.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		for _, r := range text {
			// CDP "char" events deliver one character through the keypress/input
			// path only (no keydown), so xterm forwards each exactly once.
			// chromedp.KeyEvent double-fires keydown+input and doubles every char.
			if err := input.DispatchKeyEvent(input.KeyChar).WithText(string(r)).Do(ctx); err != nil {
				return err
			}
		}
		return nil
	}))
}

// submitAgentTerminal clicks the terminal to (re)focus xterm and sends one Enter
// so the typed message is submitted to the resumed Claude TUI. A dedicated step
// (not the global press-Enter) so it always targets the docker-agent pane, via a
// single trusted rawKeyDown/keyUp that xterm turns into one \r.
func (tc *TestContext) submitAgentTerminal() error {
	if err := tc.clickAgentTerminalToFocus(); err != nil {
		return fmt.Errorf("submit agent terminal: %w", err)
	}
	return chromedp.Run(tc.chromeTab.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		if err := input.DispatchKeyEvent(input.KeyRawDown).
			WithKey("Enter").WithCode("Enter").WithWindowsVirtualKeyCode(13).Do(ctx); err != nil {
			return err
		}
		return input.DispatchKeyEvent(input.KeyUp).
			WithKey("Enter").WithCode("Enter").WithWindowsVirtualKeyCode(13).Do(ctx)
	}))
}

// clickAgentTerminalToFocus issues a real trusted mouse click at the centre of
// the Docker Agent pane's xterm so the terminal grabs keyboard focus (a DOM
// .focus() on the offscreen helper textarea doesn't reliably stick once Claude's
// TUI boots and xterm re-fits). Subsequent KeyEvents then reach the terminal.
func (tc *TestContext) clickAgentTerminalToFocus() error {
	var box struct {
		X  float64 `json:"x"`
		Y  float64 `json:"y"`
		OK bool    `json:"ok"`
	}
	rectJS := `(() => {
		const pane = document.querySelector('[data-testid="docker-agent-pane"]');
		if (!pane) return null;
		const el = pane.querySelector('.xterm-screen') || pane.querySelector('.xterm');
		if (!el) return null;
		const r = el.getBoundingClientRect();
		return { x: r.left + r.width / 2, y: r.top + r.height / 2, ok: true };
	})()`
	if err := chromedp.Run(tc.chromeTab.ctx,
		chromedp.Poll(`!!document.querySelector('[data-testid="docker-agent-pane"] .xterm-helper-textarea')`, nil, chromedp.WithPollingTimeout(20*time.Second)),
		chromedp.Evaluate(rectJS, &box),
	); err != nil {
		return err
	}
	if !box.OK {
		return fmt.Errorf("docker-agent pane xterm not found")
	}
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.MouseClickXY(box.X, box.Y),
		chromedp.Sleep(300*time.Millisecond),
	)
}

func (tc *TestContext) waitForVisible(selector string) error {
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.WaitVisible(selector, chromedp.ByQuery),
	)
}

// --- Assertion steps ---

func (tc *TestContext) assertPageContainsText(expected string) error {
	var body string
	if err := chromedp.Run(tc.chromeTab.ctx,
		chromedp.Text("body", &body, chromedp.ByQuery),
	); err != nil {
		return err
	}
	if !strings.Contains(body, expected) {
		return fmt.Errorf("page body does not contain %q (got: %.500s)", expected, body)
	}
	return nil
}

func (tc *TestContext) assertElementVisible(selector string) error {
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.WaitVisible(selector, chromedp.ByQuery),
	)
}

func (tc *TestContext) assertElementContainsText(selector, expected string) error {
	var text string
	if err := chromedp.Run(tc.chromeTab.ctx,
		chromedp.Text(selector, &text, chromedp.ByQuery),
	); err != nil {
		return err
	}
	if !strings.Contains(text, expected) {
		return fmt.Errorf("element %q text does not contain %q (got: %q)", selector, expected, text)
	}
	return nil
}

// --- Text-based interaction steps ---

func (tc *TestContext) clickElementWithText(text string) error {
	xpath := fmt.Sprintf(`(//*[normalize-space()='%s'])[1]`, text)
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.WaitVisible(xpath),
		chromedp.Click(xpath),
	)
}

func (tc *TestContext) clickButtonWithText(text string) error {
	pollJS := fmt.Sprintf(
		`!!(Array.from(document.querySelectorAll('button')).find(b => b.innerText.trim() === %q) || Array.from(document.querySelectorAll('button')).find(b => b.innerText.includes(%q)))`,
		text, text)
	// Assign a temporary ID to the matched button, then click via CSS selector.
	clickJS := fmt.Sprintf(`(() => {
		const btns = Array.from(document.querySelectorAll('button'));
		const btn = btns.find(b => b.innerText.trim() === %q) || btns.find(b => b.innerText.includes(%q));
		if (!btn) return false;
		btn.setAttribute('data-bdd-click', 'target');
		return true;
	})()`, text, text)
	var found bool
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.Poll(pollJS, nil, chromedp.WithPollingTimeout(10*time.Second)),
		chromedp.Evaluate(clickJS, &found),
		chromedp.ActionFunc(func(ctx context.Context) error {
			if !found {
				return fmt.Errorf("button with text %q not found", text)
			}
			return nil
		}),
		chromedp.Click(`button[data-bdd-click="target"]`, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('[data-bdd-click]')?.removeAttribute('data-bdd-click')`, nil),
	)
}

// clickInRegion clicks the first visible element containing text within a
// data-testid region. Uses JS polling + .click() for reliability — CDP
// coordinate-based clicks can miss elements inside overflow or zoom
// containers.
func (tc *TestContext) clickInRegion(text, testID string) error {
	js := fmt.Sprintf(`(() => {
		const root = document.querySelector('[data-testid=%q]');
		if (!root) return false;
		const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
		while (walker.nextNode()) {
			if (walker.currentNode.textContent.includes(%q)) {
				const el = walker.currentNode.parentElement;
				if (el && el.offsetWidth > 0 && el.offsetHeight > 0) return true;
			}
		}
		return false;
	})()`, testID, text)
	clickJS := fmt.Sprintf(`(() => {
		const root = document.querySelector('[data-testid=%q]');
		if (!root) return false;
		const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
		while (walker.nextNode()) {
			if (walker.currentNode.textContent.includes(%q)) {
				const el = walker.currentNode.parentElement;
				if (el && el.offsetWidth > 0 && el.offsetHeight > 0) {
					// Carry the element's center coords so the docs-capture fake
					// cursor (window mousemove/mousedown listener) glides to and
					// pulses at the click point; harmless when no cursor is present.
					const r = el.getBoundingClientRect();
					const cx = Math.round(r.left + r.width / 2), cy = Math.round(r.top + r.height / 2);
					window.dispatchEvent(new MouseEvent('mousemove', {bubbles: true, clientX: cx, clientY: cy}));
					el.dispatchEvent(new MouseEvent('mousedown', {bubbles: true, cancelable: true, clientX: cx, clientY: cy}));
					el.dispatchEvent(new MouseEvent('mouseup', {bubbles: true, cancelable: true, clientX: cx, clientY: cy}));
					el.click();
					return true;
				}
			}
		}
		return false;
	})()`, testID, text)
	var clicked bool
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.Poll(js, nil, chromedp.WithPollingTimeout(15*time.Second)),
		chromedp.Evaluate(clickJS, &clicked),
		chromedp.ActionFunc(func(ctx context.Context) error {
			if !clicked {
				return fmt.Errorf("no visible element with text %q found in [data-testid=%q]", text, testID)
			}
			return nil
		}),
	)
}

// clickTaskRow clicks the task row containing text in a tasks panel. It targets
// the data-testid="task-row-*" ancestor (the div with onClick) instead of the
// inner text span, which may be truncated or overlap with siblings that have
// stopPropagation.
func (tc *TestContext) clickTaskRow(text, testID string) error {
	textXPath := fmt.Sprintf(`(//*[@data-testid='%s']//*[contains(text(), '%s')])[1]`, testID, text)
	rowXPath := fmt.Sprintf(
		`(//*[@data-testid='%s']//*[contains(text(), '%s')])[1]/ancestor::*[starts-with(@data-testid, 'task-row-')]`,
		testID, text)
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.WaitVisible(textXPath),
		chromedp.Click(rowXPath),
	)
}

func (tc *TestContext) openGlobalTasksPanel() error {
	// Poll-click: keep clicking the sidebar Tasks button until the global
	// panel opens.  In CI the React hydration can lag behind the DOM being
	// visible, so a single click may fire before the handler is attached.
	sel := `[data-testid="sidebar-tasks-btn"]`
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.WaitVisible(sel, chromedp.ByQuery),
		chromedp.Poll(`(() => {
			if (document.body.innerText.includes("TASKS (")) return true;
			const btn = document.querySelector('[data-testid="sidebar-tasks-btn"]');
			if (btn) btn.click();
			return false;
		})()`, nil, chromedp.WithPollingTimeout(15*time.Second)),
	)
}

// clickInGitPanel clicks a tab/element by visible text within the Git panel
// (data-testid="git-panel") — e.g. "Uncommitted Diff", "Commits", "Branches Diff".
func (tc *TestContext) clickInGitPanel(text string) error {
	return tc.clickInRegion(text, "git-panel")
}

func (tc *TestContext) clickInSidebar(text string) error {
	return tc.clickInRegion(text, "sidebar")
}

// openSettingsPanelSection clicks the sidebar Settings button (retrying until
// the panel mounts) then waits for the named NavButton to be ready (loaded=true
// has resolved + schema-driven sections rendered), then clicks it. Mirrors the
// retry-poll pattern used by openGlobalTasksPanel — single clicks can race
// React hydration in CI and silently no-op.
//
// Both Poll calls use WithPollingInterval (setTimeout-based) instead of the
// default "raf" mode — headless Chrome throttles requestAnimationFrame on
// pages that aren't actively painted (Vite dev server in CI), so the rAF
// poller can fire only a handful of times in 15s and the click retry never
// hits.
func (tc *TestContext) openSettingsPanelSection(section string) error {
	navTestID := "settings-nav-" + strings.ReplaceAll(strings.ToLower(section), " ", "-")
	openJS := `(() => {
		if (document.querySelector('[data-testid="settings-panel"]')) return true;
		const btn = document.querySelector('[data-testid="sidebar-settings-btn"]');
		if (btn) { btn.click(); }
		return false;
	})()`
	navJS := fmt.Sprintf(`(() => {
		const el = document.querySelector('[data-testid=%q]');
		if (!el) return false;
		const r = el.getBoundingClientRect();
		return r.width > 0 && r.height > 0;
	})()`, navTestID)
	clickNavJS := fmt.Sprintf(`(() => {
		const el = document.querySelector('[data-testid=%q]');
		if (!el) return false;
		el.click();
		return true;
	})()`, navTestID)
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.WaitVisible(`[data-testid="sidebar-settings-btn"]`, chromedp.ByQuery),
		chromedp.Poll(openJS, nil, chromedp.WithPollingTimeout(15*time.Second), chromedp.WithPollingInterval(100*time.Millisecond)),
		chromedp.Poll(navJS, nil, chromedp.WithPollingTimeout(15*time.Second), chromedp.WithPollingInterval(100*time.Millisecond)),
		chromedp.Evaluate(clickNavJS, nil),
	)
}

func (tc *TestContext) clickInTasksPanel(text string) error {
	return tc.clickTaskRow(text, "tasks-panel")
}

func (tc *TestContext) clickInGlobalTasksPanel(text string) error {
	return tc.clickTaskRow(text, "global-tasks-panel")
}

func (tc *TestContext) clickInContextMenu(text string) error {
	return tc.clickInRegion(text, "context-menu")
}

func (tc *TestContext) clickInBranchPicker(text string) error {
	return tc.clickInRegion(text, "branch-picker")
}

// clickButtonInRegion clicks a button whose text matches within a data-testid region.
func (tc *TestContext) clickButtonInRegion(text, testID string) error {
	pollJS := fmt.Sprintf(`(() => {
		const region = document.querySelector('[data-testid="%s"]');
		if (!region) return false;
		const btn = Array.from(region.querySelectorAll('button')).find(b => b.innerText.includes(%q));
		return !!btn;
	})()`, testID, text)
	clickJS := fmt.Sprintf(`(() => {
		const region = document.querySelector('[data-testid="%s"]');
		if (!region) return false;
		const btn = Array.from(region.querySelectorAll('button')).find(b => b.innerText.includes(%q));
		if (!btn) return false;
		btn.click();
		return true;
	})()`, testID, text)
	var clicked bool
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.Poll(pollJS, nil, chromedp.WithPollingTimeout(10*time.Second)),
		chromedp.Evaluate(clickJS, &clicked),
	)
}

func (tc *TestContext) clickButtonInTasksPanel(text string) error {
	return tc.clickButtonInRegion(text, "tasks-panel")
}

func (tc *TestContext) clickInWorktreesPanel(text string) error {
	return tc.clickInRegion(text, "worktrees-panel")
}

func (tc *TestContext) clickButtonInWorktreesPanel(text string) error {
	return tc.clickButtonInRegion(text, "worktrees-panel")
}

func (tc *TestContext) clickInBranchesPanel(text string) error {
	return tc.clickInRegion(text, "branches-panel")
}

func (tc *TestContext) clickButtonInBranchesPanel(text string) error {
	return tc.clickButtonInRegion(text, "branches-panel")
}

func (tc *TestContext) clickInKanbanPanel(text string) error {
	return tc.clickInRegion(text, "kanban-panel")
}

func (tc *TestContext) clickButtonInKanbanPanel(text string) error {
	return tc.clickButtonInRegion(text, "kanban-panel")
}

func (tc *TestContext) openWorkflowsPanel() error {
	sel := `[data-testid="sidebar-workflows-btn"]`
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.WaitVisible(sel, chromedp.ByQuery),
		chromedp.Poll(`(() => {
			if (document.querySelector('[data-testid="workflows-panel"]')) return true;
			const btn = document.querySelector('[data-testid="sidebar-workflows-btn"]');
			if (btn) btn.click();
			return false;
		})()`, nil, chromedp.WithPollingTimeout(15*time.Second), chromedp.WithPollingInterval(100*time.Millisecond)),
	)
}

func (tc *TestContext) clickInWorkflowsPanel(text string) error {
	return tc.clickInRegion(text, "workflows-panel")
}

func (tc *TestContext) clickButtonInWorkflowsPanel(text string) error {
	return tc.clickButtonInRegion(text, "workflows-panel")
}

func (tc *TestContext) clickInWorkflowsSplitPanel(text string) error {
	return tc.clickInRegion(text, "workflows-split-panel")
}

func (tc *TestContext) clickButtonInWorkflowsSplitPanel(text string) error {
	return tc.clickButtonInRegion(text, "workflows-split-panel")
}

func (tc *TestContext) clickInSettingsPanel(text string) error {
	return tc.clickInRegion(text, "settings-panel")
}

func (tc *TestContext) clickButtonInSettingsPanel(text string) error {
	return tc.clickButtonInRegion(text, "settings-panel")
}

func (tc *TestContext) clickButtonInGitPanel(text string) error {
	// Prefer exact match to avoid "Branches" matching "Branches Diff".
	exactXPath := fmt.Sprintf(`(//*[@data-testid='git-panel']//button[normalize-space()='%s'])[1]`, text)
	containsXPath := fmt.Sprintf(`(//*[@data-testid='git-panel']//button[contains(., '%s')])[1]`, text)
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			// Try exact match first.
			if err := chromedp.WaitVisible(exactXPath).Do(ctx); err == nil {
				return chromedp.Click(exactXPath).Do(ctx)
			}
			// Fallback to contains match.
			if err := chromedp.WaitVisible(containsXPath).Do(ctx); err != nil {
				return err
			}
			return chromedp.Click(containsXPath).Do(ctx)
		}),
	)
}

// triggerRunNowForVisibleTask extracts the task ID from the detail view
// and calls the run API directly from the browser context.
func (tc *TestContext) triggerRunNowForVisibleTask() error {
	js := fmt.Sprintf(`(async () => {
		const panel = document.querySelector('[data-testid="tasks-panel"]') || document.querySelector('[data-testid="global-tasks-panel"]');
		if (!panel) return 'no panel found';
		const match = panel.innerText.match(/Task #(\d+)/);
		if (!match) return 'no Task #N in panel';
		const taskId = match[1];
		const resp = await fetch(%q + '/api/tasks/' + taskId + '/run', { method: 'POST' });
		return 'taskId=' + taskId + ' status=' + resp.status;
	})()`, tc.BaseURL)
	var result string
	if err := chromedp.Run(tc.chromeTab.ctx,
		chromedp.Evaluate(js, &result, func(ep *runtime.EvaluateParams) *runtime.EvaluateParams {
			return ep.WithAwaitPromise(true)
		}),
	); err != nil {
		return err
	}
	if !strings.Contains(result, "status=202") {
		return fmt.Errorf("Run Now API call unexpected: %s", result)
	}
	return nil
}

// captureVisibleTaskID extracts the task ID from the "Task #N" text in the
// tasks panel detail view and stores it in tc.TaskID for subsequent API steps.
func (tc *TestContext) captureVisibleTaskID() error {
	js := `(() => {
		const panel = document.querySelector('[data-testid="tasks-panel"]') || document.querySelector('[data-testid="global-tasks-panel"]');
		if (!panel) return '';
		const match = panel.innerText.match(/Task #(\d+)/);
		return match ? match[1] : '';
	})()`
	var idStr string
	if err := chromedp.Run(tc.chromeTab.ctx, chromedp.Evaluate(js, &idStr)); err != nil {
		return err
	}
	if idStr == "" {
		return fmt.Errorf("no Task #N found in panel detail view")
	}
	tc.TaskID = idStr
	return nil
}

func (tc *TestContext) clickButtonWithTitle(title string) error {
	sel := fmt.Sprintf(`button[title="%s"]`, title)
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.WaitVisible(sel, chromedp.ByQuery),
		chromedp.Click(sel, chromedp.ByQuery),
	)
}

func (tc *TestContext) hoverElementWithText(text string) error {
	xpath := fmt.Sprintf(`(//*[contains(text(), '%s')])[1]`, text)
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.WaitVisible(xpath),
		chromedp.MouseClickXY(0, 0, chromedp.ButtonNone), // reset position
		chromedp.Evaluate(fmt.Sprintf(`
			(function() {
				const el = document.evaluate("%s", document, null,
					XPathResult.FIRST_ORDERED_NODE_TYPE, null).singleNodeValue;
				if (el) el.dispatchEvent(new MouseEvent('mouseover', {bubbles: true}));
			})()
		`, xpath), nil),
	)
}

func (tc *TestContext) hoverInSidebar(text string) error {
	xpath := fmt.Sprintf(`(//*[@data-testid='sidebar']//*[contains(text(), '%s')])[1]`, text)
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.WaitVisible(xpath),
		chromedp.Evaluate(fmt.Sprintf(`
			(function() {
				const el = document.evaluate("%s", document, null,
					XPathResult.FIRST_ORDERED_NODE_TYPE, null).singleNodeValue;
				if (el) el.dispatchEvent(new MouseEvent('mouseover', {bubbles: true}));
			})()
		`, xpath), nil),
	)
}

func (tc *TestContext) rightClickElementWithText(text string) error {
	xpath := fmt.Sprintf(`(//*[contains(text(), '%s')])[1]`, text)
	return tc.rightClickXPath(xpath)
}

func (tc *TestContext) rightClickInSidebar(text string) error {
	xpath := fmt.Sprintf(`(//*[@data-testid='sidebar']//*[contains(text(), '%s')])[1]`, text)
	return tc.rightClickXPath(xpath)
}

func (tc *TestContext) rightClickXPath(xpath string) error {
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.WaitVisible(xpath),
		chromedp.Evaluate(fmt.Sprintf(`
			(function() {
				const el = document.evaluate("%s", document, null,
					XPathResult.FIRST_ORDERED_NODE_TYPE, null).singleNodeValue;
				if (el) el.dispatchEvent(new MouseEvent('contextmenu', {bubbles: true, button: 2}));
			})()
		`, xpath), nil),
	)
}

// --- Extended DOM interaction steps ---

func (tc *TestContext) clearAndTypeInto(text, selector string) error {
	// Clear the field using React-compatible native setter + input event.
	clearJS := fmt.Sprintf(`
		(function() {
			const el = document.querySelector(%q);
			if (!el) return false;
			const tag = el.tagName.toLowerCase();
			const proto = tag === 'textarea'
				? window.HTMLTextAreaElement.prototype
				: window.HTMLInputElement.prototype;
			const setter = Object.getOwnPropertyDescriptor(proto, 'value').set;
			setter.call(el, '');
			el.dispatchEvent(new Event('input', { bubbles: true }));
			return true;
		})()
	`, selector)
	var ok bool
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.Click(selector, chromedp.ByQuery),
		chromedp.Evaluate(clearJS, &ok),
		chromedp.SendKeys(selector, text, chromedp.ByQuery),
	)
}

func (tc *TestContext) selectFrom(value, selector string) error {
	// Set value and dispatch change event for React compatibility.
	js := fmt.Sprintf(`
		(function() {
			const el = document.querySelector(%q);
			if (!el) return false;
			const nativeInputValueSetter = Object.getOwnPropertyDescriptor(
				window.HTMLSelectElement.prototype, 'value').set;
			nativeInputValueSetter.call(el, %q);
			el.dispatchEvent(new Event('change', { bubbles: true }));
			return true;
		})()
	`, selector, value)
	var ok bool
	if err := chromedp.Run(tc.chromeTab.ctx,
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.Evaluate(js, &ok),
	); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("select element %q not found", selector)
	}
	return nil
}

// --- Keyboard steps ---

func (tc *TestContext) pressEnter() error {
	return chromedp.Run(tc.chromeTab.ctx, chromedp.KeyEvent("\r"))
}

func (tc *TestContext) pressEscape() error {
	return chromedp.Run(tc.chromeTab.ctx, chromedp.KeyEvent("\x1b"))
}

// --- Editor steps ---

// openFileInEditorTree clicks a file by name in the Editor layout's file-tree
// panel (data-testid="file-tree-panel"), loading it into the CodeMirror editor.
func (tc *TestContext) openFileInEditorTree(name string) error {
	return tc.clickInRegion(name, "file-tree-panel")
}

// appendToCodeEditor focuses the CodeMirror editor, moves the caret to the end
// of the document, and types text there. CM6 renders a contenteditable (not a
// textarea), so the caret is positioned via the DOM Selection API and then real
// key events are sent — CM6 ingests them through its beforeinput/keydown path.
func (tc *TestContext) appendToCodeEditor(text string) error {
	focusJS := `(() => {
		const cm = document.querySelector('.cm-content');
		if (!cm) return false;
		cm.focus();
		const sel = window.getSelection();
		const range = document.createRange();
		range.selectNodeContents(cm);
		range.collapse(false); // collapse to the end of the document
		sel.removeAllRanges();
		sel.addRange(range);
		return true;
	})()`
	var ok bool
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.Poll(`!!document.querySelector('.cm-content')`, nil, chromedp.WithPollingTimeout(15*time.Second)),
		chromedp.Evaluate(focusJS, &ok),
		chromedp.KeyEvent(text),
	)
}

// saveEditor triggers the editor's Cmd/Ctrl+S save handler (registered on
// window) so the edited buffer is flushed to disk before the agent commits it.
func (tc *TestContext) saveEditor() error {
	js := `(() => {
		window.dispatchEvent(new KeyboardEvent('keydown', {key:'s', code:'KeyS', ctrlKey:true, metaKey:true, bubbles:true, cancelable:true}));
		return true;
	})()`
	var ok bool
	return chromedp.Run(tc.chromeTab.ctx, chromedp.Evaluate(js, &ok))
}

// --- Wait steps ---

func (tc *TestContext) waitDuration(duration string) error {
	d, err := time.ParseDuration(duration)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", duration, err)
	}
	return chromedp.Run(tc.chromeTab.ctx, chromedp.Sleep(d))
}

// --- Extended assertion steps ---

func (tc *TestContext) assertPageNotContainsText(unexpected string) error {
	var body string
	if err := chromedp.Run(tc.chromeTab.ctx,
		chromedp.Text("body", &body, chromedp.ByQuery),
	); err != nil {
		return err
	}
	if strings.Contains(body, unexpected) {
		return fmt.Errorf("page body unexpectedly contains %q", unexpected)
	}
	return nil
}

func (tc *TestContext) assertElementNotExist(selector string) error {
	var exists bool
	if err := chromedp.Run(tc.chromeTab.ctx,
		chromedp.Evaluate(fmt.Sprintf(`document.querySelector(%q) !== null`, selector), &exists),
	); err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("element %q unexpectedly exists", selector)
	}
	return nil
}

func (tc *TestContext) addPanel(panelName string) error {
	// Click "Add panel" button, wait for dropdown, then click the panel option.
	// The dropdown is a grid of buttons inside a position:absolute div near
	// the "Add panel" button. Each button has text like "Chat ↓", "Tasks →", etc.
	js := fmt.Sprintf(`
		(function() {
			// Click the Add panel button to open the dropdown.
			const addBtn = document.querySelector('button[title="Add panel"]');
			if (!addBtn) return "no Add panel button found";
			addBtn.click();

			// Wait a tick for React to render the dropdown.
			return new Promise(resolve => {
				setTimeout(() => {
					// The menu is portaled to document.body with data-testid="add-panel-menu".
					const dropdown = document.querySelector('[data-testid="add-panel-menu"]');
					if (!dropdown) { resolve("no dropdown found"); return; }
					const btn = Array.from(dropdown.querySelectorAll('button')).find(
						b => b.textContent.includes('%s')
					);
					if (!btn) { resolve("panel option not found in dropdown"); return; }
					// Singleton panels (Tasks, Git, Memory, ...) render the option
					// disabled when one is already open. Treat as a no-op so the
					// step is idempotent across navigation back to a channel whose
					// saved layout already contains the panel; close the dropdown
					// by re-clicking the toggle so it doesn't block later clicks.
					if (btn.disabled) { addBtn.click(); resolve("ok"); return; }
					btn.click();
					resolve("ok");
				}, 300);
			});
		})()
	`, panelName)
	var result string
	if err := chromedp.Run(tc.chromeTab.ctx,
		chromedp.Evaluate(js, &result, func(ep *runtime.EvaluateParams) *runtime.EvaluateParams {
			return ep.WithAwaitPromise(true)
		}),
	); err != nil {
		return fmt.Errorf("addPanel %q: %w", panelName, err)
	}
	if result != "ok" {
		return fmt.Errorf("addPanel %q: %s", panelName, result)
	}
	return nil
}

// openAddPanelMenuInGitPanel clicks the Git leaf's own "Add panel" button (each
// leaf header has one) so the panel selector opens anchored to the Git panel —
// choosing a "↓" option there splits the new panel in BELOW Git. The first
// document-wide Add-panel button would target the wrong leaf, so we scope to the
// leaf wrapping [data-testid="git-panel"]. A synthetic mouse event at the button
// drives the docs-capture cursor.
func (tc *TestContext) openAddPanelMenuInGitPanel() error {
	js := `(function(){
		var git = document.querySelector('[data-testid="git-panel"]');
		if (!git) return 'no git panel';
		var addBtn = null, el = git;
		for (var i = 0; i < 6 && el; i++) {
			el = el.parentElement;
			if (el) { var b = el.querySelector('button[title="Add panel"]'); if (b) { addBtn = b; break; } }
		}
		if (!addBtn) return 'no add-panel button near git panel';
		var r = addBtn.getBoundingClientRect();
		var cx = Math.round(r.left + r.width / 2), cy = Math.round(r.top + r.height / 2);
		window.dispatchEvent(new MouseEvent('mousemove', {bubbles: true, clientX: cx, clientY: cy}));
		window.dispatchEvent(new MouseEvent('mousedown', {bubbles: true, clientX: cx, clientY: cy}));
		addBtn.click();
		return 'ok';
	})()`
	var res string
	if err := chromedp.Run(tc.chromeTab.ctx, chromedp.Evaluate(js, &res)); err != nil {
		return err
	}
	if res != "ok" {
		return fmt.Errorf("open add-panel menu in git panel: %s", res)
	}
	return nil
}

// openAddPanelMenuFirst clicks the first pane's "Add panel" button (cursor-
// driven so the docs cursor moves to it) and leaves the selector open. Use on a
// single-pane layout (e.g. the Terminal tab with just a shell) where there's one
// such button; the open menu is then recorded before a "<name> ↓" pick.
func (tc *TestContext) openAddPanelMenuFirst() error {
	js := `(function(){
		var b = document.querySelector('button[title="Add panel"]');
		if (!b) return 'no add-panel button';
		var r = b.getBoundingClientRect();
		var cx = Math.round(r.left + r.width / 2), cy = Math.round(r.top + r.height / 2);
		window.dispatchEvent(new MouseEvent('mousemove', {bubbles: true, clientX: cx, clientY: cy}));
		window.dispatchEvent(new MouseEvent('mousedown', {bubbles: true, clientX: cx, clientY: cy}));
		b.click();
		return 'ok';
	})()`
	var res string
	if err := chromedp.Run(tc.chromeTab.ctx, chromedp.Evaluate(js, &res)); err != nil {
		return err
	}
	if res != "ok" {
		return fmt.Errorf("open add-panel menu: %s", res)
	}
	return nil
}

// addPanelBelowFromMenu clicks the "<name> ↓" option in the open add-panel menu,
// which splits the new panel in below the leaf the menu was opened from.
func (tc *TestContext) addPanelBelowFromMenu(name string) error {
	js := fmt.Sprintf(`(function(){
		var menu = document.querySelector('[data-testid="add-panel-menu"]');
		if (!menu) return 'no add-panel menu';
		var btn = Array.from(menu.querySelectorAll('button')).find(function(b){
			return b.textContent.includes(%q) && b.textContent.includes('↓');
		});
		if (!btn) return 'option not found';
		var r = btn.getBoundingClientRect();
		var cx = Math.round(r.left + r.width / 2), cy = Math.round(r.top + r.height / 2);
		window.dispatchEvent(new MouseEvent('mousemove', {bubbles: true, clientX: cx, clientY: cy}));
		window.dispatchEvent(new MouseEvent('mousedown', {bubbles: true, clientX: cx, clientY: cy}));
		btn.click();
		return 'ok';
	})()`, name)
	var res string
	if err := chromedp.Run(tc.chromeTab.ctx, chromedp.Evaluate(js, &res)); err != nil {
		return err
	}
	if res != "ok" {
		return fmt.Errorf("add %q panel below from menu: %s", name, res)
	}
	return nil
}

func (tc *TestContext) clickTaskCreateButton() error {
	// The task create "+" button is a small button inside the tasks panel list header,
	// next to the "{n} task(s)" count text. We find it by locating the span with
	// "task" text and clicking the sibling button.
	js := `
		(function() {
			const spans = document.querySelectorAll('span');
			for (const s of spans) {
				if (s.textContent.match(/\d+ tasks?$/)) {
					const btn = s.parentElement.querySelector('button');
					if (btn && btn.textContent.trim() === '+') {
						btn.click();
						return true;
					}
				}
			}
			return false;
		})()
	`
	var found bool
	if err := chromedp.Run(tc.chromeTab.ctx, chromedp.Evaluate(js, &found)); err != nil {
		return fmt.Errorf("clicking task create button: %w", err)
	}
	if !found {
		return fmt.Errorf("task create '+' button not found near task count")
	}
	return nil
}

// WithPollingInterval(100ms) switches Poll to setTimeout-based polling — the
// default "raf" mode is throttled to ~1Hz in headless Chrome on pages that
// aren't actively painted (Vite dev server in CI), causing 10s waits to fire
// only a handful of times and miss text that briefly flashed.
func (tc *TestContext) waitForTextToAppearWithTimeout(timeout, text string) error {
	d, err := time.ParseDuration(timeout)
	if err != nil {
		return fmt.Errorf("invalid timeout %q: %w", timeout, err)
	}
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.Poll(fmt.Sprintf(`document.body.innerText.includes(%q)`, text),
			nil, chromedp.WithPollingTimeout(d), chromedp.WithPollingInterval(100*time.Millisecond)),
	)
}

func (tc *TestContext) waitForTextToDisappearWithTimeout(timeout, text string) error {
	d, err := time.ParseDuration(timeout)
	if err != nil {
		return fmt.Errorf("invalid timeout %q: %w", timeout, err)
	}
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.Poll(fmt.Sprintf(`!document.body.innerText.includes(%q)`, text),
			nil, chromedp.WithPollingTimeout(d), chromedp.WithPollingInterval(100*time.Millisecond)),
	)
}

// waitForSelectorToDisappear polls until the CSS selector matches no element.
// Used to wait for the agent's run to finish (the chat's Stop button unmounts).
func (tc *TestContext) waitForSelectorToDisappear(timeout, selector string) error {
	d, err := time.ParseDuration(timeout)
	if err != nil {
		return fmt.Errorf("invalid timeout %q: %w", timeout, err)
	}
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.Poll(fmt.Sprintf(`document.querySelector(%q) === null`, selector),
			nil, chromedp.WithPollingTimeout(d), chromedp.WithPollingInterval(200*time.Millisecond)),
	)
}

// waitForSelectorVisibleWithTimeout polls until the selector exists and has a
// non-zero box, bounded by timeout (a hung agent fails fast rather than at the
// scenario deadline).
func (tc *TestContext) waitForSelectorVisibleWithTimeout(timeout, selector string) error {
	d, err := time.ParseDuration(timeout)
	if err != nil {
		return fmt.Errorf("invalid timeout %q: %w", timeout, err)
	}
	js := fmt.Sprintf(`(() => { const el = document.querySelector(%q); if (!el) return false; const r = el.getBoundingClientRect(); return r.width > 0 && r.height > 0; })()`, selector)
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.Poll(js, nil, chromedp.WithPollingTimeout(d), chromedp.WithPollingInterval(200*time.Millisecond)),
	)
}

// waitForSelectorVisibleBestEffort waits like waitForSelectorVisibleWithTimeout
// but never fails the scenario — used for content that depends on a live agent
// (e.g. an agent-created playground) so a rare miss doesn't break the journey.
func (tc *TestContext) waitForSelectorVisibleBestEffort(timeout, selector string) error {
	_ = tc.waitForSelectorVisibleWithTimeout(timeout, selector)
	return nil
}

// selectCreatedPlayground ensures the Playground panel shows a playground: if its
// iframe isn't already rendering, it picks the first real option from the panel's
// <select> (the agent-created playground). Best-effort — no-op if nothing to pick
// (the iframe wait that follows is the real, lenient gate). This avoids relying
// on the playground.update WS event's auto-select, which can be missed.
func (tc *TestContext) selectCreatedPlayground() error {
	js := `(() => {
		const panel = document.querySelector('[data-testid="playground-panel"]');
		if (!panel) return 'no-panel';
		if (panel.querySelector('iframe')) return 'ok';
		const sel = panel.querySelector('select');
		if (!sel) return 'no-select';
		const opt = Array.from(sel.options).find(o => o.value && !o.disabled);
		if (!opt) return 'no-option';
		const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value').set;
		setter.call(sel, opt.value);
		sel.dispatchEvent(new Event('change', { bubbles: true }));
		return 'ok';
	})()`
	var res string
	return chromedp.Run(tc.chromeTab.ctx, chromedp.Evaluate(js, &res))
}

func (tc *TestContext) clickLabelWithText(text string) error {
	xpath := fmt.Sprintf(`//label[contains(text(), '%s')]`, text)
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.WaitVisible(xpath),
		chromedp.Click(xpath),
	)
}

func (tc *TestContext) waitForTextToAppear(text string) error {
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.Poll(fmt.Sprintf(`document.body.innerText.includes(%q)`, text),
			nil, chromedp.WithPollingTimeout(10*time.Second), chromedp.WithPollingInterval(100*time.Millisecond)),
	)
}

func (tc *TestContext) waitForTextToDisappear(text string) error {
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.Poll(fmt.Sprintf(`!document.body.innerText.includes(%q)`, text),
			nil, chromedp.WithPollingTimeout(10*time.Second), chromedp.WithPollingInterval(100*time.Millisecond)),
	)
}

// --- Debug steps ---

func (tc *TestContext) takeScreenshot() error {
	var buf []byte
	if err := chromedp.Run(tc.chromeTab.ctx,
		chromedp.FullScreenshot(&buf, 90),
	); err != nil {
		return err
	}
	dir := "screenshots"
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, fmt.Sprintf("screenshot-%d.png", time.Now().UnixMilli()))
	return os.WriteFile(path, buf, 0o644)
}

// captureDocScreenshot writes a deterministically-named viewport PNG for the
// docs site. It is a no-op unless LOOP_DOCS_CAPTURE is set, so @docs scenarios
// that accidentally run in a normal suite never write assets. The destination
// defaults to the docs static images tree (relative to the test package dir)
// and is overridable via LOOP_DOCS_OUT. name may contain "/" to nest assets.
func (tc *TestContext) captureDocScreenshot(name string) error {
	if os.Getenv("LOOP_DOCS_CAPTURE") == "" {
		return nil
	}
	// Hide the fake cursor (if injected) so it never appears in still PNGs.
	hideCursor := `(function(){var c=document.getElementById('loop-docs-cursor');if(c)c.style.display='none';return 'ok';})()`
	showCursor := `(function(){var c=document.getElementById('loop-docs-cursor');if(c)c.style.display='';return 'ok';})()`
	var res string
	_ = chromedp.Run(tc.chromeTab.ctx, chromedp.Evaluate(hideCursor, &res))
	var buf []byte
	err := chromedp.Run(tc.chromeTab.ctx,
		chromedp.CaptureScreenshot(&buf),
	)
	_ = chromedp.Run(tc.chromeTab.ctx, chromedp.Evaluate(showCursor, &res))
	if err != nil {
		return err
	}
	outDir := os.Getenv("LOOP_DOCS_OUT")
	if outDir == "" {
		outDir = filepath.Join("..", "..", "docs", "static", "images", "features")
	}
	// Strip any leading slash and ".." segments so name can't escape outDir.
	safe := strings.TrimPrefix(filepath.Clean("/"+name), "/")
	path := filepath.Join(outDir, safe+".png")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, buf, 0o644)
}

// showCaption injects a full-screen title-card overlay so the recorded video has
// an on-screen explanation between panels. No-op outside LOOP_DOCS_CAPTURE.
// Captions must be hidden before a screenshot so they don't obscure the panel.
func (tc *TestContext) showCaption(text string) error {
	if os.Getenv("LOOP_DOCS_CAPTURE") == "" {
		return nil
	}
	js := fmt.Sprintf(`(function(){
		var id='loop-docs-caption';
		var el=document.getElementById(id);
		if(!el){el=document.createElement('div');el.id=id;document.body.appendChild(el);}
		el.style.cssText='position:fixed;inset:0;z-index:2147483647;display:flex;'+
			'align-items:center;justify-content:center;text-align:center;padding:0 10%%;'+
			'background:rgba(8,10,14,0.92);color:#e8eaed;'+
			'font:600 44px/1.45 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;'+
			'-webkit-font-smoothing:antialiased';
		el.textContent=%q;
		return 'ok';
	})()`, text)
	var res string
	return chromedp.Run(tc.chromeTab.ctx, chromedp.Evaluate(js, &res))
}

// showLoopTitleCard shows a branded "∞ Loop" intro/outro card that fades in,
// holds, and fades out (the journey waits ~3.4s for the animation to be
// recorded, then hides it). No-op outside LOOP_DOCS_CAPTURE.
// injectMouseCursor adds a fake pointer that follows mouse events, so the
// screencast can "show the mouse." CDP's screencast records the page, not the OS
// cursor, so we render our own: a window-level (capture-phase) mousemove listener
// glides the pointer (CSS transition) and mousedown draws a click ripple. Real
// CDP clicks (chromedp.Click) emit native mouse events the listener catches;
// clickInRegion's JS-dispatched clicks carry clientX/clientY for the same reason.
func (tc *TestContext) injectMouseCursor() error {
	if os.Getenv("LOOP_DOCS_CAPTURE") == "" {
		return nil
	}
	js := `(function(){
		if (document.getElementById('loop-docs-cursor')) return 'exists';
		var c = document.createElement('div');
		c.id = 'loop-docs-cursor';
		c.style.cssText = 'position:fixed;left:0;top:0;z-index:2147483646;pointer-events:none;'+
			'transition:transform 0.45s cubic-bezier(.22,.61,.36,1);will-change:transform;'+
			'filter:drop-shadow(0 1px 2px rgba(0,0,0,.55));';
		c.innerHTML = '<svg width="22" height="22" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">'+
			'<path d="M5 2.5 L5 19 L9.2 14.8 L12 21 L14.4 20 L11.6 13.8 L17.5 13.8 Z" '+
			'fill="#ffffff" stroke="#111111" stroke-width="1.2" stroke-linejoin="round"/></svg>';
		document.body.appendChild(c);
		var x = window.innerWidth/2, y = window.innerHeight*0.6;
		function move(nx, ny){ x=nx; y=ny; c.style.transform='translate('+nx+'px,'+ny+'px)'; }
		move(x, y);
		function pulse(px, py){
			var r = document.createElement('div');
			r.style.cssText='position:fixed;left:'+px+'px;top:'+py+'px;width:10px;height:10px;'+
				'margin:-5px 0 0 -5px;border-radius:50%;border:2px solid rgba(90,170,255,.9);'+
				'background:rgba(90,170,255,.22);z-index:2147483645;pointer-events:none;'+
				'transition:width .45s ease-out,height .45s ease-out,margin .45s ease-out,opacity .45s ease-out;';
			document.body.appendChild(r);
			requestAnimationFrame(function(){
				r.style.width='44px'; r.style.height='44px'; r.style.margin='-22px 0 0 -22px'; r.style.opacity='0';
			});
			setTimeout(function(){ r.remove(); }, 480);
		}
		window.addEventListener('mousemove', function(e){ move(e.clientX, e.clientY); }, true);
		window.addEventListener('mousedown', function(e){ move(e.clientX, e.clientY); pulse(e.clientX, e.clientY); }, true);
		window.__loopCursor = { move: move, pulse: pulse };
		return 'ok';
	})()`
	var res string
	return chromedp.Run(tc.chromeTab.ctx, chromedp.Evaluate(js, &res))
}

func (tc *TestContext) showLoopTitleCard() error     { return tc.loopTitleCard(false) }
func (tc *TestContext) showLoopTitleCardHold() error { return tc.loopTitleCard(true) }

// loopCardInjectJS builds the JS that injects (or updates) the full-screen
// branded "∞ Loop" card at the given opacity. The logo is the inlined codebase
// logo (app/src/assets/logo-horizontal.svg): the infinity mark next to the
// "Loop" wordmark, recoloured light for the dark card. The SVG has no '%' so the
// single %s opacity placeholder is safe.
func loopCardInjectJS(opacity string) string {
	return fmt.Sprintf(`(function(){
		var id='loop-docs-caption';
		var el=document.getElementById(id);
		if(!el){el=document.createElement('div');el.id=id;document.body.appendChild(el);}
		el.style.cssText='position:fixed;inset:0;z-index:2147483647;display:flex;'+
			'align-items:center;justify-content:center;background:#06080c;opacity:%s';
		el.innerHTML='<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 567 148" width="500">'+
			'<g transform="translate(94.9, 74.0) scale(0.4154)"><path d="M0 0c-43-57.3-86-86-128.7-86a86 86 0 1 0 0 172c42.7 0 85.7-28.7 128.7-86Zm0 0c43 57.3 86 86 128.7 86a86 86 0 0 0 0-172c-42.7 0-85.7 28.7-128.7 86Z" fill="none" stroke="#e8eaed" stroke-width="18" stroke-linecap="round" stroke-linejoin="round"/></g>'+
			'<g transform="translate(213.8, 116.3)"><path d="M20.00 0.00Q15.31 0.00 12.62 -2.77Q9.92 -5.55 9.92 -10.47L9.92 -103.59Q9.92 -108.59 12.62 -111.33Q15.31 -114.06 20.00 -114.06Q24.69 -114.06 27.38 -111.33Q30.08 -108.59 30.08 -103.59L30.08 -17.03L73.28 -17.03Q77.42 -17.03 79.96 -14.73Q82.50 -12.42 82.50 -8.52Q82.50 -4.61 79.96 -2.30Q77.42 0.00 73.28 0.00ZM128.81 1.64Q116.70 1.64 107.67 -3.52Q98.65 -8.67 93.69 -18.36Q88.73 -28.05 88.73 -41.41Q88.73 -54.84 93.73 -64.45Q98.73 -74.06 107.75 -79.30Q116.77 -84.53 128.81 -84.53Q140.91 -84.53 149.90 -79.34Q158.88 -74.14 163.88 -64.49Q168.88 -54.84 168.88 -41.41Q168.88 -27.97 163.92 -18.32Q158.96 -8.67 149.98 -3.52Q140.99 1.64 128.81 1.64ZM128.81 -13.75Q134.98 -13.75 139.55 -16.99Q144.12 -20.23 146.62 -26.45Q149.12 -32.66 149.12 -41.41Q149.12 -50.23 146.62 -56.41Q144.12 -62.58 139.55 -65.82Q134.98 -69.06 128.81 -69.06Q122.63 -69.06 118.06 -65.82Q113.49 -62.58 110.99 -56.41Q108.49 -50.23 108.49 -41.41Q108.49 -32.66 110.99 -26.45Q113.49 -20.23 118.06 -16.99Q122.63 -13.75 128.81 -13.75ZM218.00 1.64Q205.89 1.64 196.87 -3.52Q187.84 -8.67 182.88 -18.36Q177.92 -28.05 177.92 -41.41Q177.92 -54.84 182.92 -64.45Q187.92 -74.06 196.95 -79.30Q205.97 -84.53 218.00 -84.53Q230.11 -84.53 239.09 -79.34Q248.08 -74.14 253.08 -64.49Q258.08 -54.84 258.08 -41.41Q258.08 -27.97 253.12 -18.32Q248.16 -8.67 239.17 -3.52Q230.19 1.64 218.00 1.64ZM218.00 -13.75Q224.17 -13.75 228.74 -16.99Q233.31 -20.23 235.81 -26.45Q238.31 -32.66 238.31 -41.41Q238.31 -50.23 235.81 -56.41Q233.31 -62.58 228.74 -65.82Q224.17 -69.06 218.00 -69.06Q211.83 -69.06 207.26 -65.82Q202.69 -62.58 200.19 -56.41Q197.69 -50.23 197.69 -41.41Q197.69 -32.66 200.19 -26.45Q202.69 -20.23 207.26 -16.99Q211.83 -13.75 218.00 -13.75ZM280.24 29.45Q276.02 29.45 273.29 26.80Q270.55 24.14 270.55 19.22L270.55 -74.45Q270.55 -79.22 273.17 -81.84Q275.79 -84.45 280.01 -84.45Q284.23 -84.45 286.88 -81.84Q289.54 -79.22 289.54 -74.45L289.54 -68.52L289.93 -68.52Q292.27 -73.36 296.10 -76.88Q299.93 -80.39 304.97 -82.30Q310.01 -84.22 316.02 -84.22Q326.49 -84.22 334.23 -79.02Q341.96 -73.83 346.18 -64.26Q350.40 -54.69 350.40 -41.41Q350.40 -28.20 346.22 -18.59Q342.04 -8.98 334.38 -3.83Q326.73 1.33 316.34 1.33Q310.40 1.33 305.32 -0.51Q300.24 -2.34 296.45 -5.74Q292.66 -9.14 290.40 -13.75L290.01 -13.75L290.01 19.22Q290.01 24.14 287.27 26.80Q284.54 29.45 280.24 29.45ZM310.09 -14.61Q316.41 -14.61 320.98 -17.89Q325.55 -21.17 328.02 -27.19Q330.48 -33.20 330.48 -41.41Q330.48 -49.61 328.02 -55.59Q325.55 -61.56 320.98 -64.88Q316.41 -68.20 310.09 -68.20Q304.07 -68.20 299.50 -64.88Q294.93 -61.56 292.43 -55.51Q289.93 -49.45 289.85 -41.41Q289.93 -33.28 292.43 -27.27Q294.93 -21.25 299.50 -17.93Q304.07 -14.61 310.09 -14.61Z" fill="#e8eaed"/></g></svg>';
		return 'ok';
	})()`, opacity)
}

// showLoopIntroCard injects the branded card at full opacity. Called BEFORE the
// recording starts so the screencast's very first frame is the "∞ Loop" card
// (otherwise the app UI shows until the card fades in).
func (tc *TestContext) showLoopIntroCard() error {
	if os.Getenv("LOOP_DOCS_CAPTURE") == "" {
		return nil
	}
	var res string
	if err := chromedp.Run(tc.chromeTab.ctx, chromedp.Evaluate(loopCardInjectJS("1"), &res)); err != nil {
		return err
	}
	// Let the card actually composite before the caller starts the screencast,
	// otherwise the recorder's first frame catches the app underneath.
	time.Sleep(600 * time.Millisecond)
	return nil
}

// fadeOutLoopTitleCard fades the already-shown card from full to transparent
// (JS-stepped so headless Chrome actually repaints/records it). The caller then
// removes it with `I hide caption`.
func (tc *TestContext) fadeOutLoopTitleCard() error {
	if os.Getenv("LOOP_DOCS_CAPTURE") == "" {
		return nil
	}
	ctx := tc.chromeTab.ctx
	var res string
	const total = 0.8
	start := time.Now()
	for {
		elapsed := time.Since(start).Seconds()
		if elapsed >= total {
			break
		}
		op := (total - elapsed) / total
		step := fmt.Sprintf(`(function(){var e=document.getElementById('loop-docs-caption');if(e)e.style.opacity=%q;return 'ok';})()`, fmt.Sprintf("%.3f", op))
		_ = chromedp.Run(ctx, chromedp.Evaluate(step, &res))
		time.Sleep(33 * time.Millisecond) // ~30fps opacity steps for a smooth, well-recorded fade
	}
	return nil
}

// loopTitleCard injects the branded "∞ Loop" card and drives its opacity from
// JS (headless Chrome throttles CSS animations on a static page). holdAtEnd=false
// fades in, holds, then fades out (intro); holdAtEnd=true fades in and stays at
// full opacity so the recording can end on the card (outro). A tiny opacity
// jitter during the hold forces the repaints the screencast records.
func (tc *TestContext) loopTitleCard(holdAtEnd bool) error {
	if os.Getenv("LOOP_DOCS_CAPTURE") == "" {
		return nil
	}
	ctx := tc.chromeTab.ctx
	var res string
	if err := chromedp.Run(ctx, chromedp.Evaluate(loopCardInjectJS("0"), &res)); err != nil {
		return err
	}
	// Drive the fade in/hold/out from JS rather than a CSS animation: headless
	// Chrome throttles CSS animations on otherwise-static pages, leaving the card
	// stuck invisible. Stepping opacity from JS both animates it and forces the
	// repaints the screencast records.
	const total = 4.5
	start := time.Now()
	for i := 0; ; i++ {
		elapsed := time.Since(start).Seconds()
		if elapsed >= total {
			break
		}
		op := 1.0
		switch {
		case elapsed < 0.6:
			op = elapsed / 0.6 // fade in
		case !holdAtEnd && elapsed > total-0.8:
			op = (total - elapsed) / 0.8 // fade out (intro)
		case holdAtEnd:
			op = 1.0 - 0.004*float64(i%2) // hold at full opacity, jitter to keep recording
		}
		step := fmt.Sprintf(`(function(){var e=document.getElementById('loop-docs-caption');if(e)e.style.opacity=%q;return 'ok';})()`, fmt.Sprintf("%.3f", op))
		_ = chromedp.Run(ctx, chromedp.Evaluate(step, &res))
		time.Sleep(33 * time.Millisecond) // ~30fps opacity steps for a smooth, well-recorded fade
	}
	return nil
}

// hideCaption removes the title-card overlay injected by showCaption.
func (tc *TestContext) hideCaption() error {
	if os.Getenv("LOOP_DOCS_CAPTURE") == "" {
		return nil
	}
	js := `(function(){var el=document.getElementById('loop-docs-caption');if(el)el.remove();return 'ok';})()`
	var res string
	return chromedp.Run(tc.chromeTab.ctx, chromedp.Evaluate(js, &res))
}

// dumpPaneLeaves prints the pane-header-slot element ids that are currently
// mounted, plus any element whose data-testid contains "approval-card". Used
// to debug "the Docker Agent pane didn't mount" vs "the gate event went to
// the wrong source key" failures.
func (tc *TestContext) dumpPaneLeaves() error {
	js := `(() => {
		const slots = Array.from(document.querySelectorAll('[id^="pane-header-slot-"]')).map(el => el.id);
		const cards = Array.from(document.querySelectorAll('[data-testid^="approval-card"]')).map(el => el.getAttribute('data-testid'));
		return 'slots=' + JSON.stringify(slots) + ' cards=' + JSON.stringify(cards);
	})()`
	var result string
	if err := chromedp.Run(tc.chromeTab.ctx, chromedp.Evaluate(js, &result)); err != nil {
		return err
	}
	fmt.Printf("[DUMP PANE LEAVES] %s\n", result)
	return nil
}

func (tc *TestContext) dumpVisibleRefs() error {
	js := `(() => {
		const els = document.querySelectorAll('button, a[href], input, select, textarea, [role="button"], [role="link"], [role="tab"]');
		const results = [];
		for (let i = 0; i < els.length; i++) {
			const el = els[i];
			const rect = el.getBoundingClientRect();
			if (rect.width <= 0 || rect.height <= 0) continue;
			const tag = el.tagName.toLowerCase();
			const text = (el.innerText || '').trim().substring(0, 60);
			const testid = el.dataset?.testid || '';
			const parentTestid = el.closest('[data-testid]')?.dataset?.testid || '';
			results.push(tag + ' | text=' + JSON.stringify(text) + ' | disabled=' + el.disabled + ' | testid=' + testid + ' | parent=' + parentTestid);
		}
		return results.join('\n');
	})()`
	var result string
	if err := chromedp.Run(tc.chromeTab.ctx, chromedp.Evaluate(js, &result)); err != nil {
		return err
	}
	fmt.Printf("[DUMP VISIBLE REFS]\n%s\n", result)
	return nil
}

func (tc *TestContext) dumpPageText() error {
	var body string
	if err := chromedp.Run(tc.chromeTab.ctx,
		chromedp.Text("body", &body, chromedp.ByQuery),
	); err != nil {
		return err
	}
	fmt.Printf("[DUMP PAGE TEXT] %.2000s\n", body)
	return nil
}

// dragLayoutTab synthesizes an HTML5 drag+drop between two layout tabs.
// Real chromedp mouse events can't carry a DataTransfer payload, and React's
// drop handler reads dataTransfer.getData(TAB_DRAG_MIME) to decide what to
// reorder. We bypass that by dispatching a synthetic drop event whose
// dataTransfer is a plain object exposing the same shape (getData / types).
func (tc *TestContext) dragLayoutTab(fromName, toName string) error {
	js := fmt.Sprintf(`
		(function() {
			const MIME = 'application/x-loop-layout-tab';
			const src = document.querySelector('[data-testid="layout-tab-%s"]');
			const dst = document.querySelector('[data-testid="layout-tab-%s"]');
			if (!src || !dst) return 'tab not found';
			const dt = {
				_data: {[MIME]: %q},
				types: [MIME],
				effectAllowed: 'move',
				dropEffect: 'move',
				getData(m) { return this._data[m] || ''; },
				setData(m, v) { this._data[m] = v; },
			};
			function fire(target, type) {
				const e = new Event(type, {bubbles: true, cancelable: true});
				Object.defineProperty(e, 'dataTransfer', {value: dt});
				target.dispatchEvent(e);
			}
			fire(src, 'dragstart');
			fire(dst, 'dragenter');
			fire(dst, 'dragover');
			fire(dst, 'drop');
			fire(src, 'dragend');
			return 'ok';
		})()
	`, fromName, toName, fromName)
	var result string
	if err := chromedp.Run(tc.chromeTab.ctx, chromedp.Evaluate(js, &result)); err != nil {
		return fmt.Errorf("dragLayoutTab %q→%q: %w", fromName, toName, err)
	}
	if result != "ok" {
		return fmt.Errorf("dragLayoutTab %q→%q: %s", fromName, toName, result)
	}
	return nil
}

// assertLayoutTabOrder verifies the layout tab strip's left-to-right order
// matches the comma-separated list of tab names.
func (tc *TestContext) assertLayoutTabOrder(expected string) error {
	js := `(() => Array.from(document.querySelectorAll('[data-testid^="layout-tab-"]'))
		.map(el => el.getAttribute('data-testid').replace('layout-tab-', ''))
		.join(','))()`
	var actual string
	if err := chromedp.Run(tc.chromeTab.ctx, chromedp.Evaluate(js, &actual)); err != nil {
		return err
	}
	want := strings.Join(strings.Split(strings.ReplaceAll(expected, " ", ""), ","), ",")
	got := strings.ReplaceAll(actual, " ", "")
	if got != want {
		return fmt.Errorf("layout tab order: want %q, got %q", want, got)
	}
	return nil
}

// scrollChatMessagesToBottom finds the chat messages scroll container and
// snaps it to the bottom synchronously. Used to drive the trigger-quote
// banner: the banner appears when no user message bubble is visible in the
// container and at least one is above it.
func (tc *TestContext) scrollChatMessagesToBottom() error {
	js := `(() => {
		// The messages container is the only element with overflowY:auto
		// inside the chat view. Its first child is the sticky banner slot;
		// next is the messageColumn carrying [data-msg-uuid] bubbles.
		const bubble = document.querySelector('[data-msg-uuid]');
		if (!bubble) return 'no message bubble';
		let el = bubble.parentElement;
		while (el && getComputedStyle(el).overflowY !== 'auto') el = el.parentElement;
		if (!el) return 'no scroll container';
		el.scrollTop = el.scrollHeight;
		el.dispatchEvent(new Event('scroll'));
		return 'ok';
	})()`
	var result string
	if err := chromedp.Run(tc.chromeTab.ctx, chromedp.Evaluate(js, &result)); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("scrollChatMessagesToBottom: %s", result)
	}
	return nil
}

// injectUserMessage seeds a single user message.created event into the chat
// store with a stable msg_id so subsequent bot messages stack after it.
func (tc *TestContext) injectUserMessage(content string) error {
	if tc.ChannelID == "" {
		return fmt.Errorf("no channel_id set; use 'I set up a test channel via API' step first")
	}
	if err := tc.ensureChromeTab(); err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"type":       "message.created",
		"channel_id": tc.ChannelID,
		"timestamp":  1,
		"data": map[string]any{
			"msg_id":       "bdd-user-1",
			"author_id":    "user",
			"author_name":  "tester",
			"content":      content,
			"is_bot":       false,
			"is_processed": true,
			"priority":     0,
		},
	})
	if err != nil {
		return err
	}
	return tc.dispatchTestEvents(payload)
}

// injectBotMessages dispatches N synthetic bot message.created events so the
// chat container grows tall enough to push the prior user message above the
// viewport when scrolled to bottom.
func (tc *TestContext) injectBotMessages(countStr, content string) error {
	if tc.ChannelID == "" {
		return fmt.Errorf("no channel_id set; use 'I set up a test channel via API' step first")
	}
	if err := tc.ensureChromeTab(); err != nil {
		return err
	}
	n, err := strconv.Atoi(countStr)
	if err != nil {
		return fmt.Errorf("invalid count %q: %w", countStr, err)
	}
	payloads := make([][]byte, 0, n)
	for i := 0; i < n; i++ {
		p, err := json.Marshal(map[string]any{
			"type":       "message.created",
			"channel_id": tc.ChannelID,
			"timestamp":  2 + i,
			"data": map[string]any{
				"msg_id":       fmt.Sprintf("bdd-bot-%d", i),
				"author_id":    "bot",
				"author_name":  "bot",
				"content":      content,
				"is_bot":       true,
				"is_processed": true,
				"priority":     0,
			},
		})
		if err != nil {
			return err
		}
		payloads = append(payloads, p)
	}
	return tc.dispatchTestEvents(payloads...)
}

// seedChatTimeline injects a synthetic message.created event so
// ChatMessages mounts. ChatView.tsx falls back to a welcome screen when the
// chat is empty, which keeps the ExitPlan/AskUserQuestion cards offscreen.
func (tc *TestContext) seedChatTimeline() ([]byte, error) {
	return json.Marshal(map[string]any{
		"type":       "message.created",
		"channel_id": tc.ChannelID,
		"timestamp":  1,
		"data": map[string]any{
			"msg_id":       "bdd-seed-1",
			"author_id":    "user",
			"author_name":  "tester",
			"content":      "seed",
			"is_bot":       false,
			"is_processed": true,
			"priority":     0,
		},
	})
}

// dispatchTestEvents fires N synthetic CustomEvents at the chat store via
// the loop:test-event hook. The store handles them like real WS messages.
//
// Waits for window.__loopWsRehydrated first: the store's WS-onOpen rehydrates
// reconcile local cards against the backend's pending lists and drop anything
// without a server-side counterpart. A synthetic card injected before that
// pass settles gets wiped — on slow CI runners the events WS can still be
// connecting when the scenario reaches its inject step, which was the cause
// of the recurring journey_gate_approval timeout flake.
func (tc *TestContext) dispatchTestEvents(payloads ...[]byte) error {
	if err := chromedp.Run(tc.chromeTab.ctx,
		chromedp.Poll("window.__loopWsRehydrated === true", nil, chromedp.WithPollingTimeout(20*time.Second)),
	); err != nil {
		return fmt.Errorf("waiting for WS rehydrate before injecting test events: %w", err)
	}
	dispatches := make([]string, 0, len(payloads))
	for _, p := range payloads {
		dispatches = append(dispatches,
			fmt.Sprintf("window.dispatchEvent(new CustomEvent('loop:test-event', {detail: JSON.stringify(%s)}));", string(p)))
	}
	js := "(() => {" + strings.Join(dispatches, "") + "})()"
	return chromedp.Run(tc.chromeTab.ctx, chromedp.Evaluate(js, nil))
}

// injectExitPlanEvent fires a synthetic agent.exit_plan event into the
// page's chat store via the loop:test-event CustomEvent hook. Lets us render
// the ExitPlanCard in BDD without spinning up a real agent run.
//
// ChatMessages (which renders the ExitPlanCard) only mounts when the chat is
// non-empty (ChatView.tsx falls back to a welcome screen otherwise), so we
// first inject a synthetic message.created event to populate the timeline.
func (tc *TestContext) injectExitPlanEvent(planText string) error {
	if tc.ChannelID == "" {
		return fmt.Errorf("no channel_id set; use 'I set up a test channel via API' step first")
	}
	if err := tc.ensureChromeTab(); err != nil {
		return err
	}
	seed, err := tc.seedChatTimeline()
	if err != nil {
		return fmt.Errorf("marshalling seed payload: %w", err)
	}
	payload, err := json.Marshal(map[string]any{
		"type":       "agent.exit_plan",
		"channel_id": tc.ChannelID,
		"data":       map[string]string{"plan": planText},
	})
	if err != nil {
		return fmt.Errorf("marshalling exit_plan payload: %w", err)
	}
	return tc.dispatchTestEvents(seed, payload)
}

// injectAskUserEvent fires a synthetic agent.ask_user event into the chat
// store. options is a comma-separated list of option labels (e.g. "yes,no").
// Builds one question with those options; for tests that need multi-question
// scenarios extend with a richer step signature.
func (tc *TestContext) injectAskUserEvent(question, options string) error {
	if tc.ChannelID == "" {
		return fmt.Errorf("no channel_id set; use 'I set up a test channel via API' step first")
	}
	if err := tc.ensureChromeTab(); err != nil {
		return err
	}
	seed, err := tc.seedChatTimeline()
	if err != nil {
		return fmt.Errorf("marshalling seed payload: %w", err)
	}
	opts := []map[string]string{}
	for _, label := range strings.Split(options, ",") {
		label = strings.TrimSpace(label)
		if label == "" {
			continue
		}
		opts = append(opts, map[string]string{"label": label})
	}
	payload, err := json.Marshal(map[string]any{
		"type":       "agent.ask_user",
		"channel_id": tc.ChannelID,
		"data": map[string]any{
			"questions": []map[string]any{
				{
					"question": question,
					"header":   "BDD",
					"options":  opts,
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("marshalling ask_user payload: %w", err)
	}
	return tc.dispatchTestEvents(seed, payload)
}

// injectChannelID returns the channel to address synthetic gate events to:
// the per-scenario API channel when set, else the shared sample-project
// channel (the @docs walkthrough deliberately leaves tc.ChannelID unset so
// per-scenario cleanup can't delete the shared channel).
func (tc *TestContext) injectChannelID() string {
	if tc.ChannelID != "" {
		return tc.ChannelID
	}
	return sampleProject.channelID
}

// injectGateApprovalRequested fires a synthetic gate.approval_requested event
// into the chat store. `source` is the per-pane routing tag the backend
// attaches: "chat" (or empty → defaults to chat) routes the ApprovalCard to
// ChatMessages; "terminal:<leafId>" routes it to the matching Terminal pane
// overlay (see WorkspaceLayout's paneSourceTag wiring). `target` is the path
// or arg the card displays — use a unique value per scenario so text
// assertions are unambiguous.
func (tc *TestContext) injectGateApprovalRequested(reqID, source, target string) error {
	channelID := tc.injectChannelID()
	if channelID == "" {
		return fmt.Errorf("no channel_id set; use 'I set up a test channel via API' step first")
	}
	if err := tc.ensureChromeTab(); err != nil {
		return err
	}
	// "terminal:newest-docker-agent" resolves to the highest-numbered
	// docker-agent pane currently in the layout. Scenarios must not hardcode
	// "docker-agent-1": the leaf-id counter's start value depends on which
	// features ran earlier in the suite, so the first added pane is not
	// always #1 — the cause of the recurring gate-approval flake.
	if source == "terminal:newest-docker-agent" {
		// Pane header slots carry id="pane-header-slot-<leafId>"; poll because
		// the pane mounts a React tick after the add-panel click.
		js := `(() => {
			const ids = Array.from(document.querySelectorAll('[id^="pane-header-slot-docker-agent-"]'))
				.map(el => el.id.replace('pane-header-slot-', ''));
			ids.sort((a, b) => Number(a.split('-').pop()) - Number(b.split('-').pop()));
			return ids.length ? ids[ids.length - 1] : "";
		})()`
		var leafID string
		if err := chromedp.Run(tc.chromeTab.ctx,
			chromedp.Poll(js, &leafID, chromedp.WithPollingTimeout(10*time.Second)),
		); err != nil {
			return fmt.Errorf("resolving newest docker-agent pane: %w", err)
		}
		source = "terminal:" + leafID
	}
	seed, err := tc.seedChatTimeline()
	if err != nil {
		return fmt.Errorf("marshalling seed payload: %w", err)
	}
	payload, err := json.Marshal(map[string]any{
		"type":       "gate.approval_requested",
		"channel_id": channelID,
		"data": map[string]any{
			"req_id": reqID,
			"kind":   "exec",
			"target": target,
			"source": source,
		},
	})
	if err != nil {
		return fmt.Errorf("marshalling gate.approval_requested payload: %w", err)
	}
	return tc.dispatchTestEvents(seed, payload)
}

// injectGateApprovalResolved fires a synthetic gate.approval_resolved event
// into the chat store. The store walks gateApprovals and drops the entry
// whose req_id matches, regardless of source — mirrors what happens when a
// real approval is resolved over the wire.
func (tc *TestContext) injectGateApprovalResolved(reqID string) error {
	channelID := tc.injectChannelID()
	if channelID == "" {
		return fmt.Errorf("no channel_id set; use 'I set up a test channel via API' step first")
	}
	if err := tc.ensureChromeTab(); err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"type":       "gate.approval_resolved",
		"channel_id": channelID,
		"data": map[string]any{
			"req_id":   reqID,
			"decision": "once",
		},
	})
	if err != nil {
		return fmt.Errorf("marshalling gate.approval_resolved payload: %w", err)
	}
	return tc.dispatchTestEvents(payload)
}
