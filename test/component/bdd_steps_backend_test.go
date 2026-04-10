//go:build component

package component

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cucumber/godog"
	"github.com/gorilla/websocket"
)

func registerBackendSteps(ctx *godog.ScenarioContext, tc *TestContext) {
	// HTTP request steps
	ctx.Step(`^I send a GET request to "([^"]*)"$`, tc.sendGET)
	ctx.Step(`^I send a POST request to "([^"]*)" with body:$`, tc.sendPOST)
	ctx.Step(`^I send a PATCH request to "([^"]*)" with body:$`, tc.sendPATCH)
	ctx.Step(`^I send a DELETE request to "([^"]*)" with body:$`, tc.sendDELETEWithBody)
	ctx.Step(`^I send a DELETE request to "([^"]*)"$`, tc.sendDELETE)

	// Response assertion steps
	ctx.Step(`^the response status should be (\d+)$`, tc.assertStatus)
	ctx.Step(`^the response should contain "([^"]*)"$`, tc.assertBodyContains)
	ctx.Step(`^the response JSON "([^"]*)" should be "([^"]*)"$`, tc.assertJSONField)
	ctx.Step(`^the response JSON "([^"]*)" should not be empty$`, tc.assertJSONFieldNotEmpty)

	// Entity steps
	ctx.Step(`^a channel exists for directory "([^"]*)"$`, tc.ensureChannel)
	ctx.Step(`^I create a task with prompt "([^"]*)" and schedule "([^"]*)"$`, tc.createTask)
	ctx.Step(`^the task list should contain the created task$`, tc.assertTaskInList)

	// Hybrid API setup steps (for frontend scenarios that need pre-seeded data)
	ctx.Step(`^I set up a test channel via API for directory "([^"]*)"$`, tc.setupChannelViaAPI)
	ctx.Step(`^I set up a test channel via API for git repo "([^"]*)"$`, tc.setupChannelViaAPIForGitRepo)
	ctx.Step(`^I create a thread "([^"]*)" under the current channel via API$`, tc.createThreadViaAPI)
	ctx.Step(`^I set up a test task via API with prompt "([^"]*)" and schedule "([^"]*)"$`, tc.setupTaskViaAPI)
	ctx.Step(`^I set up a test task via API with type "([^"]*)" prompt "([^"]*)" and schedule "([^"]*)"$`, tc.setupTaskViaAPIWithType)
	ctx.Step(`^I trigger Run Now via API for the current task$`, tc.triggerRunNowViaAPI)
	ctx.Step(`^I set up a worktree "([^"]*)" on branch "([^"]*)" under the current channel via API$`, tc.setupWorktreeViaAPI)
	ctx.Step(`^I create a disk-only git worktree "([^"]*)" on branch "([^"]*)"$`, tc.createDiskOnlyWorktree)
	ctx.Step(`^I create a branch "([^"]*)" via API$`, tc.createBranchViaAPI)
	ctx.Step(`^I create uncommitted files "([^"]*)" in the repo$`, tc.createUncommittedFiles)

	// Shortcut setup steps
	ctx.Step(`^I add a prompt shortcut "([^"]*)" with prompt "([^"]*)" via API$`, tc.addShortcutViaAPI)

	// WebSocket steps
	ctx.Step(`^I connect to the events WebSocket$`, tc.connectEventsWS)
	ctx.Step(`^the WebSocket connection should be established$`, tc.assertWSConnected)
	ctx.Step(`^I close the WebSocket connection$`, tc.closeWS)
	ctx.Step(`^I wait up to "([^"]*)" for a WebSocket event of type "([^"]*)"$`, tc.waitForWSEventOfType)
	ctx.Step(`^the WebSocket event data "([^"]*)" should be "([^"]*)"$`, tc.assertWSEventDataField)
	ctx.Step(`^the WebSocket event data "([^"]*)" should not be empty$`, tc.assertWSEventDataFieldNotEmpty)

	// Polling steps
	ctx.Step(`^I wait up to "([^"]*)" for the task to stop running via API$`, tc.waitForTaskStopRunning)

	// Worktree task setup
	ctx.Step(`^I set up a worktree task via API with prompt "([^"]*)" and schedule "([^"]*)"$`, tc.setupWorktreeTaskViaAPI)
}

