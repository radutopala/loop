package runtime

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/testutil"
)

// TestHealthSpec holds the health check to the plugin contract's OpenAPI
// spec.
func (s *ManagerSuite) TestHealthSpec() {
	spec := testutil.LoadOpenAPI(s.T(), "../../../pluginsdk/openapi/plugin-contract-v1.yaml")
	var route string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		route = spec.Route(r.Method, r.URL.Path)
	}))
	defer srv.Close()
	m := NewManager(s.docker, s.store, s.tokens, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.True(s.T(), m.healthy(s.ctx, srv.URL, "s3cret"))
	require.Equal(s.T(), "GET /healthz", route)
}
