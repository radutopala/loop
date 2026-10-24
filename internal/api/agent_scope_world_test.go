package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agentregistry"
	"github.com/radutopala/loop/internal/apiauth"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/memory"
	"github.com/radutopala/loop/internal/monitor"
	"github.com/radutopala/loop/internal/osutil"
	"github.com/radutopala/loop/internal/review"
	"github.com/radutopala/loop/internal/scheduler"
	"github.com/radutopala/loop/internal/workflow"
)

// The two-project world of agent_scope_spec_test.go. Project alpha is the
// agent's; project bravo is another's. Everything bravo owns carries
// bravoSecret in its name or content, so a response that shows any of it
// shows the secret.

const (
	alphaChan    = "alpha-chan"
	alphaThread  = "alpha-thread"
	alphaAgent   = "alpha-agent"
	alphaRun     = "alpha-run"
	alphaMonitor = "alpha-monitor"
	alphaComment = "alpha-comment"
	alphaMsg     = "alpha-msg"
	alphaSession = "aaaaaaaa-0000-4000-8000-000000000001"
	alphaLearn   = "alpha-learn"
	// alphaPlayground is alpha's project playground.
	alphaPlayground = "alpha-pg"

	bravoChan    = "bravo-chan"
	bravoThread  = "bravo-thread"
	bravoSide    = "side-bravo"
	bravoAgent   = "bravo-agent"
	bravoRun     = "bravo-run"
	bravoMonitor = "bravo-monitor"
	bravoComment = "bravo-comment"
	bravoMsg     = "bravo-msg"
	bravoSession = "bbbbbbbb-0000-4000-8000-000000000002"
	bravoLearn   = "bravo-learn"
	bravoSecret  = "bravo-secret"
	// bravoPlayground is bravo's project playground.
	bravoPlayground = "bravo-pg"

	worldAgentToken = "alpha-agent-token"
	worldOwnerToken = "owner-token"
)

// worldSys is the real file system with the world's home dir.
type worldSys struct {
	osutil.RealSystem
	home string
}

func (s worldSys) UserHomeDir() (string, error) { return s.home, nil }

// worldWrite is a change a fake was asked to make, with every id it names.
type worldWrite struct {
	op   string
	refs []string
}

// worldWrites records what the world's fakes are asked to change.
type worldWrites struct {
	mu   sync.Mutex
	list []worldWrite
}

func (w *worldWrites) add(op string, refs ...string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.list = append(w.list, worldWrite{op, refs})
}

// since returns the writes recorded after the first n.
func (w *worldWrites) since(n int) []worldWrite {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.list[n:])
}

func (w *worldWrites) len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.list)
}

// worldMessages is the message handler, sender and session switcher.
type worldMessages struct{ w *worldWrites }

func (m worldMessages) HandleIncomingMessage(_ context.Context, channelID, _, _, _ string) {
	m.w.add("message", channelID)
}

func (m worldMessages) HandleIncomingMessageWithPriority(_ context.Context, channelID, _, _, _ string, _ int) {
	m.w.add("message", channelID)
}

func (m worldMessages) HandleIncomingMessageDelayed(_ context.Context, channelID, _, _, _, _ string, _ int64) {
	m.w.add("message", channelID)
}

func (m worldMessages) HandleThreadCreated(_ context.Context, threadID, _, _ string) {
	m.w.add("thread message", threadID)
}

func (m worldMessages) PostMessage(_ context.Context, channelID, _ string) error {
	m.w.add("post", channelID)
	return nil
}

func (m worldMessages) SwitchSession(_ context.Context, channelID, sessionID string) (bool, error) {
	m.w.add("switch session", channelID, sessionID)
	return false, nil
}

// worldThreads creates and deletes threads in the world's store.
type worldThreads struct {
	w     *worldWrites
	store *db.SQLiteStore
}

func (t worldThreads) CreateThread(ctx context.Context, channelID, name, _, _ string) (string, error) {
	t.w.add("create thread", channelID)
	parent, err := t.store.GetChannel(ctx, channelID)
	if err != nil || parent == nil {
		return "", fmt.Errorf("no channel %s", channelID)
	}
	id := fmt.Sprintf("%s-t%d", channelID, time.Now().UnixNano())
	return id, t.store.UpsertChannel(ctx, &db.Channel{ChannelID: id, Name: name, ParentID: channelID, DirPath: parent.DirPath, Platform: parent.Platform, Active: true})
}

