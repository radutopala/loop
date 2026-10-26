package api

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/db"
)

// Spec: an agent's token holds it to its project. In a world of two
// projects, alpha (the agent's) and bravo, no request made with alpha's
// agent token, on any route the token reaches, with any of bravo's ids in
// any place a handler reads one (path, query, JSON body), shows, lists or
// changes anything of bravo's.
//
// The world (agent_scope_world_test.go) is a real SQLite store, the real
// task scheduler, agent registry and review store, real project dirs and
// session transcripts on disk, and fakes for the message, thread, monitor,
// workflow and memory services that record what they are asked to change.
// bravo has a channel, a thread, a side chat, a learn thread and proposal,
// a task, a queued message, a session, an agent, a monitor, a review
// comment, a workflow run, a playground and a project config, all named or
// filled with bravoSecret.
//
// Each agent route has a request template that reaches its handler with
// alpha's ids; every id in it is a position. For each position, the spec
// puts each of bravo's ids there in turn, sends the request through the
// daemon's handler chain (authenticator, agent guard, router, handler), and
// checks that
//   - the response shows none of bravo's ids, dir or secret that the
//     request didn't name itself, and
//   - bravo's rows in every table naming a channel or dir, its files and
//     transcripts, agents, review, monitors and run are unchanged, and no
//     fake was asked to change anything naming bravo.
//
// It also sends each route's template with every id-like query parameter
// and body field the api package reads anywhere set to a bravo id, so a
// handler that starts reading a field its template doesn't name is covered;
// those requests may show none of bravo's, named or not.
//
// Trusted base: the authenticator's token lookup, the fakes (they hold a
// monitor or run to its channel the way the real services do), and project
// dirs that don't nest: a project inside another's dir is that project's,
// files and all. Routes that reach a service the world leaves out (review
// and quality services, browser, plugins, the app window) answer that it
// isn't configured; the guard's own tests (agent_scope_routes_test.go)
// cover what those requests name. Global shortcuts, workflows and
// playgrounds are shared by every project and belong to neither.

// worldRoute is how the spec calls an agent route: target is the path and
// query, body the JSON body, both with <kind> placeholders for ids. reveals
// and writes, when set, say why the route may show or change bravo's.
type worldRoute struct {
	target  string
	body    string
	reveals string
	writes  string
}

