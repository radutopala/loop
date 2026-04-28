package container

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/agentgate"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/testutil"
	"github.com/radutopala/loop/internal/types"
)

type GateSuite struct {
	suite.Suite

	client *MockDockerClient
	runner *DockerRunner
}

func TestGateSuite(t *testing.T) {
	suite.Run(t, new(GateSuite))
}

func (s *GateSuite) SetupTest() {
	s.client = new(MockDockerClient)
	s.runner = NewDockerRunner(s.client, &config.Config{}, nil)
}

// newCompiledPolicy returns a fresh default-allow Policy — callers keep
// CompilePolicy as a black-box dep.
func (s *GateSuite) newCompiledPolicy() *agentgate.Policy {
	p, err := agentgate.CompilePolicy(types.DecisionAllow, nil, nil, nil)
	require.NoError(s.T(), err)
	return p
}

// --- SetGatePolicy ---

func (s *GateSuite) TestSetGatePolicyStoresFields() {
	policy := s.newCompiledPolicy()
	s.runner.SetGatePolicy(policy, "/custom/dir")

	require.Same(s.T(), policy, s.runner.gatePolicy)
	require.Equal(s.T(), "/custom/dir", s.runner.policyDir)
}

func (s *GateSuite) TestSetGatePolicyEmptyDirLeavesExisting() {
	// policyDir was set via SetDockerProxyDeps; SetGatePolicy("") must not
	// clobber it — the proxy path already configured it.
	s.runner.SetDockerProxyDeps("/shared/dir", "")
	s.runner.SetGatePolicy(s.newCompiledPolicy(), "")
	require.Equal(s.T(), "/shared/dir", s.runner.policyDir)
}

func (s *GateSuite) TestSetGatePolicyNilClearsPolicy() {
	s.runner.SetGatePolicy(s.newCompiledPolicy(), "x")
	s.runner.SetGatePolicy(nil, "")
	require.Nil(s.T(), s.runner.gatePolicy)
}

// --- writeGatePolicyFile ---

func (s *GateSuite) TestWriteGatePolicyFileDisabledReturnsEmpty() {
	path, err := s.runner.writeGatePolicyFile(&config.Config{}, "ch-1", "", "")
	require.NoError(s.T(), err)
	require.Empty(s.T(), path)
}

func (s *GateSuite) TestWriteGatePolicyFileNoPolicyDirReturnsEmpty() {
	cfg := &config.Config{Gates: config.GatesConfig{Agentgate: config.AgentgateConfig{Enabled: true}}}
	path, err := s.runner.writeGatePolicyFile(cfg, "ch-1", "", "")
	require.NoError(s.T(), err)
	require.Empty(s.T(), path)
}

