package pluginsdktest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/pluginsdk"
)

type FakeLoopSuite struct {
	suite.Suite
	ctx context.Context
}

func TestFakeLoopSuite(t *testing.T) {
	suite.Run(t, new(FakeLoopSuite))
}

func (s *FakeLoopSuite) SetupTest() {
	s.ctx = context.Background()
}

// raw calls the fake with the plugin token, for requests the Client can't
// make.
func (s *FakeLoopSuite) raw(f *FakeLoop, method, path, body string) int {
	req, err := http.NewRequestWithContext(s.ctx, method, f.URL+"/api/plugin/v1"+path, strings.NewReader(body))
	require.NoError(s.T(), err)
	req.Header.Set("Authorization", "Bearer plugin-token")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(s.T(), err)
	_, _ = io.Copy(io.Discard, resp.Body)
	require.NoError(s.T(), resp.Body.Close())
	return resp.StatusCode
}

func (s *FakeLoopSuite) TestChannel() {
	c := NewFakeLoop(s.T(), Config{}).Client()
	ch, err := c.Channel(s.ctx)
	require.NoError(s.T(), err)
	require.Equal(s.T(), &pluginsdk.Channel{ID: "ch1"}, ch)

	c = NewFakeLoop(s.T(), Config{Channel: pluginsdk.Channel{ID: "other", Settings: map[string]any{"n": float64(2)}}}).Client()
	ch, err = c.Channel(s.ctx)
	require.NoError(s.T(), err)
	require.Equal(s.T(), &pluginsdk.Channel{ID: "other", Settings: map[string]any{"n": float64(2)}}, ch)
}

func (s *FakeLoopSuite) TestToken() {
	f := NewFakeLoop(s.T(), Config{})
	resp, err := http.Get(f.URL + "/api/plugin/v1/channel")
	require.NoError(s.T(), err)
	require.NoError(s.T(), resp.Body.Close())
	require.Equal(s.T(), http.StatusUnauthorized, resp.StatusCode)
}

func (s *FakeLoopSuite) TestFile() {
	c := NewFakeLoop(s.T(), Config{Files: map[string]string{"a.go": "one\ntwo\nthree\n", "empty": ""}}).Client()
	tests := []struct {
		name       string
		path       string
		start, end int
		want       *pluginsdk.File
		status     int
	}{
		{"whole", "a.go", 0, 0, &pluginsdk.File{Path: "a.go", Content: "one\ntwo\nthree\n", Start: 1, End: 3, TotalLines: 3}, 0},
		{"empty", "empty", 0, 0, &pluginsdk.File{Path: "empty", TotalLines: 0}, 0},
		{"range", "a.go", 2, 3, &pluginsdk.File{Path: "a.go", Content: "two\nthree", Start: 2, End: 3, TotalLines: 3}, 0},
		{"open start", "a.go", 0, 1, &pluginsdk.File{Path: "a.go", Content: "one", Start: 1, End: 1, TotalLines: 3}, 0},
		{"end past the file", "a.go", 3, 9, &pluginsdk.File{Path: "a.go", Content: "three", Start: 3, End: 3, TotalLines: 3}, 0},
		{"start past the file", "a.go", 4, 0, nil, http.StatusBadRequest},
		{"end before start", "a.go", 3, 2, nil, http.StatusBadRequest},
		{"missing", "b.go", 0, 0, nil, http.StatusNotFound},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			got, err := c.File(s.ctx, tt.path, tt.start, tt.end)
			if tt.status != 0 {
				require.True(s.T(), pluginsdk.HasStatus(err, tt.status), "%v", err)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), tt.want, got)
		})
	}
}

func (s *FakeLoopSuite) TestBadQuery() {
	f := NewFakeLoop(s.T(), Config{Files: map[string]string{"a.go": "x"}, Hotspots: []pluginsdk.Hotspot{}})
	for _, path := range []string{"/channel/files?path=a.go&start=x", "/channel/files?path=a.go&end=-1", "/channel/quality/hotspots?limit=x"} {
		require.Equal(s.T(), http.StatusBadRequest, s.raw(f, http.MethodGet, path, ""), path)
	}
}

func (s *FakeLoopSuite) TestTree() {
	c := NewFakeLoop(s.T(), Config{Files: map[string]string{"cmd/main.go": "", "go.mod": "", "README.md": ""}}).Client()
	tests := []struct {
		glob string
		want []string
	}{
		{"", []string{"README.md", "cmd/main.go", "go.mod"}},
		{"*.go", []string{"cmd/main.go"}},
		{"cmd/*", []string{"cmd/main.go"}},
		{"*.txt", []string{}},
	}
	for _, tt := range tests {
		s.Run(tt.glob, func() {
			got, err := c.Tree(s.ctx, tt.glob)
			require.NoError(s.T(), err)
			require.Equal(s.T(), &pluginsdk.Tree{Files: tt.want}, got)
		})
	}
	_, err := c.Tree(s.ctx, "[")
	require.True(s.T(), pluginsdk.HasStatus(err, http.StatusBadRequest))
}

