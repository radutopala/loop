package testutil

import (
	"maps"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// OpenAPI is the operations of an OpenAPI spec, for tests that hold code to
// it.
type OpenAPI struct {
	// Capabilities are each operation's x-loop-capabilities, keyed by its
	// method and path, as in "GET /channel".
	Capabilities map[string][]string
	mux          *http.ServeMux
	schemas      map[string]openAPISchema
}

// openAPISchema is the part of a schema of components.schemas Enum reads.
type openAPISchema struct {
	Properties map[string]struct {
		Enum []string `yaml:"enum"`
	} `yaml:"properties"`
}

// LoadOpenAPI reads the OpenAPI spec at file.
func LoadOpenAPI(t testing.TB, file string) *OpenAPI {
	t.Helper()
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	var doc struct {
		Paths      map[string]map[string]yaml.Node `yaml:"paths"`
		Components struct {
			Schemas map[string]openAPISchema `yaml:"schemas"`
		} `yaml:"components"`
	}
	require.NoError(t, yaml.Unmarshal(data, &doc), file)
	// The keys of a path item that are operations.
	methods := []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}
	spec := &OpenAPI{Capabilities: map[string][]string{}, mux: http.NewServeMux(), schemas: doc.Components.Schemas}
	for p, item := range doc.Paths {
		for key, node := range item {
			if !slices.Contains(methods, key) {
				continue
			}
			var op struct {
				Capabilities []string `yaml:"x-loop-capabilities"`
			}
			require.NoError(t, node.Decode(&op), "%s %s", key, p)
			route := strings.ToUpper(key) + " " + p
			spec.Capabilities[route] = op.Capabilities
			// A {path} parameter spans the rest of the path.
			spec.mux.Handle(strings.ToUpper(key)+" "+strings.ReplaceAll(p, "{path}", "{path...}"), http.NotFoundHandler())
		}
	}
	return spec
}

// Route returns the spec's operation that a request with method and path
// is for, as in "POST /actions/{action}", or "" when there is none.
func (o *OpenAPI) Route(method, path string) string {
	_, pattern := o.mux.Handler(&http.Request{Method: method, URL: &url.URL{Path: path}})
	return strings.Replace(pattern, "{path...}", "{path}", 1)
}

// Routes returns the spec's operations, sorted.
func (o *OpenAPI) Routes() []string {
	return slices.Sorted(maps.Keys(o.Capabilities))
}

// Enum returns the enum of property of components.schemas' schema, sorted.
func (o *OpenAPI) Enum(schema, property string) []string {
	return slices.Sorted(slices.Values(o.schemas[schema].Properties[property].Enum))
}
