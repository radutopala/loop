package api

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/mcpserver"
	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/review"
	"github.com/radutopala/loop/internal/uibridge"
)

// Spec: every request a `loop mcp` tool sends is one the daemon's API
// takes. For every tool, called with every input set, each request it sends
//   - matches a route the API registers, method included,
//   - has only the query parameters the route's handler reads,
//   - has a body only if the handler reads one, and, for a JSON object, only
//     keys the type the handler decodes it into has, at every depth, matched
//     the way encoding/json matches them (case-insensitively),
// and every string and integer input reaches the API in some request's
// path, query or body, unless the tool's entry says why it stays local.
//
// The tools come from mcpserver.New with every option on, listed and called
// over the MCP protocol; their inputs are generated from each tool's input
// schema, with a distinct value per input. A tool's entry in parityTools
// adds the input sets its actions need and the responses its later requests
// depend on.
//
// What a handler reads is read from the api package's source: the route's
// handler, and every function it passes the request or its body to, type
// checked with go/types; the target of each decodeJSON call, Decode from a
// decoder of the body, or Unmarshal of the body read whole is the body's
// type (another package's, by reflection, from externalBodies), and each
// string literal given to a Get, Has or FormValue call, or to a function
// passed the request, a query parameter.
//
// Trusted base: go/types (with the api package's imports stubbed, so a
// nested field whose type is from another package accepts any value), the
// mcpserver test transport, http.ServeMux's pattern matching, and the
// reading of decode calls as what a handler accepts: a handler that decodes
// into one type and reads another isn't caught.
//
// Responses aren't checked: a tool's reading of what the API answers is
// mcpserver's own tests'.

// parityTool is what a tool's calls need beyond the inputs generated from
// its schema.
type parityTool struct {
	// sets are input sets, each called in turn over the generated inputs;
	// none calls the tool once with the generated inputs alone.
	sets []map[string]any
	// local names inputs that don't reach the API, by dotted path, and why.
	local map[string]string
	// responses are bodies the API answers, by route pattern, where a
	// later request depends on them; "{}" otherwise.
	responses map[string]string
	// statuses are the statuses the API answers, by route pattern, where a
	// later request depends on one other than 200.
	statuses map[string]int
	// noAPI says why the tool calls no API at all.
	noAPI string
}

// shortcutSets are the input sets of the shortcut tools, one per action.
func shortcutSets() []map[string]any {
	return []map[string]any{
		{"action": "list"},
		{"action": "add", "scope": "project"},
		{"action": "update", "scope": "global"},
		{"action": "delete", "scope": "project"},
	}
}