func (s *FakeLoopSuite) TestGitAndHotspots() {
	c := NewFakeLoop(s.T(), Config{}).Client()
	_, err := c.GitRecent(s.ctx, 0)
	require.True(s.T(), pluginsdk.HasStatus(err, http.StatusConflict))
	_, err = c.Hotspots(s.ctx, 0)
	require.True(s.T(), pluginsdk.HasStatus(err, http.StatusServiceUnavailable))

	git := &pluginsdk.GitRecent{Commits: []pluginsdk.Commit{{SHA: "abc"}}}
	hot := []pluginsdk.Hotspot{{Name: "a"}, {Name: "b"}}
	c = NewFakeLoop(s.T(), Config{Git: git, Hotspots: hot}).Client()
	gotGit, err := c.GitRecent(s.ctx, 3)
	require.NoError(s.T(), err)
	require.Equal(s.T(), git, gotGit)
	for _, tt := range []struct {
		limit int
		want  []pluginsdk.Hotspot
	}{{0, hot}, {1, hot[:1]}, {5, hot}} {
		got, err := c.Hotspots(s.ctx, tt.limit)
		require.NoError(s.T(), err)
		require.Equal(s.T(), tt.want, got)
	}
}

func (s *FakeLoopSuite) TestCapabilities() {
	f := NewFakeLoop(s.T(), Config{Manifest: &pluginsdk.Manifest{}, Files: map[string]string{"a.go": ""}, Git: &pluginsdk.GitRecent{}, Hotspots: []pluginsdk.Hotspot{}})
	c := f.Client()
	_, err := c.File(s.ctx, "a.go", 0, 0)
	require.True(s.T(), pluginsdk.HasStatus(err, http.StatusForbidden))
	_, err = c.Tree(s.ctx, "")
	require.True(s.T(), pluginsdk.HasStatus(err, http.StatusForbidden))
	_, err = c.GitRecent(s.ctx, 0)
	require.True(s.T(), pluginsdk.HasStatus(err, http.StatusForbidden))
	_, err = c.Hotspots(s.ctx, 0)
	require.True(s.T(), pluginsdk.HasStatus(err, http.StatusForbidden))
	err = c.Notify(s.ctx, "t", "b")
	require.True(s.T(), pluginsdk.HasStatus(err, http.StatusForbidden))
	require.Empty(s.T(), f.Notifications())
}

func (s *FakeLoopSuite) TestRuns() {
	agents := []pluginsdk.Agent{{ID: "reader", ReadOnly: true}, {ID: "writer"}}
	tests := []struct {
		name   string
		caps   []string
		agent  string
		status int
	}{
		{"read-only agent", []string{pluginsdk.CapAgentReadOnly}, "reader", 0},
		{"writing agent", []string{pluginsdk.CapAgentWrite}, "writer", 0},
		{"read-only agent under write", []string{pluginsdk.CapAgentWrite}, "reader", 0},
		{"writing agent under readonly", []string{pluginsdk.CapAgentReadOnly}, "writer", http.StatusForbidden},
		{"no capability", nil, "reader", http.StatusForbidden},
		{"undeclared agent", []string{pluginsdk.CapAgentWrite}, "nope", http.StatusNotFound},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			f := NewFakeLoop(s.T(), Config{Manifest: &pluginsdk.Manifest{Capabilities: tt.caps, Agents: agents}})
			run, err := f.Client().StartRun(s.ctx, pluginsdk.RunRequest{Agent: tt.agent, Prompt: "go"})
			if tt.status != 0 {
				require.True(s.T(), pluginsdk.HasStatus(err, tt.status), "%v", err)
				require.Empty(s.T(), f.Runs())
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), &pluginsdk.Run{RunID: "run-1", ThreadID: "plugin-thread"}, run)
			require.Equal(s.T(), []StartedRun{{RunRequest: pluginsdk.RunRequest{Agent: tt.agent, Prompt: "go"}, Run: *run}}, f.Runs())
		})
	}

	f := NewFakeLoop(s.T(), Config{})
	c := f.Client()
	for i := range 2 {
		run, err := c.StartRun(s.ctx, pluginsdk.RunRequest{Agent: "any", Prompt: "go"})
		require.NoError(s.T(), err, "without a manifest any agent runs")
		require.Equal(s.T(), "run-"+string(rune('1'+i)), run.RunID)
	}
	_, err := c.StartRun(s.ctx, pluginsdk.RunRequest{Agent: "any"})
	require.True(s.T(), pluginsdk.HasStatus(err, http.StatusBadRequest))
	require.Equal(s.T(), http.StatusBadRequest, s.raw(f, http.MethodPost, "/runs", "{"))
	require.Len(s.T(), f.Runs(), 2)
}