// --- HTTP request steps ---

func (tc *TestContext) sendGET(path string) error {
	return tc.doRequest(http.MethodGet, path, "")
}

func (tc *TestContext) sendPOST(path string, body *godog.DocString) error {
	return tc.doRequest(http.MethodPost, path, body.Content)
}

func (tc *TestContext) sendPATCH(path string, body *godog.DocString) error {
	return tc.doRequest(http.MethodPatch, path, body.Content)
}

func (tc *TestContext) sendDELETEWithBody(path string, body *godog.DocString) error {
	return tc.doRequest(http.MethodDelete, path, body.Content)
}

func (tc *TestContext) sendDELETE(path string) error {
	return tc.doRequest(http.MethodDelete, path, "")
}

// --- Response assertion steps ---

func (tc *TestContext) assertStatus(expected int) error {
	if tc.LastStatus != expected {
		return fmt.Errorf("expected status %d, got %d (body: %s)", expected, tc.LastStatus, string(tc.LastBody))
	}
	return nil
}

func (tc *TestContext) assertBodyContains(expected string) error {
	if !strings.Contains(string(tc.LastBody), expected) {
		return fmt.Errorf("response body does not contain %q: %s", expected, string(tc.LastBody))
	}
	return nil
}

func (tc *TestContext) assertJSONField(field, expected string) error {
	if tc.LastJSON == nil {
		return fmt.Errorf("response is not JSON: %s", string(tc.LastBody))
	}
	val, ok := tc.LastJSON[field]
	if !ok {
		return fmt.Errorf("JSON field %q not found in response: %s", field, string(tc.LastBody))
	}
	actual := fmt.Sprintf("%v", val)
	if actual != expected {
		return fmt.Errorf("JSON field %q: expected %q, got %q", field, expected, actual)
	}
	return nil
}

func (tc *TestContext) assertJSONFieldNotEmpty(field string) error {
	if tc.LastJSON == nil {
		return fmt.Errorf("response is not JSON: %s", string(tc.LastBody))
	}
	val, ok := tc.LastJSON[field]
	if !ok {
		return fmt.Errorf("JSON field %q not found in response: %s", field, string(tc.LastBody))
	}
	if val == nil || fmt.Sprintf("%v", val) == "" {
		return fmt.Errorf("JSON field %q is empty", field)
	}
	return nil
}

// --- Entity steps ---

func (tc *TestContext) ensureChannel(dirPath string) error {
	body := fmt.Sprintf(`{"dir_path": %q, "platform": "local"}`, dirPath)
	if err := tc.doRequest(http.MethodPost, "/api/channels", body); err != nil {
		return err
	}
	if tc.LastStatus != http.StatusOK {
		return fmt.Errorf("failed to create channel: status %d, body: %s", tc.LastStatus, string(tc.LastBody))
	}
	if tc.LastJSON != nil {
		if id, ok := tc.LastJSON["channel_id"].(string); ok {
			tc.ChannelID = id
		}
	}
	return nil
}

func (tc *TestContext) createTask(prompt, schedule string) error {
	if tc.ChannelID == "" {
		return fmt.Errorf("no channel_id set; use 'a channel exists for directory' step first")
	}
	payload := map[string]any{
		"channel_id": tc.ChannelID,
		"prompt":     prompt,
		"schedule":   schedule,
		"type":       "interval",
	}
	b, _ := json.Marshal(payload)
	if err := tc.doRequest(http.MethodPost, "/api/tasks", string(b)); err != nil {
		return err
	}
	if tc.LastStatus != http.StatusOK && tc.LastStatus != http.StatusCreated {
		return fmt.Errorf("failed to create task: status %d, body: %s", tc.LastStatus, string(tc.LastBody))
	}
	if tc.LastJSON != nil {
		if id, ok := tc.LastJSON["id"]; ok {
			tc.TaskID = fmt.Sprintf("%v", id)
		}
	}
	return nil
}

func (tc *TestContext) assertTaskInList() error {
	if tc.TaskID == "" {
		return fmt.Errorf("no task_id set; use 'I create a task' step first")
	}
	if err := tc.doRequest(http.MethodGet, "/api/tasks", ""); err != nil {
		return err
	}
	if !strings.Contains(string(tc.LastBody), tc.TaskID) {
		return fmt.Errorf("task %s not found in task list: %s", tc.TaskID, string(tc.LastBody))
	}
	return nil
}