// parityTools lists every tool the MCP server registers.
func parityTools() map[string]parityTool {
	selectsAction := "selects the action"
	return map[string]parityTool{
		"assess_review_thread":     {},
		"bash_shortcut":            {sets: shortcutSets(), local: map[string]string{"action": selectsAction}},
		"cancel_monitor":           {},
		"cancel_task":              {},
		"cancel_workflow_run":      {},
		"channel_status":           {responses: map[string]string{"GET /api/channels": `[{"channel_id":"v-channel_id-v","name":"n","branch":"b"}]`}},
		"chat_component":           {sets: []map[string]any{{"action": "templates"}, {"action": "show"}}, local: map[string]string{"action": selectsAction}},
		"create_channel":           {},
		"create_thread":            {},
		"create_worktree_thread":   {},
		"dedup_review_findings":    {},
		"delete_queued_message":    {responses: map[string]string{"GET /api/channels/{id}/queued": `{"messages":[{"msg_id":"v-msg_id-v"}]}`}},
		"delete_review_comment":    {},
		"delete_thread":            {},
		"delete_workflow":          {sets: []map[string]any{{"scope": "project"}}},
		"delete_workflow_run":      {},
		"edit_task":                {},
		"fork_thread":              {},
		"get_readme":               {noAPI: "returns the README built into the binary"},
		"get_review_comments":      {local: map[string]string{"path": "filters the comments the API answers"}},
		"get_workflow_run":         {},
		"index_memory":             {},
		"list_agents":              {},
		"list_monitors":            {},
		"list_queued_messages":     {},
		"list_sessions":            {},
		"list_tasks":               {},
		"list_workflow_runs":       {},
		"list_workflows":           {},
		"monitor":                  {},
		"move_task":                {},
		"permission_prompt":        {noAPI: "always allows, echoing its input"},
		"playground":               {sets: []map[string]any{{"action": "create"}, {"action": "update", "scope": "project"}, {"action": "delete"}}, local: map[string]string{"action": selectsAction}},
		"playground_file":          {sets: []map[string]any{{"action": "create"}, {"action": "read", "scope": "project"}, {"action": "delete"}, {"action": "list"}}, local: map[string]string{"action": selectsAction}},
		"playground_share":         {sets: []map[string]any{{"action": "share", "scope": "project"}, {"action": "unshare"}}, local: map[string]string{"action": selectsAction}},
		"plugin_t":                 {},
		"prompt_shortcut":          {sets: shortcutSets(), local: map[string]string{"action": selectsAction}},
		"propose_learnings":        {},
		"push_all_review_comments": {},
		"push_review_comment":      {},
		"quality_bugfactor":        {},
		"quality_c4":               {},
		"quality_clones":           {},
		"quality_complexity":       {},
		"quality_cycles":           {},
		"quality_diagnostics":      {local: map[string]string{"limit": "caps the files the API answers"}},
		"quality_evolution":        {},
		"quality_metrics":          {},
		"quality_rules":            {},
		"quality_scan":             {},
		"quality_snapshot":         {},
		"quality_whatif":           {},
		"queue_message":            {},
		"rename_thread":            {},
		"report_review_findings":   {},
		"resume_session":           {},
		"resume_workflow_run":      {},
		"retry_workflow_run":       {},
		"run_review":               {},
		"run_workflow":             {},
		"save_workflow":            {sets: []map[string]any{{"action": "add"}, {"action": "update", "scope": "project"}}},
		"schedule_task":            {},
		"search_channels":          {},
		"search_memory":            {},
		"send_agent_message":       {},
		"send_message":             {},
		"set_thread_description":   {},
		"set_ticket_url":           {},
		"show_task":                {},
		"toggle_task":              {},
		"ui_run":                   {},
		"ui_state":                 {},
		"ui_walkthrough":           {responses: map[string]string{"POST /api/channels/{id}/walkthroughs": `{"id":1}`}, statuses: map[string]int{"POST /api/channels/{id}/walkthroughs": http.StatusCreated}},
		"update_agent_status":      {},
		"update_review_comment":    {},
	}
}

// parityRequest is a request the API got.
type parityRequest struct {
	method  string
	path    string
	query   url.Values
	body    []byte
	pattern string // the route it matched; "" for none
}

func (r parityRequest) String() string {
	s := r.method + " " + r.path
	if len(r.query) > 0 {
		s += "?" + r.query.Encode()
	}
	if len(r.body) > 0 {
		s += " " + string(r.body)
	}
	return s
}

// parityCall is one call of a tool and the requests it sent.
type parityCall struct {
	tool string
	// values are the generated inputs that must reach the API, by dotted
	// path.
	values   map[string]string
	requests []parityRequest
	answer   string // the tool's result text
}

// parityAPI records the requests to the routes the API registers, and
// answers each with the response its tool's entry names.
type parityAPI struct {
	mux       *http.ServeMux
	mu        sync.Mutex
	requests  []parityRequest
	responses map[string]string
	statuses  map[string]int
}

func newParityAPI(patterns []string) *parityAPI {
	a := &parityAPI{mux: http.NewServeMux()}
	for _, p := range patterns {
		a.mux.HandleFunc(p, func(http.ResponseWriter, *http.Request) {})
	}
	return a
}

func (a *parityAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	_, pattern := a.mux.Handler(r)
	a.mu.Lock()
	a.requests = append(a.requests, parityRequest{method: r.Method, path: r.URL.Path, query: r.URL.Query(), body: body, pattern: pattern})
	resp, ok := a.responses[pattern]
	status := a.statuses[pattern]
	a.mu.Unlock()
	if pattern == "" {
		http.NotFound(w, r)
		return
	}
	if !ok {
		resp = "{}"
	}
	w.WriteHeader(cmp.Or(status, http.StatusOK))
	_, _ = w.Write([]byte(resp))
}