func (t worldThreads) DeleteThread(ctx context.Context, threadID string) error {
	t.w.add("delete thread", threadID)
	return t.store.DeleteChannel(ctx, threadID)
}

func (t worldThreads) EnsureChannel(_ context.Context, dirPath, _ string) (string, error) {
	t.w.add("ensure channel", dirPath)
	return "", fmt.Errorf("not in this world")
}

func (t worldThreads) CreateChannel(_ context.Context, _, _, sourceChannelID, _ string) (string, error) {
	t.w.add("create channel", sourceChannelID)
	return "", fmt.Errorf("not in this world")
}

func (t worldThreads) EnsureChannelAllPlatforms(_ context.Context, dirPath string) ([]EnsureResult, error) {
	t.w.add("ensure channel", dirPath)
	return nil, fmt.Errorf("not in this world")
}

// worldMonitors holds each project's running monitor.
type worldMonitors struct {
	w  *worldWrites
	mu sync.Mutex
	m  map[string]monitor.Monitor
}

func (m *worldMonitors) Start(channelID, dirPath, _, _, _ string, _ time.Duration, _ bool) (*monitor.Monitor, error) {
	m.w.add("start monitor", channelID, dirPath)
	return &monitor.Monitor{ID: "new-monitor", ChannelID: channelID}, nil
}

func (m *worldMonitors) List(channelID string) []monitor.Monitor {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []monitor.Monitor
	for _, mon := range m.m {
		if mon.ChannelID == channelID {
			out = append(out, mon)
		}
	}
	return out
}

// Cancel holds a monitor to its channel, as monitor.Manager does.
func (m *worldMonitors) Cancel(channelID, id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	mon, ok := m.m[id]
	if !ok || mon.ChannelID != channelID {
		return false
	}
	m.w.add("cancel monitor", channelID, id)
	delete(m.m, id)
	return true
}

// worldWorkflows holds each project's workflow run.
type worldWorkflows struct {
	w    *worldWrites
	mu   sync.Mutex
	runs map[string]*db.WorkflowRun
}

func (e *worldWorkflows) run(id string) *db.WorkflowRun {
	e.mu.Lock()
	defer e.mu.Unlock()
	if r, ok := e.runs[id]; ok {
		c := *r
		return &c
	}
	return nil
}

func (e *worldWorkflows) StartRun(_ context.Context, opts workflow.StartRunOptions) (string, error) {
	e.w.add("start run", opts.ChannelID, opts.DirPath, opts.ParentDirPath)
	return "new-run", nil
}

func (e *worldWorkflows) ResumeRun(_ context.Context, runID, _ string) error {
	e.w.add("resume run", runID)
	return nil
}

func (e *worldWorkflows) CancelRun(_ context.Context, runID string) error {
	e.w.add("cancel run", runID)
	return nil
}

func (e *worldWorkflows) DeleteRun(_ context.Context, runID string) error {
	e.w.add("delete run", runID)
	return nil
}

func (e *worldWorkflows) RetryRun(_ context.Context, runID string) (string, error) {
	e.w.add("retry run", runID)
	return "new-run", nil
}

func (e *worldWorkflows) GetRun(_ context.Context, runID string) (*db.WorkflowRun, []*db.NodeRun, error) {
	if r := e.run(runID); r != nil {
		return r, nil, nil
	}
	return nil, nil, fmt.Errorf("run %s not found", runID)
}