func (s *GateSuite) TestWriteGatePolicyFileMkdirError() {
	sys := newDefaultMockSystem()
	sys.ExpectedCalls = nil
	sys.On("MkdirAll", mock.Anything, mock.Anything).Return(errors.New("eacces"))

	s.runner.sys = sys
	s.runner.policyDir = "/run/loop"
	cfg := &config.Config{Gates: config.GatesConfig{Agentgate: config.AgentgateConfig{Enabled: true}}}

	_, err := s.runner.writeGatePolicyFile(cfg, "ch-1", "", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating policy dir")
}

func (s *GateSuite) TestWriteGatePolicyFileWriteError() {
	sys := newDefaultMockSystem()
	sys.ExpectedCalls = nil
	sys.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
	sys.On("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("enospc"))

	s.runner.sys = sys
	s.runner.policyDir = "/run/loop"
	cfg := &config.Config{Gates: config.GatesConfig{Agentgate: config.AgentgateConfig{Enabled: true}}}

	_, err := s.runner.writeGatePolicyFile(cfg, "ch-1", "", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing gate policy")
}

func (s *GateSuite) TestWriteGatePolicyFileSerialisesSubset() {
	sys := newDefaultMockSystem()
	var captured []byte
	sys.ExpectedCalls = nil
	sys.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
	sys.On("WriteFile", "/run/loop/ch-1/gate-policy.json", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = append([]byte(nil), args.Get(1).([]byte)...) }).
		Return(nil)

	s.runner.sys = sys
	s.runner.policyDir = "/run/loop"
	cfg := &config.Config{
		Gates: config.GatesConfig{
			Agentgate: config.AgentgateConfig{
				Enabled:         true,
				DefaultDecision: types.DecisionAllow,
				PathRules: []types.PathRule{{
					Pattern:  "/etc/secret",
					Decision: types.DecisionDeny,
				}},
			},
		},
	}

	path, err := s.runner.writeGatePolicyFile(cfg, "ch-1", "", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "/run/loop/ch-1/gate-policy.json", path)

	// Unmarshal into the same shape cmd/loop-syscallwrap decodes.
	var got struct {
		DefaultDecision types.Decision      `json:"default_decision"`
		PathRules       []types.PathRule    `json:"path_rules"`
		CommandRules    []types.CommandRule `json:"command_rules"`
		FileRules       []types.FileRule    `json:"file_rules"`
	}
	require.NoError(s.T(), json.Unmarshal(captured, &got))
	require.Equal(s.T(), cfg.Gates.Agentgate.DefaultDecision, got.DefaultDecision)
	require.Equal(s.T(), cfg.Gates.Agentgate.PathRules, got.PathRules)
}

// --- ensureGateAuditDir ---

func (s *GateSuite) TestEnsureGateAuditDirNoPolicyDirReturnsEmpty() {
	path, err := s.runner.ensureGateAuditDir("ch1", "")
	require.NoError(s.T(), err)
	require.Empty(s.T(), path)
}

// The dir is keyed by channel/thread (not container), so every restart of
// the same channel reuses /run/loop/<channel>/audit/ and the rotating jsonl
// files accumulate one history per channel instead of fragmenting into
// thousands of per-spawn dirs. Channel is the primary key so all per-channel
// artifacts live under one tree.
func (s *GateSuite) TestEnsureGateAuditDirCreatesAndReturnsPath() {
	sys := newDefaultMockSystem()
	sys.ExpectedCalls = nil
	sys.On("MkdirAll", "/run/loop/ch1/audit", os.FileMode(0o770)).Return(nil)

	s.runner.sys = sys
	s.runner.policyDir = "/run/loop"

	path, err := s.runner.ensureGateAuditDir("ch1", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "/run/loop/ch1/audit", path)
	sys.AssertExpectations(s.T())
}

// With no channelID, fall back to the dirPath basename so ad-hoc one-shot
// runs still get a stable (per-worktree) audit dir.
func (s *GateSuite) TestEnsureGateAuditDirFallsBackToDirBasenameWhenNoChannel() {
	sys := newDefaultMockSystem()
	sys.ExpectedCalls = nil
	sys.On("MkdirAll", "/run/loop/wt-abc/audit", os.FileMode(0o770)).Return(nil)

	s.runner.sys = sys
	s.runner.policyDir = "/run/loop"

	path, err := s.runner.ensureGateAuditDir("", "/some/path/wt-abc")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "/run/loop/wt-abc/audit", path)
	sys.AssertExpectations(s.T())
}

func (s *GateSuite) TestEnsureGateAuditDirMkdirError() {
	sys := newDefaultMockSystem()
	sys.ExpectedCalls = nil
	sys.On("MkdirAll", mock.Anything, mock.Anything).Return(errors.New("eacces"))

	s.runner.sys = sys
	s.runner.policyDir = "/run/loop"

	_, err := s.runner.ensureGateAuditDir("ch1", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating audit dir")
}

// Both channelID empty AND dirPath basename degenerate ("/", ".", "") should
// hit the _unkeyed fallback bucket instead of crashing. SanitizeName strips
// the leading underscore, so the on-disk bucket is just "unkeyed".
func (s *GateSuite) TestEnsureGateAuditDirFallsBackToUnkeyedWhenNoBase() {
	sys := newDefaultMockSystem()
	sys.ExpectedCalls = nil
	sys.On("MkdirAll", "/run/loop/unkeyed/audit", os.FileMode(0o770)).Return(nil)

	s.runner.sys = sys
	s.runner.policyDir = "/run/loop"

	path, err := s.runner.ensureGateAuditDir("", "/")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "/run/loop/unkeyed/audit", path)
	sys.AssertExpectations(s.T())
}

// --- AuditDir ---

func (s *GateSuite) TestAuditDirNoPolicyDirReturnsEmpty() {
	s.runner.policyDir = ""
	require.Empty(s.T(), s.runner.AuditDir("ch1"))
}

// Read-only resolver used by the API server to list / read jsonl audit
// files. Mirrors the same path that ensureGateAuditDir creates at spawn.
func (s *GateSuite) TestAuditDirReturnsSanitizedChannelPath() {
	s.runner.policyDir = "/run/loop"
	require.Equal(s.T(), "/run/loop/ch1/audit", s.runner.AuditDir("ch1"))
}

func (s *GateSuite) TestAuditDirEmptyChannelFallsBackToUnkeyed() {
	s.runner.policyDir = "/run/loop"
	require.Equal(s.T(), "/run/loop/unkeyed/audit", s.runner.AuditDir(""))
}

// --- injectWorkspaceRule ---

func (s *GateSuite) TestInjectWorkspaceRuleEmptyWorkDirReturnsInput() {
	in := []types.FileRule{{Paths: []string{"/tmp/**"}, Decision: types.DecisionAllow}}
	out := injectWorkspaceRule(in, "", "")
	require.Equal(s.T(), in, out)
}

func (s *GateSuite) TestInjectWorkspaceRuleInsertsBeforeFirstAllow() {
	in := []types.FileRule{
		{Paths: []string{"**/.ssh/**"}, Decision: types.DecisionDeny, Message: "creds"},
		{Paths: []string{"**/approve-me*"}, Decision: types.DecisionApprove, Message: "approve marker"},
		{Paths: []string{"/tmp/**"}, Decision: types.DecisionAllow, Message: "tmp fast-path"},
	}
	out := injectWorkspaceRule(in, "/host/work", "")

	require.Len(s.T(), out, 4)
	require.Equal(s.T(), types.DecisionDeny, out[0].Decision)
	require.Equal(s.T(), types.DecisionApprove, out[1].Decision)
	require.Equal(s.T(), types.DecisionAllow, out[2].Decision, "workspace allow inserted before tmp fast-path")
	require.Equal(s.T(), "workspace fast-path", out[2].Message)
	require.Equal(s.T(), []string{"/host/work/**"}, out[2].Paths)
	require.Equal(s.T(), "tmp fast-path", out[3].Message)
}

func (s *GateSuite) TestInjectWorkspaceRuleIncludesParentDirPath() {
	in := []types.FileRule{{Paths: []string{"/tmp/**"}, Decision: types.DecisionAllow}}
	out := injectWorkspaceRule(in, "/host/repo/.worktrees/wt-1", "/host/repo")

	require.Len(s.T(), out, 2)
	require.Equal(s.T(), []string{"/host/repo/.worktrees/wt-1/**", "/host/repo/**"}, out[0].Paths)
}

func (s *GateSuite) TestInjectWorkspaceRuleSkipsParentWhenEqual() {
	in := []types.FileRule{{Paths: []string{"/tmp/**"}, Decision: types.DecisionAllow}}
	out := injectWorkspaceRule(in, "/host/repo", "/host/repo")

	require.Equal(s.T(), []string{"/host/repo/**"}, out[0].Paths, "parent path deduped when equal to workDir")
}

func (s *GateSuite) TestInjectWorkspaceRuleAppendsWhenNoAllow() {
	in := []types.FileRule{
		{Paths: []string{"**/.ssh/**"}, Decision: types.DecisionDeny},
	}
	out := injectWorkspaceRule(in, "/host/work", "")

	require.Len(s.T(), out, 2)
	require.Equal(s.T(), types.DecisionDeny, out[0].Decision)
	require.Equal(s.T(), types.DecisionAllow, out[1].Decision, "appended at end when no existing Allow")
}

// --- End-to-end Run() with gate enabled ---

func (s *GateSuite) gateRunCfg() *config.Config {
	c := &config.Config{
		ClaudeBinPath:      "claude",
		ContainerImage:     "loop-agent:latest",
		ContainerMemoryMB:  512,
		ContainerCPUs:      1.0,
		APIAddr:            ":8222",
		LoopDir:            "/home/testuser/.loop",
		ContainerKeepAlive: 1,
	}
	c.Browser.Enabled = true
	c.Gates.Agentgate.Enabled = true
	c.Gates.Agentgate.DefaultDecision = types.DecisionAllow
	return c
}

func (s *GateSuite) installRunnerDefaults(cfg *config.Config) {
	s.runner = NewDockerRunner(s.client, cfg, nil)
	s.runner.sys = newDefaultMockSystem()
	s.runner.instanceID = "test-instance"
	s.runner.osRandRead = func(b []byte) (int, error) {
		for i := range b {
			b[i] = 0xbb
		}
		return len(b), nil
	}
	s.runner.osTimeLocalName = func() string { return "Local" }
	s.client.On("NetworkEnsure", mock.Anything, mock.Anything).Maybe().Return(nil)
}

func (s *GateSuite) expectRunCompletes(ctx context.Context, cid string) {
	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)
	s.client.On("ContainerStart", ctx, cid).Return(nil)
	s.client.On("ContainerWait", ctx, cid).Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerLogs", ctx, cid).Return(
		newStringReader(`{"type":"result","result":"ok","session_id":"s1","is_error":false}`), nil,
	)
}