// take returns the requests since the last take and sets the responses and
// statuses of the next call.
func (a *parityAPI) take(responses map[string]string, statuses map[string]int) []parityRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	reqs := a.requests
	a.requests = nil
	a.responses = responses
	a.statuses = statuses
	return reqs
}

// paritySchema is the part of a JSON schema the inputs are generated from.
type paritySchema struct {
	Type       any                      `json:"type"`
	Enum       []any                    `json:"enum"`
	Properties map[string]*paritySchema `json:"properties"`
	Items      *paritySchema            `json:"items"`
	Additional *paritySchema            `json:"additionalProperties"`
	Maximum    *float64                 `json:"maximum"`
}

// UnmarshalJSON reads a boolean schema (true, any value) as an empty one.
func (s *paritySchema) UnmarshalJSON(data []byte) error {
	if string(data) == "true" || string(data) == "false" {
		return nil
	}
	type plain paritySchema
	return json.Unmarshal(data, (*plain)(s))
}

// kind is the schema's type, a nullable one's non-null type.
func (s *paritySchema) kind() string {
	switch t := s.Type.(type) {
	case string:
		return t
	case []any:
		for _, v := range t {
			if v != "null" {
				return v.(string)
			}
		}
	}
	return ""
}

// parityInputs generates a value for each input of a schema: an enum's
// first value, a distinct string or integer that values records by dotted
// path, true, one generated item, or a property of each.
type parityInputs struct {
	values map[string]string
	next   int
}

func (g *parityInputs) value(path string, s *paritySchema) any {
	if len(s.Enum) > 0 {
		return s.Enum[0]
	}
	switch s.kind() {
	case "object":
		obj := map[string]any{}
		if len(s.Properties) == 0 {
			item := s.Additional
			if item == nil || item.kind() == "" {
				item = &paritySchema{Type: "string"}
			}
			obj["key"] = g.value(path+".key", item)
		}
		for name, p := range s.Properties {
			obj[name] = g.value(joinPath(path, name), p)
		}
		return obj
	case "array":
		if s.Items == nil {
			return []any{}
		}
		return []any{g.value(path, s.Items)}
	case "boolean":
		return true
	case "integer", "number":
		g.next++
		n := 7000 + g.next
		if s.Maximum != nil && float64(n) > *s.Maximum {
			n = int(*s.Maximum)
		}
		g.values[path] = strconv.Itoa(n)
		return n
	default:
		v := "v-" + path + "-v"
		g.values[path] = v
		return v
	}
}

func joinPath(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// mcpParityServer is the MCP server with every option on.
func mcpParityServer(apiURL string, client mcpserver.HTTPClient) *mcpserver.Server {
	tools := []plugin.Tool{{Name: "t", InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)}}
	return mcpserver.New("ch1", apiURL, "u1", client, nil,
		mcpserver.WithMemoryAPI("/dir"), mcpserver.WithWorkflowAPI(), mcpserver.WithLearnTools(),
		mcpserver.WithAgentTools("a1"), mcpserver.WithPluginTools("plug", tools))
}

// mcpSession connects a client to srv over in-memory transports.
func mcpSession(t require.TestingT, srv *mcpserver.Server) *mcp.ClientSession {
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	go func() { _ = srv.Run(ctx, st) }()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "parity", Version: "1"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	return session
}

