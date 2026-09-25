package dockerproxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/agentgate"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/types"
)

type FoldSuite struct {
	suite.Suite
}

func TestFoldSuite(t *testing.T) {
	suite.Run(t, new(FoldSuite))
}

// The default body rules must see fields whatever their case, since the
// daemon honours {"hostconfig":{"privileged":true}}.
func (s *FoldSuite) TestDefaultRulesMatchAnyCase() {
	cases := []struct {
		name       string
		path       string
		body       string
		wantStatus int
		wantRule   string
	}{
		{name: "lowercase privileged", path: "/containers/create", body: `{"Image":"a","hostconfig":{"privileged":true}}`, wantStatus: http.StatusForbidden},
		{name: "mixed case cap add", path: "/containers/create", body: `{"HOSTCONFIG":{"capAdd":["SYS_ADMIN"]}}`, wantStatus: http.StatusForbidden},
		{name: "lowercase bind source", path: "/containers/create", body: `{"hostconfig":{"mounts":[{"type":"bind","source":"/etc","target":"/x"}]}}`, wantStatus: http.StatusForbidden},
		{name: "lowercase update", path: "/containers/abc/update", body: `{"privileged":true}`, wantStatus: http.StatusForbidden},
		{name: "ambiguous rule key", path: "/containers/abc/update", body: `{"Privileged":false,"privileged":true}`, wantStatus: http.StatusBadRequest, wantRule: "body-eval-error"},
		{name: "ambiguous create key", path: "/containers/create", body: `{"HostConfig":{"Privileged":false,"privileged":true}}`, wantStatus: http.StatusBadRequest, wantRule: "create-body"},
		{name: "ambiguous details key", path: "/containers/create", body: `{"Image":"a","image":"b"}`, wantStatus: http.StatusBadRequest, wantRule: "create-body"},
		{name: "label keys may differ in case", path: "/containers/create", body: `{"Image":"a","Labels":{"foo":"1","FOO":"2"}}`, wantStatus: http.StatusCreated},
		{name: "plain create", path: "/containers/create", body: `{"image":"a","hostconfig":{"binds":["/work:/work"]}}`, wantStatus: http.StatusCreated},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			sock, stop := upstreamUnix(s.T(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusCreated)
			}))
			defer stop()
			policy, err := CompilePolicy(types.DecisionAllow, config.DefaultDockerProxyHTTPRules(), config.DefaultDockerProxyBodyRules())
			require.NoError(s.T(), err)
			auditor := &capturingAuditor{}
			srv, err := NewServer(ServerConfig{CID: "cid-1", Policy: policy, Approver: &fakeApprover{}, DockerSock: sock, Auditor: auditor, NestedVolume: "vol-1"})
			require.NoError(s.T(), err)

			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			srv.ServeHTTP(rr, req)

			require.Equal(s.T(), tc.wantStatus, rr.Code, rr.Body.String())
			if tc.wantRule != "" {
				snap := auditor.snapshot()
				require.Len(s.T(), snap, 1)
				require.Equal(s.T(), tc.wantRule, snap[0].RuleID)
				require.Equal(s.T(), errAmbiguousKeys.Error(), snap[0].Reason)
			}
		})
	}
}

// Approval prompts summarise the fields the daemon will use, whatever
// their case.
func (s *FoldSuite) TestApprovalDetailsAnyCase() {
	sock, stop := upstreamUnix(s.T(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer stop()
	policy, err := CompilePolicy(types.DecisionAllow, nil, []types.BodyRule{{
		AppliesTo:    "POST ^/containers/create$",
		MaxBodyBytes: 1024,
		JSONChecks:   []types.JSONCheck{{Path: "image", Op: "present"}},
		Decision:     types.DecisionApprove,
	}})
	require.NoError(s.T(), err)
	ap := &fakeApprover{outcome: agentgate.Outcome{Decision: types.DecisionAllow}}
	srv, err := NewServer(ServerConfig{CID: "cid-1", Policy: policy, Approver: ap, DockerSock: sock})
	require.NoError(s.T(), err)

	body := `{"image":"alpine","cmd":["sh"],"hostconfig":{"privileged":true,"binds":["/w:/w"]}}`
	req := httptest.NewRequest(http.MethodPost, "/containers/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	require.Equal(s.T(), http.StatusCreated, rr.Code)
	require.Len(s.T(), ap.calls, 1)
	require.Equal(s.T(), map[string]string{"image": "alpine", "cmd": "sh", "privileged": "true", "binds": "/w:/w"}, ap.calls[0].Details)
}

func (s *FoldSuite) TestHasFoldDuplicates() {
	names := map[string]bool{"hostconfig": true, "binds": true}
	cases := []struct {
		name string
		v    any
		want bool
	}{
		{name: "none", v: map[string]any{"HostConfig": map[string]any{"Binds": []any{"a"}}}, want: false},
		{name: "top level", v: map[string]any{"HostConfig": 1, "hostconfig": 2}, want: true},
		{name: "nested", v: map[string]any{"HostConfig": map[string]any{"Binds": 1, "BINDS": 2}}, want: true},
		{name: "inside array", v: []any{map[string]any{"binds": 1, "Binds": 2}}, want: true},
		{name: "unwatched name", v: map[string]any{"Labels": 1, "labels": 2}, want: false},
		{name: "scalar", v: "x", want: false},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, hasFoldDuplicates(tc.v, names))
		})
	}
}