// worldRoutes has an entry for every route an agent token reaches.
var worldRoutes = map[string]worldRoute{
	"POST /api/agents":              {target: "/api/agents", body: `{"channel_id":"<channel>","agent_id":"new-agent","name":"n"}`},
	"GET /api/agents":               {target: "/api/agents?channel_id=<channel>"},
	"PATCH /api/agents/{id}":        {target: "/api/agents/<agent>", body: `{"channel_id":"<channel>","status":"busy"}`},
	"DELETE /api/agents/{id}":       {target: "/api/agents/<agent>?channel_id=<channel>"},
	"POST /api/agents/{id}/message": {target: "/api/agents/<agent>/message", body: `{"channel_id":"<channel>","from_agent_id":"<agent>","content":"hi"}`},

	"GET /api/channels":                        {target: "/api/channels?query=<channel>", reveals: "search_channels lists every channel, for an agent to hand work to another project's"},
	"POST /api/channels/create":                {target: "/api/channels/create", body: `{"name":"n","channel_id":"<channel>","author_id":"u"}`},
	"POST /api/channels/{id}/rename":           {target: "/api/channels/<thread>/rename", body: `{"name":"n"}`},
	"POST /api/channels/{id}/description":      {target: "/api/channels/<thread>/description", body: `{"description":"d"}`},
	"POST /api/channels/{id}/ticket":           {target: "/api/channels/<thread>/ticket", body: `{"ticket_url":"https://example.com/t/1"}`},
	"POST /api/channels/{id}/monitors":         {target: "/api/channels/<channel>/monitors", body: `{"command":"true","description":"d"}`},
	"GET /api/channels/{id}/monitors":          {target: "/api/channels/<channel>/monitors"},
	"DELETE /api/channels/{id}/monitors/{mid}": {target: "/api/channels/<channel>/monitors/<monitor>"},
	"GET /api/channels/{id}/sessions":          {target: "/api/channels/<channel>/sessions"},
	"PUT /api/channels/{id}/session":           {target: "/api/channels/<channel>/session", body: `{"session_id":"<session>"}`},
	"POST /api/messages":                       {target: "/api/messages", body: `{"channel_id":"<channel>","content":"hi"}`, writes: "agents post to any channel, the way they did before API tokens"},
	"DELETE /api/messages/{id}":                {target: "/api/messages/<msg>?channel_id=<channel>"},
	"GET /api/channels/{id}/queued":            {target: "/api/channels/<channel>/queued"},
	"POST /api/threads":                        {target: "/api/threads", body: `{"channel_id":"<channel>","name":"n","session_id":"<session>","message":"hi"}`},
	"DELETE /api/threads/{id}":                 {target: "/api/threads/<thread>"},
	"POST /api/threads/{id}/fork":              {target: "/api/threads/<thread>/fork"},
	"POST /api/worktrees":                      {target: "/api/worktrees", body: `{"channel_id":"<channel>","branch":"b"}`},

	"POST /api/tasks":              {target: "/api/tasks", body: `{"channel_id":"<channel>","schedule":"0 9 * * *","type":"cron","prompt":"p"}`},
	"GET /api/tasks":               {target: "/api/tasks?channel_id=<channel>"},
	"GET /api/tasks/{id}":          {target: "/api/tasks/<task>"},
	"PATCH /api/tasks/{id}":        {target: "/api/tasks/<task>", body: `{"prompt":"p"}`},
	"DELETE /api/tasks/{id}":       {target: "/api/tasks/<task>"},
	"POST /api/tasks/{id}/move":    {target: "/api/tasks/<task>/move", body: `{"channel_id":"<thread>"}`},
	"GET /api/shortcuts":           {target: "/api/shortcuts?channel_id=<channel>"},
	"POST /api/shortcuts":          {target: "/api/shortcuts", body: `{"action":"add","scope":"project","channel_id":"<channel>","name":"s","prompt":"p"}`},
	"GET /api/bash-shortcuts":      {target: "/api/bash-shortcuts?channel_id=<channel>"},
	"POST /api/bash-shortcuts":     {target: "/api/bash-shortcuts", body: `{"action":"add","scope":"project","channel_id":"<channel>","name":"s","command":"true"}`},
	"GET /api/components":          {target: "/api/components?channel_id=<channel>"},
	"POST /api/components":         {target: "/api/components?channel_id=<channel>", body: `{"template":"math","html":"x"}`},
	"POST /api/memory/search":      {target: "/api/memory/search", body: `{"query":"q","channel_id":"<channel>","dir_path":"<dir>"}`},
	"POST /api/memory/index":       {target: "/api/memory/index", body: `{"channel_id":"<channel>","dir_path":"<dir>"}`},
	"GET /api/playground":          {target: "/api/playground?scope=project&channel_id=<channel>&name=<playground>"},
	"PUT /api/playground":          {target: "/api/playground?scope=project&channel_id=<channel>&name=<playground>", body: `{"html":"x"}`},
	"DELETE /api/playground":       {target: "/api/playground?scope=project&channel_id=<channel>&name=<playground>"},
	"GET /api/playground/file":     {target: "/api/playground/file?scope=project&channel_id=<channel>&name=<playground>&path=index.html"},
	"PUT /api/playground/file":     {target: "/api/playground/file?scope=project&channel_id=<channel>&name=<playground>&path=x.html", body: `x`},
	"DELETE /api/playground/file":  {target: "/api/playground/file?scope=project&channel_id=<channel>&name=<playground>&path=index.html"},
	"GET /api/playground/files":    {target: "/api/playground/files?scope=project&channel_id=<channel>&name=<playground>"},
	"PUT /api/playground/share":    {target: "/api/playground/share?scope=project&channel_id=<channel>&name=<playground>"},
	"DELETE /api/playground/share": {target: "/api/playground/share?scope=project&channel_id=<channel>&name=<playground>"},

	"GET /api/workflows":                   {target: "/api/workflows?channel_id=<channel>&dir_path=<dir>"},
	"POST /api/workflows":                  {target: "/api/workflows", body: `{"action":"delete","scope":"project","channel_id":"<channel>","name":"w"}`},
	"POST /api/workflows/runs":             {target: "/api/workflows/runs", body: `{"workflow_name":"w","channel_id":"<channel>","dir_path":"<dir>"}`},
	"GET /api/workflows/runs":              {target: "/api/workflows/runs?channel_id=<channel>"},
	"GET /api/workflows/runs/{id}":         {target: "/api/workflows/runs/<run>"},
	"DELETE /api/workflows/runs/{id}":      {target: "/api/workflows/runs/<run>"},
	"POST /api/workflows/runs/{id}/cancel": {target: "/api/workflows/runs/<run>/cancel"},
	"POST /api/workflows/runs/{id}/retry":  {target: "/api/workflows/runs/<run>/retry"},
	"POST /api/workflows/runs/{id}/resume": {target: "/api/workflows/runs/<run>/resume", body: `{"response":"ok"}`},

	"POST /api/channels/{id}/learn/proposals":              {target: "/api/channels/<learn>/learn/proposals", body: `{"withdraw":[{"id":<proposal>,"reason":"stale"}]}`},
	"POST /api/channels/{id}/review/comments":              {target: "/api/channels/<channel>/review/comments", body: `{"findings":[{"path":"a.go","line":1,"body":"b"}]}`},
	"POST /api/channels/{id}/review/load":                  {target: "/api/channels/<channel>/review/load", body: `{"pr_number":1}`},
	"GET /api/channels/{id}/review":                        {target: "/api/channels/<channel>/review"},
	"POST /api/channels/{id}/review/run":                   {target: "/api/channels/<channel>/review/run"},
	"POST /api/channels/{id}/review/start":                 {target: "/api/channels/<channel>/review/start", body: `{"pr_number":1}`},
	"POST /api/channels/{id}/review/dedup":                 {target: "/api/channels/<channel>/review/dedup"},
	"POST /api/channels/{id}/review/verify":                {target: "/api/channels/<channel>/review/verify"},
	"GET /api/channels/{id}/review/stats":                  {target: "/api/channels/<channel>/review/stats"},
	"DELETE /api/channels/{id}/review/comments/{cid}":      {target: "/api/channels/<channel>/review/comments/<comment>"},
	"PATCH /api/channels/{id}/review/comments/{cid}":       {target: "/api/channels/<channel>/review/comments/<comment>", body: `{"body":"b"}`},
	"POST /api/channels/{id}/review/comments/{cid}/push":   {target: "/api/channels/<channel>/review/comments/<comment>/push"},
	"POST /api/channels/{id}/review/comments/{cid}/assess": {target: "/api/channels/<channel>/review/comments/<comment>/assess", body: `{"status":"addressed"}`},
	"POST /api/channels/{id}/review/push-all":              {target: "/api/channels/<channel>/review/push-all"},

	"POST /api/channels/{id}/quality/scan":       {target: "/api/channels/<channel>/quality/scan"},
	"GET /api/channels/{id}/quality/snapshot":    {target: "/api/channels/<channel>/quality/snapshot"},
	"GET /api/channels/{id}/quality/cycles":      {target: "/api/channels/<channel>/quality/cycles"},
	"GET /api/channels/{id}/quality/metrics":     {target: "/api/channels/<channel>/quality/metrics"},
	"GET /api/channels/{id}/quality/diagnostics": {target: "/api/channels/<channel>/quality/diagnostics"},
	"GET /api/channels/{id}/quality/rules":       {target: "/api/channels/<channel>/quality/rules"},
	"POST /api/channels/{id}/quality/whatif":     {target: "/api/channels/<channel>/quality/whatif", body: `{}`},
	"GET /api/channels/{id}/quality/evolution":   {target: "/api/channels/<channel>/quality/evolution"},
	"GET /api/channels/{id}/quality/c4":          {target: "/api/channels/<channel>/quality/c4"},
	"GET /api/channels/{id}/quality/bugfactor":   {target: "/api/channels/<channel>/quality/bugfactor"},
	"GET /api/channels/{id}/quality/complexity":  {target: "/api/channels/<channel>/quality/complexity"},
	"GET /api/channels/{id}/quality/clones":      {target: "/api/channels/<channel>/quality/clones"},
	"POST /api/browser/action":                   {target: "/api/browser/action", body: `{"channel_id":"<channel>","action":"tabs"}`},
	"POST /api/plugins/{plugin}/tools/{tool}":    {target: "/api/plugins/p/tools/t", body: `{"channel_id":"<channel>","args":{}}`},
	"GET /api/ui/state":                          {target: "/api/ui/state"},
	"POST /api/ui/commands":                      {target: "/api/ui/commands", body: `{"steps":[{"op":"select_channel","channel_id":"<channel>"}]}`},
}