func (s *FakeLoopSuite) TestEventsAndNotifications() {
	f := NewFakeLoop(s.T(), Config{Manifest: &pluginsdk.Manifest{Capabilities: []string{pluginsdk.CapUINotify}}})
	c := f.Client()
	require.NoError(s.T(), c.Event(s.ctx, "quiz.ready", map[string]int{"id": 1}))
	err := c.Event(s.ctx, "", nil)
	require.True(s.T(), pluginsdk.HasStatus(err, http.StatusBadRequest))
	require.NoError(s.T(), c.Notify(s.ctx, "Quiz", "ready"))

	require.Equal(s.T(), http.StatusBadRequest, s.raw(f, http.MethodPost, "/events", "{"))
	require.Equal(s.T(), http.StatusBadRequest, s.raw(f, http.MethodPost, "/notify", "{"))

	require.Equal(s.T(), []Event{{Type: "quiz.ready", Data: []byte(`{"id":1}`)}}, f.Events())
	require.Equal(s.T(), []Notification{{Title: "Quiz", Body: "ready"}}, f.Notifications())
}

func (s *FakeLoopSuite) TestMemory() {
	f := NewFakeLoop(s.T(), Config{Manifest: &pluginsdk.Manifest{Capabilities: []string{pluginsdk.CapMemoryWrite}}})
	c := f.Client()
	require.NoError(s.T(), c.WriteMemory(s.ctx, "quiz-notes", "# Notes"))
	require.NoError(s.T(), c.WriteMemory(s.ctx, "old", "x"))
	require.NoError(s.T(), c.DeleteMemory(s.ctx, "old"))
	require.NoError(s.T(), c.DeleteMemory(s.ctx, "never-written"))
	err := c.WriteMemory(s.ctx, "Bad Name", "x")
	require.True(s.T(), pluginsdk.HasStatus(err, http.StatusBadRequest))
	require.Equal(s.T(), http.StatusBadRequest, s.raw(f, http.MethodPut, "/memory/notes", "{"))
	require.Equal(s.T(), map[string]string{"quiz-notes": "# Notes"}, f.Memory())

	denied := NewFakeLoop(s.T(), Config{Manifest: &pluginsdk.Manifest{}})
	err = denied.Client().WriteMemory(s.ctx, "notes", "x")
	require.True(s.T(), pluginsdk.HasStatus(err, http.StatusForbidden))
}

func (s *FakeLoopSuite) TestBadge() {
	f := NewFakeLoop(s.T(), Config{Manifest: &pluginsdk.Manifest{Capabilities: []string{pluginsdk.CapUIBadge}}})
	c := f.Client()
	require.Nil(s.T(), f.Badge())
	require.NoError(s.T(), c.SetBadge(s.ctx, " 3 due ", "3 quizzes due"))
	require.Equal(s.T(), &Badge{Text: "3 due", Title: "3 quizzes due"}, f.Badge())
	for _, b := range [][2]string{{"", ""}, {strings.Repeat("a", 17), ""}, {"a\nb", ""}, {"a", strings.Repeat("a", 201)}} {
		err := c.SetBadge(s.ctx, b[0], b[1])
		require.True(s.T(), pluginsdk.HasStatus(err, http.StatusBadRequest), b)
	}
	require.Equal(s.T(), http.StatusBadRequest, s.raw(f, http.MethodPut, "/badge", "{"))
	require.Equal(s.T(), &Badge{Text: "3 due", Title: "3 quizzes due"}, f.Badge(), "a refused badge keeps the last one")
	require.NoError(s.T(), c.ClearBadge(s.ctx))
	require.Nil(s.T(), f.Badge())

	denied := NewFakeLoop(s.T(), Config{Manifest: &pluginsdk.Manifest{}})
	err := denied.Client().SetBadge(s.ctx, "a", "")
	require.True(s.T(), pluginsdk.HasStatus(err, http.StatusForbidden))
}