func (s *FoldSuite) TestFoldHelpers() {
	m := map[string]any{"hostConfig": "x", "Flag": true}
	k, ok := foldKey(m, "HostConfig")
	require.True(s.T(), ok)
	require.Equal(s.T(), "hostConfig", k)
	_, ok = foldKey(m, "Binds")
	require.False(s.T(), ok)
	require.Equal(s.T(), "x", foldString(m, "HOSTCONFIG"))
	require.Empty(s.T(), foldString(m, "flag"))
	v, ok := foldGet(m, "flag")
	require.True(s.T(), ok)
	require.Equal(s.T(), true, v)
}

// The default create and update rules, as the daemon would read the body.
func (s *FoldSuite) TestDefaultRulesCapsNamespacesRuntime() {
	cases := []struct {
		name     string
		path     string
		body     string
		decision types.Decision
	}{
		{name: "cap lowercase with prefix", path: "/containers/create", body: `{"HostConfig":{"CapAdd":["cap_sys_admin"]}}`, decision: types.DecisionDeny},
		{name: "cap mixed case", path: "/containers/create", body: `{"HostConfig":{"CapAdd":["Sys_Ptrace"]}}`, decision: types.DecisionDeny},
		{name: "cap all", path: "/containers/create", body: `{"HostConfig":{"CapAdd":["ALL"]}}`, decision: types.DecisionDeny},
		{name: "cap net admin", path: "/containers/create", body: `{"HostConfig":{"CapAdd":["NET_ADMIN"]}}`, decision: types.DecisionDeny},
		{name: "allowed caps", path: "/containers/create", body: `{"HostConfig":{"CapAdd":["CAP_IPC_LOCK","sys_nice","CHOWN"]}}`},
		{name: "cgroupns host", path: "/containers/create", body: `{"HostConfig":{"CgroupnsMode":"host"}}`, decision: types.DecisionDeny},
		{name: "cgroupns private", path: "/containers/create", body: `{"HostConfig":{"CgroupnsMode":"private"}}`},
		{name: "uts host", path: "/containers/create", body: `{"HostConfig":{"UTSMode":"host"}}`, decision: types.DecisionDeny},
		{name: "cgroup parent", path: "/containers/create", body: `{"HostConfig":{"CgroupParent":"/"}}`, decision: types.DecisionDeny},
		{name: "other runtime", path: "/containers/create", body: `{"HostConfig":{"Runtime":"runsc-debug"}}`, decision: types.DecisionDeny},
		{name: "stock runtime", path: "/containers/create", body: `{"HostConfig":{"Runtime":"runc"}}`},
		{name: "empty runtime", path: "/containers/create", body: `{"HostConfig":{"Runtime":""}}`},
		{name: "pid of another container", path: "/containers/create", body: `{"HostConfig":{"PidMode":"container:loop-x"}}`, decision: types.DecisionApprove},
		{name: "ipc of another container", path: "/containers/create", body: `{"HostConfig":{"IpcMode":"container:loop-x"}}`, decision: types.DecisionApprove},
		{name: "network of another container", path: "/containers/create", body: `{"HostConfig":{"NetworkMode":"container:loop-x"}}`},
		{name: "update cap", path: "/containers/abc/update", body: `{"CapAdd":["cap_sys_module"]}`, decision: types.DecisionDeny},
		{name: "update allowed cap", path: "/containers/abc/update", body: `{"CapAdd":["KILL"]}`},
	}
	policy, err := CompilePolicy(types.DecisionAllow, config.DefaultDockerProxyHTTPRules(), config.DefaultDockerProxyBodyRules())
	require.NoError(s.T(), err)
	for _, tc := range cases {
		s.Run(tc.name, func() {
			var body any
			require.NoError(s.T(), json.Unmarshal([]byte(tc.body), &body))
			res := policy.CheckBody(http.MethodPost, tc.path, "application/json", body)
			if tc.decision == "" {
				require.False(s.T(), res.Fired, "rule %s fired", res.RuleID)
				return
			}
			require.True(s.T(), res.Fired)
			require.Equal(s.T(), tc.decision, res.Decision)
		})
	}
}

func (s *FoldSuite) TestCapabilityNotInRequiresValues() {
	_, err := CompilePolicy(types.DecisionAllow, nil, []types.BodyRule{{
		AppliesTo:  "POST ^/x$",
		JSONChecks: []types.JSONCheck{{Path: "CapAdd[*]", Op: "capability_not_in"}},
		Decision:   types.DecisionDeny,
	}})
	require.ErrorContains(s.T(), err, `op "capability_not_in" requires at least one value`)
}
