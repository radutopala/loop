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
	"sync"
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
	ctx.Step(`^I set up a sample project channel$`, tc.setupSampleProjectChannel)
	ctx.Step(`^I create a thread "([^"]*)" under the current channel via API$`, tc.createThreadViaAPI)
	ctx.Step(`^I set up a test task via API with prompt "([^"]*)" and schedule "([^"]*)"$`, tc.setupTaskViaAPI)
	ctx.Step(`^I set up a test task via API with type "([^"]*)" prompt "([^"]*)" and schedule "([^"]*)"$`, tc.setupTaskViaAPIWithType)
	ctx.Step(`^I trigger Run Now via API for the current task$`, tc.triggerRunNowViaAPI)
	ctx.Step(`^I set up a worktree "([^"]*)" on branch "([^"]*)" under the current channel via API$`, tc.setupWorktreeViaAPI)
	ctx.Step(`^I create a disk-only git worktree "([^"]*)" on branch "([^"]*)"$`, tc.createDiskOnlyWorktree)
	ctx.Step(`^I create a branch "([^"]*)" via API$`, tc.createBranchViaAPI)
	ctx.Step(`^I create uncommitted files "([^"]*)" in the repo$`, tc.createUncommittedFiles)
	ctx.Step(`^I stage a new file "([^"]*)" in the repo$`, tc.stageNewFile)
	ctx.Step(`^I modify "([^"]*)" without staging$`, tc.modifyWithoutStaging)

	// Ticket setup steps
	ctx.Step(`^I create a ticket "([^"]*)" with type "([^"]*)" via API$`, tc.createTicketViaAPI)

	// Shortcut setup steps
	ctx.Step(`^I add a prompt shortcut "([^"]*)" with prompt "([^"]*)" via API$`, tc.addShortcutViaAPI)
	ctx.Step(`^I clear all prompt shortcuts via API$`, tc.clearAllShortcutsViaAPI)
	ctx.Step(`^I add a bash shortcut "([^"]*)" with command "([^"]*)" via API$`, tc.addBashShortcutViaAPI)
	ctx.Step(`^I clear all bash shortcuts via API$`, tc.clearAllBashShortcutsViaAPI)

	// WebSocket steps
	ctx.Step(`^I connect to the events WebSocket$`, tc.connectEventsWS)
	ctx.Step(`^the WebSocket connection should be established$`, tc.assertWSConnected)
	ctx.Step(`^I close the WebSocket connection$`, tc.closeWS)
	ctx.Step(`^I wait up to "([^"]*)" for a WebSocket event of type "([^"]*)"$`, tc.waitForWSEventOfType)
	ctx.Step(`^the WebSocket event data "([^"]*)" should be "([^"]*)"$`, tc.assertWSEventDataField)
	ctx.Step(`^the WebSocket event data "([^"]*)" should not be empty$`, tc.assertWSEventDataFieldNotEmpty)

	// Polling steps
	ctx.Step(`^I wait up to "([^"]*)" for the task to stop running via API$`, tc.waitForTaskStopRunning)

	// Workflow task setup
	ctx.Step(`^I set up a workflow task via API with workflow "([^"]*)" and schedule "([^"]*)"$`, tc.setupWorkflowTaskViaAPINoInputs)
	ctx.Step(`^I set up a workflow task via API with workflow "([^"]*)" and schedule "([^"]*)" with inputs:$`, tc.setupWorkflowTaskViaAPIWithInputs)

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

// gitRun runs a git command in dir, returning a descriptive error on failure.
func gitRun(dir string, args ...string) error {
	full := append([]string{"-C", dir}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		return fmt.Errorf("git %v: %s: %w", args, out, err)
	}
	return nil
}

// setupSampleProjectChannel materializes a small, realistic sample project (an
// "Acme Notes" TypeScript service) with a loop project config, gives it git
// history plus a couple of uncommitted edits, and opens a channel on it. Used
// by @docs capture scenarios so the Files/Git panels show real content instead
// of an empty repo. The dir base ("acme-notes") becomes the channel name.
// sampleProject is built once per run and reused across all @docs scenarios
// (one channel in the DB) — far cheaper than regenerating the git repo every
// scenario, and it keeps the sidebar to a single acme-notes channel.
var sampleProject struct {
	once      sync.Once
	channelID string
	dir       string
	err       error
}

