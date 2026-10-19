package pluginsdktest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	// eventTypes is the Event schema's type enum.
	eventTypes []string
	// answers are the schemas of the operations' 2xx JSON answers.
	answers map[string]map[string]any
	// schemas are components.schemas, for $ref.
	schemas map[string]any
}

func loadSpec(t *testing.T, file string) *spec {
	t.Helper()
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	var doc struct {
		Paths      map[string]map[string]yaml.Node `yaml:"paths"`
		Components struct {
			Schemas struct {
				Event struct {
					Properties struct {
						Type struct {
							Enum []string `yaml:"enum"`
						} `yaml:"type"`
					} `yaml:"properties"`
				} `yaml:"Event"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	require.NoError(t, yaml.Unmarshal(data, &doc), file)
	var raw struct {
		Paths      map[string]map[string]any `yaml:"paths"`
		Components struct {
			Schemas map[string]any `yaml:"schemas"`
		} `yaml:"components"`
	}
	require.NoError(t, yaml.Unmarshal(data, &raw), file)
	methods := []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}
	s := &spec{ops: map[string]specOp{}, mux: http.NewServeMux(), eventTypes: doc.Components.Schemas.Event.Properties.Type.Enum,
		answers: map[string]map[string]any{}, schemas: raw.Components.Schemas}
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
			if success != 0 {
				responses, _ := raw.Paths[p][key].(map[string]any)["responses"].(map[string]any)
				content, _ := responses[strconv.Itoa(success)].(map[string]any)["content"].(map[string]any)
				if body, ok := content["application/json"].(map[string]any); ok {
					s.answers[route] = body["schema"].(map[string]any)
				}
			}
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

// check returns why the JSON answer body of route breaks the spec's
// schema, or nil when it doesn't.
func (s *spec) check(route string, body []byte) []string {
	schema, ok := s.answers[route]
	if !ok {
		return nil
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return []string{"the answer isn't JSON: " + err.Error()}
	}
	return s.checkValue(schema, v, "answer")
}

// checkValue returns why v breaks schema; at names v.
func (s *spec) checkValue(schema map[string]any, v any, at string) []string {
	if ref, ok := schema["$ref"].(string); ok {
		schema = s.schemas[strings.TrimPrefix(ref, "#/components/schemas/")].(map[string]any)
	}
	if enum, ok := schema["enum"].([]any); ok && !slices.Contains(enum, v) {
		return []string{fmt.Sprintf("%s is %v, not one of %v", at, v, enum)}
	}
	if c, ok := schema["const"]; ok && c != v {
		return []string{fmt.Sprintf("%s is %v, not %v", at, v, c)}
	}
	switch schema["type"] {
	case "object":
		obj, ok := v.(map[string]any)
		if !ok {
			return []string{at + " isn't an object"}
		}
		return s.checkObject(schema, obj, at)
	case "array":
		arr, ok := v.([]any)
		if !ok {
			return []string{at + " isn't an array"}
		}
		var out []string
		for i, item := range arr {
			out = append(out, s.checkValue(schema["items"].(map[string]any), item, fmt.Sprintf("%s[%d]", at, i))...)
		}
		return out
	case "integer":
		if n, ok := v.(float64); !ok || n != float64(int64(n)) {
			return []string{at + " isn't an integer"}
		}
	case "number":
		if _, ok := v.(float64); !ok {
			return []string{at + " isn't a number"}
		}
	case "string":
		if _, ok := v.(string); !ok {
			return []string{at + " isn't a string"}
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return []string{at + " isn't a boolean"}
		}
	}
	return nil
}

// checkObject returns why obj breaks the object schema: a required
// property missing, one the schema doesn't declare, or a bad value.
func (s *spec) checkObject(schema, obj map[string]any, at string) []string {
	var out []string
	required, _ := schema["required"].([]any)
	for _, name := range required {
		if _, ok := obj[name.(string)]; !ok {
			out = append(out, fmt.Sprintf("%s has no %s", at, name))
		}
	}
	props, _ := schema["properties"].(map[string]any)
	for name, v := range obj {
		prop, ok := props[name].(map[string]any)
		switch extra := schema["additionalProperties"].(type) {
		case map[string]any:
			if !ok {
				prop, ok = extra, true
			}
		case bool:
			if !ok && extra {
				continue
			}
		}
		if !ok {
			out = append(out, fmt.Sprintf("%s has %s, which the schema doesn't declare", at, name))
			continue
		}
		out = append(out, s.checkValue(prop, v, at+"."+name)...)
	}
	return out
}

// sample turns a route into a request path, filling in its parameters.
func sample(route string) (method, path string) {
	method, path, _ = strings.Cut(route, " ")
	return method, strings.NewReplacer("{action}", "a", "{tool}", "t", "{run_id}", "r1", "{path}", "p/q", "{name}", "notes", "{id}", "s1", "{switch}", "s").Replace(path)
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
	_, err = c.HotspotsPage(ctx, 5, 10)
	require.NoError(s.T(), err)
	_, err = c.StartRun(ctx, pluginsdk.RunRequest{Agent: "gen", Prompt: "p"})
	require.NoError(s.T(), err)
	require.NoError(s.T(), c.Event(ctx, "done", nil))
	require.NoError(s.T(), c.Notify(ctx, "t", "b"))
	require.NoError(s.T(), c.WriteMemory(ctx, "notes", "x"))
	require.NoError(s.T(), c.DeleteMemory(ctx, "notes"))
	_, err = c.RunUI(ctx, pluginsdk.UICommand{Steps: []pluginsdk.UIStep{{"op": "select_channel", "channel_id": "ch1"}}})
	require.NoError(s.T(), err)
	require.NoError(s.T(), c.SetBadge(ctx, "3", ""))
	require.NoError(s.T(), c.ClearBadge(ctx))
	_, err = c.PostCard(ctx, "score", "", nil)
	require.NoError(s.T(), err)
	_, err = c.GitHead(ctx)
	require.NoError(s.T(), err)
	_, err = c.RunState(ctx, "r1")
	require.NoError(s.T(), err)
	require.NoError(s.T(), c.CancelRun(ctx, "r1"))
	_, err = c.OpenFile(ctx, "a.go", 3)
	require.NoError(s.T(), err)
	require.NoError(s.T(), c.SetSwitch(ctx, "mode", "", pluginsdk.SwitchState{On: true}))
	require.NoError(s.T(), c.ResetSwitch(ctx, "mode", ""))
	_, err = c.LiveControl(ctx, "", map[string]any{"subtype": "set_mode"})
	require.NoError(s.T(), err)
	require.NoError(s.T(), c.HoldLive(ctx, ""))
	require.NoError(s.T(), c.ReleaseLive(ctx, ""))

	require.NotContains(s.T(), rec.routes, "", "a call isn't in %s", apiSpec)
	require.Len(s.T(), rec.routes, reflect.TypeFor[pluginsdk.API]().NumMethod(),
		"a method of API isn't called above")
	slices.Sort(rec.routes)
	require.Equal(s.T(), s.api.routes(), slices.Compact(rec.routes))
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
		Switches: map[string]pluginsdk.SwitchFunc{"s": func(context.Context, pluginsdk.SwitchRequest) (pluginsdk.SwitchState, error) {
			return pluginsdk.SwitchState{On: true}, nil
		}},
		X: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(xStatus) }),
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
			if code == want {
				require.Empty(s.T(), s.contract.check(route, w.Body.Bytes()), route)
			}
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

// TestEventTypes checks that the SDK names the events the contract sends.
func (s *OpenAPISuite) TestEventTypes() {
	require.ElementsMatch(s.T(), []string{pluginsdk.EventMessageCreated, pluginsdk.EventAgentStatus, pluginsdk.EventQualityScanned,
		pluginsdk.EventLiveStatus, pluginsdk.EventLiveSystem, pluginsdk.EventSettingsChanged},
		s.contract.eventTypes)
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
		pluginsdk.CapAgentReadOnly, pluginsdk.CapAgentWrite, pluginsdk.CapUINotify, pluginsdk.CapMemoryWrite, pluginsdk.CapUIRun, pluginsdk.CapUIBadge, pluginsdk.CapUICard, pluginsdk.CapUIOpenFile,
		pluginsdk.CapUISwitch, pluginsdk.CapLiveControl}
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
				Manifest: &pluginsdk.Manifest{Capabilities: tt.caps, Agents: []pluginsdk.Agent{{ID: "gen", ReadOnly: true}},
					Live: &pluginsdk.Live{Control: []string{"set_mode"}},
					UI: &pluginsdk.UI{Cards: []pluginsdk.Card{{ID: "c", Entry: "ui/c.html"}},
						ComposerSwitches: []pluginsdk.ComposerSwitch{{ID: "s1", Label: "S", Icon: "ui/s.svg"}}}},
				Channel:  pluginsdk.Channel{Settings: map[string]any{"n": 1}},
				Files:    map[string]string{"a.go": "package a\n"},
				Git:      &pluginsdk.GitRecent{Commits: []pluginsdk.Commit{{SHA: "abc"}}, Files: []pluginsdk.FileChurn{{Path: "a.go", Commits: 1}}},
				GitHead:  &pluginsdk.GitHead{SHA: "abc", Branch: "main"},
				Hotspots: []pluginsdk.Hotspot{{Name: "f", Score: 0.5}},
				Quality:  pluginsdk.Hotspots{Score: 0.9, Histogram: map[string]map[string]int{"cyclomatic": {"ok": 1}}},
			})
			token, err := os.ReadFile(loop.TokenFile)
			require.NoError(s.T(), err)
			client := loop.Client()
			for _, route := range s.api.routes() {
				method, path := sample(route)
				if strings.Contains(route, "{run_id}") && tt.caps != nil {
					// A run's routes answer for a run the plugin has queued.
					run, err := client.StartRun(context.Background(), pluginsdk.RunRequest{Agent: "gen", Prompt: "p"})
					require.NoError(s.T(), err)
					path = strings.Replace(path, "r1", run.RunID, 1)
				}
				req, err := http.NewRequest(method, loop.URL+apiPrefix+path+"?path=a.go",
					strings.NewReader(`{"card":"c","agent":"gen","prompt":"p","type":"t","title":"t","text":"t","path":"a.go","steps":[{"op":"select_channel","channel_id":"ch1"}],"on":true,"request":{"subtype":"set_mode"}}`))
				require.NoError(s.T(), err)
				req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
				resp, err := http.DefaultClient.Do(req)
				require.NoError(s.T(), err)
				body, err := io.ReadAll(resp.Body)
				require.NoError(s.T(), err)
				resp.Body.Close()
				op := s.api.ops[route]
				want := op.success
				if len(op.caps) > 0 && tt.caps == nil {
					want = http.StatusForbidden
				}
				require.Equal(s.T(), want, resp.StatusCode, route)
				if want == op.success {
					require.Empty(s.T(), s.api.check(route, body), route)
				}
			}
		})
	}
}