// --- Hybrid API setup steps ---

func (tc *TestContext) setupChannelViaAPI(dirPath string) error {
	body := fmt.Sprintf(`{"dir_path": %q, "platform": "local"}`, dirPath)
	if err := tc.doRequest(http.MethodPost, "/api/channels", body); err != nil {
		return err
	}
	if tc.LastStatus != http.StatusOK {
		return fmt.Errorf("failed to create channel: status %d, body: %s", tc.LastStatus, string(tc.LastBody))
	}
	if tc.LastJSON != nil {
		if id, ok := tc.LastJSON["channel_id"].(string); ok {
			tc.ChannelID = id
			tc.CreatedChannelIDs = append(tc.CreatedChannelIDs, id)
		}
	}
	return nil
}

func (tc *TestContext) createThreadViaAPI(name string) error {
	if tc.ChannelID == "" {
		return fmt.Errorf("no channel_id set; use 'I set up a test channel via API' step first")
	}
	payload := map[string]string{
		"channel_id": tc.ChannelID,
		"name":       name,
	}
	b, _ := json.Marshal(payload)
	if err := tc.doRequest(http.MethodPost, "/api/threads", string(b)); err != nil {
		return err
	}
	if tc.LastStatus != http.StatusOK && tc.LastStatus != http.StatusCreated {
		return fmt.Errorf("failed to create thread: status %d, body: %s", tc.LastStatus, string(tc.LastBody))
	}
	if tc.LastJSON != nil {
		if id, ok := tc.LastJSON["thread_id"].(string); ok {
			tc.CreatedThreadIDs = append(tc.CreatedThreadIDs, id)
		}
	}
	return nil
}

func (tc *TestContext) setupTaskViaAPI(prompt, schedule string) error {
	return tc.setupTaskViaAPIWithType("interval", prompt, schedule)
}

func (tc *TestContext) setupTaskViaAPIWithType(taskType, prompt, schedule string) error {
	if tc.ChannelID == "" {
		return fmt.Errorf("no channel_id set; use 'I set up a test channel via API' step first")
	}
	payload := map[string]any{
		"channel_id": tc.ChannelID,
		"prompt":     prompt,
		"schedule":   schedule,
		"type":       taskType,
	}
	b, _ := json.Marshal(payload)
	if err := tc.doRequest(http.MethodPost, "/api/tasks", string(b)); err != nil {
		return err
	}
	if tc.LastStatus != http.StatusOK && tc.LastStatus != http.StatusCreated {
		return fmt.Errorf("failed to create task: status %d, body: %s", tc.LastStatus, string(tc.LastBody))
	}
	if tc.LastJSON != nil {
		if id, ok := tc.LastJSON["id"]; ok {
			tc.TaskID = fmt.Sprintf("%v", id)
			tc.CreatedTaskIDs = append(tc.CreatedTaskIDs, tc.TaskID)
		}
	}
	return nil
}

func (tc *TestContext) setupChannelViaAPIForGitRepo(name string) error {
	dir, err := os.MkdirTemp("", "bdd-git-"+name+"-")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	tc.CreatedDirs = append(tc.CreatedDirs, dir)

	// Initialize a git repo with an initial commit.
	cmds := [][]string{
		{"git", "init", "-b", "main", dir},
		{"git", "-C", dir, "config", "user.email", "bdd@test.local"},
		{"git", "-C", dir, "config", "user.name", "BDD Test"},
	}
	for _, args := range cmds {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("running %v: %s: %w", args, out, err)
		}
	}

	// Create a file and commit.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# BDD Test Repo\n"), 0o644); err != nil {
		return fmt.Errorf("writing README: %w", err)
	}
	cmds = [][]string{
		{"git", "-C", dir, "add", "."},
		{"git", "-C", dir, "commit", "-m", "initial commit"},
	}
	for _, args := range cmds {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("running %v: %s: %w", args, out, err)
		}
	}

	tc.ChannelDir = dir
	return tc.setupChannelViaAPI(dir)
}