func (tc *TestContext) setupSampleProjectChannel() error {
	sampleProject.once.Do(func() { sampleProject.err = tc.buildSampleProjectOnce() })
	if sampleProject.err != nil {
		return sampleProject.err
	}
	// Deliberately do NOT set tc.ChannelID: per-scenario cleanup() deletes
	// tc.ChannelID, which would remove the shared sample channel after the
	// first scenario. Docs scenarios drive the channel via the UI (sidebar),
	// not the API, so they don't need it.
	tc.ChannelDir = sampleProject.dir
	return nil
}

// buildSampleProjectOnce materializes the sample project and opens a channel on
// it, recording both in the package-level sampleProject for reuse. The channel
// and dir are intentionally NOT tracked for per-scenario cleanup so they
// survive the whole run (the harness wipes the temp dir at the end).
func (tc *TestContext) buildSampleProjectOnce() error {
	// LOOP_DOCS_WORKDIR_BASE (set by docs-capture) is a shared, writable base so
	// a sibling agent container can bind-mount the workdir by the same path.
	parent, err := os.MkdirTemp(os.Getenv("LOOP_DOCS_WORKDIR_BASE"), "bdd-sample-")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	dir := filepath.Join(parent, "acme-notes")

	// Committed baseline: a tiny notes service + loop project config.
	committed := map[string]string{
		"README.md": "# Acme Notes API\n\nA small notes service used in the Loop documentation screenshots.\n\n## Endpoints\n\n- `GET  /notes` — list notes\n- `POST /notes` — create a note\n\n## Develop\n\n```\nnpm install\nnpm run dev\n```\n",
		"package.json": `{
  "name": "acme-notes",
  "version": "0.1.0",
  "private": true,
  "scripts": {
    "dev": "tsx watch src/index.ts",
    "build": "tsc -p ."
  },
  "dependencies": { "express": "^4.19.2" },
  "devDependencies": { "tsx": "^4.7.0", "typescript": "^5.4.0" }
}
`,
		".gitignore": "node_modules/\ndist/\n",
		".loop/config.json": "{\n  \"claude_model\": \"claude-sonnet-4-6\"\n}\n",
		"src/index.ts": `import express from "express";
import { listNotes, createNote } from "./notes";

const app = express();
app.use(express.json());

app.get("/notes", (_req, res) => {
  res.json(listNotes());
});

app.post("/notes", (req, res) => {
  const note = createNote(req.body.title ?? "untitled");
  res.status(201).json(note);
});

const port = Number(process.env.PORT ?? 3000);
app.listen(port, () => console.log(` + "`acme-notes listening on :${port}`" + `));
`,
		"src/notes.ts": `export interface Note {
  id: number;
  title: string;
  createdAt: string;
}

const notes: Note[] = [];

export function listNotes(): Note[] {
  return notes;
}

export function createNote(title: string): Note {
  const note: Note = { id: notes.length + 1, title, createdAt: new Date().toISOString() };
  notes.push(note);
  return note;
}
`,
	}
	for rel, content := range committed {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", rel, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", rel, err)
		}
	}

	if err := gitRun(dir, "init", "-b", "main"); err != nil {
		return err
	}
	for _, kv := range [][2]string{{"user.email", "dev@acme.test"}, {"user.name", "Acme Dev"}} {
		if err := gitRun(dir, "config", kv[0], kv[1]); err != nil {
			return err
		}
	}
	// Two commits give the Commits view some history.
	if err := gitRun(dir, "add", "."); err != nil {
		return err
	}
	if err := gitRun(dir, "commit", "-m", "Initial commit: notes service"); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Acme Notes API\n\nA small notes service used in the Loop documentation screenshots.\n\n## Endpoints\n\n- `GET  /notes` — list notes\n- `POST /notes` — create a note\n- `DELETE /notes/:id` — delete a note\n\n## Develop\n\n```\nnpm install\nnpm run dev\n```\n"), 0o644); err != nil {
		return err
	}
	if err := gitRun(dir, "commit", "-am", "Document delete endpoint"); err != nil {
		return err
	}

	// Uncommitted edits so the Git panel shows real changes: one modified
	// tracked file and one untracked file.
	notesEdit := `export interface Note {
  id: number;
  title: string;
  createdAt: string;
}

const notes: Note[] = [];

export function listNotes(): Note[] {
  return notes;
}

export function createNote(title: string): Note {
  const note: Note = { id: notes.length + 1, title, createdAt: new Date().toISOString() };
  notes.push(note);
  return note;
}

export function deleteNote(id: number): boolean {
  const idx = notes.findIndex((n) => n.id === id);
  if (idx === -1) return false;
  notes.splice(idx, 1);
  return true;
}
`
	if err := os.WriteFile(filepath.Join(dir, "src", "notes.ts"), []byte(notesEdit), 0o644); err != nil {
		return err
	}
	searchFile := `import type { Note } from "./notes";
import { listNotes } from "./notes";

export function searchNotes(query: string): Note[] {
  const q = query.toLowerCase();
  return listNotes().filter((n) => n.title.toLowerCase().includes(q));
}
`
	if err := os.WriteFile(filepath.Join(dir, "src", "search.ts"), []byte(searchFile), 0o644); err != nil {
		return err
	}

	// Docs-capture runs the agent as a non-root uid (loop runs as root in the
	// sandbox; claude refuses root). Make the workspace world-writable so the
	// agent's MCP server / tools can write under the workdir.
	if os.Getenv("LOOP_DOCS_CAPTURE") != "" {
		_ = exec.Command("chmod", "-R", "0777", parent).Run()
	}

	// Open a channel on the sample dir without tracking it for per-scenario
	// cleanup, so it persists for the whole run and is reused by every scenario.
	body := fmt.Sprintf(`{"dir_path": %q, "platform": "local"}`, dir)
	if err := tc.doRequest(http.MethodPost, "/api/channels", body); err != nil {
		return err
	}
	if tc.LastStatus != http.StatusOK {
		return fmt.Errorf("failed to create sample channel: status %d, body: %s", tc.LastStatus, string(tc.LastBody))
	}
	id, _ := tc.LastJSON["channel_id"].(string)
	sampleProject.channelID = id
	sampleProject.dir = dir

	// Seed Kanban tickets and a scheduled task so those panels show real content
	// in the docs walkthrough (best-effort; the channel works without them).
	for _, t := range []struct{ title, kind string }{
		{"Add note search endpoint", "feature"},
		{"Fix pagination off-by-one", "bug"},
	} {
		tb, _ := json.Marshal(map[string]any{"dir": dir, "title": t.title, "type": t.kind})
		_ = tc.doRequest(http.MethodPost, "/api/tickets", string(tb))
	}
	taskBody, _ := json.Marshal(map[string]any{
		"channel_id": id, "prompt": "Summarise the open notes each morning",
		"schedule": "0 9 * * *", "type": "cron",
	})
	_ = tc.doRequest(http.MethodPost, "/api/tasks", string(taskBody))

	// Seed a prompt shortcut (chat # picker) and a bash shortcut (Docker Shell
	// $ picker) so those affordances have content in the docs walkthrough.
	psBody, _ := json.Marshal(map[string]any{
		"action": "add", "name": "review-diff", "description": "Review the current diff",
		"prompt": "Review the uncommitted changes and summarise risks in 3 bullets.",
	})
	_ = tc.doRequest(http.MethodPost, "/api/shortcuts", string(psBody))
	bsBody, _ := json.Marshal(map[string]any{
		"action": "add", "name": "run-tests", "description": "Run the test suite",
		"command": "npm test",
	})
	_ = tc.doRequest(http.MethodPost, "/api/bash-shortcuts", string(bsBody))

	// Preseed a project-scoped playground via the server's own API (files on disk;
	// no DB/Ollama) so the Playground panel reliably shows a live sandbox. The
	// journey also asks the agent to create one in chat, but the panel's item list
	// only refreshes on a playground.update WS event that can be missed mid-run.
	pgHTML := `<style>html,body{margin:0;height:100%}body{display:flex;align-items:center;justify-content:center;background:#0b0d12;font-family:system-ui,-apple-system,sans-serif}.card{padding:46px 64px;border-radius:16px;background:#12151c;color:#e8eaed;font-size:30px;font-weight:600;letter-spacing:.3px;box-shadow:0 0 0 1px rgba(255,255,255,.06);animation:glow 3s ease-in-out infinite}@keyframes glow{0%,100%{box-shadow:0 0 24px rgba(124,92,255,.25)}50%{box-shadow:0 0 60px rgba(124,92,255,.65)}}</style>
<div class="card">Hello from Loop</div>`
	pgBody, _ := json.Marshal(map[string]any{
		"html":        pgHTML,
		"title":       "Hello from Loop",
		"description": "A centered card with a gentle pulsing glow.",
	})
	_ = tc.doRequest(http.MethodPut, fmt.Sprintf("/api/playground?name=hello-loop&scope=project&channel_id=%s", id), string(pgBody))
	return nil
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

