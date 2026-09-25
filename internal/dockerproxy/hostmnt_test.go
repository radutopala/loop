package dockerproxy

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/types"
)

type HostMntSuite struct {
	suite.Suite
}

func TestHostMntSuite(t *testing.T) {
	suite.Run(t, new(HostMntSuite))
}

// hostMntResolve fakes symlink resolution in the agent container: /host_mnt
// doesn't exist there, and /ws/root points to /.
func hostMntResolve(p string) (string, error) {
	if strings.HasPrefix(p, "/host_mnt") {
		return "", errors.New("no such file or directory")
	}
	if rest, ok := strings.CutPrefix(p, "/ws/root"); ok {
		return "/" + strings.TrimPrefix(rest, "/"), nil
	}
	return p, nil
}

func (s *HostMntSuite) TestAgentPath() {
	srv := &Server{cfg: ServerConfig{
		BindRoots:     []string{"/ws", "/tmp/t"},
		BindHostPaths: map[string]string{"/tmp/t": "/private/tmp/t"},
	}}
	cases := []struct {
		src  string
		want string
	}{
		{"/host_mnt/ws", "/ws"},
		{"/host_mnt/ws/", "/ws"},
		{"/host_mnt/ws/src", "/ws/src"},
		{"/host_mnt/tmp/t/x", "/tmp/t/x"},
		{"/host_mnt/private/tmp/t", "/tmp/t"},
		{"/host_mnt/private/tmp/t/x", "/tmp/t/x"},
		{"/host_mnt/ws/../etc", ""},
		{"/host_mnt/ws2", ""},
		{"/host_mnt", ""},
		{"/host_mntws", ""},
		{"/host_mnt/other", ""},
		{"/ws/src", ""},
		{"named", ""},
	}
	for _, tc := range cases {
		s.Run(tc.src, func() {
			got, ok := srv.agentPath(tc.src)
			require.Equal(s.T(), tc.want != "", ok)
			require.Equal(s.T(), tc.want, got)
		})
	}
}

func (s *HostMntSuite) TestUnmapHostMnt() {
	srv := &Server{cfg: ServerConfig{BindRoots: []string{"/ws"}}}
	cases := []struct {
		name    string
		body    string
		want    string
		changed bool
	}{
		{
			name:    "binds",
			body:    `{"HostConfig":{"Binds":["/host_mnt/ws/src:/src:rw,Z","/host_mnt/other:/o","named:/n","/ws:/w","junk",1]}}`,
			want:    `{"HostConfig":{"Binds":["/ws/src:/src:rw,Z","/host_mnt/other:/o","named:/n","/ws:/w","junk",1]}}`,
			changed: true,
		},
		{
			name:    "bind mounts, keys in any case",
			body:    `{"hostconfig":{"mounts":[{"type":"bind","source":"/host_mnt/ws/a","target":"/a"},{"Type":"volume","Source":"/host_mnt/ws","Target":"/v"},{"Type":"bind","Source":"/host_mnt/other","Target":"/o"},"junk"]}}`,
			want:    `{"hostconfig":{"mounts":[{"type":"bind","source":"/ws/a","target":"/a"},{"Type":"volume","Source":"/host_mnt/ws","Target":"/v"},{"Type":"bind","Source":"/host_mnt/other","Target":"/o"},"junk"]}}`,
			changed: true,
		},
		{
			name: "nothing under /host_mnt",
			body: `{"HostConfig":{"Binds":["/ws:/w"],"Mounts":[{"Type":"bind","Source":"/ws","Target":"/w"}]}}`,
			want: `{"HostConfig":{"Binds":["/ws:/w"],"Mounts":[{"Type":"bind","Source":"/ws","Target":"/w"}]}}`,
		},
		{
			name: "no host config",
			body: `{"Image":"alpine"}`,
			want: `{"Image":"alpine"}`,
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			var body map[string]any
			require.NoError(s.T(), json.Unmarshal([]byte(tc.body), &body))
			require.Equal(s.T(), tc.changed, srv.unmapHostMnt(body))
			got, _ := json.Marshal(body)
			require.JSONEq(s.T(), tc.want, string(got))
		})
	}
}