// mcpToolNames are the tools the MCP server with every option on registers.
func mcpToolNames(t require.TestingT) []string {
	session := mcpSession(t, mcpParityServer("http://127.0.0.1:0", http.DefaultClient))
	defer session.Close()
	res, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

// mcpParityCalls calls every tool with each of its input sets against a
// recording API, through client, and returns the calls; the agent's
// registration and unregistration come first and last.
func mcpParityCalls(t require.TestingT, client func(http.Client) mcpserver.HTTPClient) []parityCall {
	api := newParityAPI(registeredRoutes(t))
	ts := httptest.NewServer(api)
	defer ts.Close()
	srv := mcpParityServer(ts.URL, client(*ts.Client()))
	session := mcpSession(t, srv)
	defer session.Close()
	ctx := context.Background()
	res, err := session.ListTools(ctx, nil)
	require.NoError(t, err)

	srv.RegisterAgent()
	calls := []parityCall{{tool: "(agent registration)", requests: api.take(nil, nil)}}
	fixtures := parityTools()
	for _, tool := range res.Tools {
		fx := fixtures[tool.Name]
		raw, err := json.Marshal(tool.InputSchema)
		require.NoError(t, err)
		var schema paritySchema
		require.NoError(t, json.Unmarshal(raw, &schema))
		sets := fx.sets
		if len(sets) == 0 {
			sets = []map[string]any{{}}
		}
		for _, set := range sets {
			gen := &parityInputs{values: map[string]string{}}
			args := map[string]any{}
			for name, p := range schema.Properties {
				args[name] = gen.value(name, p)
			}
			for name, v := range set {
				args[name] = v
				delete(gen.values, name)
				if _, local := fx.local[name]; !local {
					gen.values[name] = fmt.Sprint(v)
				}
			}
			api.take(fx.responses, fx.statuses)
			res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool.Name, Arguments: args})
			require.NoError(t, err, tool.Name)
			call := parityCall{tool: tool.Name, values: gen.values, requests: api.take(nil, nil)}
			if text, ok := res.Content[0].(*mcp.TextContent); ok {
				call.answer = text.Text
			}
			calls = append(calls, call)
		}
	}
	srv.UnregisterAgent()
	return append(calls, parityCall{tool: "(agent unregistration)", requests: api.take(nil, nil)})
}

// handlerReads is what a route's handler reads of a request.
type handlerReads struct {
	query    map[string]bool
	body     bool       // reads the body at all
	bodyType []bodyType // the types it decodes the body into
}

// bodyType is a type a handler decodes a body into: the api package's,
// read with go/types, or another package's, by reflection.
type bodyType struct {
	t    types.Type
	r    reflect.Type
	name string // as the handler names it
}

// externalBodies are the types of other packages handlers decode a body
// into, by the name the handler uses.
func externalBodies() map[string]reflect.Type {
	return map[string]reflect.Type{
		"review.ThreadAssessment":  reflect.TypeFor[review.ThreadAssessment](),
		"container.ReclaimOptions": reflect.TypeFor[container.ReclaimOptions](),
		"uibridge.Message":         reflect.TypeFor[uibridge.Message](),
		"json.RawMessage":          reflect.TypeFor[json.RawMessage](),
	}
}

// stubImporter gives every import an empty package, so the api package
// type checks in a moment; what it uses of them stays untyped.
type stubImporter struct{}

func (stubImporter) Import(path string) (*types.Package, error) {
	pkg := types.NewPackage(path, path[strings.LastIndex(path, "/")+1:])
	pkg.MarkComplete()
	return pkg, nil
}

// apiSource is the api package's source, type checked.
type apiSource struct {
	info  *types.Info
	decls map[types.Object]*ast.FuncDecl
	files []*ast.File
	// declared are the type expressions variables are declared with.
	declared map[types.Object]ast.Expr
}

func loadAPISource(t require.TestingT) *apiSource {
	names, err := filepath.Glob("*.go")
	require.NoError(t, err)
	fset := token.NewFileSet()
	src := &apiSource{decls: map[types.Object]*ast.FuncDecl{}, declared: map[types.Object]ast.Expr{}}
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, err)
		src.files = append(src.files, f)
	}
	src.info = &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{}, Defs: map[*ast.Ident]types.Object{}}
	conf := types.Config{Importer: stubImporter{}, Error: func(error) {}}
	_, _ = conf.Check("github.com/radutopala/loop/internal/api", fset, src.files, src.info)
	for _, f := range src.files {
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
				src.decls[src.info.Defs[fn.Name]] = fn
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if spec, ok := n.(*ast.ValueSpec); ok && spec.Type != nil {
				for _, name := range spec.Names {
					src.declared[src.info.Defs[name]] = spec.Type
				}
			}
			return true
		})
	}
	return src
}

// callee is the function decl a call or a reference names, if it's the api
// package's.
func (src *apiSource) callee(e ast.Expr) *ast.FuncDecl {
	switch e := e.(type) {
	case *ast.Ident:
		return src.decls[src.info.Uses[e]]
	case *ast.SelectorExpr:
		return src.decls[src.info.Uses[e.Sel]]
	case *ast.IndexExpr: // a generic function's instance
		return src.callee(e.X)
	}
	return nil
}

