package api

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/stretchr/testify/require"
)

// routeScope is what of a project an agent route names, the way its handler
// reads it.
type routeScope struct {
	path  string   // "channel", "task" or "run": what the {id} wildcard names
	query []string // query parameters naming a channel or a dir
	body  []string // body fields naming a channel or a dir
	none  string   // why the route names nothing the guard checks
	base  string   // body fields the route needs to pass the guard
}

// agentRouteScopes has an entry for every agent route. A route added to
// agentRoutes must be added here: with the refs its handler reads, which the
// test checks the guard holds to the agent's project, or with why it names
// none.
var agentRouteScopes = map[string]routeScope{
	"POST /api/agents":              {body: []string{"channel_id"}},
	"GET /api/agents":               {query: []string{"channel_id"}},
	"PATCH /api/agents/{id}":        {body: []string{"channel_id"}},
	"DELETE /api/agents/{id}":       {query: []string{"channel_id"}},
	"POST /api/agents/{id}/message": {body: []string{"channel_id"}},

	"GET /api/channels":                        {none: "search_channels lists every channel, for an agent to hand work to another project's"},
	"POST /api/channels/create":                {body: []string{"channel_id"}},
	"POST /api/channels/{id}/rename":           {path: "channel"},
	"POST /api/channels/{id}/description":      {path: "channel"},
	"POST /api/channels/{id}/ticket":           {path: "channel"},
	"POST /api/channels/{id}/monitors":         {path: "channel"},
	"GET /api/channels/{id}/monitors":          {path: "channel"},
	"DELETE /api/channels/{id}/monitors/{mid}": {path: "channel"},
	"GET /api/channels/{id}/sessions":          {path: "channel"},
	"PUT /api/channels/{id}/session":           {path: "channel"},
	"POST /api/messages":                       {none: "agents post to any channel, the way they did before API tokens"},
	"DELETE /api/messages/{id}":                {query: []string{"channel_id"}},
	"GET /api/channels/{id}/queued":            {path: "channel"},
	"POST /api/threads":                        {body: []string{"channel_id"}},
	"DELETE /api/threads/{id}":                 {path: "channel"},
	"POST /api/threads/{id}/fork":              {path: "channel"},
	"POST /api/worktrees":                      {body: []string{"channel_id"}},

	"POST /api/tasks":              {body: []string{"channel_id"}},
	"GET /api/tasks":               {query: []string{"channel_id"}},
	"GET /api/tasks/{id}":          {path: "task"},
	"PATCH /api/tasks/{id}":        {path: "task"},
	"DELETE /api/tasks/{id}":       {path: "task"},
	"POST /api/tasks/{id}/move":    {path: "task", body: []string{"channel_id"}},
	"GET /api/shortcuts":           {query: []string{"channel_id"}},
	"POST /api/shortcuts":          {body: []string{"channel_id"}},
	"GET /api/bash-shortcuts":      {query: []string{"channel_id"}},
	"POST /api/bash-shortcuts":     {body: []string{"channel_id"}, base: `"scope":"project"`},
	"GET /api/components":          {query: []string{"channel_id"}},
	"POST /api/components":         {query: []string{"channel_id"}},
	"POST /api/memory/search":      {body: []string{"channel_id", "dir_path"}},
	"POST /api/memory/index":       {body: []string{"channel_id", "dir_path"}},
	"GET /api/playground":          {query: []string{"channel_id"}},
	"PUT /api/playground":          {query: []string{"channel_id"}},
	"DELETE /api/playground":       {query: []string{"channel_id"}},
	"GET /api/playground/file":     {query: []string{"channel_id"}},
	"PUT /api/playground/file":     {query: []string{"channel_id"}},
	"DELETE /api/playground/file":  {query: []string{"channel_id"}},
	"GET /api/playground/files":    {query: []string{"channel_id"}},
	"PUT /api/playground/share":    {query: []string{"channel_id"}},
	"DELETE /api/playground/share": {query: []string{"channel_id"}},

	"GET /api/workflows":                   {query: []string{"channel_id", "dir_path"}},
	"POST /api/workflows":                  {body: []string{"channel_id"}, base: `"scope":"project"`},
	"POST /api/workflows/runs":             {body: []string{"channel_id", "dir_path"}},
	"GET /api/workflows/runs":              {query: []string{"channel_id"}},
	"GET /api/workflows/runs/{id}":         {path: "run"},
	"DELETE /api/workflows/runs/{id}":      {path: "run"},
	"POST /api/workflows/runs/{id}/cancel": {path: "run"},
	"POST /api/workflows/runs/{id}/retry":  {path: "run"},
	"POST /api/workflows/runs/{id}/resume": {path: "run"},

	"POST /api/channels/{id}/learn/proposals":              {path: "channel"},
	"POST /api/channels/{id}/review/comments":              {path: "channel"},
	"POST /api/channels/{id}/review/load":                  {path: "channel"},
	"GET /api/channels/{id}/review":                        {path: "channel"},
	"POST /api/channels/{id}/review/run":                   {path: "channel"},
	"POST /api/channels/{id}/review/start":                 {path: "channel"},
	"POST /api/channels/{id}/review/dedup":                 {path: "channel"},
	"POST /api/channels/{id}/review/verify":                {path: "channel"},
	"GET /api/channels/{id}/review/stats":                  {path: "channel"},
	"DELETE /api/channels/{id}/review/comments/{cid}":      {path: "channel"},
	"PATCH /api/channels/{id}/review/comments/{cid}":       {path: "channel"},
	"POST /api/channels/{id}/review/comments/{cid}/push":   {path: "channel"},
	"POST /api/channels/{id}/review/comments/{cid}/assess": {path: "channel"},
	"POST /api/channels/{id}/review/push-all":              {path: "channel"},
	"POST /api/channels/{id}/quality/scan":                 {path: "channel"},
	"GET /api/channels/{id}/quality/snapshot":              {path: "channel"},
	"GET /api/channels/{id}/quality/cycles":                {path: "channel"},
	"GET /api/channels/{id}/quality/metrics":               {path: "channel"},
	"GET /api/channels/{id}/quality/diagnostics":           {path: "channel"},
	"GET /api/channels/{id}/quality/rules":                 {path: "channel"},
	"POST /api/channels/{id}/quality/whatif":               {path: "channel"},
	"GET /api/channels/{id}/quality/evolution":             {path: "channel"},
	"GET /api/channels/{id}/quality/c4":                    {path: "channel"},
	"GET /api/channels/{id}/quality/bugfactor":             {path: "channel"},
	"GET /api/channels/{id}/quality/complexity":            {path: "channel"},
	"GET /api/channels/{id}/quality/clones":                {path: "channel"},

	"POST /api/browser/action":                {body: []string{"channel_id"}},
	"POST /api/plugins/{plugin}/tools/{tool}": {none: "the handler calls the plugin run of the agent's own channel"},
	"GET /api/ui/state":                       {none: "the handler blanks the windows showing another project's channel"},
	"POST /api/ui/commands":                   {none: "the handler checks every select_channel step's channel (uiAgentRefusal)"},
}

