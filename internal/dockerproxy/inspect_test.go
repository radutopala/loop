package dockerproxy

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/types"
)

type InspectSuite struct {
	suite.Suite
}

func TestInspectSuite(t *testing.T) {
	suite.Run(t, new(InspectSuite))
}

// daemonUpstream fakes the daemon's volume create, container create and
// container inspect endpoints. Inspect answers like the daemon: volume
// mounts are reported with the volume's directory in the data root.
type daemonUpstream struct {
	mu     sync.Mutex
	hostCf map[string]any // HostConfig of the last created container
}

func (u *daemonUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	defer u.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	switch {
	case strings.HasSuffix(r.URL.Path, "/volumes/create"):
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Api-Version", "1.56")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"Name": req["Name"], "Driver": "local", "Options": req["DriverOpts"]})
	case strings.HasSuffix(r.URL.Path, "/containers/create"):
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		u.hostCf, _ = req["HostConfig"].(map[string]any)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"Id":"c1"}`))
	default:
		var mounts []any
		hcMounts, _ := u.hostCf["Mounts"].([]any)
		for _, m := range hcMounts {
			mm := m.(map[string]any)
			src := mm["Source"].(string)
			if mm["Type"] == "volume" {
				mounts = append(mounts, map[string]any{
					"Type": "volume", "Name": src, "Source": "/var/lib/docker/volumes/" + src + "/_data",
					"Destination": mm["Target"], "Driver": "local", "Mode": "z", "RW": mm["ReadOnly"] != true, "Propagation": "",
				})
				continue
			}
			mounts = append(mounts, map[string]any{
				"Type": "bind", "Source": src, "Destination": mm["Target"], "Mode": "", "RW": true, "Propagation": "rprivate",
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"Id": "c1", "Mounts": mounts, "HostConfig": u.hostCf})
	}
}

func (s *InspectSuite) server(h http.Handler, roots []string) *Server {
	sock, stop := upstreamUnix(s.T(), h)
	s.T().Cleanup(stop)
	policy, err := CompilePolicy(types.DecisionAllow, nil, nil)
	require.NoError(s.T(), err)
	srv, err := NewServer(ServerConfig{
		CID:          "cid-1",
		ChannelID:    "ch-1",
		Policy:       policy,
		Approver:     &fakeApprover{},
		DockerSock:   sock,
		EvalSymlinks: func(p string) (string, error) { return p, nil },
		BindRoots:    roots,
	})
	require.NoError(s.T(), err)
	return srv
}

func (s *InspectSuite) do(srv *Server, method, url, body string) (int, map[string]any) {
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, url, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if method == http.MethodGet {
		require.Equal(s.T(), rec.Body.Len(), int(rec.Result().ContentLength))
	}
	return rec.Code, out
}

// A container created with a bind of the worktree is pinned to a loop-bind
// volume, but inspecting it reports the worktree path, so a script comparing
// the mount's source with $PWD finds its container.
func (s *InspectSuite) TestInspectReportsWorktreeBind() {
	srv := s.server(&daemonUpstream{}, []string{"/ws"})
	code, _ := s.do(srv, http.MethodPost, "/v1.47/containers/create",
		`{"Image":"dev","HostConfig":{"Binds":["/ws/project:/app","/data:/data:ro"],"Mounts":[{"Type":"bind","Source":"/ws","Target":"/ws","ReadOnly":true}]}}`)
	require.Equal(s.T(), http.StatusCreated, code)

	code, body := s.do(srv, http.MethodGet, "/v1.47/containers/c1/json", "")
	require.Equal(s.T(), http.StatusOK, code)
	sources := map[string]map[string]any{}
	for _, m := range body["Mounts"].([]any) {
		mm := m.(map[string]any)
		sources[mm["Destination"].(string)] = mm
	}
	require.Equal(s.T(), map[string]any{"Type": "bind", "Source": "/ws/project", "Destination": "/app", "Mode": "", "RW": true, "Propagation": ""}, sources["/app"])
	require.Equal(s.T(), "/ws", sources["/ws"]["Source"])
	require.Equal(s.T(), false, sources["/ws"]["RW"])
	require.Equal(s.T(), "/data", sources["/data"]["Source"])
	require.ElementsMatch(s.T(), []any{
		map[string]any{"Type": "bind", "Source": "/ws/project", "Target": "/app", "ReadOnly": false},
		map[string]any{"Type": "bind", "Source": "/ws", "Target": "/ws", "ReadOnly": true},
		map[string]any{"Type": "bind", "Source": "/data", "Target": "/data", "ReadOnly": true},
	}, body["HostConfig"].(map[string]any)["Mounts"])
}

func (s *InspectSuite) TestInspectPassThrough() {
	vol := bindVolumeName("/ws")
	pinnedInspect := `{"Mounts":[{"Type":"volume","Name":"` + vol + `","Source":"/var/lib/docker/volumes/` + vol + `/_data","Destination":"/app"}]}`
	tests := []struct {
		name   string
		roots  []string
		method string
		url    string
		status int
		ctype  string
		body   string
	}{
		{"no roots", nil, http.MethodGet, "/containers/c1/json", http.StatusOK, "application/json", pinnedInspect},
		{"not inspect", []string{"/ws"}, http.MethodGet, "/containers/json", http.StatusOK, "application/json", pinnedInspect},
		{"not GET", []string{"/ws"}, http.MethodPost, "/containers/c1/json", http.StatusOK, "application/json", pinnedInspect},
		{"error status", []string{"/ws"}, http.MethodGet, "/containers/c1/json", http.StatusNotFound, "application/json", pinnedInspect},
		{"not JSON", []string{"/ws"}, http.MethodGet, "/containers/c1/json", http.StatusOK, "text/plain", pinnedInspect},
		{"invalid JSON", []string{"/ws"}, http.MethodGet, "/containers/c1/json", http.StatusOK, "application/json", `{"Mounts":`},
		{"other volume", []string{"/ws"}, http.MethodGet, "/containers/c1/json", http.StatusOK, "application/json",
			`{"Mounts":[{"Type":"volume","Name":"data","Source":"/var/lib/docker/volumes/data/_data","Destination":"/d"}],"HostConfig":{"Mounts":[{"Type":"volume","Source":"data","Target":"/d"}]}}`},
		{"other root", []string{"/other"}, http.MethodGet, "/containers/c1/json", http.StatusOK, "application/json", pinnedInspect},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			srv := s.server(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.ctype)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}), tc.roots)
			req := httptest.NewRequest(tc.method, tc.url, nil)
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			require.Equal(s.T(), tc.status, rec.Code)
			require.Equal(s.T(), tc.body, rec.Body.String())
		})
	}
}

// A pinned mount missing from HostConfig.Mounts is reported as its root.
func (s *InspectSuite) TestInspectWithoutHostConfigReportsRoot() {
	vol := bindVolumeName("/ws")
	srv := s.server(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Mounts":[{"Type":"volume","Name":"` + vol + `","Source":"/var/lib/docker/volumes/` + vol + `/_data","Destination":"/app","Driver":"local","Mode":"z"}],"SizeRw":12345678901234567890}`))
	}), []string{"/ws"})
	code, body := s.do(srv, http.MethodGet, "/containers/c1/json", "")
	require.Equal(s.T(), http.StatusOK, code)
	require.Equal(s.T(), []any{map[string]any{"Type": "bind", "Source": "/ws", "Destination": "/app", "Mode": ""}}, body["Mounts"])
}

func (s *InspectSuite) TestModifyResponseReadError() {
	srv := s.server(http.NotFoundHandler(), []string{"/ws"})
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       &errReader{err: errors.New("boom")},
		Request:    httptest.NewRequest(http.MethodGet, "/containers/c1/json", nil),
	}
	require.EqualError(s.T(), srv.modifyResponse(resp), "boom")
}
