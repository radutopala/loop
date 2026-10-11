package pluginsdktest

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"gopkg.in/yaml.v3"

	"github.com/radutopala/loop/pluginsdk"
)

const (
	// apiSpec is the Plugin API, which Loop serves.
	apiSpec = "../openapi/plugin-api-v1.yaml"
	// contractSpec is what a plugin serves to Loop.
	contractSpec = "../openapi/plugin-contract-v1.yaml"
	// apiPrefix is where Loop serves the Plugin API v1.
	apiPrefix = "/api/plugin/v1"
)

// specOp is one operation of an OpenAPI spec.
type specOp struct {
	caps []string
	// success is the operation's 2xx status, or 0 when it documents none.
	success int
}

// spec is the operations of an OpenAPI spec by method and path, as in
// "POST /tools/{tool}".
type spec struct {
	ops map[string]specOp
	mux *http.ServeMux
}

func loadSpec(t *testing.T, file string) *spec {
	t.Helper()
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	var doc struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	require.NoError(t, yaml.Unmarshal(data, &doc), file)
	methods := []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}
	s := &spec{ops: map[string]specOp{}, mux: http.NewServeMux()}
	for p, item := range doc.Paths {
		for key, node := range item {
			if !slices.Contains(methods, key) {
				continue
			}
			var op struct {
				Caps      []string             `yaml:"x-loop-capabilities"`
				Responses map[string]yaml.Node `yaml:"responses"`
			}
			require.NoError(t, node.Decode(&op), "%s %s", key, p)
			var success int
			for code := range op.Responses {
				if n, err := strconv.Atoi(code); err == nil && n/100 == 2 {
					success = n
				}
			}
			route := strings.ToUpper(key) + " " + p
			s.ops[route] = specOp{caps: op.Caps, success: success}
			// A {path} parameter spans the rest of the path.
			s.mux.Handle(strings.ToUpper(key)+" "+strings.ReplaceAll(p, "{path}", "{path...}"), http.NotFoundHandler())
		}
	}
	return s
}

// route returns the operation a request with method and path is for, or
// "" when there is none.
func (s *spec) route(method, path string) string {
	_, pattern := s.mux.Handler(&http.Request{Method: method, URL: &url.URL{Path: path}})
	return strings.Replace(pattern, "{path...}", "{path}", 1)
}

func (s *spec) routes() []string {
	return slices.Sorted(maps.Keys(s.ops))
}

// sample turns a route into a request path, filling in its parameters.
func sample(route string) (method, path string) {
	method, path, _ = strings.Cut(route, " ")
	return method, strings.NewReplacer("{action}", "a", "{tool}", "t", "{run_id}", "r1", "{path}", "p/q", "{name}", "notes").Replace(path)
}

// muxPatterns returns the patterns file registers with Handle or HandleFunc.
func muxPatterns(t *testing.T, file string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	require.NoError(t, err)
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		lit, isLit := call.Args[0].(*ast.BasicLit)
		if ok && isLit && (sel.Sel.Name == "Handle" || sel.Sel.Name == "HandleFunc") {
			p, err := strconv.Unquote(lit.Value)
			require.NoError(t, err)
			out = append(out, p)
		}
		return true
	})
	slices.Sort(out)
	return out
}

// OpenAPISuite holds the SDK to the OpenAPI specs under pluginsdk/openapi.
type OpenAPISuite struct {
	suite.Suite
	api      *spec
	contract *spec
}

func TestOpenAPISuite(t *testing.T) {
	suite.Run(t, new(OpenAPISuite))
}

func (s *OpenAPISuite) SetupSuite() {
	s.api = loadSpec(s.T(), apiSpec)
	s.contract = loadSpec(s.T(), contractSpec)
}

// recorder answers every request 200 and keeps its route in the spec.
type recorder struct {
	spec   *spec
	routes []string
}

func (r *recorder) Do(req *http.Request) (*http.Response, error) {
	r.routes = append(r.routes, r.spec.route(req.Method, strings.TrimPrefix(req.URL.Path, apiPrefix)))
	body := `{"run_id":"r1","thread_id":"th1","functions":[]}`
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
}