func (s *FakeLoopSuite) TestCards() {
	m := &pluginsdk.Manifest{Capabilities: []string{pluginsdk.CapUICard}, UI: &pluginsdk.UI{Cards: []pluginsdk.Card{{ID: "score", Entry: "ui/score.html"}}}}
	f := NewFakeLoop(s.T(), Config{Manifest: m})
	c := f.Client()
	id, err := c.PostCard(s.ctx, "score", "Quiz", map[string]int{"score": 3})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "card-1", id)
	_, err = c.PostCard(s.ctx, "other", "", nil)
	require.True(s.T(), pluginsdk.HasStatus(err, http.StatusBadRequest))
	require.Equal(s.T(), http.StatusBadRequest, s.raw(f, http.MethodPost, "/cards", "{"))
	require.Equal(s.T(), []Card{{MsgID: "card-1", Card: "score", Title: "Quiz", Data: json.RawMessage(`{"score":3}`)}}, f.Cards())

	_, err = NewFakeLoop(s.T(), Config{Manifest: &pluginsdk.Manifest{Capabilities: []string{pluginsdk.CapUICard}}}).Client().PostCard(s.ctx, "score", "", nil)
	require.True(s.T(), pluginsdk.HasStatus(err, http.StatusBadRequest), "a manifest without ui declares no cards")

	open := NewFakeLoop(s.T(), Config{})
	_, err = open.Client().PostCard(s.ctx, "any", "", nil)
	require.NoError(s.T(), err)
	_, err = open.Client().PostCard(s.ctx, "", "", nil)
	require.True(s.T(), pluginsdk.HasStatus(err, http.StatusBadRequest))

	_, err = NewFakeLoop(s.T(), Config{Manifest: &pluginsdk.Manifest{}}).Client().PostCard(s.ctx, "score", "", nil)
	require.True(s.T(), pluginsdk.HasStatus(err, http.StatusForbidden))
}

func (s *FakeLoopSuite) TestRunUI() {
	f := NewFakeLoop(s.T(), Config{Manifest: &pluginsdk.Manifest{Capabilities: []string{pluginsdk.CapUIRun}}, Threads: []string{"th1"}})
	c := f.Client()
	open := pluginsdk.UIStep{"op": "select_channel", "channel_id": "ch1"}

	reply, err := c.RunUI(s.ctx, pluginsdk.UICommand{Steps: []pluginsdk.UIStep{open, {"op": "wait", "ms": 10}}})
	require.NoError(s.T(), err)
	require.Equal(s.T(), &pluginsdk.UIReply{ClientID: "window", Results: []map[string]any{
		{"op": "select_channel", "ok": true}, {"op": "wait", "ok": true}}}, reply)
	thread := pluginsdk.UICommand{ClientID: "w1", Steps: []pluginsdk.UIStep{{"op": "select_channel", "channel_id": "th1"}}, Timeout: 5 * time.Minute}
	reply, err = c.RunUI(s.ctx, thread)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "w1", reply.ClientID)

	tests := []struct {
		name   string
		cmd    pluginsdk.UICommand
		status int
	}{
		{name: "no steps", status: http.StatusBadRequest},
		{name: "a step without an op", cmd: pluginsdk.UICommand{Steps: []pluginsdk.UIStep{open, {"ms": 1}}}, status: http.StatusBadRequest},
		{name: "a long timeout", cmd: pluginsdk.UICommand{Steps: []pluginsdk.UIStep{open}, Timeout: time.Hour}, status: http.StatusBadRequest},
		{name: "not opening the channel first", cmd: pluginsdk.UICommand{Steps: []pluginsdk.UIStep{{"op": "wait"}}}, status: http.StatusForbidden},
		{name: "another channel", cmd: pluginsdk.UICommand{Steps: []pluginsdk.UIStep{open, {"op": "select_channel", "channel_id": "ch2"}}}, status: http.StatusForbidden},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			_, err := c.RunUI(s.ctx, tt.cmd)
			require.True(s.T(), pluginsdk.HasStatus(err, tt.status), "%v", err)
		})
	}
	require.Equal(s.T(), http.StatusBadRequest, s.raw(f, http.MethodPost, "/ui/commands", "{"))
	require.Equal(s.T(), http.StatusBadRequest, s.raw(f, http.MethodPost, "/ui/commands", `{"steps":[{"op":"wait"}],"timeout":"soon"}`))
	require.Equal(s.T(), []pluginsdk.UICommand{{Steps: []pluginsdk.UIStep{open, {"op": "wait", "ms": float64(10)}}}, thread}, f.UICommands())

	denied := NewFakeLoop(s.T(), Config{Manifest: &pluginsdk.Manifest{}})
	_, err = denied.Client().RunUI(s.ctx, pluginsdk.UICommand{Steps: []pluginsdk.UIStep{open}})
	require.True(s.T(), pluginsdk.HasStatus(err, http.StatusForbidden))
}