// TestServeHTTP runs create requests through the baseline body rules, the
// way pre-commit's docker_image hooks send them on Docker Desktop.
func (s *HostMntSuite) TestServeHTTP() {
	cases := []struct {
		name       string
		body       string
		wantStatus int
		wantMounts []any // forwarded HostConfig.Mounts
	}{
		{
			name:       "workspace, project config overlaid read-only",
			body:       `{"HostConfig":{"Binds":["/host_mnt/ws:/src:rw,Z"]}}`,
			wantStatus: http.StatusCreated,
			wantMounts: []any{pinned("/ws", "/src/.loop", ".loop", true), pinned("/ws", "/src", "", false)},
		},
		{
			name:       "workspace subdir, --mount",
			body:       `{"HostConfig":{"Mounts":[{"Type":"bind","Source":"/host_mnt/ws/sub","Target":"/src"}]}}`,
			wantStatus: http.StatusCreated,
			wantMounts: []any{pinned("/ws", "/src", "sub", false)},
		},
		{
			name:       "resolved host path of a symlinked root",
			body:       `{"HostConfig":{"Binds":["/host_mnt/private/tmp/t/x:/x"]}}`,
			wantStatus: http.StatusCreated,
			wantMounts: []any{pinned("/tmp/t", "/x", "x", false)},
		},
		{
			name:       "project config stays read-only",
			body:       `{"HostConfig":{"Binds":["/host_mnt/ws/.loop:/c"]}}`,
			wantStatus: http.StatusCreated,
			wantMounts: []any{pinned("/ws", "/c", ".loop", true)},
		},
		{
			name:       "home directory",
			body:       `{"HostConfig":{"Binds":["/host_mnt/Users/r:/h"]}}`,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "dotdot out of the workspace",
			body:       `{"HostConfig":{"Binds":["/host_mnt/ws/../Users/r:/h"]}}`,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "sibling prefix",
			body:       `{"HostConfig":{"Binds":["/host_mnt/ws2:/h"]}}`,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "workspace link to root",
			body:       `{"HostConfig":{"Binds":["/host_mnt/ws/root:/h"]}}`,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "workspace plus privileged",
			body:       `{"HostConfig":{"Privileged":true,"Binds":["/host_mnt/ws:/src"]}}`,
			wantStatus: http.StatusForbidden,
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			up := &pinUpstream{}
			sock, stop := upstreamUnix(s.T(), up)
			s.T().Cleanup(stop)
			policy, err := CompilePolicy(types.DecisionAllow, config.DefaultDockerProxyHTTPRules(), config.DefaultDockerProxyBodyRules())
			require.NoError(s.T(), err)
			policy.SetSymlinkResolver(hostMntResolve)
			srv, err := NewServer(ServerConfig{
				CID:           "cid-1",
				ChannelID:     "ch-1",
				Policy:        policy,
				Approver:      &fakeApprover{},
				DockerSock:    sock,
				Auditor:       &capturingAuditor{},
				EvalSymlinks:  hostMntResolve,
				ReadOnlyDirs:  []string{"/ws/.loop"},
				BindRoots:     []string{"/ws", "/tmp/t"},
				BindHostPaths: map[string]string{"/tmp/t": "/private/tmp/t"},
			})
			require.NoError(s.T(), err)

			req := httptest.NewRequest(http.MethodPost, "/v1.47/containers/create", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			srv.ServeHTTP(rr, req)

			require.Equal(s.T(), tc.wantStatus, rr.Code, rr.Body.String())
			if tc.wantStatus != http.StatusCreated {
				require.Nil(s.T(), up.forwarded)
				return
			}
			var got struct{ HostConfig map[string]any }
			require.NoError(s.T(), json.Unmarshal(up.forwarded, &got))
			wantJSON, _ := json.Marshal(tc.wantMounts)
			gotJSON, _ := json.Marshal(got.HostConfig["Mounts"])
			require.JSONEq(s.T(), string(wantJSON), string(gotJSON))
		})
	}
}
