package pluginsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Channel is the channel the plugin's container serves.
type Channel struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	DirPath  string `json:"dir_path"`
	Platform string `json:"platform"`
	Model    string `json:"model"`
	Effort   string `json:"effort"`
	// Settings are the plugin's settings in the channel's project.
	Settings map[string]any `json:"settings"`
}

// File is a range of lines of a file in the channel.
type File struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	Start      int    `json:"start"`
	End        int    `json:"end"`
	TotalLines int    `json:"total_lines"`
}

// Tree is the channel's files.
type Tree struct {
	Files []string `json:"files"`
	// Truncated is set when there were more files than Loop lists.
	Truncated bool `json:"truncated"`
}

// Commit is a recent commit.
type Commit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	Author  string `json:"author"`
	Time    string `json:"time"`
}

// FileChurn is how many recent commits touched a file.
type FileChurn struct {
	Path    string `json:"path"`
	Commits int    `json:"commits"`
}

// GitRecent is the channel's recent git history.
type GitRecent struct {
	Commits []Commit    `json:"commits"`
	Files   []FileChurn `json:"files"`
}

// Hotspot is a function from the latest quality scan.
type Hotspot struct {
	Path       string  `json:"path"`
	Name       string  `json:"name"`
	StartLine  int     `json:"start_line"`
	Cyclomatic int     `json:"cyclomatic"`
	Cognitive  int     `json:"cognitive"`
	MaxNesting int     `json:"max_nesting"`
	ParamCount int     `json:"param_count"`
	LOC        int     `json:"loc"`
	Score      float64 `json:"score"`
}

// RunRequest asks Loop for a run of one of the plugin's agents.
type RunRequest struct {
	Agent  string `json:"agent"`
	Prompt string `json:"prompt"`
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
}

// Run is a started agent run.
type Run struct {
	RunID    string `json:"run_id"`
	ThreadID string `json:"thread_id"`
}

// API is Loop's Plugin API v1, as the plugin's channel-scoped token sees it.
type API interface {
	Channel(ctx context.Context) (*Channel, error)
	File(ctx context.Context, path string, start, end int) (*File, error)
	Tree(ctx context.Context, glob string) (*Tree, error)
	GitRecent(ctx context.Context, days int) (*GitRecent, error)
	Hotspots(ctx context.Context, limit int) ([]Hotspot, error)
	StartRun(ctx context.Context, req RunRequest) (*Run, error)
	Event(ctx context.Context, typ string, data any) error
	Notify(ctx context.Context, title, body string) error
	WriteMemory(ctx context.Context, name, content string) error
	DeleteMemory(ctx context.Context, name string) error
}

// StatusError is a non-2xx answer from Loop.
type StatusError struct {
	Method string
	Path   string
	Status int
	Body   string
}

func (e *StatusError) Error() string {
	msg := fmt.Sprintf("%s %s: Loop answered %d", e.Method, e.Path, e.Status)
	if e.Body != "" {
		msg += ": " + e.Body
	}
	return msg
}

// HasStatus reports whether err is a StatusError with one of codes.
func HasStatus(err error, codes ...int) bool {
	var se *StatusError
	return errors.As(err, &se) && slices.Contains(codes, se.Status)
}

// Doer sends HTTP requests.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Client is the API over HTTP.
type Client struct {
	// BaseURL is LOOP_API_URL.
	BaseURL string
	// TokenFile is LOOP_PLUGIN_TOKEN_FILE, read on every call: Loop
	// replaces the token when it restarts.
	TokenFile string
	// HTTP sends the requests.
	HTTP Doer
	// Timeout caps each call; zero leaves it to ctx.
	Timeout time.Duration
}

var _ API = (*Client)(nil)

// NewClient returns a Client for Loop at baseURL.
func NewClient(baseURL, tokenFile string) *Client {
	return &Client{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		TokenFile: tokenFile,
		HTTP:      &http.Client{},
		Timeout:   20 * time.Second,
	}
}

// maxAnswer caps an answer from Loop.
const maxAnswer = 8 << 20