// routeHandlers maps every route pattern registered with a literal pattern
// to its handler's decl.
func (src *apiSource) routeHandlers() map[string]*ast.FuncDecl {
	out := map[string]*ast.FuncDecl{}
	for _, f := range src.files {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			lit, isLit := call.Args[0].(*ast.BasicLit)
			if !ok || sel.Sel.Name != "HandleFunc" || !isLit {
				return true
			}
			pattern, _ := strconv.Unquote(lit.Value)
			out[pattern] = src.callee(call.Args[1])
			return true
		})
	}
	return out
}

// requestParams are the names of fn's *http.Request parameters.
func requestParams(fn *ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	for _, field := range fn.Type.Params.List {
		if star, ok := field.Type.(*ast.StarExpr); ok {
			if sel, ok := star.X.(*ast.SelectorExpr); ok && sel.Sel.Name == "Request" {
				for _, name := range field.Names {
					out[name.Name] = true
				}
			}
		}
	}
	return out
}

// paramName is the name of fn's i-th parameter.
func paramName(fn *ast.FuncDecl, i int) string {
	for _, field := range fn.Type.Params.List {
		if i < len(field.Names) {
			return field.Names[i].Name
		}
		i -= max(len(field.Names), 1)
	}
	return ""
}

// reads is what fn reads of a request it holds as one of reqs, its body as
// one of bodies, and what each function it passes them to reads.
func (src *apiSource) reads(fn *ast.FuncDecl) handlerReads {
	out := handlerReads{query: map[string]bool{}}
	src.collect(fn, requestParams(fn), map[string]bool{}, &out, map[*ast.FuncDecl]bool{})
	return out
}

func (src *apiSource) collect(fn *ast.FuncDecl, reqs, bodies map[string]bool, out *handlerReads, seen map[*ast.FuncDecl]bool) {
	seen[fn] = true
	isReq := func(e ast.Expr) bool {
		id, ok := e.(*ast.Ident)
		return ok && reqs[id.Name]
	}
	isBody := func(e ast.Expr) bool {
		if id, ok := e.(*ast.Ident); ok {
			return bodies[id.Name]
		}
		sel, ok := e.(*ast.SelectorExpr)
		return ok && sel.Sel.Name == "Body" && isReq(sel.X)
	}
	// fromBody reports whether e is the body, or a call wrapping it, like
	// json.NewDecoder(io.LimitReader(r.Body, n)).
	var fromBody func(e ast.Expr) bool
	fromBody = func(e ast.Expr) bool {
		call, ok := e.(*ast.CallExpr)
		if !ok {
			return isBody(e)
		}
		return slices.ContainsFunc(call.Args, fromBody)
	}
	read := map[string]bool{} // variables holding the body read whole
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			if call, ok := n.Rhs[0].(*ast.CallExpr); ok && len(n.Rhs) == 1 && len(call.Args) == 1 && isBody(call.Args[0]) {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "ReadAll" {
					read[types.ExprString(n.Lhs[0])] = true
				}
			}
		case *ast.SelectorExpr:
			if isBody(n) {
				out.body = true
			}
		case *ast.CallExpr:
			name := ""
			switch f := n.Fun.(type) {
			case *ast.Ident:
				name = f.Name
			case *ast.SelectorExpr:
				name = f.Sel.Name
			case *ast.IndexExpr:
				if id, ok := f.X.(*ast.Ident); ok {
					name = id.Name
				}
			}
			switch {
			case name == "decodeJSON" && len(n.Args) == 3:
				out.body = true
				out.bodyType = append(out.bodyType, src.decodeTarget(n.Args[2]))
				return true
			case name == "Decode" && len(n.Args) == 1 && fromBody(n.Fun.(*ast.SelectorExpr).X):
				out.bodyType = append(out.bodyType, src.decodeTarget(n.Args[0]))
			case name == "Unmarshal" && len(n.Args) == 2 && read[types.ExprString(n.Args[0])]:
				out.bodyType = append(out.bodyType, src.decodeTarget(n.Args[1]))
			case (name == "Get" || name == "Has" || name == "FormValue") && len(n.Args) == 1:
				addLiteral(out.query, n.Args[0])
			}
			callee := src.callee(n.Fun)
			passes := false
			for i, arg := range n.Args {
				if !isReq(arg) && !isBody(arg) {
					continue
				}
				passes = true
				if isBody(arg) {
					out.body = true
				}
				if callee == nil || seen[callee] {
					continue
				}
				p := paramName(callee, i)
				if isReq(arg) {
					src.collect(callee, map[string]bool{p: true}, map[string]bool{}, out, seen)
				} else {
					src.collect(callee, map[string]bool{}, map[string]bool{p: true}, out, seen)
				}
			}
			if passes {
				for _, arg := range n.Args {
					addLiteral(out.query, arg)
				}
			}
		}
		return true
	})
}