func (e *worldWorkflows) ListRuns(_ context.Context, channelID string, _, _ int) ([]*db.WorkflowRun, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := []*db.WorkflowRun{}
	for _, id := range slices.Sorted(func(yield func(string) bool) {
		for id := range e.runs {
			if !yield(id) {
				return
			}
		}
	}) {
		if r := e.runs[id]; channelID == "" || r.ChannelID == channelID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (e *worldWorkflows) ListWorkflows(context.Context, string, string) ([]config.WorkflowDef, error) {
	return nil, nil
}

func (e *worldWorkflows) RecoverRuns(context.Context) error { return nil }

// worldMemory searches a dir's README, as the indexer searches its files.
type worldMemory struct{ w *worldWrites }

func (m worldMemory) Search(_ context.Context, dir, _ string, _ int) ([]memory.SearchResult, error) {
	b, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		return nil, err
	}
	return []memory.SearchResult{{FilePath: "README.md", Content: string(b)}}, nil
}

func (m worldMemory) Index(_ context.Context, dir string) (int, error) {
	m.w.add("index memory", dir)
	return 1, nil
}

// scopeWorld is a daemon serving two projects, alpha and bravo.
type scopeWorld struct {
	root, home         string
	alphaDir, bravoDir string
	store              *db.SQLiteStore
	srv                *Server
	writes             *worldWrites
	alphaTask          int64
	bravoTask          int64
	alphaProposal      int64
	bravoProposal      int64
	// stateStmt is bravoState's query; state is what it last read.
	stateStmt *sql.Stmt
	state     string
}

// newScopeWorld builds the world in a fresh copy of migrated, a migrated
// store's file, with stateQuery, bravoQuery's, for bravoState.
func newScopeWorld(t require.TestingT, tmp string, migrated []byte, stateQuery string) *scopeWorld {
	ctx := context.Background()
	w := &scopeWorld{root: tmp, home: filepath.Join(tmp, "home"), writes: &worldWrites{}}
	w.alphaDir = filepath.Join(tmp, "proj-alpha")
	w.bravoDir = filepath.Join(tmp, "proj-bravo")

	path := filepath.Join(tmp, "loop.db")
	require.NoError(t, os.WriteFile(path, migrated, 0o600))
	var err error
	w.store, err = db.NewSQLiteStore(path)
	require.NoError(t, err)
	w.stateStmt, err = w.store.WriterDB().Prepare(stateQuery)
	require.NoError(t, err)

	for _, ch := range []*db.Channel{
		{ChannelID: alphaChan, Name: "alpha", DirPath: w.alphaDir, SessionID: alphaSession},
		{ChannelID: alphaThread, Name: "alpha thread", DirPath: w.alphaDir, ParentID: alphaChan},
		{ChannelID: bravoChan, Name: bravoSecret + " channel", DirPath: w.bravoDir, SessionID: bravoSession},
		{ChannelID: bravoThread, Name: bravoSecret + " thread", DirPath: w.bravoDir, ParentID: bravoChan, SessionID: bravoSession},
	} {
		ch.Platform = "discord"
		ch.Active = true
		require.NoError(t, w.store.UpsertChannel(ctx, ch))
	}
	require.NoError(t, w.store.InsertSideChat(ctx, &db.Channel{ChannelID: bravoSide, Name: bravoSecret + " side", ParentID: bravoChan, DirPath: w.bravoDir, Platform: "discord", Kind: db.ChannelKindSide}))
	for _, l := range []struct {
		id, parent, dir string
		proposal        *int64
	}{
		{alphaLearn, alphaChan, w.alphaDir, &w.alphaProposal},
		{bravoLearn, bravoChan, w.bravoDir, &w.bravoProposal},
	} {
		require.NoError(t, w.store.InsertHiddenThread(ctx, &db.Channel{ChannelID: l.id, Name: l.id, ParentID: l.parent, DirPath: l.dir, Platform: "discord", Kind: db.ChannelKindLearn}))
		p := &db.LearnProposal{ChannelID: l.parent, LearnChannelID: l.id, Kind: db.LearnKindRename, Title: l.parent + " lesson", Rationale: "r", Payload: "{}"}
		_, err = w.store.FileLearnProposals(ctx, l.parent, []*db.LearnProposal{p}, nil)
		require.NoError(t, err)
		*l.proposal = p.ID
	}

	sched := scheduler.NewTaskScheduler(w.store, nil, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	w.alphaTask, err = sched.AddTask(ctx, &db.ScheduledTask{ChannelID: alphaChan, Schedule: "0 9 * * *", Type: db.TaskTypeCron, Prompt: "alpha task", Enabled: true})
	require.NoError(t, err)
	w.bravoTask, err = sched.AddTask(ctx, &db.ScheduledTask{ChannelID: bravoChan, Schedule: "0 9 * * *", Type: db.TaskTypeCron, Prompt: bravoSecret + " task", Enabled: true})
	require.NoError(t, err)

	for _, m := range []*db.Message{
		{ChannelID: alphaChan, MsgID: alphaMsg, AuthorID: "u", Content: "alpha queued"},
		{ChannelID: bravoChan, MsgID: bravoMsg, AuthorID: "u", Content: bravoSecret + " queued"},
	} {
		ch, err := w.store.GetChannel(ctx, m.ChannelID)
		require.NoError(t, err)
		m.ChatID, m.CreatedAt = ch.ID, time.Now()
		require.NoError(t, w.store.InsertMessage(ctx, m))
	}

	for _, f := range []struct{ path, content string }{
		{filepath.Join(w.alphaDir, "README.md"), "alpha"},
		{filepath.Join(w.alphaDir, ".loop", "playground", alphaPlayground, "index.html"), "alpha playground"},
		{filepath.Join(w.bravoDir, "README.md"), bravoSecret},
		{filepath.Join(w.bravoDir, ".loop", "playground", bravoPlayground, "index.html"), bravoSecret},
		{filepath.Join(w.bravoDir, ".loop", "config.json"), `{"prompt_shortcuts":[{"name":"` + bravoSecret + `","prompt":"` + bravoSecret + `"}]}`},
		{w.transcript(w.alphaDir, alphaSession), transcriptLine("alpha said")},
		{w.transcript(w.bravoDir, bravoSession), transcriptLine(bravoSecret + " said")},
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(f.path), 0o755))
		require.NoError(t, os.WriteFile(f.path, []byte(f.content), 0o644))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, "loop"), 0o755))

	agents := agentLookup{worldAgentToken: {Kind: apiauth.KindAgent, ContainerID: "c-alpha", ChannelID: alphaChan, DirPath: w.alphaDir}}
	msgs := worldMessages{w.writes}
	threads := worldThreads{w.writes, w.store}
	srv := NewServer(sched, threads, threads, w.store, msgs, slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithAuth(AuthDeps{OwnerToken: worldOwnerToken, Agents: agents}))
	srv.sys = worldSys{home: w.home}
	srv.worktreeCreator.Sys = srv.sys
	srv.SetLoopDir(filepath.Join(tmp, "loop"))
	srv.configs.load = func() (*config.Config, error) { return &config.Config{}, nil }
	srv.deliverAsync = func(f func()) { f() }
	srv.uiStateWaitOverride = time.Millisecond
	srv.SetIncomingMessageHandler(msgs)
	srv.SetSessionSwitcher(msgs)
	srv.SetMemoryIndexer(worldMemory{w.writes})

	registry := agentregistry.New()
	registry.Register(&agentregistry.AgentInfo{AgentID: alphaAgent, ChannelID: alphaChan, Name: "alpha agent"})
	registry.Register(&agentregistry.AgentInfo{AgentID: bravoAgent, ChannelID: bravoChan, Name: bravoSecret + " agent"})
	srv.SetAgentRegistry(registry)

	srv.SetMonitors(&worldMonitors{w: w.writes, m: map[string]monitor.Monitor{
		alphaMonitor: {ID: alphaMonitor, ChannelID: alphaChan, Description: "alpha watch"},
		bravoMonitor: {ID: bravoMonitor, ChannelID: bravoChan, Description: bravoSecret + " watch"},
	}})
	srv.SetWorkflowEngine(&worldWorkflows{w: w.writes, runs: map[string]*db.WorkflowRun{
		alphaRun: {ID: alphaRun, ChannelID: alphaChan, DirPath: w.alphaDir, WorkflowName: "alpha flow", Status: db.WorkflowRunStatusPaused},
		bravoRun: {ID: bravoRun, ChannelID: bravoChan, DirPath: w.bravoDir, WorkflowName: bravoSecret + " flow", Status: db.WorkflowRunStatusPaused},
	}})

	reviews := review.NewStore()
	reviews.Put(alphaChan, &review.Session{ChannelID: alphaChan, Comments: []*review.Comment{{ID: alphaComment, Path: "a.go", Line: 1, Body: "alpha finding"}}})
	reviews.Put(bravoChan, &review.Session{ChannelID: bravoChan, Comments: []*review.Comment{{ID: bravoComment, Path: "b.go", Line: 1, Body: bravoSecret + " finding"}}})
	srv.review.sessions = reviews

	w.srv = srv
	return w
}