func (s *GateSuite) TestRunGateEnabledWritesPolicyFileAndBindsIt() {
	cfg := s.gateRunCfg()
	s.installRunnerDefaults(cfg)
	s.runner.SetGatePolicy(s.newCompiledPolicy(), "/run/loop")

	resolver := agentgate.NewMultiManagerResolver()
	s.runner.SetGateDeps(resolver, &stubGateRouter{bot: stubGateBot{}}, types.RateLimits{})

	ctx := context.Background()
	var captured *ContainerConfig
	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cc *ContainerConfig) bool {
		captured = cc
		return true
	}), "loop-ch-1-bbbbbb").Return("cid-real", nil)
	s.expectRunCompletes(ctx, "cid-real")

	_, err := s.runner.Run(ctx, &agent.AgentRequest{ChannelID: "ch-1"})
	require.NoError(s.T(), err)
	require.NotNil(s.T(), captured)

	wantBind := "/run/loop/ch-1/gate-policy.json:/etc/loop/gate-policy.json:ro"
	require.True(s.T(), slices.Contains(captured.Binds, wantBind),
		"binds should include %q; got %v", wantBind, captured.Binds)
	require.False(s.T(), slices.ContainsFunc(captured.Binds, func(b string) bool {
		return strings.Contains(b, "gate.sock")
	}), "no socket bind-mounts — gate is now purely in-container")

	require.Equal(s.T(), "1", findEnv(captured.Env, "LOOP_GATE_ENABLED"))
	require.Equal(s.T(), "/etc/loop/gate-policy.json", findEnv(captured.Env, "LOOP_GATE_POLICY_FILE"))
	require.Equal(s.T(), "ch-1", findEnv(captured.Env, "LOOP_CHANNEL_ID"))
	token := findEnv(captured.Env, "LOOP_GATE_TOKEN")
	require.Len(s.T(), token, 64)
	require.Equal(s.T(), "loop-ch-1-bbbbbb", findEnv(captured.Env, "LOOP_CONTAINER_ID"))

	// Audit dir is keyed by channel (not containerName), so restarts of the
	// same channel share one accumulating audit history.
	wantAuditBind := "/run/loop/ch-1/audit:/var/log/loop-gate:rw"
	require.True(s.T(), slices.Contains(captured.Binds, wantAuditBind),
		"binds should include audit bind %q; got %v", wantAuditBind, captured.Binds)
	require.Equal(s.T(), "/var/log/loop-gate", findEnv(captured.Env, "LOOP_GATE_AUDIT_DIR"))
	require.Equal(s.T(), "0", findEnv(captured.Env, "LOOP_GATE_AUDIT_RETENTION_DAYS"),
		"retention defaults to 0 when cfg.Gates.Audit.RetentionDays is unset in this test cfg")
	require.Equal(s.T(), "", findEnv(captured.Env, "LOOP_GATE_AUDIT_VERBOSE"),
		"verbose defaults to unset when cfg.Gates.Audit.Verbose is false")

	gotCid, mgr, gotChannel, ok := resolver.ByToken(token)
	require.True(s.T(), ok)
	require.Equal(s.T(), "cid-real", gotCid)
	require.NotNil(s.T(), mgr)
	require.Equal(s.T(), "ch-1", gotChannel)
}