func (tc *TestContext) triggerRunNowViaAPI() error {
	if tc.TaskID == "" {
		return fmt.Errorf("no task_id set; use a task setup step first")
	}
	return tc.doRequest(http.MethodPost, "/api/tasks/"+tc.TaskID+"/run", "")
}

func (tc *TestContext) setupWorktreeViaAPI(name, branch string) error {
	if tc.ChannelID == "" {
		return fmt.Errorf("no channel_id set; use 'I set up a test channel via API' step first")
	}
	payload := map[string]any{
		"channel_id": tc.ChannelID,
		"branch":     branch,
		"name":       name,
	}
	b, _ := json.Marshal(payload)
	if err := tc.doRequest(http.MethodPost, "/api/worktrees", string(b)); err != nil {
		return err
	}
	if tc.LastStatus != http.StatusOK && tc.LastStatus != http.StatusCreated {
		return fmt.Errorf("failed to create worktree: status %d, body: %s", tc.LastStatus, string(tc.LastBody))
	}
	if tc.LastJSON != nil {
		if id, ok := tc.LastJSON["thread_id"].(string); ok {
			tc.WorktreeThreadID = id
			tc.CreatedThreadIDs = append(tc.CreatedThreadIDs, id)
		}
		if wp, ok := tc.LastJSON["worktree_path"].(string); ok {
			tc.WorktreePath = wp
		}
	}
	return nil
}

func (tc *TestContext) createBranchViaAPI(name string) error {
	if tc.ChannelID == "" {
		return fmt.Errorf("no channel_id set; use 'I set up a test channel via API' step first")
	}
	payload := map[string]any{"name": name}
	b, _ := json.Marshal(payload)
	if err := tc.doRequest(http.MethodPost, fmt.Sprintf("/api/channels/%s/branches/create", tc.ChannelID), string(b)); err != nil {
		return err
	}
	if tc.LastStatus != http.StatusOK {
		return fmt.Errorf("failed to create branch: status %d, body: %s", tc.LastStatus, string(tc.LastBody))
	}
	return nil
}

func (tc *TestContext) createUncommittedFiles(namesCsv string) error {
	if tc.ChannelDir == "" {
		return fmt.Errorf("no channel dir set; use 'I set up a test channel via API for git repo' step first")
	}
	for _, name := range strings.Split(namesCsv, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		fpath := filepath.Join(tc.ChannelDir, name)
		if err := os.MkdirAll(filepath.Dir(fpath), 0o755); err != nil {
			return fmt.Errorf("creating dirs for %s: %w", name, err)
		}
		if err := os.WriteFile(fpath, []byte("// "+name+"\n"), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", name, err)
		}
	}
	return nil
}

// createDiskOnlyWorktree creates a raw git worktree on disk without importing
// it through the Loop API, so it appears as a non-imported entry in the panel.
func (tc *TestContext) createDiskOnlyWorktree(name, branch string) error {
	if tc.ChannelDir == "" {
		return fmt.Errorf("no channel dir set; use 'I set up a test channel via API for git repo' step first")
	}
	wtPath := filepath.Join(tc.ChannelDir, ".worktrees", name)
	out, err := exec.Command("git", "-C", tc.ChannelDir, "worktree", "add", wtPath, "-b", name, branch).CombinedOutput()
	if err != nil {
		return fmt.Errorf("creating git worktree: %s: %w", out, err)
	}
	return nil
}

func (tc *TestContext) addShortcutViaAPI(name, prompt string) error {
	payload := map[string]string{
		"action":      "add",
		"name":        name,
		"description": name,
		"prompt":      prompt,
	}
	b, _ := json.Marshal(payload)
	if err := tc.doRequest(http.MethodPost, "/api/shortcuts", string(b)); err != nil {
		return err
	}
	if tc.LastStatus != http.StatusNoContent {
		return fmt.Errorf("failed to add shortcut: status %d, body: %s", tc.LastStatus, string(tc.LastBody))
	}
	tc.CreatedShortcutNames = append(tc.CreatedShortcutNames, name)
	return nil
}

// --- WebSocket steps ---

func (tc *TestContext) connectEventsWS() error {
	wsURL := strings.Replace(tc.BaseURL, "http://", "ws://", 1) + "/api/ws?channels=bdd-test"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("connecting to WebSocket: %w", err)
	}
	tc.WSConn = conn
	return nil
}