// transcript is where Claude Code keeps a session of a project dir.
func (w *scopeWorld) transcript(dir, session string) string {
	return filepath.Join(w.home, ".claude", "projects", osutil.EncodeClaudeProjectPath(dir), session+".jsonl")
}

// transcriptLine is a session transcript with one prompt.
func transcriptLine(prompt string) string {
	b, _ := json.Marshal(map[string]any{"type": "user", "uuid": "u1", "message": map[string]any{"role": "user", "content": prompt}})
	return string(b) + "\n"
}

func (w *scopeWorld) close() {
	_ = w.stateStmt.Close()
	_ = w.store.Close()
}

// bravoIDs are the ids of what bravo owns.
func bravoIDs() []string {
	return []string{bravoChan, bravoThread, bravoSide, bravoLearn, bravoAgent, bravoRun, bravoMonitor, bravoComment, bravoMsg, bravoSession}
}

// bravoState is everything bravo owns, as text: its rows in every table that
// names a channel, its files and session transcripts, its agents, review,
// monitors and runs. A request that changes any of it changes the text.
func (w *scopeWorld) bravoState(t require.TestingT) string {
	var sb strings.Builder
	rows, err := w.stateStmt.Query(w.bravoDir)
	require.NoError(t, err)
	for rows.Next() {
		var table, row string
		var rowid int64
		require.NoError(t, rows.Scan(&table, &rowid, &row))
		fmt.Fprintf(&sb, "%s %s\n", table, row)
	}
	require.NoError(t, rows.Close())
	for _, root := range []string{w.bravoDir, filepath.Dir(w.transcript(w.bravoDir, bravoSession))} {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				b, _ := os.ReadFile(path)
				fmt.Fprintf(&sb, "file %s %q\n", path, b)
			}
			return nil
		})
	}
	for _, a := range w.srv.agentRegistry.List(bravoChan) {
		fmt.Fprintf(&sb, "agent %+v\n", *a)
	}
	b, _ := json.Marshal(w.srv.review.sessions.Get(bravoChan))
	fmt.Fprintf(&sb, "review %s\n", b)
	fmt.Fprintf(&sb, "monitors %+v\n", w.srv.monitors.List(bravoChan))
	fmt.Fprintf(&sb, "run %+v\n", w.srv.workflowEngine.(*worldWorkflows).run(bravoRun))
	return sb.String()
}