// unscopedWildcards are the path wildcards of agent routes that name nothing
// the guard checks, each with why that's safe.
var unscopedWildcards = map[string]string{
	"PATCH /api/agents/{id} id":                                "an agent of the body's channel_id",
	"DELETE /api/agents/{id} id":                               "an agent of the query's channel_id",
	"POST /api/agents/{id}/message id":                         "an agent of the body's channel_id",
	"DELETE /api/messages/{id} id":                             "a queued message of the query's channel_id",
	"DELETE /api/channels/{id}/monitors/{mid} mid":             "a monitor of channel {id}",
	"DELETE /api/channels/{id}/review/comments/{cid} cid":      "a comment of channel {id}'s review",
	"PATCH /api/channels/{id}/review/comments/{cid} cid":       "a comment of channel {id}'s review",
	"POST /api/channels/{id}/review/comments/{cid}/push cid":   "a comment of channel {id}'s review",
	"POST /api/channels/{id}/review/comments/{cid}/assess cid": "a comment of channel {id}'s review",
	"POST /api/plugins/{plugin}/tools/{tool} plugin":           "the handler calls the agent's own channel's plugin run",
	"POST /api/plugins/{plugin}/tools/{tool} tool":             "a tool of that run",
}

// refSurface is one place a request names a channel or a dir: the path's
// {id}, a query parameter or a body field.
type refSurface struct {
	in   string // "query" or "body"
	name string
}