// TestRunGateAuditVerboseSetsEnv asserts that cfg.Gates.Audit.Verbose=true
// surfaces as LOOP_GATE_AUDIT_VERBOSE=1 in the container env. The in-container
// loop-syscallwrap parent reads this to decide whether the FileAuditor drops
// silent allows or logs every decision.
func (s *GateSuite) TestRunGateAuditVerboseSetsEnv() {
	cfg := s.gateRunCfg()
	cfg.Gates.Audit.Verbose = true
	s.installRunnerDefaults(cfg)
	s.runner.SetGatePolicy(s.newCompiledPolicy(), "/run/loop")

	resolver := agentgate.NewMultiManagerResolver()
	s.runner.SetGateDeps(resolver, &stubGateRouter{bot: stubGateBot{}}, types.RateLimits{})

	ctx := context.Background()
	var captured *ContainerConfig
	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cc *ContainerConfig) bool {
		captured = cc
		return true
	}), "loop-ch-1-bbbbbb").Return("cid-real", nil)
	s.expectRunCompletes(ctx, "cid-real")

	_, err := s.runner.Run(ctx, &agent.AgentRequest{ChannelID: "ch-1"})
	require.NoError(s.T(), err)
	require.NotNil(s.T(), captured)
	require.Equal(s.T(), "1", findEnv(captured.Env, "LOOP_GATE_AUDIT_VERBOSE"))
}

