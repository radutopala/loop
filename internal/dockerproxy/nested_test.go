package dockerproxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/types"
)

type NestedSuite struct {
	suite.Suite
}

func TestNestedSuite(t *testing.T) {
	suite.Run(t, new(NestedSuite))
}

// nestedServer builds a Server over the default policy whose upstream
// records the body of each forwarded request.
func (s *NestedSuite) nestedServer(volume string, resolve SymlinkResolver) (*Server, *capturingAuditor, func() []byte) {
	var forwarded []byte
	sock, stop := upstreamUnix(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	s.T().Cleanup(stop)
	policy, err := CompilePolicy(types.DecisionAllow, config.DefaultDockerProxyHTTPRules(), config.DefaultDockerProxyBodyRules())
	require.NoError(s.T(), err)
	policy.SetSymlinkResolver(resolve)
	auditor := &capturingAuditor{}
	srv, err := NewServer(ServerConfig{
		CID:          "cid-1",
		ChannelID:    "ch-1",
		Policy:       policy,
		Approver:     &fakeApprover{},
		DockerSock:   sock,
		Auditor:      auditor,
		NestedVolume: volume,
		EvalSymlinks: resolve,
	})
	require.NoError(s.T(), err)
	return srv, auditor, func() []byte { return forwarded }
}

func noSymlinks(p string) (string, error) { return p, nil }

func sockMount(target string, readOnly bool) map[string]any {
	return map[string]any{
		"Type":          "volume",
		"Source":        "vol-1",
		"Target":        target,
		"ReadOnly":      readOnly,
		"VolumeOptions": map[string]any{"Subpath": "docker.sock"},
	}
}

func (s *NestedSuite) TestServeHTTPRewritesSocketMounts() {
	cases := []struct {
		name     string
		path     string
		body     string
		resolve  SymlinkResolver
		wantHost map[string]any
	}{
		{
			name: "bind",
			path: "/v1.47/containers/create",
			body: `{"Image":"alpine","HostConfig":{"Binds":["/work:/work","/var/run/docker.sock:/var/run/docker.sock:ro"]}}`,
			wantHost: map[string]any{
				"Binds":  []any{"/work:/work"},
				"Mounts": []any{sockMount("/var/run/docker.sock", true)},
			},
		},
		{
			name: "bind appended to existing mounts",
			path: "/containers/create",
			body: `{"HostConfig":{"Binds":["/run/docker.sock:/sock:rw,z"],"Mounts":[{"Type":"bind","Source":"/work","Target":"/w"}]}}`,
			wantHost: map[string]any{
				"Binds": []any{},
				"Mounts": []any{
					map[string]any{"Type": "bind", "Source": "/work", "Target": "/w"},
					sockMount("/sock", false),
				},
			},
		},
		{
			name: "long-form mount with lowercase keys",
			path: "/v1.45/containers/create",
			body: `{"hostconfig":{"mounts":[{"type":"bind","source":"/var/run/docker.sock","target":"/d.sock","readonly":true},{"Type":"volume","Source":"cache","Target":"/c"},"junk"]}}`,
			wantHost: map[string]any{
				"mounts": []any{
					sockMount("/d.sock", true),
					map[string]any{"Type": "volume", "Source": "cache", "Target": "/c"},
					"junk",
				},
			},
		},
		{
			name: "symlink to the socket",
			path: "/containers/create",
			body: `{"HostConfig":{"Binds":["/work/s:/var/run/docker.sock","named:/data"]}}`,
			resolve: func(p string) (string, error) {
				if p == "/work/s" {
					return "/var/run/docker.sock", nil
				}
				return p, nil
			},
			wantHost: map[string]any{
				"Binds":  []any{"named:/data"},
				"Mounts": []any{sockMount("/var/run/docker.sock", false)},
			},
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			resolve := tc.resolve
			if resolve == nil {
				resolve = noSymlinks
			}
			srv, _, forwarded := s.nestedServer("vol-1", resolve)
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			srv.ServeHTTP(rr, req)

			require.Equal(s.T(), http.StatusCreated, rr.Code, rr.Body.String())
			var got map[string]any
			require.NoError(s.T(), json.Unmarshal(forwarded(), &got))
			for k, v := range got {
				if strings.EqualFold(k, "HostConfig") {
					require.Equal(s.T(), tc.wantHost, v)
				}
			}
		})
	}
}