// guardedSurfaces are every query parameter and body field the guard reads.
var guardedSurfaces = []refSurface{
	{"query", "channel_id"},
	{"query", "dir_path"},
	{"body", "channel_id"},
	{"body", "learn_channel_id"},
	{"body", "thread_id"},
	{"body", "dir_path"},
}

// unguardedSurfaces are the surfaces of a route the guard lets name another
// project, each with why.
var unguardedSurfaces = map[string]string{
	"POST /api/messages body channel_id":             "agents post to any channel",
	"PUT /api/playground/file body channel_id":       "the body is the file, not a request",
	"PUT /api/playground/file body learn_channel_id": "the body is the file, not a request",
	"PUT /api/playground/file body thread_id":        "the body is the file, not a request",
	"PUT /api/playground/file body dir_path":         "the body is the file, not a request",
}

// refValues are an agent's own project's value and another project's for a
// kind of ref, with the suite's fixtures: ch1 is the agent's channel, ch2 in
// its project, ch3 in another; task 5 and run r1 are the project's, task 6
// and run r3 the other's.
func refValues(kind string) (own, other string) {
	switch kind {
	case "task":
		return "5", "6"
	case "run":
		return "r1", "r3"
	case "dir_path":
		return "/proj/sub", "/other/x"
	}
	return "ch2", "ch3"
}

var wildcardRe = regexp.MustCompile(`\{([^}.]+)(\.\.\.)?\}`)

// routeTarget splits a pattern into its method, "GET" when it has none, and
// a path with its wildcards filled: by fill, or "w1" when fill has no value.
func routeTarget(pattern string, fill map[string]string) (method, path string) {
	method, path, ok := strings.Cut(pattern, " ")
	if !ok {
		method, path = http.MethodGet, pattern
	}
	path = wildcardRe.ReplaceAllStringFunc(path, func(w string) string {
		m := wildcardRe.FindStringSubmatch(w)
		if v, ok := fill[m[1]]; ok {
			return v
		}
		if m[2] != "" {
			return "w1/w2"
		}
		return "w1"
	})
	return method, path
}

// wildcards returns the names of a pattern's wildcards.
func wildcards(pattern string) []string {
	var names []string
	for _, m := range wildcardRe.FindAllStringSubmatch(pattern, -1) {
		names = append(names, m[1])
	}
	return names
}

// registeredRoutes reads every route the API server registers, from the
// mux.HandleFunc calls in *Server's methods. The playground share server's
// routes, on a mux of its own without API tokens, aren't among them.
func registeredRoutes(t require.TestingT) []string {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	fset := token.NewFileSet()
	var routes []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		require.NoError(t, err)
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && isServerMethod(fn) {
				routes = append(routes, handleFuncPatterns(t, fn)...)
			}
		}
	}
	return routes
}

// isServerMethod reports whether fn is a method of *Server.
func isServerMethod(fn *ast.FuncDecl) bool {
	if fn.Recv == nil || len(fn.Recv.List) != 1 {
		return false
	}
	star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	ident, ok := star.X.(*ast.Ident)
	return ok && ident.Name == "Server"
}

