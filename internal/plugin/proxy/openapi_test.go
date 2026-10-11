package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/plugin/runtime"
	"github.com/radutopala/loop/internal/testutil"
)

// TestSpec holds the calls Client makes to the plugin contract's OpenAPI
// spec: each one is an operation of it, and every operation but /healthz,
// which the runtime calls, is made.
func (s *ClientSuite) TestSpec() {
	spec := testutil.LoadOpenAPI(s.T(), "../../../pluginsdk/openapi/plugin-contract-v1.yaml")
	var called []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := spec.Route(r.Method, r.URL.Path)
		require.NotEmpty(s.T(), route, "%s %s isn't in the spec", r.Method, r.URL.Path)
		called = append(called, route)
		_, _ = w.Write([]byte(`{"content":"ok"}`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client())
	ep := runtime.Endpoint{URL: srv.URL, Secret: "sec"}
	ctx := context.Background()

	require.NoError(s.T(), c.Action(ctx, ep, "daily", ActionRequest{Trigger: TriggerManual}))
	_, err := c.Tool(ctx, ep, "echo", ToolRequest{RunID: "r1", Input: []byte(`{}`)})
	require.NoError(s.T(), err)
	require.NoError(s.T(), c.RunDone(ctx, ep, "r1", RunDoneRequest{Status: RunStatusDone}))
	require.NoError(s.T(), c.Event(ctx, ep, Event{Type: "message.created", ChannelID: "ch1"}))
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		_, err := c.X(ctx, ep, XRequest{Method: m, Path: "api/items/1"})
		require.NoError(s.T(), err)
	}

	slices.Sort(called)
	want := slices.DeleteFunc(spec.Routes(), func(r string) bool { return r == "GET /healthz" })
	require.Equal(s.T(), want, slices.Compact(called))
	require.Equal(s.T(), slices.Sorted(slices.Values(plugin.Events)), spec.Enum("Event", "type"),
		"the events a manifest may subscribe to aren't the spec's")
}