func (s *NestedSuite) TestServeHTTPLeavesOtherBodiesAlone() {
	cases := []struct {
		name        string
		method      string
		path        string
		contentType string
		body        string
	}{
		{name: "no socket mount", method: http.MethodPost, path: "/containers/create", contentType: "application/json", body: `{"Image":"alpine","HostConfig":{"Binds":["/work:/work","bad"],"Mounts":"x"},"Size":12345678901234567890}`},
		{name: "no host config", method: http.MethodPost, path: "/containers/create", contentType: "application/json", body: `{"Image":"alpine"}`},
		{name: "not json", method: http.MethodPost, path: "/containers/create", contentType: "text/plain", body: `{"HostConfig":{"Binds":["/var/run/docker.sock:/s"]}}`},
		{name: "other endpoint", method: http.MethodPost, path: "/volumes/create", contentType: "application/json", body: `{"Name":"/var/run/docker.sock:/s"}`},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			srv, _, forwarded := s.nestedServer("vol-1", noSymlinks)
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", tc.contentType)
			rr := httptest.NewRecorder()
			srv.ServeHTTP(rr, req)

			require.Equal(s.T(), http.StatusCreated, rr.Code, rr.Body.String())
			require.Equal(s.T(), tc.body, string(forwarded()))
		})
	}
}

func (s *NestedSuite) TestServeHTTPRejects() {
	cases := []struct {
		name       string
		volume     string
		path       string
		body       io.ReadCloser
		wantStatus int
		wantReason string
	}{
		{
			name:       "no nested volume",
			path:       "/containers/create",
			body:       io.NopCloser(strings.NewReader(`{"HostConfig":{"Binds":["/var/run/docker.sock:/var/run/docker.sock"]}}`)),
			wantStatus: http.StatusForbidden,
			wantReason: "nested proxy socket is not configured",
		},
		{
			name:       "api too old for subpath",
			volume:     "vol-1",
			path:       "/v1.44/containers/create",
			body:       io.NopCloser(strings.NewReader(`{"HostConfig":{"Binds":["/var/run/docker.sock:/var/run/docker.sock"]}}`)),
			wantStatus: http.StatusBadRequest,
			wantReason: "Docker API >= 1.45",
		},
		{
			name:       "case-variant duplicate keys",
			volume:     "vol-1",
			path:       "/containers/create",
			body:       io.NopCloser(strings.NewReader(`{"HostConfig":{"Privileged":false},"hostconfig":{"Privileged":true}}`)),
			wantStatus: http.StatusBadRequest,
			wantReason: "keys differ only in case",
		},
		{
			name:       "nested case-variant duplicate keys",
			volume:     "vol-1",
			path:       "/containers/create",
			body:       io.NopCloser(strings.NewReader(`{"HostConfig":{"Mounts":[{"Source":"/a","source":"/var/run/docker.sock"}]}}`)),
			wantStatus: http.StatusBadRequest,
			wantReason: "keys differ only in case",
		},
		{
			name:       "oversized body",
			volume:     "vol-1",
			path:       "/containers/create",
			body:       io.NopCloser(strings.NewReader(`{"Image":"` + strings.Repeat("a", nestedBodyCap) + `"}`)),
			wantStatus: http.StatusRequestEntityTooLarge,
			wantReason: "too large",
		},
		{
			name:       "read error",
			volume:     "vol-1",
			path:       "/containers/create",
			body:       &errReader{err: errors.New("boom")},
			wantStatus: http.StatusBadRequest,
			wantReason: "invalid request body",
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			srv, auditor, forwarded := s.nestedServer(tc.volume, noSymlinks)
			req := httptest.NewRequest(http.MethodPost, tc.path, nil)
			req.Body = tc.body
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			srv.ServeHTTP(rr, req)

			require.Equal(s.T(), tc.wantStatus, rr.Code)
			require.Contains(s.T(), rr.Body.String(), tc.wantReason)
			require.Nil(s.T(), forwarded())
			snap := auditor.snapshot()
			require.Len(s.T(), snap, 1)
			require.Equal(s.T(), "create-body", snap[0].RuleID)
			require.Equal(s.T(), "deny", snap[0].Decision)
		})
	}
}