// literalWildcards are the path wildcards a template fills with a literal
// rather than an id, each with why.
var literalWildcards = map[string]string{
	"POST /api/plugins/{plugin}/tools/{tool} plugin": "the handler calls the plugin run of the agent's own channel",
	"POST /api/plugins/{plugin}/tools/{tool} tool":   "a tool of that run",
}

var placeholderRe = regexp.MustCompile(`<([a-z]+)>`)

// alphaValue is alpha's id of kind.
func (w *scopeWorld) alphaValue(kind string) string {
	switch kind {
	case "channel":
		return alphaChan
	case "thread":
		return alphaThread
	case "learn":
		return alphaLearn
	case "task":
		return strconv.FormatInt(w.alphaTask, 10)
	case "proposal":
		return strconv.FormatInt(w.alphaProposal, 10)
	case "dir":
		return w.alphaDir
	case "session":
		return alphaSession
	case "agent":
		return alphaAgent
	case "monitor":
		return alphaMonitor
	case "comment":
		return alphaComment
	case "msg":
		return alphaMsg
	case "run":
		return alphaRun
	case "playground":
		return alphaPlayground
	}
	panic("unknown kind " + kind)
}

// bravoValues are bravo's ids a position of kind gets in turn: bravo's of
// that kind, and its channels and dir, which a handler might take for one.
// A number goes only where a number does: anything else fails the decode.
func (w *scopeWorld) bravoValues(kind string) []string {
	switch kind {
	case "task":
		return []string{strconv.FormatInt(w.bravoTask, 10)}
	case "proposal":
		return []string{strconv.FormatInt(w.bravoProposal, 10)}
	}
	vals := []string{bravoChan, bravoThread, bravoSide, bravoLearn, w.bravoDir, w.bravoDir + "/README.md"}
	switch kind {
	case "session":
		vals = append(vals, bravoSession)
	case "agent":
		vals = append(vals, bravoAgent)
	case "monitor":
		vals = append(vals, bravoMonitor)
	case "comment":
		vals = append(vals, bravoComment)
	case "msg":
		vals = append(vals, bravoMsg)
	case "run":
		vals = append(vals, bravoRun)
	case "playground":
		vals = append(vals, bravoPlayground)
	}
	return vals
}