func (c *Client) do(ctx context.Context, method, path string, query url.Values, in, out any) error {
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	token, err := os.ReadFile(c.TokenFile)
	if err != nil {
		return fmt.Errorf("reading the plugin token: %w", err)
	}
	u := c.BaseURL + "/api/plugin/v1" + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("encoding %s %s: %w", method, path, err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return fmt.Errorf("building %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return &StatusError{Method: method, Path: path, Status: resp.StatusCode, Body: strings.TrimSpace(string(msg))}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxAnswer))
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAnswer)).Decode(out); err != nil {
		return fmt.Errorf("decoding %s %s: %w", method, path, err)
	}
	return nil
}

// positive sets key in q to n when n is above zero, leaving Loop's default
// otherwise.
func positive(q url.Values, key string, n int) url.Values {
	if n > 0 {
		q.Set(key, strconv.Itoa(n))
	}
	return q
}

// Channel returns the plugin's channel.
func (c *Client) Channel(ctx context.Context) (*Channel, error) {
	var ch Channel
	if err := c.do(ctx, http.MethodGet, "/channel", nil, nil, &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

// File reads lines start..end of path, 1-based and inclusive; zero leaves a
// bound open. Needs files.read.
func (c *Client) File(ctx context.Context, path string, start, end int) (*File, error) {
	q := positive(positive(url.Values{"path": {path}}, "start", start), "end", end)
	var f File
	if err := c.do(ctx, http.MethodGet, "/channel/files", q, nil, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// Tree lists the channel's files, those matching glob when it's set. Needs
// files.read.
func (c *Client) Tree(ctx context.Context, glob string) (*Tree, error) {
	q := url.Values{}
	if glob != "" {
		q.Set("glob", glob)
	}
	var t Tree
	if err := c.do(ctx, http.MethodGet, "/channel/tree", q, nil, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// GitRecent returns the last days of history; zero is Loop's default. Needs
// git.read.
func (c *Client) GitRecent(ctx context.Context, days int) (*GitRecent, error) {
	var g GitRecent
	if err := c.do(ctx, http.MethodGet, "/channel/git/recent", positive(url.Values{}, "days", days), nil, &g); err != nil {
		return nil, err
	}
	return &g, nil
}

// Hotspots returns up to limit functions of the latest quality scan, worst
// first; zero is Loop's default. Needs quality.read.
func (c *Client) Hotspots(ctx context.Context, limit int) ([]Hotspot, error) {
	var out struct {
		Functions []Hotspot `json:"functions"`
	}
	if err := c.do(ctx, http.MethodGet, "/channel/quality/hotspots", positive(url.Values{}, "limit", limit), nil, &out); err != nil {
		return nil, err
	}
	return out.Functions, nil
}

// StartRun starts a run of one of the plugin's agents. Loop calls the
// plugin's /runs/{run_id}/done when it ends. Needs agent.readonly or
// agent.write.
func (c *Client) StartRun(ctx context.Context, req RunRequest) (*Run, error) {
	var run Run
	if err := c.do(ctx, http.MethodPost, "/runs", nil, req, &run); err != nil {
		return nil, err
	}
	if run.RunID == "" {
		return nil, errors.New("POST /runs: Loop answered without a run_id")
	}
	return &run, nil
}

// Event sends an event to the plugin's panels in the channel.
func (c *Client) Event(ctx context.Context, typ string, data any) error {
	return c.do(ctx, http.MethodPost, "/events", nil, map[string]any{"type": typ, "data": data}, nil)
}

// Notify shows a desktop notification. Needs ui.notify.
func (c *Client) Notify(ctx context.Context, title, body string) error {
	return c.do(ctx, http.MethodPost, "/notify", nil, map[string]string{"title": title, "body": body}, nil)
}

// WriteMemory writes the plugin's memory file name (lowercase letters,
// digits and dashes) into the channel's memory, where its agents find it
// with search_memory. Needs memory.write.
func (c *Client) WriteMemory(ctx context.Context, name, content string) error {
	return c.do(ctx, http.MethodPut, "/memory/"+url.PathEscape(name), nil, map[string]string{"content": content}, nil)
}

// DeleteMemory removes the plugin's memory file name. Needs memory.write.
func (c *Client) DeleteMemory(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/memory/"+url.PathEscape(name), nil, nil, nil)
}