// stageNewFile writes a fresh file into the channel's repo and `git add`s
// it so the diff handler reports it as status=staged rather than untracked.
func (tc *TestContext) stageNewFile(name string) error {
	if tc.ChannelDir == "" {
		return fmt.Errorf("no channel dir set; use 'I set up a test channel via API for git repo' step first")
	}
	fpath := filepath.Join(tc.ChannelDir, name)
	if err := os.MkdirAll(filepath.Dir(fpath), 0o755); err != nil {
		return fmt.Errorf("creating dirs for %s: %w", name, err)
	}
	if err := os.WriteFile(fpath, []byte("// "+name+"\n"), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", name, err)
	}
	out, err := exec.Command("git", "-C", tc.ChannelDir, "add", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git add %s: %s: %w", name, out, err)
	}
	return nil
}

// modifyWithoutStaging overwrites a tracked file but does not stage,
// producing a status=unstaged entry.
func (tc *TestContext) modifyWithoutStaging(name string) error {
	if tc.ChannelDir == "" {
		return fmt.Errorf("no channel dir set; use 'I set up a test channel via API for git repo' step first")
	}
	fpath := filepath.Join(tc.ChannelDir, name)
	if err := os.WriteFile(fpath, []byte("// unstaged "+name+"\n"), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", name, err)
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

// clearAllShortcutsViaAPI deletes every prompt shortcut currently configured
// on the daemon. Used by scenarios that need to assert the empty-list state
// without being affected by built-in seeds (e.g. the "builtin code review"
// shortcut seeded by fsmigrate on first launch).
func (tc *TestContext) clearAllShortcutsViaAPI() error {
	if err := tc.doRequest(http.MethodGet, "/api/shortcuts", ""); err != nil {
		return err
	}
	if tc.LastStatus != http.StatusOK {
		return fmt.Errorf("failed to list shortcuts: status %d, body: %s", tc.LastStatus, string(tc.LastBody))
	}
	var shortcuts []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(tc.LastBody, &shortcuts); err != nil {
		return fmt.Errorf("decoding shortcuts list: %w", err)
	}
	for _, sc := range shortcuts {
		body := fmt.Sprintf(`{"action":"delete","name":%q}`, sc.Name)
		if err := tc.doRequest(http.MethodPost, "/api/shortcuts", body); err != nil {
			return err
		}
		if tc.LastStatus != http.StatusNoContent {
			return fmt.Errorf("failed to delete shortcut %q: status %d, body: %s", sc.Name, tc.LastStatus, string(tc.LastBody))
		}
	}
	return nil
}

func (tc *TestContext) addBashShortcutViaAPI(name, command string) error {
	payload := map[string]string{
		"action":      "add",
		"name":        name,
		"description": name,
		"command":     command,
	}
	b, _ := json.Marshal(payload)
	if err := tc.doRequest(http.MethodPost, "/api/bash-shortcuts", string(b)); err != nil {
		return err
	}
	if tc.LastStatus != http.StatusNoContent {
		return fmt.Errorf("failed to add bash shortcut: status %d, body: %s", tc.LastStatus, string(tc.LastBody))
	}
	tc.CreatedBashShortcutNames = append(tc.CreatedBashShortcutNames, name)
	return nil
}

// clearAllBashShortcutsViaAPI deletes every bash shortcut currently
// configured on the daemon. Mirror of clearAllShortcutsViaAPI for the
// /api/bash-shortcuts endpoint.
func (tc *TestContext) clearAllBashShortcutsViaAPI() error {
	if err := tc.doRequest(http.MethodGet, "/api/bash-shortcuts", ""); err != nil {
		return err
	}
	if tc.LastStatus != http.StatusOK {
		return fmt.Errorf("failed to list bash shortcuts: status %d, body: %s", tc.LastStatus, string(tc.LastBody))
	}
	var shortcuts []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(tc.LastBody, &shortcuts); err != nil {
		return fmt.Errorf("decoding bash shortcuts list: %w", err)
	}
	for _, sc := range shortcuts {
		body := fmt.Sprintf(`{"action":"delete","name":%q}`, sc.Name)
		if err := tc.doRequest(http.MethodPost, "/api/bash-shortcuts", body); err != nil {
			return err
		}
		if tc.LastStatus != http.StatusNoContent {
			return fmt.Errorf("failed to delete bash shortcut %q: status %d, body: %s", sc.Name, tc.LastStatus, string(tc.LastBody))
		}
	}
	return nil
}

// --- Ticket steps ---

func (tc *TestContext) createTicketViaAPI(title, ticketType string) error {
	if tc.ChannelDir == "" {
		return fmt.Errorf("no channel dir set; use 'I set up a test channel via API for git repo' step first")
	}
	payload := map[string]any{
		"dir":   tc.ChannelDir,
		"title": title,
		"type":  ticketType,
	}
	b, _ := json.Marshal(payload)
	if err := tc.doRequest(http.MethodPost, "/api/tickets", string(b)); err != nil {
		return err
	}
	if tc.LastStatus != http.StatusCreated {
		return fmt.Errorf("failed to create ticket: status %d, body: %s", tc.LastStatus, string(tc.LastBody))
	}
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

	// Poll until the task is no longer running. The task may complete before we
	// ever observe running=true (fast failures), so we also accept run log
	// entries as evidence the task ran and finished.
	sawRunning := false
	for time.Now().Before(deadline) {
		if err := tc.doRequest(http.MethodGet, "/api/tasks/"+tc.TaskID, ""); err != nil {
			return err
		}
		running, ok := tc.LastJSON["running"].(bool)
		if ok && running {
			sawRunning = true
		}
		if ok && !running {
			if sawRunning {
				return nil
			}
			// Task may have completed before we first polled.
			if tc.taskHasRunLogs() {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("task %s still running after %s", tc.TaskID, timeout)
}

// taskHasRunLogs checks if the current task has any run log entries, indicating
// that the task ran and completed (possibly before polling could observe running=true).
func (tc *TestContext) taskHasRunLogs() bool {
	if err := tc.doRequest(http.MethodGet, "/api/tasks/"+tc.TaskID+"/runs", ""); err != nil {
		return false
	}
	var runs []any
	if err := json.Unmarshal(tc.LastBody, &runs); err != nil {
		return false
	}
	return len(runs) > 0
}

// --- Worktree task setup ---

func (tc *TestContext) setupWorkflowTaskViaAPINoInputs(workflowName, schedule string) error {
	return tc.createWorkflowTask(workflowName, "{}", schedule)
}

func (tc *TestContext) setupWorkflowTaskViaAPIWithInputs(workflowName, schedule string, body *godog.DocString) error {
	return tc.createWorkflowTask(workflowName, strings.TrimSpace(body.Content), schedule)
}

func (tc *TestContext) createWorkflowTask(workflowName, inputs, schedule string) error {
	if tc.ChannelID == "" {
		return fmt.Errorf("no channel_id set; use 'I set up a test channel via API' step first")
	}
	payload := map[string]any{
		"channel_id":      tc.ChannelID,
		"schedule":        schedule,
		"type":            "interval",
		"prompt":          "",
		"workflow_name":   workflowName,
		"workflow_inputs": inputs,
	}
	b, _ := json.Marshal(payload)
	if err := tc.doRequest(http.MethodPost, "/api/tasks", string(b)); err != nil {
		return err
	}
	if tc.LastStatus != http.StatusOK && tc.LastStatus != http.StatusCreated {
		return fmt.Errorf("failed to create workflow task: status %d, body: %s", tc.LastStatus, string(tc.LastBody))
	}
	if tc.LastJSON != nil {
		if id, ok := tc.LastJSON["id"]; ok {
			tc.TaskID = fmt.Sprintf("%v", id)
			tc.CreatedTaskIDs = append(tc.CreatedTaskIDs, tc.TaskID)
		}
	}
	return nil
}

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