// worldRequest is a request the spec sends, as text. A response may show
// what an excused request names itself, as an error naming the id it
// refuses does; a stray request is excused nothing, since it names every
// bravo id at once.
type worldRequest struct {
	method, target, body string
	excused              bool
}

func (r worldRequest) String() string { return r.method + " " + r.target + " " + r.body }

// fill replaces the template's placeholders with alpha's ids, but the
// at'th, which gets value; at -1 fills them all with alpha's.
func (w *scopeWorld) fill(template string, at int, value string) string {
	i := -1
	return placeholderRe.ReplaceAllStringFunc(template, func(m string) string {
		i++
		if i == at {
			return value
		}
		return w.alphaValue(m[1 : len(m)-1])
	})
}

// positions returns the kinds of rt's placeholders, target first.
func (rt worldRoute) positions() []string {
	var kinds []string
	for _, m := range placeholderRe.FindAllStringSubmatch(rt.target+rt.body, -1) {
		kinds = append(kinds, m[1])
	}
	return kinds
}

// requests are the requests the spec sends on pattern: for each position,
// one per bravo value.
func (w *scopeWorld) requests(pattern string, rt worldRoute, at int, kind string) []worldRequest {
	method, _, _ := strings.Cut(pattern, " ")
	template := rt.target + "\x00" + rt.body
	var out []worldRequest
	for _, v := range w.bravoValues(kind) {
		target, body, _ := strings.Cut(w.fill(template, at, queryOrJSON(template, at, v)), "\x00")
		out = append(out, worldRequest{method, target, body, true})
	}
	return out
}