// TestRunGatePolicyWriteErrorFailsRun covers the writeGatePolicyFile error
// branch in createAndStartContainer when the gate is enabled but the policy
// dir MkdirAll fails.
func (s *GateSuite) TestRunGatePolicyWriteErrorFailsRun() {
	cfg := s.gateRunCfg()
	s.installRunnerDefaults(cfg)
	s.runner.SetGatePolicy(s.newCompiledPolicy(), "/run/loop")

	sys := new(testutil.MockSystem)
	sys.On("MkdirAll", "/run/loop/ch-1", mock.Anything).Return(errors.New("eacces"))
	sys.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
	sys.On("Getenv", "USER").Return("testuser")
	sys.On("Getenv", mock.Anything).Return("")
	sys.On("Getuid").Return(1000)
	sys.On("Getgid").Return(1000)
	sys.On("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("Stat", mock.Anything).Return(nil, os.ErrNotExist)
	sys.On("ExecCommandOutput", mock.Anything, mock.Anything).Return([]byte{}, nil)
	sys.On("Readlink", mock.Anything).Return("", os.ErrNotExist)
	sys.On("ReadFile", mock.Anything).Return(nil, os.ErrNotExist)
	sys.On("Remove", mock.Anything).Return(nil)
	s.runner.sys = sys

	ctx := context.Background()
	_, err := s.runner.Run(ctx, &agent.AgentRequest{ChannelID: "ch-1"})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating policy dir")
}

// TestRunGateAuditDirErrorFailsRun covers the ensureGateAuditDir error branch
// in createAndStartContainer: gate is enabled, policy file write succeeds, but
// the audit subdir MkdirAll fails. The whole Run must surface the error.
func (s *GateSuite) TestRunGateAuditDirErrorFailsRun() {
	cfg := s.gateRunCfg()
	s.installRunnerDefaults(cfg)
	s.runner.SetGatePolicy(s.newCompiledPolicy(), "/run/loop")

	sys := new(testutil.MockSystem)
	// Specific error for the channel-keyed audit dir; other MkdirAll calls succeed.
	sys.On("MkdirAll", "/run/loop/ch-1/audit", mock.Anything).Return(errors.New("eacces"))
	sys.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
	sys.On("Getenv", "USER").Return("testuser")
	sys.On("Getenv", mock.Anything).Return("")
	sys.On("Getuid").Return(1000)
	sys.On("Getgid").Return(1000)
	sys.On("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("Stat", mock.Anything).Return(nil, os.ErrNotExist)
	sys.On("ExecCommandOutput", mock.Anything, mock.Anything).Return([]byte{}, nil)
	sys.On("Readlink", mock.Anything).Return("", os.ErrNotExist)
	sys.On("ReadFile", mock.Anything).Return(nil, os.ErrNotExist)
	sys.On("Remove", mock.Anything).Return(nil)
	s.runner.sys = sys

	ctx := context.Background()
	_, err := s.runner.Run(ctx, &agent.AgentRequest{ChannelID: "ch-1"})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating audit dir")
}

func (s *GateSuite) TestRunGateDisabledNoBindNoEnv() {
	cfg := s.gateRunCfg()
	cfg.Gates.Agentgate.Enabled = false
	s.installRunnerDefaults(cfg)
	s.runner.SetGatePolicy(s.newCompiledPolicy(), "/run/loop")

	ctx := context.Background()
	var captured *ContainerConfig
	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cc *ContainerConfig) bool {
		captured = cc
		return true
	}), "loop-ch-1-bbbbbb").Return("cid-real", nil)
	s.expectRunCompletes(ctx, "cid-real")

	_, err := s.runner.Run(ctx, &agent.AgentRequest{ChannelID: "ch-1"})
	require.NoError(s.T(), err)
	require.NotNil(s.T(), captured)

	require.False(s.T(), slices.ContainsFunc(captured.Binds, func(b string) bool {
		return strings.Contains(b, "gate-policy.json")
	}))
	require.False(s.T(), slices.ContainsFunc(captured.Env, func(e string) bool {
		return strings.HasPrefix(e, "LOOP_GATE_ENABLED=")
	}))
}