// TestClient checks that the client's calls are the spec's operations, all
// of them.
func (s *OpenAPISuite) TestClient() {
	token := filepath.Join(s.T().TempDir(), "token")
	require.NoError(s.T(), os.WriteFile(token, []byte("tok"), 0o600))
	rec := &recorder{spec: s.api}
	c := &pluginsdk.Client{BaseURL: "http://loop", TokenFile: token, HTTP: rec}
	ctx := context.Background()

	_, err := c.Channel(ctx)
	require.NoError(s.T(), err)
	_, err = c.File(ctx, "a.go", 1, 2)
	require.NoError(s.T(), err)
	_, err = c.Tree(ctx, "*.go")
	require.NoError(s.T(), err)
	_, err = c.GitRecent(ctx, 7)
	require.NoError(s.T(), err)
	_, err = c.Hotspots(ctx, 5)
	require.NoError(s.T(), err)
	_, err = c.StartRun(ctx, pluginsdk.RunRequest{Agent: "gen", Prompt: "p"})
	require.NoError(s.T(), err)
	require.NoError(s.T(), c.Event(ctx, "done", nil))
	require.NoError(s.T(), c.Notify(ctx, "t", "b"))
	require.NoError(s.T(), c.WriteMemory(ctx, "notes", "x"))
	require.NoError(s.T(), c.DeleteMemory(ctx, "notes"))

	require.NotContains(s.T(), rec.routes, "", "a call isn't in %s", apiSpec)
	slices.Sort(rec.routes)
	require.Equal(s.T(), s.api.routes(), rec.routes)
	require.Equal(s.T(), len(rec.routes), reflect.TypeFor[pluginsdk.API]().NumMethod(),
		"a method of API isn't called above")
}

// TestServer checks that the server serves the contract's operations with
// their success status, behind the secret, and nothing else.
func (s *OpenAPISuite) TestServer() {
	const xStatus = http.StatusMultiStatus
	srv := &pluginsdk.Server{
		Secret:  "sec",
		Actions: map[string]pluginsdk.ActionFunc{"a": func(context.Context, pluginsdk.ActionRequest) error { return nil }},
		Tools: map[string]pluginsdk.ToolFunc{"t": func(context.Context, pluginsdk.ToolRequest) (pluginsdk.ToolResult, error) {
			return pluginsdk.ToolResult{}, nil
		}},
		RunDone: func(context.Context, string, pluginsdk.RunDoneRequest) error { return nil },
		X:       http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(xStatus) }),
	}
	defer srv.Close()
	h := srv.Handler()
	for _, route := range s.contract.routes() {
		method, path := sample(route)
		want := s.contract.ops[route].success
		if want == 0 {
			// The plugin's own answer, passed on as is.
			want = xStatus
		}
		for _, secret := range []string{"sec", "wrong"} {
			req := httptest.NewRequest(method, path, bytes.NewBufferString(`{}`))
			req.Header.Set("Authorization", "Bearer "+secret)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			code := want
			if secret != "sec" {
				code = http.StatusUnauthorized
			}
			require.Equal(s.T(), code, w.Code, "%s with secret %q", route, secret)
		}
	}
	srv.Wait()

	for _, p := range muxPatterns(s.T(), "../server.go") {
		method, path, ok := strings.Cut(p, " ")
		if !ok {
			// No method: every method of the subtree.
			method, path = http.MethodGet, p+"z"
		}
		require.NotEmpty(s.T(), s.contract.route(method, path), "the server's %q isn't in %s", p, contractSpec)
	}
}

// TestFakeLoop checks that the fake serves the spec's operations, all of
// them, with their capabilities and success status.
func (s *OpenAPISuite) TestFakeLoop() {
	var served []string
	for _, p := range muxPatterns(s.T(), "fakeloop.go") {
		method, path, _ := strings.Cut(p, " ")
		served = append(served, method+" "+strings.TrimPrefix(path, apiPrefix))
	}
	require.Equal(s.T(), s.api.routes(), served)

	all := []string{pluginsdk.CapFilesRead, pluginsdk.CapGitRead, pluginsdk.CapQualityRead,
		pluginsdk.CapAgentReadOnly, pluginsdk.CapAgentWrite, pluginsdk.CapUINotify, pluginsdk.CapMemoryWrite}
	tests := []struct {
		name string
		caps []string
	}{
		{name: "none approved"},
		{name: "all approved", caps: all},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			loop := NewFakeLoop(s.T(), Config{
				Manifest: &pluginsdk.Manifest{Capabilities: tt.caps, Agents: []pluginsdk.Agent{{ID: "gen", ReadOnly: true}}},
				Files:    map[string]string{"a.go": "package a\n"},
				Git:      &pluginsdk.GitRecent{},
				Hotspots: []pluginsdk.Hotspot{},
			})
			token, err := os.ReadFile(loop.TokenFile)
			require.NoError(s.T(), err)
			for _, route := range s.api.routes() {
				method, path := sample(route)
				req, err := http.NewRequest(method, loop.URL+apiPrefix+path+"?path=a.go",
					strings.NewReader(`{"agent":"gen","prompt":"p","type":"t","title":"t"}`))
				require.NoError(s.T(), err)
				req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
				resp, err := http.DefaultClient.Do(req)
				require.NoError(s.T(), err)
				resp.Body.Close()
				op := s.api.ops[route]
				want := op.success
				if len(op.caps) > 0 && tt.caps == nil {
					want = http.StatusForbidden
				}
				require.Equal(s.T(), want, resp.StatusCode, route)
			}
		})
	}
}