// queryOrJSON escapes v for where the at'th placeholder of template sits.
func queryOrJSON(template string, at int, v string) string {
	loc := placeholderRe.FindAllStringIndex(template, -1)[at]
	if strings.Contains(template[:loc[0]], "\x00") {
		q := strconv.Quote(v)
		return q[1 : len(q)-1]
	}
	if strings.Contains(template[:loc[0]], "?") {
		return urlQueryEscape(v)
	}
	return v
}

// urlQueryEscape escapes a query value, keeping slashes readable.
func urlQueryEscape(v string) string {
	return strings.NewReplacer("%", "%25", "&", "%26", "#", "%23", "+", "%2B", " ", "%20").Replace(v)
}

// counterexample is a request that showed or changed bravo's.
type counterexample struct {
	req  worldRequest
	what string
}

// check sends req through h and returns what it showed or changed of
// bravo's, "" when nothing, and the response's status.
func (w *scopeWorld) check(t require.TestingT, h http.Handler, rt worldRoute, req worldRequest) (string, int) {
	before := w.state
	if before == "" {
		before = w.bravoState(t)
	}
	writes := w.writes.len()

	r := httptest.NewRequest(req.method, req.target, strings.NewReader(req.body))
	r.Header.Set("Authorization", "Bearer "+worldAgentToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	resp := rec.Body.String()
	w.state = w.bravoState(t)

	if rt.reveals == "" {
		for _, marker := range append(bravoIDs(), bravoSecret, w.bravoDir) {
			if strings.Contains(resp, marker) && (!req.excused || !strings.Contains(req.target+req.body, marker)) {
				return fmt.Sprintf("response %d shows %q: %.300s", rec.Code, marker, resp), rec.Code
			}
		}
	}
	if rt.writes != "" {
		return "", rec.Code
	}
	if w.state != before {
		return fmt.Sprintf("response %d, bravo's state changed:\n%s\n---\n%s", rec.Code, before, w.state), rec.Code
	}
	for _, wr := range w.writes.since(writes) {
		for _, ref := range wr.refs {
			for _, marker := range append(bravoIDs(), w.bravoDir) {
				if strings.Contains(ref, marker) {
					return fmt.Sprintf("response %d, asked to %s %v", rec.Code, wr.op, wr.refs), rec.Code
				}
			}
		}
	}
	return "", rec.Code
}

// AgentScopeSpecSuite runs the spec over a fresh world for each route.
type AgentScopeSpecSuite struct {
	suite.Suite
	// migrated is a migrated store's file, each world's starting store;
	// stateQuery is bravoQuery's over it; keys are strayKeys'.
	migrated   []byte
	stateQuery string
	keys       []string
}

func TestAgentScopeSpecSuite(t *testing.T) {
	suite.Run(t, new(AgentScopeSpecSuite))
}

func (s *AgentScopeSpecSuite) SetupSuite() {
	// Each world has a home of its own; a handler that skipped it would
	// find this empty one, not the user's.
	s.T().Setenv("HOME", s.T().TempDir())
	dir := s.T().TempDir()
	store, err := db.NewSQLiteStore(filepath.Join(dir, "loop.db"))
	require.NoError(s.T(), err)
	path := filepath.Join(dir, "migrated.db")
	_, err = store.WriterDB().Exec("VACUUM INTO ?", path)
	require.NoError(s.T(), err)
	s.stateQuery = bravoQuery(s.T(), store.WriterDB())
	require.NoError(s.T(), store.Close())
	s.migrated, err = os.ReadFile(path)
	require.NoError(s.T(), err)
	s.keys = strayKeys(s.T())
}

func (s *AgentScopeSpecSuite) world(t *testing.T) *scopeWorld {
	w := newScopeWorld(t, t.TempDir(), s.migrated, s.stateQuery)
	t.Cleanup(w.close)
	return w
}

// counterexamples runs the spec with guard in the agent guard's place and
// returns the requests that showed or changed bravo's: every one, or, when
// first is set, those of the routes checked before one turned up. Routes
// run in parallel, each in worlds of its own.
func (s *AgentScopeSpecSuite) counterexamples(guard func(w *scopeWorld) func(http.Handler) http.Handler, first bool) []counterexample {
	var mu sync.Mutex
	var found []counterexample
	s.T().Run("routes", func(t *testing.T) {
		for _, pattern := range slices.Sorted(maps.Keys(worldRoutes)) {
			t.Run(pattern, func(t *testing.T) {
				t.Parallel()
				mu.Lock()
				done := first && len(found) > 0
				mu.Unlock()
				if done {
					return
				}
				cs := s.routeCounterexamples(t, guard, pattern)
				mu.Lock()
				found = append(found, cs...)
				mu.Unlock()
			})
		}
	})
	slices.SortFunc(found, func(a, b counterexample) int { return strings.Compare(a.req.String(), b.req.String()) })
	return found
}

// routeCounterexamples runs the spec on pattern.
//
// The route's positions, then its stray query share a world: a request
// with one of bravo's ids changes nothing of alpha's the next relies on,
// unless it is a counterexample. Stray bodies get a world of their own.
func (s *AgentScopeSpecSuite) routeCounterexamples(t *testing.T, guard func(w *scopeWorld) func(http.Handler) http.Handler, pattern string) []counterexample {
	var found []counterexample
	rt := worldRoutes[pattern]
	var w *scopeWorld
	var h http.Handler
	fresh := func() {
		w = s.world(t)
		h = w.handler(guard(w))
	}
	try := func(req worldRequest) int {
		what, code := w.check(t, h, rt, req)
		if what != "" {
			found = append(found, counterexample{req, what})
		}
		return code
	}
	fresh()
	for at, kind := range rt.positions() {
		for _, req := range w.requests(pattern, rt, at, kind) {
			try(req)
		}
	}
	strays := rt.strays(s.keys)
	try(w.strayQuery(strays, pattern, rt))
	// The body of PUT /api/playground/file is the file.
	if pattern != "PUT /api/playground/file" {
		fresh()
		if try(w.strayBody(strays, pattern, rt)) == http.StatusBadRequest {
			// A field of the wrong type fails the decode: one at a time.
			for _, key := range strays {
				try(w.strayBody([]string{key}, pattern, rt))
			}
		}
	}
	return found
}

func (s *AgentScopeSpecSuite) TestAgentScopeOverTwoProjects() {
	found := s.counterexamples(func(w *scopeWorld) func(http.Handler) http.Handler { return w.srv.agentGuard }, false)
	for _, c := range found {
		s.T().Errorf("%s\n  %s", c.req, c.what)
	}
}

// The spec must be able to fail: a guard that checks only the path's ids,
// or none, lets requests through that show or change bravo's.
func (s *AgentScopeSpecSuite) TestAgentScopeSpecCanFail() {
	tests := []struct {
		name  string
		guard func(w *scopeWorld) func(http.Handler) http.Handler
	}{
		{"path ids only", pathOnlyGuard},
		{"routes only", routesOnlyGuard},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.NotEmpty(s.T(), s.counterexamples(tc.guard, true))
		})
	}
}

