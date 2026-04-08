//go:build component

package component

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// TestContext holds per-scenario state for BDD steps.
type TestContext struct {
	// Infrastructure
	BaseURL    string
	AppURL     string
	HTTPClient *http.Client

	// HTTP response state
	LastResponse *http.Response
	LastBody     []byte
	LastStatus   int
	LastJSON     map[string]any

	// Entity tracking for cleanup
	ChannelID         string
	TaskID            string
	CreatedChannelIDs []string
	CreatedThreadIDs  []string
	CreatedTaskIDs    []string
	CreatedDirs       []string
	WorktreeThreadID     string
	CreatedShortcutNames []string

	// Frontend (lazily initialized)
	chromeTab *chromeTab

	// WebSocket
	WSConn     *websocket.Conn
	LastWSEvent map[string]any
}

// NewTestContext creates a fresh context for a scenario.
func NewTestContext() *TestContext {
	return &TestContext{
		BaseURL:    getEnvOrDefault("LOOP_BASE_URL", "http://localhost:8222"),
		AppURL:     getEnvOrDefault("LOOP_APP_URL", "http://localhost:5173"),
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// doRequest sends an HTTP request and stores the response state.
func (tc *TestContext) doRequest(method, path string, body string) error {
	url := tc.BaseURL + tc.resolvePlaceholders(path)

	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := tc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	tc.LastResponse = resp
	tc.LastStatus = resp.StatusCode
	tc.LastBody, err = io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	// Try to parse JSON (best effort).
	tc.LastJSON = nil
	_ = json.Unmarshal(tc.LastBody, &tc.LastJSON)

	return nil
}

// resolvePlaceholders replaces {channel_id} and {task_id} in path strings.
func (tc *TestContext) resolvePlaceholders(path string) string {
	path = strings.ReplaceAll(path, "{channel_id}", tc.ChannelID)
	path = strings.ReplaceAll(path, "{task_id}", tc.TaskID)
	path = strings.ReplaceAll(path, "{worktree_thread_id}", tc.WorktreeThreadID)
	return path
}

// cleanup deletes entities created during the scenario.
// Deletion order: tasks → threads → channels (reverse dependency order).
func (tc *TestContext) cleanup() {
	// Delete all tracked shortcuts.
	for _, name := range tc.CreatedShortcutNames {
		body := fmt.Sprintf(`{"action":"delete","name":%q}`, name)
		req, _ := http.NewRequest("POST", tc.BaseURL+"/api/shortcuts", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		tc.HTTPClient.Do(req) //nolint:errcheck
	}

	// Delete all tracked tasks.
	for _, id := range tc.CreatedTaskIDs {
		req, _ := http.NewRequest("DELETE", tc.BaseURL+"/api/tasks/"+id, nil)
		tc.HTTPClient.Do(req) //nolint:errcheck
	}
	if tc.TaskID != "" {
		req, _ := http.NewRequest("DELETE", tc.BaseURL+"/api/tasks/"+tc.TaskID, nil)
		tc.HTTPClient.Do(req) //nolint:errcheck
	}

	// Delete all tracked threads.
	for _, id := range tc.CreatedThreadIDs {
		req, _ := http.NewRequest("DELETE", tc.BaseURL+"/api/threads/"+id, nil)
		tc.HTTPClient.Do(req) //nolint:errcheck
	}

	// Delete all tracked channels.
	for _, id := range tc.CreatedChannelIDs {
		req, _ := http.NewRequest("DELETE", tc.BaseURL+"/api/channels/"+id, nil)
		tc.HTTPClient.Do(req) //nolint:errcheck
	}
	if tc.ChannelID != "" {
		req, _ := http.NewRequest("DELETE", tc.BaseURL+"/api/channels/"+tc.ChannelID, nil)
		tc.HTTPClient.Do(req) //nolint:errcheck
	}

	// Delete worktree thread if created.
	if tc.WorktreeThreadID != "" {
		req, _ := http.NewRequest("DELETE", tc.BaseURL+"/api/threads/"+tc.WorktreeThreadID, nil)
		tc.HTTPClient.Do(req) //nolint:errcheck
	}

	// Remove temp directories.
	for _, dir := range tc.CreatedDirs {
		os.RemoveAll(dir) //nolint:errcheck
	}

	if tc.WSConn != nil {
		tc.WSConn.Close()
	}
	if tc.chromeTab != nil {
		tc.chromeTab.close()
	}
}
