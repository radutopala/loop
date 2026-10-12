package api

import (
	"cmp"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
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

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/radutopala/loop/internal/apiauth"
	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/plugin/install"
	"github.com/radutopala/loop/internal/testutil"
	"github.com/radutopala/loop/internal/uibridge"
)

// pluginAPISpec is the Plugin API's OpenAPI spec, published with the SDK.
const pluginAPISpec = "../../pluginsdk/openapi/plugin-api-v1.yaml"

// pluginAPIPrefix is where Loop serves the Plugin API v1.
const pluginAPIPrefix = "/api/plugin/v1"

// pluginAPIPatterns returns the Plugin API route patterns the package's
// source registers, without the prefix, as in "GET /channel".
func pluginAPIPatterns(t require.TestingT) []string {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	route := regexp.MustCompile(`^([A-Z]+) ` + regexp.QuoteMeta(pluginAPIPrefix) + `(/.*)$`)
	var out []string
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		require.NoError(t, err)
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if m := route.FindStringSubmatch(s); err == nil && m != nil {
				out = append(out, m[1]+" "+m[2])
			}
			return true
		})
	}
	slices.Sort(out)
	return out
}

// TestPluginAPISpecRoutes holds the Plugin API's OpenAPI spec to the routes
// Loop serves: the same methods and paths, both ways.
func (s *ServerSuite) TestPluginAPISpecRoutes() {
	spec := testutil.LoadOpenAPI(s.T(), pluginAPISpec)
	require.Equal(s.T(), spec.Routes(), pluginAPIPatterns(s.T()),
		"the Plugin API routes and %s differ", filepath.Base(pluginAPISpec))

	mux := s.srv.buildMux()
	for _, route := range spec.Routes() {
		method, path, _ := strings.Cut(route, " ")
		_, pattern := mux.Handler(httptest.NewRequest(method, pluginAPIPrefix+path, nil))
		require.Equal(s.T(), method+" "+pluginAPIPrefix+path, pattern, route)
	}
}

// TestPluginAPISpecCapabilities holds each route's x-loop-capabilities to
// the capability its handler asks for.
func (s *ServerSuite) TestPluginAPISpecCapabilities() {
	spec := testutil.LoadOpenAPI(s.T(), pluginAPISpec)
	for _, route := range spec.Routes() {
		s.Run(route, func() {
			s.SetupTest()
			pm := new(MockPluginManager)
			s.srv.SetPluginManager(pm)
			s.srv.SetPluginRunner(new(MockPluginRunner))
			var asked []string
			pm.On("Allows", mock.Anything, "echo", mock.Anything).Run(func(args mock.Arguments) {
				if c := args.Get(2).(plugin.Capability); c != "" {
					asked = append(asked, string(c))
				}
			}).Return(install.ErrNotApproved).Maybe()
			// An agent run's capability depends on the agent: the
			// service's AgentRun checks it.
			pm.On("AgentRun", mock.Anything, "echo", "gen").Run(func(mock.Arguments) {
				asked = append(asked, string(plugin.CapAgentReadOnly), string(plugin.CapAgentWrite))
			}).Return(nil, install.ErrNotApproved).Maybe()

			method, path, _ := strings.Cut(route, " ")
			req := httptest.NewRequest(method, pluginAPIPrefix+path, strings.NewReader(`{"agent":"gen","prompt":"p"}`))
			req = req.WithContext(apiauth.WithPrincipal(req.Context(), echoPlugin))
			rec := httptest.NewRecorder()
			s.srv.buildMux().ServeHTTP(rec, req)

			require.Equal(s.T(), http.StatusForbidden, rec.Code, rec.Body.String())
			require.Equal(s.T(), spec.Capabilities[route], asked)
		})
	}
}

// TestPluginAPISpecResponses holds each route's JSON answer to the spec's
// schema: the Go type its handler answers with has the schema's
// properties, of matching types, and leaves out only optional ones.
func (s *ServerSuite) TestPluginAPISpecResponses() {
	answers := map[string]reflect.Type{
		"GET /channel":                  reflect.TypeFor[pluginChannelResponse](),
		"GET /channel/files":            reflect.TypeFor[pluginFileResponse](),
		"GET /channel/tree":             reflect.TypeFor[pluginTreeResponse](),
		"GET /channel/git/recent":       reflect.TypeFor[pluginRecentResponse](),
		"GET /channel/git/head":         reflect.TypeFor[pluginHeadResponse](),
		"GET /channel/quality/hotspots": reflect.TypeFor[QualityComplexityResponse](),
		"POST /runs":                    reflect.TypeFor[pluginRunResponse](),
		"GET /runs/{run_id}":            reflect.TypeFor[pluginRunStateResponse](),
		"POST /ui/commands":             reflect.TypeFor[uibridge.Reply](),
		"POST /ui/open_file":            reflect.TypeFor[uibridge.Reply](),
		"POST /cards":                   reflect.TypeFor[pluginCardResponse](),
	}
	spec := loadSpecSchemas(s.T(), pluginAPISpec)
	require.Equal(s.T(), slices.Sorted(maps.Keys(spec.answers)), slices.Sorted(maps.Keys(answers)),
		"the routes with a JSON answer in %s differ from the table", filepath.Base(pluginAPISpec))
	for route, typ := range answers {
		s.Run(route, func() {
			spec.match(s.T(), spec.answers[route], typ, typ.Name())
		})
	}
}

// specSchemas is an OpenAPI spec's JSON answer schemas by route, and its
// component schemas for $ref.
type specSchemas struct {
	answers map[string]map[string]any
	schemas map[string]any
}