// pathOnlyGuard is the agent guard judging a request by its path alone, as
// if it read neither the query nor the body.
func pathOnlyGuard(w *scopeWorld) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			bare := r.Clone(r.Context())
			bare.URL.RawQuery = ""
			bare.Body = http.NoBody
			passed := false
			judged := httptest.NewRecorder()
			w.srv.agentGuard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { passed = true })).ServeHTTP(judged, bare)
			if !passed {
				rw.WriteHeader(judged.Code)
				_, _ = io.Copy(rw, judged.Body)
				return
			}
			next.ServeHTTP(rw, r)
		})
	}
}

// routesOnlyGuard lets an agent call its routes with any ids.
func routesOnlyGuard(*scopeWorld) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

// Every route an agent token reaches, read from the router, has a template
// whose path is the route's, with an id in each wildcard.
func (s *AgentScopeSpecSuite) TestEveryAgentRouteHasATemplate() {
	w := s.world(s.T())
	reached := map[string]bool{}
	for _, pattern := range registeredRoutes(s.T()) {
		method, path := routeTarget(pattern, nil)
		ran := false
		h := w.srv.auth.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { ran = true }))
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("Authorization", "Bearer "+worldAgentToken)
		h.ServeHTTP(httptest.NewRecorder(), r)
		if ran && !slices.Contains(publicRoutes, pattern) {
			reached[pattern] = true
		}
	}
	require.ElementsMatch(s.T(), slices.Collect(maps.Keys(reached)), slices.Collect(maps.Keys(worldRoutes)), "routes an agent token reaches and worldRoutes differ")

	mux := w.srv.buildMux()
	for pattern, rt := range worldRoutes {
		method, _, _ := strings.Cut(pattern, " ")
		target := w.fill(rt.target, -1, "")
		_, got := mux.Handler(httptest.NewRequest(method, target, nil))
		require.Equal(s.T(), pattern, got, "%s: template %s", pattern, rt.target)
		path, _, _ := strings.Cut(rt.target, "?")
		segs := strings.Split(path, "/")
		for i, seg := range strings.Split(strings.TrimPrefix(pattern, method+" "), "/") {
			if m := wildcardRe.FindStringSubmatch(seg); m != nil && !placeholderRe.MatchString(segs[i]) {
				require.Contains(s.T(), literalWildcards, pattern+" "+m[1], "%s: wildcard {%s} filled with a literal", pattern, m[1])
			}
		}
		require.False(s.T(), rt.reveals != "" && rt.writes != "", "%s: one exception per route", pattern)
	}
	for key := range literalWildcards {
		i := strings.LastIndex(key, " ")
		require.Contains(s.T(), worldRoutes, key[:i], "stale literalWildcards entry %q", key)
	}
}

