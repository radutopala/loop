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

	"github.com/radutopala/loop/pluginsdk/openapi/pluginapi"
)

// Channel is the channel the plugin's container serves.
type Channel = pluginapi.Channel

// File is a range of lines of a file in the channel.
type File = pluginapi.File

// Tree is the channel's files.
type Tree = pluginapi.Tree

// Commit is a recent commit.
type Commit = pluginapi.Commit

// FileChurn is how many recent commits touched a file.
type FileChurn = pluginapi.FileChurn

// GitRecent is the channel's recent git history.
type GitRecent = pluginapi.GitRecent

// Hotspot is a function from the latest quality scan.
type Hotspot = pluginapi.Hotspot

// Hotspots is a page of the latest quality scan's functions, worst first,
// with the scan's complexity score.
type Hotspots = pluginapi.Hotspots

// RunRequest asks Loop for a run of one of the plugin's agents.
type RunRequest = pluginapi.RunRequest

// Run is a started agent run.
type Run = pluginapi.Run

// UIStep is a UI bridge step: its op and the op's fields, as Loop's API
// docs list them.
type UIStep map[string]any

// UICommand is steps for an app window to run in order. The first step is
// select_channel, and each select_channel opens the plugin's channel or one
// of its threads.
type UICommand struct {
	// ClientID picks the window; "" is the one the user last focused.
	ClientID string
	Steps    []UIStep
	// Timeout is how long to wait for the window; zero is a minute, and
	// Loop allows up to ten.
	Timeout time.Duration
}

// UIReply is how the window ran the steps. A failed step has ok false and
// an error, and the steps after it don't run.
type UIReply = pluginapi.UIReply

// API is Loop's Plugin API v1, as the plugin's channel-scoped token sees it.
type API interface {
	Channel(ctx context.Context) (*Channel, error)
	File(ctx context.Context, path string, start, end int) (*File, error)
	Tree(ctx context.Context, glob string) (*Tree, error)
	GitRecent(ctx context.Context, days int) (*GitRecent, error)
	Hotspots(ctx context.Context, limit int) ([]Hotspot, error)
	HotspotsPage(ctx context.Context, limit, offset int) (*Hotspots, error)
	StartRun(ctx context.Context, req RunRequest) (*Run, error)
	Event(ctx context.Context, typ string, data any) error
	Notify(ctx context.Context, title, body string) error
	WriteMemory(ctx context.Context, name, content string) error
	DeleteMemory(ctx context.Context, name string) error
	RunUI(ctx context.Context, cmd UICommand) (*UIReply, error)
	SetBadge(ctx context.Context, text, title string) error
	ClearBadge(ctx context.Context) error
	PostCard(ctx context.Context, card, title string, data any) (int64, error)
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
	return c.doFor(ctx, c.Timeout, method, path, query, in, out)
}

// doFor is do capped at timeout instead of c.Timeout.
func (c *Client) doFor(ctx context.Context, timeout time.Duration, method, path string, query url.Values, in, out any) error {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
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
	h, err := c.HotspotsPage(ctx, limit, 0)
	if err != nil {
		return nil, err
	}
	return h.Functions, nil
}

// HotspotsPage returns up to limit functions of the latest quality scan,
// worst first, after skipping offset, along with the scan's complexity
// score and totals. Zero limit is Loop's default. Needs quality.read.
func (c *Client) HotspotsPage(ctx context.Context, limit, offset int) (*Hotspots, error) {
	var h Hotspots
	if err := c.do(ctx, http.MethodGet, "/channel/quality/hotspots", positive(positive(url.Values{}, "limit", limit), "offset", offset), nil, &h); err != nil {
		return nil, err
	}
	return &h, nil
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
	return c.do(ctx, http.MethodPost, "/events", nil, pluginapi.Event{Type: typ, Data: data}, nil)
}

// Notify shows a desktop notification. Needs ui.notify.
func (c *Client) Notify(ctx context.Context, title, body string) error {
	return c.do(ctx, http.MethodPost, "/notify", nil, pluginapi.Notification{Title: title, Body: body}, nil)
}

// WriteMemory writes the plugin's memory file name (lowercase letters,
// digits and dashes) into the channel's memory, where its agents find it
// with search_memory. Needs memory.write.
func (c *Client) WriteMemory(ctx context.Context, name, content string) error {
	return c.do(ctx, http.MethodPut, "/memory/"+url.PathEscape(name), nil, pluginapi.Memory{Content: content}, nil)
}

// DeleteMemory removes the plugin's memory file name. Needs memory.write.
func (c *Client) DeleteMemory(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/memory/"+url.PathEscape(name), nil, nil, nil)
}

// SetBadge shows text, at most 16 characters on one line, on the sidebar
// row of the plugin's channel, with title as its tooltip. It replaces the
// plugin's badge there and stays until ClearBadge. Needs ui.badge.
func (c *Client) SetBadge(ctx context.Context, text, title string) error {
	return c.do(ctx, http.MethodPut, "/badge", nil, map[string]string{"text": text, "title": title}, nil)
}

// ClearBadge removes the plugin's badge. Needs ui.badge.
func (c *Client) ClearBadge(ctx context.Context) error {
	return c.do(ctx, http.MethodDelete, "/badge", nil, nil, nil)
}

// PostCard posts the manifest's card, under title, to the desktop app's
// notification drawer, and returns the notification's id. The card's page
// gets data, at most 64 KB of JSON, from the bridge's context.get. Cards
// share Notify's rate limit and show in the desktop app only. Needs
// ui.card.
func (c *Client) PostCard(ctx context.Context, card, title string, data any) (int64, error) {
	var out struct {
		ID int64 `json:"id"`
	}
	err := c.do(ctx, http.MethodPost, "/cards", nil, map[string]any{"card": card, "title": title, "data": data}, &out)
	return out.ID, err
}

// uiGrace is how much longer than a UI command's timeout RunUI waits, so
// Loop's own answer to a window that ran out of time gets back.
const uiGrace = 10 * time.Second

// RunUI has an app window run cmd and returns how it went. It waits for the
// window, up to cmd.Timeout. Needs ui.run.
func (c *Client) RunUI(ctx context.Context, cmd UICommand) (*UIReply, error) {
	body := map[string]any{"steps": cmd.Steps}
	if cmd.ClientID != "" {
		body["client_id"] = cmd.ClientID
	}
	wait := time.Minute
	if cmd.Timeout > 0 {
		body["timeout"] = cmd.Timeout.String()
		wait = cmd.Timeout
	}
	timeout := c.Timeout
	if timeout > 0 {
		timeout = max(timeout, wait+uiGrace)
	}
	var reply UIReply
	if err := c.doFor(ctx, timeout, http.MethodPost, "/ui/commands", nil, body, &reply); err != nil {
		return nil, err
	}
	return &reply, nil
}