func addLiteral(set map[string]bool, e ast.Expr) {
	if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		s, _ := strconv.Unquote(lit.Value)
		set[s] = true
	}
}

// decodeTarget is the type a pointer expression points to.
func (src *apiSource) decodeTarget(e ast.Expr) bodyType {
	u, ok := e.(*ast.UnaryExpr)
	if !ok || u.Op != token.AND {
		if p, ok := src.info.TypeOf(e).(*types.Pointer); ok {
			return bodyType{t: p.Elem(), name: p.Elem().String()}
		}
		return bodyType{name: types.ExprString(e)}
	}
	if t := src.info.TypeOf(u.X); usable(t) {
		return bodyType{t: t, name: t.String()}
	}
	id, _ := u.X.(*ast.Ident)
	if id == nil {
		return bodyType{name: types.ExprString(u.X)}
	}
	name := types.ExprString(src.declared[src.info.Uses[id]])
	return bodyType{r: externalBodies()[name], name: name}
}

// usable reports whether t is a type go/types worked out.
func usable(t types.Type) bool {
	return t != nil && t != types.Typ[types.Invalid]
}

// strayKeysOf returns the keys of v a value of type t has no field for, by
// dotted path.
func strayKeysOf(v any, t types.Type, path string) []string {
	if !usable(t) {
		return nil
	}
	switch u := t.Underlying().(type) {
	case *types.Pointer:
		return strayKeysOf(v, u.Elem(), path)
	case *types.Slice:
		var out []string
		items, _ := v.([]any)
		for _, item := range items {
			out = append(out, strayKeysOf(item, u.Elem(), path+"[]")...)
		}
		return out
	case *types.Struct:
		obj, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		fields := map[string]types.Type{}
		structFields(u, fields)
		var out []string
		for key, val := range obj {
			ft, ok := fieldByKey(fields, key)
			if !ok {
				out = append(out, joinPath(path, key))
				continue
			}
			out = append(out, strayKeysOf(val, ft, joinPath(path, key))...)
		}
		return out
	}
	return nil
}

// reflectStrayKeys is strayKeysOf for a type read by reflection.
func reflectStrayKeys(v any, t reflect.Type, path string) []string {
	switch t.Kind() {
	case reflect.Pointer:
		return reflectStrayKeys(v, t.Elem(), path)
	case reflect.Slice:
		var out []string
		items, _ := v.([]any)
		for _, item := range items {
			out = append(out, reflectStrayKeys(item, t.Elem(), path+"[]")...)
		}
		return out
	case reflect.Struct:
		obj, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		fields := map[string]reflect.Type{}
		for _, f := range reflect.VisibleFields(t) {
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if f.IsExported() && name != "-" && (!f.Anonymous || name != "") {
				fields[jsonName(f)] = f.Type
			}
		}
		var out []string
		for key, val := range obj {
			ft, ok := fields[key]
			for name, t := range fields {
				if !ok && strings.EqualFold(name, key) {
					ft, ok = t, true
				}
			}
			if !ok {
				out = append(out, joinPath(path, key))
				continue
			}
			out = append(out, reflectStrayKeys(val, ft, joinPath(path, key))...)
		}
		return out
	}
	return nil
}

// structFields adds st's fields to fields by JSON key, an embedded struct's
// own; an embedded field of a type go/types didn't work out takes any key.
func structFields(st *types.Struct, fields map[string]types.Type) {
	for i := range st.NumFields() {
		f := st.Field(i)
		name, _, _ := strings.Cut(reflect.StructTag(st.Tag(i)).Get("json"), ",")
		switch {
		case name == "-":
		case f.Anonymous() && name == "":
			ft := f.Type()
			if p, ok := ft.(*types.Pointer); ok {
				ft = p.Elem()
			}
			if inner, ok := ft.Underlying().(*types.Struct); ok && usable(ft) {
				structFields(inner, fields)
			} else {
				fields["*"] = nil
			}
		case f.Exported():
			if name == "" {
				name = f.Name()
			}
			fields[name] = f.Type()
		}
	}
}