// Each template reaches its handler with alpha's ids: the guard lets it
// through.
func (s *AgentScopeSpecSuite) TestTemplatesPassTheGuard() {
	w := s.world(s.T())
	for _, pattern := range slices.Sorted(maps.Keys(worldRoutes)) {
		rt := worldRoutes[pattern]
		method, _, _ := strings.Cut(pattern, " ")
		ran := false
		h := w.srv.auth.Wrap(w.srv.agentGuard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { ran = true })))
		r := httptest.NewRequest(method, w.fill(rt.target, -1, ""), strings.NewReader(w.fill(rt.body, -1, "")))
		r.Header.Set("Authorization", "Bearer "+worldAgentToken)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		require.True(s.T(), ran, "%s: %d %s", pattern, rec.Code, rec.Body.String())
	}
}

// Examples of what the spec found: a session list naming every project's
// imported sessions, and refusals naming the channel of another project's
// task, run or side chat.
func (s *AgentScopeSpecSuite) TestFoundLeaks() {
	tests := []struct {
		name         string
		token        string
		method       string
		target       string
		body         string
		code         int
		want, absent []string
	}{
		{
			name: "an agent's session list names its project's imports", token: worldAgentToken,
			method: http.MethodGet, target: "/api/channels/" + alphaChan + "/sessions",
			code: http.StatusOK, want: []string{alphaSession}, absent: []string{bravoSession},
		},
		{
			name: "the owner's names every import", token: worldOwnerToken,
			method: http.MethodGet, target: "/api/channels/" + alphaChan + "/sessions",
			code: http.StatusOK, want: []string{alphaSession, bravoSession},
		},
		{
			name: "another project's task is refused by its id", token: worldAgentToken,
			method: http.MethodGet, target: "/api/tasks/<task>",
			code: http.StatusForbidden, want: []string{"task <task> is outside this agent's project"}, absent: []string{bravoChan},
		},
		{
			name: "another project's run is refused by its id", token: worldAgentToken,
			method: http.MethodPost, target: "/api/workflows/runs/" + bravoRun + "/cancel",
			code: http.StatusForbidden, want: []string{"workflow run " + bravoRun + " is outside this agent's project"}, absent: []string{bravoChan},
		},
		{
			name: "another project's side chat is refused as asked", token: worldAgentToken,
			method: http.MethodPost, target: "/api/ui/commands", body: `{"steps":[{"op":"select_channel","channel_id":"` + bravoSide + `"}]}`,
			code: http.StatusForbidden, want: []string{"channel " + bravoSide + " is outside this agent's project"}, absent: []string{bravoChan},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			w := s.world(s.T())
			task := strconv.FormatInt(w.bravoTask, 10)
			r := httptest.NewRequest(tc.method, strings.ReplaceAll(tc.target, "<task>", task), strings.NewReader(tc.body))
			r.Header.Set("Authorization", "Bearer "+tc.token)
			rec := httptest.NewRecorder()
			w.handler(w.srv.agentGuard).ServeHTTP(rec, r)
			require.Equal(s.T(), tc.code, rec.Code, rec.Body.String())
			for _, want := range tc.want {
				require.Contains(s.T(), rec.Body.String(), strings.ReplaceAll(want, "<task>", task))
			}
			for _, absent := range tc.absent {
				require.NotContains(s.T(), rec.Body.String(), absent)
			}
		})
	}
}

