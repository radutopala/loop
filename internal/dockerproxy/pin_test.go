package dockerproxy

import (
	"context"
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

type PinSuite struct {
	suite.Suite
}

func TestPinSuite(t *testing.T) {
	suite.Run(t, new(PinSuite))
}

// pinResponse shapes the fake daemon's volume create response; zero values
// answer like a current daemon binding the requested dir.
type pinResponse struct {
	volStatus  int
	volBody    string
	apiVersion string
}

// pinUpstream fakes the daemon's volume and container create endpoints.
type pinUpstream struct {
	mu        sync.Mutex
	resp      pinResponse
	volumes   []map[string]any // POST /volumes/create payloads
	forwarded []byte           // last POST /containers/create body
}

func (u *pinUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	defer u.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	if strings.HasSuffix(r.URL.Path, "/volumes/create") {
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		u.volumes = append(u.volumes, req)
		v := u.resp.apiVersion
		if v == "" {
			v = "1.56"
		}
		w.Header().Set("Api-Version", v)
		if u.resp.volStatus != 0 {
			w.WriteHeader(u.resp.volStatus)
			_, _ = w.Write([]byte(u.resp.volBody))
			return
		}
		w.WriteHeader(http.StatusCreated)
		if u.resp.volBody != "" {
			_, _ = w.Write([]byte(u.resp.volBody))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Name": req["Name"], "Driver": "local", "Options": req["DriverOpts"]})
		return
	}
	u.forwarded = body
	w.WriteHeader(http.StatusCreated)
}

// pinResolve fakes symlink resolution: /ws/link* points out to /outside,
// /missing doesn't exist.
func pinResolve(p string) (string, error) {
	if p == "/missing" {
		return "", errors.New("no such file")
	}
	if rest, ok := strings.CutPrefix(p, "/ws/link"); ok {
		return "/outside" + rest, nil
	}
	return p, nil
}

func (s *PinSuite) server(u *pinUpstream, roots []string) (*Server, *capturingAuditor) {
	sock, stop := upstreamUnix(s.T(), u)
	s.T().Cleanup(stop)
	policy, err := CompilePolicy(types.DecisionAllow, nil, nil)
	require.NoError(s.T(), err)
	auditor := &capturingAuditor{}
	srv, err := NewServer(ServerConfig{
		CID:          "cid-1",
		ChannelID:    "ch-1",
		Policy:       policy,
		Approver:     &fakeApprover{},
		DockerSock:   sock,
		Auditor:      auditor,
		EvalSymlinks: pinResolve,
		BindRoots:    roots,
	})
	require.NoError(s.T(), err)
	return srv, auditor
}

func pinned(root, target, subpath string, readOnly bool) map[string]any {
	opts := map[string]any{"NoCopy": true}
	if subpath != "" {
		opts["Subpath"] = subpath
	}
	return map[string]any{"Type": "volume", "Source": bindVolumeName(root), "Target": target, "ReadOnly": readOnly, "VolumeOptions": opts}
}

func (s *PinSuite) TestPinBinds() {
	wsVol := map[string]any{
		"Name":       bindVolumeName("/ws"),
		"Driver":     "local",
		"DriverOpts": map[string]any{"type": "none", "o": "bind", "device": "/ws"},
		"Labels":     map[string]any{"app": bindVolumeLabel},
	}
	cases := []struct {
		name        string
		roots       []string
		path        string
		body        string
		up          pinResponse
		wantStatus  int
		wantMsg     string
		wantHost    map[string]any // forwarded HostConfig; nil = body forwarded verbatim
		wantVolumes []map[string]any
	}{
		{
			name:        "binds under a root become pinned volume mounts",
			body:        `{"HostConfig":{"Binds":["/ws/src:/app","/ws/cfg.yml:/etc/app.yml:ro","named:/data"]}}`,
			wantHost:    map[string]any{"Binds": []any{"named:/data"}, "Mounts": []any{pinned("/ws", "/app", "src", false), pinned("/ws", "/etc/app.yml", "cfg.yml", true)}},
			wantVolumes: []map[string]any{wsVol},
		},
		{
			name: "bind mounts under a root, read-only kept; other mounts untouched",
			body: `{"HostConfig":{"Mounts":[{"Type":"bind","Source":"/ws/a","Target":"/a","ReadOnly":true},{"Type":"volume","Source":"v","Target":"/v"},"junk"]}}`,
			wantHost: map[string]any{"Mounts": []any{
				map[string]any{"Type": "volume", "Source": "v", "Target": "/v"},
				"junk",
				pinned("/ws", "/a", "a", true),
			}},
			wantVolumes: []map[string]any{wsVol},
		},
		{
			name:        "the root itself needs no subpath, so any API version",
			path:        "/v1.41/containers/create",
			body:        `{"HostConfig":{"Binds":["/ws:/ws"]}}`,
			wantHost:    map[string]any{"Binds": []any{}, "Mounts": []any{pinned("/ws", "/ws", "", false)}},
			wantVolumes: []map[string]any{wsVol},
		},
		{
			name:        "the outermost root is used",
			roots:       []string{"/ws/sub", "/ws"},
			body:        `{"HostConfig":{"Binds":["/ws/sub/x:/x"]}}`,
			wantHost:    map[string]any{"Binds": []any{}, "Mounts": []any{pinned("/ws", "/x", "sub/x", false)}},
			wantVolumes: []map[string]any{wsVol},
		},
		{
			name:     "binds outside the roots are forwarded as the path they resolve to",
			body:     `{"HostConfig":{"Binds":["/ws/link/d:/d:ro,z"]}}`,
			wantHost: map[string]any{"Binds": []any{}, "Mounts": []any{map[string]any{"Type": "bind", "Source": "/outside/d", "Target": "/d", "ReadOnly": true}}},
		},
		{
			name: "no host-path binds",
			body: `{"HostConfig":{"Binds":["named:/data","junk"]}}`,
		},
		{
			name: "no host config",
			body: `{"Image":"alpine"}`,
		},
		{
			name:  "no roots",
			roots: []string{},
			body:  `{"HostConfig":{"Binds":["/ws/src:/app"]}}`,
		},
		{
			name:       "unresolvable source",
			body:       `{"HostConfig":{"Mounts":[{"Type":"bind","Source":"/missing","Target":"/m"}]}}`,
			wantStatus: http.StatusBadRequest,
			wantMsg:    "bind source /missing: no such file",
		},
		{
			name:       "unresolvable bind string",
			body:       `{"HostConfig":{"Binds":["/missing:/m"]}}`,
			wantStatus: http.StatusBadRequest,
			wantMsg:    "bind source /missing",
		},
		{
			name:       "subpath below API 1.45",
			path:       "/v1.44/containers/create",
			body:       `{"HostConfig":{"Binds":["/ws/src:/app"]}}`,
			wantStatus: http.StatusBadRequest,
			wantMsg:    "need Docker API >= 1.45",
		},
		{
			name:       "volume create fails",
			body:       `{"HostConfig":{"Binds":["/ws/src:/app"]}}`,
			up:         pinResponse{volStatus: http.StatusInternalServerError, volBody: "disk full\n"},
			wantStatus: http.StatusBadGateway,
			wantMsg:    "status 500: disk full",
		},
		{
			name:        "docker desktop reports the device under /host_mnt",
			body:        `{"HostConfig":{"Binds":["/ws/src:/app"]}}`,
			up:          pinResponse{volBody: `{"Driver":"local","Options":{"type":"none","o":"bind","device":"/host_mnt/ws"}}`},
			wantHost:    map[string]any{"Binds": []any{}, "Mounts": []any{pinned("/ws", "/app", "src", false)}},
			wantVolumes: []map[string]any{wsVol},
		},
		{
			name:       "existing volume bound elsewhere",
			body:       `{"HostConfig":{"Binds":["/ws/src:/app"]}}`,
			up:         pinResponse{volBody: `{"Driver":"local","Options":{"type":"none","o":"bind","device":"/elsewhere"}}`},
			wantStatus: http.StatusBadGateway,
			wantMsg:    "isn't bound to /ws",
		},
		{
			name:       "volume response unreadable",
			body:       `{"HostConfig":{"Binds":["/ws/src:/app"]}}`,
			up:         pinResponse{volBody: `{`},
			wantStatus: http.StatusBadGateway,
			wantMsg:    "creating bind volume for /ws",
		},
		{
			name:       "daemon without subpath support",
			body:       `{"HostConfig":{"Binds":["/ws/src:/app"]}}`,
			up:         pinResponse{apiVersion: "1.44"},
			wantStatus: http.StatusBadGateway,
			wantMsg:    `API >= 1.45 (got "1.44")`,
		},
		{
			name:       "daemon version unreadable",
			body:       `{"HostConfig":{"Binds":["/ws/src:/app"]}}`,
			up:         pinResponse{apiVersion: "2.x"},
			wantStatus: http.StatusBadGateway,
			wantMsg:    `(got "2.x")`,
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			roots := tc.roots
			if roots == nil {
				roots = []string{"/ws"}
			}
			up := &pinUpstream{resp: tc.up}
			srv, auditor := s.server(up, roots)
			p := tc.path
			if p == "" {
				p = "/containers/create"
			}
			req := httptest.NewRequest(http.MethodPost, p, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			srv.ServeHTTP(rr, req)

			if tc.wantStatus != 0 {
				require.Equal(s.T(), tc.wantStatus, rr.Code)
				require.Contains(s.T(), rr.Body.String(), tc.wantMsg)
				require.Nil(s.T(), up.forwarded)
				snap := auditor.snapshot()
				require.Equal(s.T(), "create-binds", snap[len(snap)-1].RuleID)
				return
			}
			require.Equal(s.T(), http.StatusCreated, rr.Code, rr.Body.String())
			require.Equal(s.T(), tc.wantVolumes, up.volumes)
			if tc.wantHost == nil {
				require.JSONEq(s.T(), tc.body, string(up.forwarded))
				return
			}
			var got map[string]any
			require.NoError(s.T(), json.Unmarshal(up.forwarded, &got))
			wantJSON, _ := json.Marshal(tc.wantHost)
			gotJSON, _ := json.Marshal(got["HostConfig"])
			require.JSONEq(s.T(), string(wantJSON), string(gotJSON))
		})
	}
}

func (s *PinSuite) TestPinBindsCreatesEachVolumeOnce() {
	up := &pinUpstream{}
	srv, _ := s.server(up, []string{"/ws", "/other"})
	req := httptest.NewRequest(http.MethodPost, "/containers/create", strings.NewReader(`{"HostConfig":{"Binds":["/ws/a:/a","/ws/b:/b","/other:/o"]}}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	require.Equal(s.T(), http.StatusCreated, rr.Code, rr.Body.String())
	require.Len(s.T(), up.volumes, 2)
	require.Equal(s.T(), bindVolumeName("/ws"), up.volumes[0]["Name"])
	require.Equal(s.T(), bindVolumeName("/other"), up.volumes[1]["Name"])
}

func (s *PinSuite) TestPinBindsLeavesOtherBodies() {
	srv, _ := s.server(&pinUpstream{}, []string{"/ws"})
	for _, tc := range []struct {
		name, ct, body string
	}{
		{"not json", "application/x-tar", `{"HostConfig":{"Binds":["/ws/a:/a"]}}`},
		{"unparseable", "application/json", `{`},
		{"no body", "application/json", ""},
	} {
		s.Run(tc.name, func() {
			req := httptest.NewRequest(http.MethodPost, "/containers/create", strings.NewReader(tc.body))
			if tc.body == "" {
				req.Body = http.NoBody
			}
			req.Header.Set("Content-Type", tc.ct)
			status, _ := srv.pinBinds(req)
			require.Zero(s.T(), status)
			got, _ := io.ReadAll(req.Body)
			require.Equal(s.T(), tc.body, string(got))
		})
	}

	noResolver := &Server{cfg: ServerConfig{BindRoots: []string{"/ws"}}}
	req := httptest.NewRequest(http.MethodPost, "/containers/create", strings.NewReader(`{"HostConfig":{"Binds":["/ws/a:/a"]}}`))
	req.Header.Set("Content-Type", "application/json")
	status, _ := noResolver.pinBinds(req)
	require.Zero(s.T(), status)
}

func (s *PinSuite) TestEnsureBindVolumeUnreachable() {
	policy, err := CompilePolicy(types.DecisionAllow, nil, nil)
	require.NoError(s.T(), err)
	srv, err := NewServer(ServerConfig{CID: "c", Policy: policy, Approver: &fakeApprover{}, DockerSock: shortSockPath(s.T(), "gone.sock")})
	require.NoError(s.T(), err)
	require.ErrorContains(s.T(), srv.ensureBindVolume(context.Background(), "/ws", true), "creating bind volume for /ws")
}

func (s *PinSuite) TestNewServerSortsRootsOutermostFirst() {
	srv, _ := s.server(&pinUpstream{}, []string{"/a/b/c", "/a", "/a/b"})
	require.Equal(s.T(), []string{"/a", "/a/b", "/a/b/c"}, srv.cfg.BindRoots)
}

func (s *PinSuite) TestReservedVolumeNames() {
	name := bindVolumeName("/ws")
	cases := []struct {
		name     string
		ct       string
		body     string
		reserved bool
	}{
		{"reserved name", "application/json", `{"Name":"` + name + `"}`, true},
		{"reserved name, other key case", "application/json", `{"name":"` + name + `"}`, true},
		{"other name", "application/json", `{"Name":"data"}`, false},
		{"not json", "text/plain", `{"Name":"` + name + `"}`, false},
		{"unparseable", "application/json", `{`, false},
		{"no body", "application/json", "", false},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			req := httptest.NewRequest(http.MethodPost, "/volumes/create", strings.NewReader(tc.body))
			if tc.body == "" {
				req.Body = http.NoBody
			}
			req.Header.Set("Content-Type", tc.ct)
			require.Equal(s.T(), tc.reserved, reservesBindVolume(req))
			got, _ := io.ReadAll(req.Body)
			require.Equal(s.T(), tc.body, string(got), "body restored for forwarding")
		})
	}
}

func (s *PinSuite) TestServeHTTPDeniesReservedVolumeName() {
	up := &pinUpstream{}
	srv, auditor := s.server(up, []string{"/ws"})
	req := httptest.NewRequest(http.MethodPost, "/v1.56/volumes/create", strings.NewReader(`{"Name":"`+bindVolumeName("/ws")+`","DriverOpts":{"device":"/"}}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	require.Equal(s.T(), http.StatusForbidden, rr.Code)
	require.Contains(s.T(), rr.Body.String(), "reserved for the docker proxy")
	require.Empty(s.T(), up.volumes)
	require.Equal(s.T(), "volume-name", auditor.snapshot()[0].RuleID)
}
