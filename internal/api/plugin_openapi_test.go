package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/apiauth"
	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/plugin/install"
	"github.com/radutopala/loop/internal/testutil"
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