// handleFuncPatterns returns the patterns of fn's mux.HandleFunc calls.
func handleFuncPatterns(t require.TestingT, fn *ast.FuncDecl) []string {
	var patterns []string
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "HandleFunc" {
			return true
		}
		if recv, ok := sel.X.(*ast.Ident); !ok || recv.Name != "mux" {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		require.True(t, ok && lit.Kind == token.STRING, "a route registered with a non-literal pattern")
		pattern, err := strconv.Unquote(lit.Value)
		require.NoError(t, err)
		patterns = append(patterns, pattern)
		return true
	})
	return patterns
}

// agentRequest is an agent's request, through the authenticator and the
// guard to a handler that records it ran.
func (s *AgentScopeSuite) agentRequest(method, target, body string) (int, string, bool) {
	ran := false
	h := s.srv.auth.Wrap(s.srv.agentGuard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		ran = true
		w.WriteHeader(http.StatusOK)
	})))
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer agent")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String(), ran
}

// The scope table and the routes agents may call stay in step, and every
// wildcard of an agent route is checked or justified.
func (s *AgentScopeSuite) TestEveryAgentRouteHasAScope() {
	require.ElementsMatch(s.T(), agentRoutes, slices.Collect(maps.Keys(agentRouteScopes)), "agentRoutes and agentRouteScopes differ")
	for _, pattern := range agentRoutes {
		sc := agentRouteScopes[pattern]
		names := sc.path != "" || len(sc.query) > 0 || len(sc.body) > 0
		require.NotEqual(s.T(), names, sc.none != "", "%s: list the refs its handler reads or why it has none, not both", pattern)
		for _, w := range wildcards(pattern) {
			if w == "id" && sc.path != "" {
				continue
			}
			require.Contains(s.T(), unscopedWildcards, pattern+" "+w, "%s: wildcard {%s} names something the guard doesn't check", pattern, w)
		}
		if sc.path != "" {
			require.Contains(s.T(), wildcards(pattern), "id", "%s: a path ref without {id}", pattern)
		}
	}
	for key := range unscopedWildcards {
		i := strings.LastIndex(key, " ")
		require.Contains(s.T(), agentRoutes, key[:i], "stale unscopedWildcards entry %q", key)
		require.Contains(s.T(), wildcards(key[:i]), key[i+1:], "stale unscopedWildcards entry %q", key)
	}
	for key := range unguardedSurfaces {
		parts := strings.Fields(key)
		require.Contains(s.T(), agentRoutes, parts[0]+" "+parts[1], "stale unguardedSurfaces entry %q", key)
	}
}

// Every agent route is a route the server serves, with the same pattern:
// the guard checks a request by the pattern its handler is registered as.
func (s *AgentScopeSuite) TestAgentRoutesAreRegistered() {
	mux := s.srv.buildMux()
	for _, pattern := range agentRoutes {
		method, path := routeTarget(pattern, nil)
		_, got := mux.Handler(httptest.NewRequest(method, path, nil))
		require.Equal(s.T(), pattern, got, "agent route %s isn't registered as such", pattern)
	}
}

// Every other route refuses an agent token: no agent route pattern also
// matches a request for an owner-only route.
func (s *AgentScopeSuite) TestOtherRoutesRefuseAgents() {
	mux := s.srv.buildMux()
	routes := registeredRoutes(s.T())
	require.Greater(s.T(), len(routes), len(agentRoutes))
	for _, pattern := range routes {
		method, path := routeTarget(pattern, nil)
		_, got := mux.Handler(httptest.NewRequest(method, path, nil))
		require.Equal(s.T(), pattern, got, "registered route %s isn't served as such", pattern)
		if slices.Contains(agentRoutes, pattern) || slices.Contains(publicRoutes, pattern) {
			continue
		}
		code, body, ran := s.agentRequest(method, path, "")
		require.False(s.T(), ran, "%s reached its handler with an agent token", pattern)
		require.Equal(s.T(), http.StatusForbidden, code, "%s: %s", pattern, body)
		require.Contains(s.T(), body, "not available to agents", pattern)
	}
}