func (tc *TestContext) assertWSConnected() error {
	if tc.WSConn == nil {
		return fmt.Errorf("WebSocket connection is not established")
	}
	return nil
}

func (tc *TestContext) closeWS() error {
	if tc.WSConn != nil {
		err := tc.WSConn.Close()
		tc.WSConn = nil
		return err
	}
	return nil
}

func (tc *TestContext) waitForWSEventOfType(timeout, eventType string) error {
	if tc.WSConn == nil {
		return fmt.Errorf("WebSocket not connected")
	}
	dur, err := time.ParseDuration(timeout)
	if err != nil {
		return fmt.Errorf("invalid timeout %q: %w", timeout, err)
	}
	deadline := time.Now().Add(dur)
	tc.WSConn.SetReadDeadline(deadline) //nolint:errcheck
	defer tc.WSConn.SetReadDeadline(time.Time{}) //nolint:errcheck
	for {
		_, msg, err := tc.WSConn.ReadMessage()
		if err != nil {
			return fmt.Errorf("waiting for WS event %q: %w", eventType, err)
		}
		var evt map[string]any
		if json.Unmarshal(msg, &evt) == nil {
			if t, _ := evt["type"].(string); t == eventType {
				tc.LastWSEvent = evt
				return nil
			}
		}
	}
}

func (tc *TestContext) assertWSEventDataField(field, expected string) error {
	if tc.LastWSEvent == nil {
		return fmt.Errorf("no WebSocket event captured")
	}
	data, _ := tc.LastWSEvent["data"].(map[string]any)
	if data == nil {
		return fmt.Errorf("WebSocket event has no data field")
	}
	actual := fmt.Sprintf("%v", data[field])
	if actual != expected {
		return fmt.Errorf("WS event data %q: expected %q, got %q", field, expected, actual)
	}
	return nil
}

func (tc *TestContext) assertWSEventDataFieldNotEmpty(field string) error {
	if tc.LastWSEvent == nil {
		return fmt.Errorf("no WebSocket event captured")
	}
	data, _ := tc.LastWSEvent["data"].(map[string]any)
	if data == nil {
		return fmt.Errorf("WebSocket event has no data field")
	}
	val, ok := data[field]
	if !ok || val == nil || fmt.Sprintf("%v", val) == "" || fmt.Sprintf("%v", val) == "0" {
		return fmt.Errorf("WS event data %q is empty or missing", field)
	}
	return nil
}

// --- Polling steps ---

func (tc *TestContext) waitForTaskStopRunning(timeout string) error {
	if tc.TaskID == "" {
		return fmt.Errorf("no task_id set")
	}
	dur, err := time.ParseDuration(timeout)
	if err != nil {
		return fmt.Errorf("invalid timeout %q: %w", timeout, err)
	}
	deadline := time.Now().Add(dur)
	for time.Now().Before(deadline) {
		if err := tc.doRequest(http.MethodGet, "/api/tasks/"+tc.TaskID, ""); err != nil {
			return err
		}
		if running, ok := tc.LastJSON["running"].(bool); ok && !running {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("task %s still running after %s", tc.TaskID, timeout)
}

// --- Worktree task setup ---

func (tc *TestContext) setupWorktreeTaskViaAPI(prompt, schedule string) error {
	if tc.ChannelID == "" {
		return fmt.Errorf("no channel_id set; use 'I set up a test channel via API' step first")
	}
	payload := map[string]any{
		"channel_id": tc.ChannelID,
		"prompt":     prompt,
		"schedule":   schedule,
		"type":       "interval",
		"worktree":   true,
	}
	b, _ := json.Marshal(payload)
	if err := tc.doRequest(http.MethodPost, "/api/tasks", string(b)); err != nil {
		return err
	}
	if tc.LastStatus != http.StatusOK && tc.LastStatus != http.StatusCreated {
		return fmt.Errorf("failed to create worktree task: status %d, body: %s", tc.LastStatus, string(tc.LastBody))
	}
	if tc.LastJSON != nil {
		if id, ok := tc.LastJSON["id"]; ok {
			tc.TaskID = fmt.Sprintf("%v", id)
			tc.CreatedTaskIDs = append(tc.CreatedTaskIDs, tc.TaskID)
		}
	}
	return nil
}
