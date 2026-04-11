//go:build component

package component

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/emulation"
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
	ctx.Step(`^I clear and type "([^"]*)" into "([^"]*)"$`, tc.clearAndTypeInto)
	ctx.Step(`^I wait for "([^"]*)" to be visible$`, tc.waitForVisible)
	ctx.Step(`^I select "([^"]*)" from "([^"]*)"$`, tc.selectFrom)

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

	// Label interaction
	ctx.Step(`^I click on the label with text "([^"]*)"$`, tc.clickLabelWithText)

	// Panel interaction
	ctx.Step(`^I add a "([^"]*)" panel$`, tc.addPanel)
	ctx.Step(`^I click the task create button$`, tc.clickTaskCreateButton)

	// Debugging
	ctx.Step(`^I take a screenshot$`, tc.takeScreenshot)
	ctx.Step(`^I dump page text$`, tc.dumpPageText)
	ctx.Step(`^I dump visible refs$`, tc.dumpVisibleRefs)
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
	timeoutCtx, timeoutCancel := context.WithTimeout(parentCtx, 120*time.Second)
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
	if chromeManager.remote {
		// In host browser mode tabs share the same profile; clear stored
		// state so each scenario starts fresh (headless mode uses isolated
		// browser contexts, so this is not needed there).
		// Also set a consistent viewport size since the host window may be
		// any size (headless mode uses WindowSize(1280,800) at launch).
		actions = append(actions,
			chromedp.ActionFunc(func(ctx context.Context) error {
				return emulation.SetDeviceMetricsOverride(1280, 800, 1.0, false).Do(ctx)
			}),
			chromedp.Evaluate(`localStorage.clear(); sessionStorage.clear()`, nil),
			chromedp.Reload(),
		)
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
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.SendKeys(selector, text, chromedp.ByQuery),
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
// data-testid region. chromedp.Click internally calls scrollIntoViewIfNeeded.
func (tc *TestContext) clickInRegion(text, testID string) error {
	xpath := fmt.Sprintf(`(//*[@data-testid='%s']//*[contains(text(), '%s')])[1]`, testID, text)
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.WaitVisible(xpath),
		chromedp.Click(xpath),
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

func (tc *TestContext) clickInSidebar(text string) error {
	return tc.clickInRegion(text, "sidebar")
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
	xpath := fmt.Sprintf(`(//*[@data-testid='%s']//button[contains(., '%s')])[1]`, testID, text)
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.WaitVisible(xpath),
		chromedp.Click(xpath),
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
					// Find the dropdown: it's a position:absolute div near the button.
					const parent = addBtn.closest('div[style*="position"]') || addBtn.parentElement;
					if (!parent) { resolve("no parent container"); return; }
					const dropdown = parent.querySelector('div[style*="position: absolute"]');
					if (!dropdown) {
						// Fallback: find any recently rendered absolute-positioned dropdown
						const all = document.querySelectorAll('div[style*="position: absolute"]');
						for (const d of all) {
							const btn = Array.from(d.querySelectorAll('button')).find(
								b => b.textContent.includes('%s')
							);
							if (btn) { btn.click(); resolve("ok"); return; }
						}
						resolve("no dropdown found");
						return;
					}
					const btn = Array.from(dropdown.querySelectorAll('button')).find(
						b => b.textContent.includes('%s')
					);
					if (btn) { btn.click(); resolve("ok"); }
					else { resolve("panel option not found in dropdown"); }
				}, 300);
			});
		})()
	`, panelName, panelName)
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

func (tc *TestContext) waitForTextToAppearWithTimeout(timeout, text string) error {
	d, err := time.ParseDuration(timeout)
	if err != nil {
		return fmt.Errorf("invalid timeout %q: %w", timeout, err)
	}
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.Poll(fmt.Sprintf(`document.body.innerText.includes(%q)`, text),
			nil, chromedp.WithPollingTimeout(d)),
	)
}

func (tc *TestContext) waitForTextToDisappearWithTimeout(timeout, text string) error {
	d, err := time.ParseDuration(timeout)
	if err != nil {
		return fmt.Errorf("invalid timeout %q: %w", timeout, err)
	}
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.Poll(fmt.Sprintf(`!document.body.innerText.includes(%q)`, text),
			nil, chromedp.WithPollingTimeout(d)),
	)
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
			nil, chromedp.WithPollingTimeout(10*time.Second)),
	)
}

func (tc *TestContext) waitForTextToDisappear(text string) error {
	return chromedp.Run(tc.chromeTab.ctx,
		chromedp.Poll(fmt.Sprintf(`!document.body.innerText.includes(%q)`, text),
			nil, chromedp.WithPollingTimeout(10*time.Second)),
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