// strayKeyRe matches the query parameters and body fields that name
// something: an id, a path or dir, a channel or thread list, a name.
var strayKeyRe = regexp.MustCompile(`(id|ids|path|dir|root|channels|threads)$|^(name|target|file)$`)

// strayKeys are every query parameter and JSON body field the api package
// reads that names something, but those the guard reads itself: the
// guard's own tests hold those to the project on every route.
func strayKeys(t require.TestingT) []string {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	fset := token.NewFileSet()
	keys := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		require.NoError(t, err)
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.Field:
				if n.Tag != nil {
					tag, _ := strconv.Unquote(n.Tag.Value)
					name, _, _ := strings.Cut(reflect.StructTag(tag).Get("json"), ",")
					keys[name] = true
				}
			case *ast.CallExpr:
				if sel, ok := n.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Get" && len(n.Args) == 1 {
					if lit, ok := n.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						name, _ := strconv.Unquote(lit.Value)
						keys[name] = true
					}
				}
			}
			return true
		})
	}
	var out []string
	for k := range keys {
		guarded := slices.ContainsFunc(guardedSurfaces, func(g refSurface) bool { return g.name == k })
		if strayKeyRe.MatchString(k) && !guarded {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// strayValue is the bravo id a stray key most likely takes.
func (w *scopeWorld) strayValue(key string) string {
	switch {
	case strings.Contains(key, "session"):
		return bravoSession
	case strings.Contains(key, "agent"):
		return bravoAgent
	case strings.Contains(key, "run"):
		return bravoRun
	case strings.Contains(key, "msg"), strings.Contains(key, "message"):
		return bravoMsg
	case strings.Contains(key, "thread"):
		return bravoThread
	case strings.Contains(key, "path"), strings.Contains(key, "dir"), strings.Contains(key, "root"), key == "file":
		return w.bravoDir
	case key == "name":
		return bravoPlayground
	}
	return bravoChan
}

// strays are the keys rt's template doesn't name: those it names are left
// to its positions.
func (rt worldRoute) strays(keys []string) []string {
	return slices.DeleteFunc(slices.Clone(keys), func(key string) bool {
		return strings.Contains(rt.target, key+"=") || strings.Contains(rt.body, `"`+key+`"`)
	})
}

// strayQuery is pattern's template, with alpha's ids, and every key in
// keys a query parameter set to a bravo id. Extra query parameters fail no
// handler, so one request covers them all.
func (w *scopeWorld) strayQuery(keys []string, pattern string, rt worldRoute) worldRequest {
	method, _, _ := strings.Cut(pattern, " ")
	target := w.fill(rt.target, -1, "")
	for _, key := range keys {
		sep := "?"
		if strings.Contains(target, "?") {
			sep = "&"
		}
		target += sep + key + "=" + urlQueryEscape(w.strayValue(key))
	}
	return worldRequest{method, target, w.fill(rt.body, -1, ""), false}
}

// strayBody is pattern's template, with alpha's ids, and every key in keys
// a JSON body field set to a bravo id, a list of one for a plural key.
func (w *scopeWorld) strayBody(keys []string, pattern string, rt worldRoute) worldRequest {
	method, _, _ := strings.Cut(pattern, " ")
	fields := make([]string, 0, len(keys))
	for _, key := range keys {
		var v any = w.strayValue(key)
		if strings.HasSuffix(key, "s") {
			v = []any{v}
		}
		jv, _ := json.Marshal(v)
		fields = append(fields, `"`+key+`":`+string(jv))
	}
	body := w.fill(rt.body, -1, "")
	if body == "" {
		body = "{}"
	}
	sep := ","
	if body == "{}" {
		sep = ""
	}
	return worldRequest{method, w.fill(rt.target, -1, ""), "{" + strings.Join(fields, ",") + sep + body[1:], false}
}
