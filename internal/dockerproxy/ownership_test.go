package dockerproxy

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/agentgate"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/types"
)

type OwnershipSuite struct {
	suite.Suite
}

func TestOwnershipSuite(t *testing.T) {
	suite.Run(t, new(OwnershipSuite))
}

// fakeDaemon answers the proxy's lookups from fixed tables and records every
// other request it receives.
type fakeDaemon struct {
	mu         sync.Mutex
	containers map[string]string // id → inspect body; missing = 404
	volumes    map[string]string // name → inspect body; missing = 404
	status     int               // non-zero overrides lookup statuses
	forwarded  []string
}

func (d *fakeDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := stripAPIVersionPrefix(r.URL.Path)
	lookup := func(table map[string]string, key string) {
		body, ok := table[key]
		switch {
		case d.status != 0:
			w.WriteHeader(d.status)
		case !ok:
			w.WriteHeader(http.StatusNotFound)
		default:
			_, _ = w.Write([]byte(body))
		}
	}
	if r.Method == http.MethodGet {
		if id, ok := strings.CutSuffix(strings.TrimPrefix(path, "/containers/"), "/json"); ok && strings.HasPrefix(path, "/containers/") {
			lookup(d.containers, id)
			return
		}
		if name, ok := strings.CutPrefix(path, "/volumes/"); ok {
			lookup(d.volumes, name)
			return
		}
	}
	d.mu.Lock()
	d.forwarded = append(d.forwarded, r.Method+" "+r.URL.RequestURI())
	d.mu.Unlock()
	if r.Method == http.MethodPost && path == "/containers/create" {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"Id":"0123456789abcdef0123456789abcdef","Warnings":[]}`))
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (d *fakeDaemon) requests() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.forwarded...)
}

// server starts d on a unix socket and returns a proxy in front of it using
// the default rules, with ownership checks on.
func (s *OwnershipSuite) server(d http.Handler, approver *fakeApprover, auditor Auditor) *Server {
	sock, stop := upstreamUnix(s.T(), d)
	s.T().Cleanup(stop)
	return s.serverOn(sock, approver, auditor)
}

func (s *OwnershipSuite) serverOn(sock string, approver *fakeApprover, auditor Auditor) *Server {
	policy, err := CompilePolicy(types.DecisionAllow, config.DefaultDockerProxyHTTPRules(), config.DefaultDockerProxyBodyRules())
	require.NoError(s.T(), err)
	srv, err := NewServer(ServerConfig{
		CID:            "cid-1",
		ChannelID:      "ch-1",
		Policy:         policy,
		Approver:       approver,
		DockerSock:     sock,
		Auditor:        auditor,
		Now:            time.Now,
		NestedVolume:   "nested-vol",
		CheckOwnership: true,
	})
	require.NoError(s.T(), err)
	return srv
}

func do(srv *Server, method, target, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func (s *OwnershipSuite) TestTargetContainer() {
	tests := []struct {
		name, method, path string
		query              url.Values
		want               string
	}{
		{"inspect", http.MethodGet, "/containers/abc/json", nil, "abc"},
		{"remove", http.MethodDelete, "/containers/abc", nil, "abc"},
		{"logs", http.MethodGet, "/containers/abc/logs", nil, "abc"},
		{"list", http.MethodGet, "/containers/json", nil, ""},
		{"create", http.MethodPost, "/containers/create", nil, ""},
		{"prune", http.MethodPost, "/containers/prune", nil, ""},
		{"container named json", http.MethodGet, "/containers/json/json", nil, "json"},
		{"commit", http.MethodPost, "/commit", url.Values{"container": {"abc"}}, "abc"},
		{"commit without container", http.MethodPost, "/commit", nil, ""},
		{"images", http.MethodGet, "/images/json", nil, ""},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, targetContainer(tc.method, tc.path, tc.query))
		})
	}
}

func (s *OwnershipSuite) TestOtherChannelContainer() {
	d := &fakeDaemon{containers: map[string]string{
		"mine":     `{"Config":{"Labels":{"loop-channel":"ch-1"}}}`,
		"theirs":   `{"Config":{"Labels":{"loop-channel":"ch-2"}}}`,
		"unlabled": `{"Config":{"Labels":{"app":"db"}}}`,
		"garbled":  `{"Config":`,
	}}
	tests := []struct {
		name, method, target string
		wantStatus           int
		wantReason           string
	}{
		{"same channel", http.MethodPost, "/containers/mine/stop", http.StatusOK, ""},
		{"no loop label", http.MethodPost, "/v1.47/containers/unlabled/kill", http.StatusOK, ""},
		{"unknown container", http.MethodDelete, "/containers/gone", http.StatusOK, ""},
		{"other channel stop", http.MethodPost, "/containers/theirs/stop", http.StatusForbidden, "container belongs to another loop channel"},
		{"other channel inspect", http.MethodGet, "/containers/theirs/json", http.StatusForbidden, "container belongs to another loop channel"},
		{"other channel remove", http.MethodDelete, "/containers/theirs", http.StatusForbidden, "container belongs to another loop channel"},
		{"other channel commit", http.MethodPost, "/commit?container=theirs", http.StatusForbidden, "container belongs to another loop channel"},
		{"garbled inspect", http.MethodPost, "/containers/garbled/stop", http.StatusForbidden, "can't check the owner of container garbled: unexpected EOF"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			auditor := &capturingAuditor{}
			approver := &fakeApprover{outcome: agentgate.Outcome{Decision: types.DecisionAllow}}
			rec := do(s.server(d, approver, auditor), tc.method, tc.target, "")
			require.Equal(s.T(), tc.wantStatus, rec.Code, rec.Body.String())
			if tc.wantReason != "" {
				require.Equal(s.T(), tc.wantReason+"\n", rec.Body.String())
				snap := auditor.snapshot()
				require.Len(s.T(), snap, 1)
				require.Equal(s.T(), "other-channel", snap[0].RuleID)
				require.Empty(s.T(), approver.calls, "a hard deny never prompts")
			}
		})
	}
}

func (s *OwnershipSuite) TestOtherChannelLabelOnOwnContainer() {
	d := &fakeDaemon{containers: map[string]string{
		"nested": `{"Config":{"Labels":{"loop-channel":"ch-2"}}}`,
	}}
	srv := s.server(d, &fakeApprover{}, nil)
	resp := &http.Response{Request: httptest.NewRequest(http.MethodPost, "/containers/create?name=nested", nil)}
	srv.recordCreated(resp, []byte(`{"Id":"nested"}`))
	rec := do(srv, http.MethodPost, "/containers/nested/stop", "")
	require.Equal(s.T(), http.StatusOK, rec.Code, rec.Body.String())
}

func (s *OwnershipSuite) TestOtherChannelContainerLookupFailures() {
	s.Run("daemon error", func() {
		d := &fakeDaemon{status: http.StatusInternalServerError}
		rec := do(s.server(d, &fakeApprover{}, nil), http.MethodPost, "/containers/abc/stop", "")
		require.Equal(s.T(), http.StatusForbidden, rec.Code)
		require.Equal(s.T(), "can't check the owner of container abc: status 500\n", rec.Body.String())
		require.Empty(s.T(), d.requests())
	})
	s.Run("daemon unreachable", func() {
		srv := s.serverOn(shortSockPath(s.T(), "none.sock"), &fakeApprover{}, nil)
		rec := do(srv, http.MethodPost, "/containers/abc/stop", "")
		require.Equal(s.T(), http.StatusForbidden, rec.Code)
		require.Contains(s.T(), rec.Body.String(), "can't check the owner of container abc: ")
	})
}

// Without CheckOwnership the proxy makes no lookups of its own.
func (s *OwnershipSuite) TestCheckOwnershipOff() {
	d := &fakeDaemon{containers: map[string]string{"theirs": `{"Config":{"Labels":{"loop-channel":"ch-2"}}}`}}
	srv := s.server(d, &fakeApprover{}, nil)
	srv.cfg.CheckOwnership = false
	rec := do(srv, http.MethodPost, "/containers/theirs/stop", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	rec = do(srv, http.MethodPost, "/containers/create", `{"Image":"a","HostConfig":{"Binds":["data:/d"]}}`)
	require.Equal(s.T(), http.StatusCreated, rec.Code)
}

// Attach asks by default, but not for the containers this proxy created:
// `docker run` attaches to its own container right after creating it.
func (s *OwnershipSuite) TestOwnedAttach() {
	d := &fakeDaemon{}
	approver := &fakeApprover{outcome: agentgate.Outcome{Decision: types.DecisionDeny}}
	srv := s.server(d, approver, nil)

	rec := do(srv, http.MethodGet, "/containers/0123456789abcdef/attach/ws", "")
	require.Equal(s.T(), http.StatusForbidden, rec.Code, "not created yet: asks, and the user says no")
	require.Len(s.T(), approver.calls, 1)
	require.Equal(s.T(), "docker:GET:/containers/0123456789abcdef/attach/ws", approver.calls[0].CacheKey)

	rec = do(srv, http.MethodPost, "/v1.47/containers/create?name=%2Fweb", `{"Image":"a"}`)
	require.Equal(s.T(), http.StatusCreated, rec.Code)

	for _, ref := range []string{"0123456789abcdef0123456789abcdef", "0123456789ab", "web"} {
		rec = do(srv, http.MethodGet, "/containers/"+ref+"/attach/ws", "")
		require.Equal(s.T(), http.StatusOK, rec.Code, ref)
	}
	require.Len(s.T(), approver.calls, 1, "attaching to an owned container never prompts")

	for _, ref := range []string{"0123456789a", "fedcba9876543210", "db"} {
		rec = do(srv, http.MethodGet, "/containers/"+ref+"/attach/ws", "")
		require.Equal(s.T(), http.StatusForbidden, rec.Code, ref)
	}
	require.Len(s.T(), approver.calls, 4)
}

func (s *OwnershipSuite) TestOwnedAttachKeepsOtherDecisions() {
	srv := s.server(&fakeDaemon{}, &fakeApprover{}, nil)
	srv.owned["abc"] = true
	for _, tc := range []struct {
		name string
		res  HTTPMatchResult
		path string
	}{
		{"allow stays", HTTPMatchResult{Decision: types.DecisionAllow, RuleID: "default"}, "/containers/abc/attach"},
		{"deny stays", HTTPMatchResult{Decision: types.DecisionDeny, RuleID: "http[0]"}, "/containers/abc/attach"},
		{"not attach", HTTPMatchResult{Decision: types.DecisionApprove, RuleID: "http[0]"}, "/containers/abc/exec"},
	} {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.res, srv.ownedAttach(tc.res, tc.path))
		})
	}
	require.Equal(s.T(),
		HTTPMatchResult{Decision: types.DecisionAllow, RuleID: "owned-attach"},
		srv.ownedAttach(HTTPMatchResult{Decision: types.DecisionApprove}, "/containers/abc/attach"))
}

func (s *OwnershipSuite) TestRecordCreatedIgnoresBodiesWithoutID() {
	srv := s.server(&fakeDaemon{}, &fakeApprover{}, nil)
	resp := &http.Response{Request: httptest.NewRequest(http.MethodPost, "/containers/create?name=web", nil)}
	for _, body := range []string{`not json`, `{"Warnings":[]}`} {
		srv.recordCreated(resp, []byte(body))
	}
	require.Empty(s.T(), srv.owned)
}

func (s *OwnershipSuite) TestModifyResponseCreateReadError() {
	srv := s.server(&fakeDaemon{}, &fakeApprover{}, nil)
	resp := &http.Response{
		StatusCode: http.StatusCreated,
		Body:       &errReader{err: errors.New("boom")},
		Request:    httptest.NewRequest(http.MethodPost, "/containers/create", nil),
	}
	require.EqualError(s.T(), srv.modifyResponse(resp), "boom")
}

func (s *OwnershipSuite) TestNamedVolumes() {
	srv := s.server(&fakeDaemon{}, &fakeApprover{}, nil)
	tests := []struct {
		name string
		body any
		want []string
	}{
		{"no body", nil, nil},
		{"no host config", map[string]any{"Image": "a"}, nil},
		{
			"binds",
			map[string]any{"HostConfig": map[string]any{"Binds": []any{"data:/d", "/host:/h", "nested-vol:/var/run", 7}}},
			[]string{"data"},
		},
		{
			"mounts",
			map[string]any{"hostconfig": map[string]any{"mounts": []any{
				map[string]any{"type": "volume", "source": "cache", "target": "/c"},
				map[string]any{"Type": "volume", "Target": "/anon"},
				map[string]any{"Type": "bind", "Source": "/host", "Target": "/h"},
				map[string]any{"Type": "volume", "Source": "nested-vol", "Target": "/var/run"},
				"junk",
			}}},
			[]string{"cache"},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, srv.namedVolumes(tc.body))
		})
	}
}

// A create mounting, by name, a volume bound to a host path asks first.
func (s *OwnershipSuite) TestDeviceVolumeApproval() {
	d := &fakeDaemon{volumes: map[string]string{
		"plain":   `{"Name":"plain","Driver":"local","Options":null}`,
		"tmpfs":   `{"Name":"tmpfs","Driver":"local","Options":{"type":"tmpfs","device":""}}`,
		"hostdir": `{"Name":"hostdir","Driver":"local","Options":{"type":"none","o":"bind","Device":"/etc"}}`,
		"garbled": `{"Options":`,
	}}
	tests := []struct {
		name       string
		body       string
		outcome    types.Decision
		wantStatus int
		wantPrompt string
		wantBody   string
	}{
		{name: "no named volumes", body: `{"Image":"a"}`, wantStatus: http.StatusCreated},
		{name: "unknown volume", body: `{"Image":"a","HostConfig":{"Binds":["fresh:/d"]}}`, wantStatus: http.StatusCreated},
		{name: "plain volume", body: `{"Image":"a","HostConfig":{"Binds":["plain:/d"]}}`, wantStatus: http.StatusCreated},
		{name: "empty device", body: `{"Image":"a","HostConfig":{"Mounts":[{"Type":"volume","Source":"tmpfs","Target":"/t"}]}}`, wantStatus: http.StatusCreated},
		{
			name: "device volume allowed", body: `{"Image":"a","HostConfig":{"Binds":["plain:/p","hostdir:/d"]}}`,
			outcome: types.DecisionAllow, wantStatus: http.StatusCreated, wantPrompt: "docker:POST:body:named-volume-device:hostdir",
		},
		{
			name: "device volume refused", body: `{"Image":"a","HostConfig":{"Mounts":[{"Type":"volume","Source":"hostdir","Target":"/d"}]}}`,
			outcome: types.DecisionDeny, wantStatus: http.StatusForbidden, wantPrompt: "docker:POST:body:named-volume-device:hostdir",
		},
		{
			name: "garbled volume", body: `{"Image":"a","HostConfig":{"Binds":["garbled:/d"]}}`,
			wantStatus: http.StatusForbidden, wantBody: "inspecting volume garbled: unexpected EOF\n",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			approver := &fakeApprover{outcome: agentgate.Outcome{Decision: tc.outcome}}
			auditor := &capturingAuditor{}
			rec := do(s.server(d, approver, auditor), http.MethodPost, "/containers/create", tc.body)
			require.Equal(s.T(), tc.wantStatus, rec.Code, rec.Body.String())
			if tc.wantBody != "" {
				require.Equal(s.T(), tc.wantBody, rec.Body.String())
				snap := auditor.snapshot()
				require.Equal(s.T(), "named-volume-device", snap[len(snap)-1].RuleID)
			}
			if tc.wantPrompt == "" {
				require.Empty(s.T(), approver.calls)
				return
			}
			require.Len(s.T(), approver.calls, 1)
			require.Equal(s.T(), tc.wantPrompt, approver.calls[0].CacheKey)
			require.Equal(s.T(), "container mounts volume hostdir, backed by a host device or path", approver.calls[0].Message)
		})
	}
}

func (s *OwnershipSuite) TestDeviceVolumeLookupFailures() {
	s.Run("daemon error", func() {
		d := &fakeDaemon{status: http.StatusInternalServerError}
		rec := do(s.server(d, &fakeApprover{}, nil), http.MethodPost, "/containers/create", `{"Image":"a","HostConfig":{"Binds":["v:/d"]}}`)
		require.Equal(s.T(), http.StatusForbidden, rec.Code)
		require.Equal(s.T(), "inspecting volume v: status 500\n", rec.Body.String())
	})
	s.Run("daemon unreachable", func() {
		srv := s.serverOn(shortSockPath(s.T(), "none.sock"), &fakeApprover{}, nil)
		rec := do(srv, http.MethodPost, "/containers/create", `{"Image":"a","HostConfig":{"Binds":["v:/d"]}}`)
		require.Equal(s.T(), http.StatusForbidden, rec.Code)
		require.Contains(s.T(), rec.Body.String(), "inspecting volume v: ")
	})
}