// Invalid JSON is left to evaluateBody, which answers 400 as before.
func (s *NestedSuite) TestServeHTTPInvalidJSONFallsThrough() {
	srv, auditor, _ := s.nestedServer("vol-1", noSymlinks)
	req := httptest.NewRequest(http.MethodPost, "/containers/create", strings.NewReader(`{`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	require.Equal(s.T(), http.StatusBadRequest, rr.Code)
	snap := auditor.snapshot()
	require.Len(s.T(), snap, 1)
	require.Equal(s.T(), "body-eval-error", snap[0].RuleID)
}

func (s *NestedSuite) TestIsDockerSocket() {
	failing := func(string) (string, error) { return "", errors.New("nope") }
	cases := []struct {
		name    string
		src     string
		resolve SymlinkResolver
		want    bool
	}{
		{name: "var run", src: "/var/run/docker.sock", want: true},
		{name: "run", src: "/run/docker.sock", want: true},
		{name: "unclean", src: "/var/run/../run/./docker.sock", want: true},
		{name: "named volume", src: "docker.sock", resolve: failing, want: false},
		{name: "other path without resolver", src: "/work/s", want: false},
		{name: "resolve error", src: "/work/s", resolve: failing, want: false},
		{name: "resolves elsewhere", src: "/work/s", resolve: noSymlinks, want: false},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			srv := &Server{cfg: ServerConfig{EvalSymlinks: tc.resolve}}
			require.Equal(s.T(), tc.want, srv.isDockerSocket(tc.src))
		})
	}
}

func (s *NestedSuite) TestHasMountOption() {
	require.True(s.T(), hasMountOption("z,ro", "ro"))
	require.False(s.T(), hasMountOption("rw", "ro"))
	require.False(s.T(), hasMountOption("", "ro"))
}

func (s *NestedSuite) TestLookupNestedVolume() {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		want    string
		wantErr string
	}{
		{
			name: "found",
			handler: func(w http.ResponseWriter, r *http.Request) {
				require.Equal(s.T(), "/containers/loop-a%2Fb/json", r.URL.EscapedPath())
				_, _ = io.WriteString(w, `{"Mounts":[{"Type":"bind","Destination":"/run/loop-dproxy"},{"Type":"volume","Name":"other","Destination":"/data"},{"Type":"volume","Name":"vol-1","Destination":"/run/loop-dproxy"}]}`)
			},
			want: "vol-1",
		},
		{
			name: "not mounted",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `{"Mounts":[]}`)
			},
			wantErr: "no volume mounted at /run/loop-dproxy",
		},
		{
			name: "status",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			},
			wantErr: "status 404",
		},
		{
			name: "bad json",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `{`)
			},
			wantErr: "inspect loop-a/b: unexpected EOF",
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			sock, stop := upstreamUnix(s.T(), tc.handler)
			defer stop()
			got, err := lookupNestedVolume(context.Background(), sock, "loop-a/b", "/run/loop-dproxy")
			if tc.wantErr != "" {
				require.ErrorContains(s.T(), err, tc.wantErr)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.want, got)
		})
	}
}

func (s *NestedSuite) TestLookupNestedVolumeDialError() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := lookupNestedVolume(ctx, shortSockPath(s.T(), "missing.sock"), "cid", "/d")
	require.ErrorContains(s.T(), err, "inspect cid:")
}