func loadSpecSchemas(t require.TestingT, file string) *specSchemas {
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	var doc struct {
		Paths      map[string]map[string]any `yaml:"paths"`
		Components struct {
			Schemas map[string]any `yaml:"schemas"`
		} `yaml:"components"`
	}
	require.NoError(t, yaml.Unmarshal(data, &doc))
	out := &specSchemas{answers: map[string]map[string]any{}, schemas: doc.Components.Schemas}
	for p, item := range doc.Paths {
		for method, op := range item {
			// A path item's parameters are a list, not an operation.
			opMap, _ := op.(map[string]any)
			responses, _ := opMap["responses"].(map[string]any)
			for code, resp := range responses {
				content, _ := resp.(map[string]any)["content"].(map[string]any)
				body, _ := content["application/json"].(map[string]any)
				if strings.HasPrefix(code, "2") && body != nil {
					out.answers[strings.ToUpper(method)+" "+p] = body["schema"].(map[string]any)
				}
			}
		}
	}
	return out
}

// resolve follows schema's $ref.
func (o *specSchemas) resolve(schema map[string]any) map[string]any {
	if ref, ok := schema["$ref"].(string); ok {
		return o.schemas[strings.TrimPrefix(ref, "#/components/schemas/")].(map[string]any)
	}
	return schema
}

// match checks that typ encodes to JSON that schema allows; at names typ
// in failures.
func (o *specSchemas) match(t require.TestingT, schema map[string]any, typ reflect.Type, at string) {
	schema = o.resolve(schema)
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == reflect.TypeFor[json.RawMessage]() || typ.Kind() == reflect.Interface {
		return
	}
	switch schema["type"] {
	case "string":
		require.Equal(t, reflect.String, typ.Kind(), at)
	case "boolean":
		require.Equal(t, reflect.Bool, typ.Kind(), at)
	case "integer":
		require.Contains(t, []reflect.Kind{reflect.Int, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint32, reflect.Uint64}, typ.Kind(),
			"%s is %s, not an integer", at, typ)
	case "number":
		require.Contains(t, []reflect.Kind{reflect.Float32, reflect.Float64}, typ.Kind(), "%s is %s, not a number", at, typ)
	case "array":
		require.Equal(t, reflect.Slice, typ.Kind(), at)
		o.match(t, schema["items"].(map[string]any), typ.Elem(), at+"[]")
	case "object":
		o.matchObject(t, schema, typ, at)
	default:
		require.Fail(t, "a schema with a known type", "%s: %v", at, schema["type"])
	}
}

func (o *specSchemas) matchObject(t require.TestingT, schema map[string]any, typ reflect.Type, at string) {
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		require.Equal(t, reflect.Map, typ.Kind(), at)
		if values, ok := schema["additionalProperties"].(map[string]any); ok {
			o.match(t, values, typ.Elem(), at+"[]")
		}
		return
	}
	require.Equal(t, reflect.Struct, typ.Kind(), at)
	fields := jsonFields(typ)
	require.ElementsMatch(t, slices.Collect(maps.Keys(props)), slices.Collect(maps.Keys(fields)), "%s's fields", at)
	required, _ := schema["required"].([]any)
	for _, name := range required {
		require.False(t, fields[name.(string)].omitEmpty, "%s.%s is required but omitempty", at, name)
	}
	for name, prop := range props {
		o.match(t, prop.(map[string]any), fields[name].typ, at+"."+name)
	}
}

// jsonField is a struct field as encoding/json writes it.
type jsonField struct {
	typ       reflect.Type
	omitEmpty bool
}

// jsonFields returns typ's JSON fields by name.
func jsonFields(typ reflect.Type) map[string]jsonField {
	out := map[string]jsonField{}
	for f := range typ.Fields() {
		tag := f.Tag.Get("json")
		name, opts, _ := strings.Cut(tag, ",")
		if tag == "-" || !f.IsExported() {
			continue
		}
		if f.Anonymous && name == "" {
			maps.Copy(out, jsonFields(f.Type))
			continue
		}
		out[cmp.Or(name, f.Name)] = jsonField{typ: f.Type, omitEmpty: slices.Contains(strings.Split(opts, ","), "omitempty")}
	}
	return out
}

// TestPluginPanelCSPMatchesSDK holds the policy the SDK's CheckUI serves
// panels with to Loop's. The SDK is its own module, so its source is read.
func (s *ServerSuite) TestPluginPanelCSPMatchesSDK() {
	const prefix = "http://h/c/t/"
	file, err := parser.ParseFile(token.NewFileSet(), "../../pluginsdk/pluginsdktest/ui.go", nil, 0)
	require.NoError(s.T(), err)
	var eval func(e ast.Expr) string
	eval = func(e ast.Expr) string {
		switch e := e.(type) {
		case *ast.BinaryExpr:
			require.Equal(s.T(), token.ADD, e.Op)
			return eval(e.X) + eval(e.Y)
		case *ast.BasicLit:
			v, err := strconv.Unquote(e.Value)
			require.NoError(s.T(), err)
			return v
		case *ast.Ident:
			require.Equal(s.T(), "prefix", e.Name)
			return prefix
		}
		s.FailNow("panelCSP is more than a concatenation of literals and prefix")
		return ""
	}
	got := ""
	for _, d := range file.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "panelCSP" {
			got = eval(fn.Body.List[0].(*ast.ReturnStmt).Results[0])
		}
	}
	require.Equal(s.T(), pluginPanelCSP(prefix), got)
}