// bravoQuery selects bravo's rows, quoted, in every table of sdb that names
// a channel or a dir; its one parameter is bravo's dir.
func bravoQuery(t require.TestingT, sdb *sql.DB) string {
	rows, err := sdb.Query(`SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`)
	require.NoError(t, err)
	var tables []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		tables = append(tables, name)
	}
	require.NoError(t, rows.Close())
	in := "'" + strings.Join(bravoIDs(), "','") + "', ?1"
	var queries []string
	for _, table := range tables {
		cols := tableColumns(t, sdb, table)
		var where []string
		for _, c := range []string{"channel_id", "parent_id", "dir_path"} {
			if slices.Contains(cols, c) {
				where = append(where, c+" IN ("+in+")")
			}
		}
		if len(where) > 0 {
			quoted := make([]string, len(cols))
			for i, c := range cols {
				quoted[i] = `quote("` + c + `")`
			}
			queries = append(queries, "SELECT '"+table+"', rowid, "+strings.Join(quoted, " || ',' || ")+" FROM "+table+" WHERE "+strings.Join(where, " OR "))
		}
	}
	return strings.Join(queries, " UNION ALL ") + " ORDER BY 1, 2"
}

func tableColumns(t require.TestingT, sdb *sql.DB, table string) []string {
	rows, err := sdb.Query("SELECT name FROM pragma_table_info(?)", table)
	require.NoError(t, err)
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		require.NoError(t, rows.Scan(&c))
		cols = append(cols, c)
	}
	return cols
}

// handler is the daemon's handler chain with guard in the agent guard's place.
func (w *scopeWorld) handler(guard func(http.Handler) http.Handler) http.Handler {
	return w.srv.auth.Wrap(guard(w.srv.buildMux()))
}