// fieldByKey finds a key's field the way encoding/json does: exactly, else
// case-insensitively.
func fieldByKey(fields map[string]types.Type, key string) (types.Type, bool) {
	if t, ok := fields[key]; ok {
		return t, true
	}
	for name, t := range fields {
		if strings.EqualFold(name, key) {
			return t, true
		}
	}
	_, any := fields["*"]
	return nil, any
}

// requestViolations returns how req isn't one its route's handler takes.
func requestViolations(req parityRequest, handlers map[string]handlerReads) []string {
	if req.pattern == "" {
		return []string{"no route takes " + req.method + " " + req.path}
	}
	h, ok := handlers[req.pattern]
	if !ok {
		return []string{req.pattern + " has no handler in the api package's source"}
	}
	var out []string
	for key := range req.query {
		if !h.query[key] {
			out = append(out, fmt.Sprintf("query parameter %s isn't read by %s", key, req.pattern))
		}
	}
	if len(req.body) == 0 {
		return out
	}
	if !h.body {
		return append(out, req.pattern+" doesn't read a body")
	}
	var obj map[string]any
	if json.Unmarshal(req.body, &obj) != nil {
		return out // a raw body, which the handler reads
	}
	if len(h.bodyType) == 0 {
		return append(out, req.pattern+" decodes no JSON body")
	}
	var strays []string
	for i, bt := range h.bodyType {
		var s []string
		switch {
		case bt.r != nil:
			s = reflectStrayKeys(obj, bt.r, "")
		case usable(bt.t):
			s = strayKeysOf(obj, bt.t, "")
		default:
			return append(out, fmt.Sprintf("%s decodes into %s, which externalBodies doesn't list", req.pattern, bt.name))
		}
		if i == 0 || len(s) < len(strays) {
			strays = s
		}
	}
	for _, key := range strays {
		out = append(out, fmt.Sprintf("body key %s isn't read by %s", key, req.pattern))
	}
	return out
}

// intRe matches a whole number.
var intRe = regexp.MustCompile(`\d+`)

// carries reports whether one of reqs carries value in its path, query or
// body.
func carries(reqs []parityRequest, value string) bool {
	for _, req := range reqs {
		text := req.path + " " + req.query.Encode() + " " + string(req.body)
		if unescaped, err := url.QueryUnescape(req.query.Encode()); err == nil {
			text += " " + unescaped
		}
		if _, err := strconv.Atoi(value); err != nil {
			if strings.Contains(text, value) {
				return true
			}
			continue
		}
		if slices.Contains(intRe.FindAllString(text, -1), value) {
			return true
		}
	}
	return false
}

// parityViolations returns every way the calls aren't ones the API takes or
// drop an input.
func parityViolations(calls []parityCall, handlers map[string]handlerReads, tools map[string]parityTool) []string {
	var out []string
	sent := map[string][]parityRequest{}
	answers := map[string][]string{}
	values := map[string]map[string]bool{}
	for _, c := range calls {
		for _, req := range c.requests {
			for _, v := range requestViolations(req, handlers) {
				out = append(out, fmt.Sprintf("%s: %s: %s", c.tool, req, v))
			}
		}
		sent[c.tool] = append(sent[c.tool], c.requests...)
		answers[c.tool] = append(answers[c.tool], c.answer)
		if values[c.tool] == nil {
			values[c.tool] = map[string]bool{}
		}
		for path, v := range c.values {
			if carries(c.requests, v) {
				values[c.tool][path] = true
			} else if _, ok := values[c.tool][path]; !ok {
				values[c.tool][path] = false
			}
		}
	}
	for tool, paths := range values {
		fx := tools[tool]
		if fx.noAPI != "" {
			if len(sent[tool]) > 0 {
				out = append(out, fmt.Sprintf("%s: calls the API, but its entry says %q", tool, fx.noAPI))
			}
			continue
		}
		if len(sent[tool]) == 0 {
			out = append(out, fmt.Sprintf("%s: sends no request, answering %q", tool, answers[tool]))
			continue
		}
		for path, reached := range paths {
			if _, local := fx.local[path]; !reached && !local {
				out = append(out, fmt.Sprintf("%s: input %s doesn't reach the API", tool, path))
			}
		}
	}
	slices.Sort(out)
	return out
}