// requestFor builds an agent route's request with its own project's refs
// everywhere, then sets one surface to value.
func requestFor(pattern string, sc routeScope, at refSurface, value string) (method, target, body string) {
	fill := map[string]string{}
	if sc.path != "" {
		own, _ := refValues(sc.path)
		if ownChannelRoutes[pattern] {
			own = "ch1"
		}
		fill["id"] = own
	}
	method, path := routeTarget(pattern, fill)
	if at.in == "path" {
		fill["id"] = value
		method, path = routeTarget(pattern, fill)
	}
	if at.in == "query" {
		path += "?" + at.name + "=" + value
	}
	fields := []string{}
	if sc.base != "" {
		fields = append(fields, sc.base)
	}
	if at.in == "body" {
		v, _ := json.Marshal(value)
		fields = append(fields, strconv.Quote(at.name)+":"+string(v))
	}
	return method, path, "{" + strings.Join(fields, ",") + "}"
}

// For each ref an agent route's handler reads, the guard refuses another
// project's before the handler runs, and lets the agent's own project's
// through.
func (s *AgentScopeSuite) TestDeclaredRefsHoldToTheProject() {
	for _, pattern := range agentRoutes {
		sc := agentRouteScopes[pattern]
		var surfaces []refSurface
		if sc.path != "" {
			surfaces = append(surfaces, refSurface{"path", sc.path})
		}
		for _, q := range sc.query {
			surfaces = append(surfaces, refSurface{"query", q})
		}
		for _, b := range sc.body {
			surfaces = append(surfaces, refSurface{"body", b})
		}
		for _, at := range surfaces {
			s.Run(pattern+" "+at.in+" "+at.name, func() {
				own, other := refValues(at.name)
				if ownChannelRoutes[pattern] && at.in == "path" {
					own = "ch1"
				}
				method, target, body := requestFor(pattern, sc, at, other)
				code, msg, ran := s.agentRequest(method, target, body)
				require.Equal(s.T(), http.StatusForbidden, code, "%s %s %s: %s", method, target, body, msg)
				require.False(s.T(), ran)

				method, target, body = requestFor(pattern, sc, at, own)
				code, msg, ran = s.agentRequest(method, target, body)
				require.Equal(s.T(), http.StatusOK, code, "%s %s %s: %s", method, target, body, msg)
				require.True(s.T(), ran)
			})
		}
		if ownChannelRoutes[pattern] {
			s.Run(pattern+" same project channel", func() {
				method, target, body := requestFor(pattern, sc, refSurface{"path", "channel"}, "ch2")
				code, _, ran := s.agentRequest(method, target, body)
				require.Equal(s.T(), http.StatusForbidden, code)
				require.False(s.T(), ran)
			})
		}
	}
}

// Every surface the guard reads is checked on every agent route, whether
// its handler reads it today or not, so a handler that starts reading one
// is already covered.
func (s *AgentScopeSuite) TestEverySurfaceOnEveryRoute() {
	for _, pattern := range agentRoutes {
		sc := agentRouteScopes[pattern]
		for _, at := range guardedSurfaces {
			s.Run(pattern+" "+at.in+" "+at.name, func() {
				own, other := refValues(at.name)
				method, target, body := requestFor(pattern, sc, at, other)
				code, msg, ran := s.agentRequest(method, target, body)
				if _, ok := unguardedSurfaces[pattern+" "+at.in+" "+at.name]; ok {
					require.Equal(s.T(), http.StatusOK, code, msg)
					require.True(s.T(), ran)
					return
				}
				require.Equal(s.T(), http.StatusForbidden, code, "%s %s %s: %s", method, target, body, msg)
				require.Contains(s.T(), msg, "outside this agent's project")
				require.False(s.T(), ran)

				method, target, body = requestFor(pattern, sc, at, own)
				code, msg, ran = s.agentRequest(method, target, body)
				require.Equal(s.T(), http.StatusOK, code, "%s %s %s: %s", method, target, body, msg)
				require.True(s.T(), ran)
			})
		}
	}
}