type MCPParitySpecSuite struct {
	suite.Suite
	handlers map[string]handlerReads
}

func TestMCPParitySpecSuite(t *testing.T) {
	suite.Run(t, new(MCPParitySpecSuite))
}

func (s *MCPParitySpecSuite) SetupSuite() {
	src := loadAPISource(s.T())
	s.handlers = map[string]handlerReads{}
	for pattern, fn := range src.routeHandlers() {
		if fn != nil {
			s.handlers[pattern] = src.reads(fn)
		}
	}
}

func realHTTPClient(c http.Client) mcpserver.HTTPClient { return &c }

// Every tool the MCP server registers has an entry, and every entry is a
// tool's, naming its own inputs and the API's routes.
func (s *MCPParitySpecSuite) TestEveryToolHasAnEntry() {
	tools := parityTools()
	require.ElementsMatch(s.T(), mcpToolNames(s.T()), slices.Collect(maps.Keys(tools)))
	routes := registeredRoutes(s.T())
	for name, fx := range tools {
		for pattern := range fx.responses {
			require.Contains(s.T(), routes, pattern, "%s: responses name a route the API doesn't register", name)
		}
		require.False(s.T(), fx.noAPI != "" && (len(fx.sets) > 0 || len(fx.local) > 0 || len(fx.responses) > 0), "%s: a tool that calls no API needs no inputs", name)
	}
}

func (s *MCPParitySpecSuite) TestToolRequestsAreOnesTheAPITakes() {
	calls := mcpParityCalls(s.T(), realHTTPClient)
	require.Empty(s.T(), parityViolations(calls, s.handlers, parityTools()))
}

// brokenClient rewrites each request the MCP server sends before it goes
// out.
type brokenClient struct {
	http.Client
	rewrite func(r *http.Request, body []byte) []byte
}

func (c *brokenClient) Do(r *http.Request) (*http.Response, error) {
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(r.Body)
	}
	body = c.rewrite(r, body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	return c.Client.Do(r)
}

func (s *MCPParitySpecSuite) TestParitySpecCanFail() {
	tests := []struct {
		name    string
		rewrite func(r *http.Request, body []byte) []byte
	}{
		{"a body key renamed", func(_ *http.Request, body []byte) []byte {
			return bytes.Replace(body, []byte(`"content"`), []byte(`"text"`), 1)
		}},
		{"a route moved", func(r *http.Request, body []byte) []byte {
			r.URL.Path = strings.Replace(r.URL.Path, "/api/tasks", "/api/task", 1)
			return body
		}},
		{"a method changed", func(r *http.Request, body []byte) []byte {
			if r.Method == http.MethodPatch {
				r.Method = http.MethodPut
			}
			return body
		}},
		{"an extra query parameter", func(r *http.Request, body []byte) []byte {
			q := r.URL.Query()
			q.Set("verbose", "1")
			r.URL.RawQuery = q.Encode()
			return body
		}},
		{"an input dropped", func(_ *http.Request, body []byte) []byte {
			return regexp.MustCompile(`v-prompt-v`).ReplaceAll(body, []byte("x"))
		}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			calls := mcpParityCalls(s.T(), func(c http.Client) mcpserver.HTTPClient {
				return &brokenClient{Client: c, rewrite: tc.rewrite}
			})
			require.NotEmpty(s.T(), parityViolations(calls, s.handlers, parityTools()))
		})
	}

	s.Run("a handler that reads fewer keys", func() {
		handlers := maps.Clone(s.handlers)
		h := handlers["POST /api/tasks"]
		h.bodyType = []bodyType{{r: reflect.TypeFor[struct{ Prompt string }](), name: "a smaller body"}}
		handlers["POST /api/tasks"] = h
		require.NotEmpty(s.T(), parityViolations(mcpParityCalls(s.T(), realHTTPClient), handlers, parityTools()))
	})
}
