package container

import (
	"bytes"
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

func newStringReader(s string) *bytes.Reader { return bytes.NewReader([]byte(s)) }

// hostDockerBind is the canonical bind createAndStartContainer injects when
// the proxy is enabled: host docker.sock → container .sock.host (read-only).
const hostDockerBind = "/var/run/docker.sock:/var/run/docker.sock.host:ro"

// stubGateRouter satisfies agentgate.BotRouter without touching a real bot.
type stubGateRouter struct{ bot agentgate.Bot }

func (r *stubGateRouter) For(string) agentgate.Bot { return r.bot }

type stubGateBot struct{}

func (stubGateBot) SendApproval(context.Context, string, agentgate.ApprovalRequest) (string, error) {
	return "msg", nil
}
func (stubGateBot) RemoveApproval(context.Context, string, string) error { return nil }

type ProxySuite struct {
	suite.Suite

	client *MockDockerClient
	runner *DockerRunner
}

func TestProxySuite(t *testing.T) {
	suite.Run(t, new(ProxySuite))
}

func (s *ProxySuite) SetupTest() {
	s.client = new(MockDockerClient)
	s.runner = NewDockerRunner(s.client, &config.Config{}, nil)
}

// --- SetDockerProxyDeps / PolicyDir ---

func (s *ProxySuite) TestInstanceIDReturnsField() {
	s.runner.instanceID = "inst-xyz"
	require.Equal(s.T(), "inst-xyz", s.runner.InstanceID())
}

func (s *ProxySuite) TestSetDockerProxyDepsStoresFields() {
	s.runner.SetDockerProxyDeps("/tmp/policies", "/var/run/docker.sock")
	require.Equal(s.T(), "/tmp/policies", s.runner.policyDir)
	require.Equal(s.T(), "/var/run/docker.sock", s.runner.hostDockerSock)
}

func (s *ProxySuite) TestPolicyDirReturnsStoredValue() {
	s.runner.SetDockerProxyDeps("/custom/dir", "")
	require.Equal(s.T(), "/custom/dir", s.runner.PolicyDir())
}

// --- writeProxyPolicyFile ---

func (s *ProxySuite) TestWriteProxyPolicyFileDisabledReturnsEmpty() {
	cfg := &config.Config{}
	path, err := s.runner.writeProxyPolicyFile(cfg, "ch-1")
	require.NoError(s.T(), err)
	require.Empty(s.T(), path)
}

func (s *ProxySuite) TestWriteProxyPolicyFileNoPolicyDirReturnsEmpty() {
	// Enabled but policyDir unset — tests exercising other code paths can
	// skip the filesystem setup entirely.
	cfg := &config.Config{Gates: config.GatesConfig{DockerProxy: config.DockerProxyConfig{Enabled: true}}}
	path, err := s.runner.writeProxyPolicyFile(cfg, "ch-1")
	require.NoError(s.T(), err)
	require.Empty(s.T(), path)
}

func (s *ProxySuite) TestWriteProxyPolicyFileMkdirError() {
	sys := newDefaultMockSystem()
	sys.ExpectedCalls = nil
	sys.On("MkdirAll", mock.Anything, mock.Anything).Return(errors.New("eacces"))

	s.runner.sys = sys
	s.runner.policyDir = "/run/loop"
	cfg := &config.Config{Gates: config.GatesConfig{DockerProxy: config.DockerProxyConfig{Enabled: true}}}

	_, err := s.runner.writeProxyPolicyFile(cfg, "ch-1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating policy dir")
}

func (s *ProxySuite) TestWriteProxyPolicyFileWriteError() {
	sys := newDefaultMockSystem()
	sys.ExpectedCalls = nil
	sys.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
	sys.On("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("enospc"))

	s.runner.sys = sys
	s.runner.policyDir = "/run/loop"
	cfg := &config.Config{Gates: config.GatesConfig{DockerProxy: config.DockerProxyConfig{Enabled: true}}}

	_, err := s.runner.writeProxyPolicyFile(cfg, "ch-1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing proxy policy")
}

func (s *ProxySuite) TestNewGateTokenRandReadError() {
	s.runner.osRandRead = func([]byte) (int, error) { return 0, errors.New("entropy") }
	_, err := s.runner.newGateToken()
	require.Error(s.T(), err)
}

func (s *ProxySuite) TestWriteProxyPolicyFileSerialisesConfig() {
	sys := newDefaultMockSystem()
	var captured []byte
	sys.ExpectedCalls = nil // drop the catch-all WriteFile so we can capture.
	sys.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
	sys.On("WriteFile", "/run/loop/ch-1/proxy-policy.json", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = append([]byte(nil), args.Get(1).([]byte)...) }).
		Return(nil)

	s.runner.sys = sys
	s.runner.policyDir = "/run/loop"
	cfg := &config.Config{
		Gates: config.GatesConfig{
			DockerProxy: config.DockerProxyConfig{
				Enabled:         true,
				DefaultDecision: types.DecisionApprove,
				HTTPRules: []types.HTTPServiceRule{{
					Methods:  []string{"GET"},
					Paths:    []string{"/containers/json"},
					Decision: types.DecisionAllow,
				}},
			},
		},
	}

	path, err := s.runner.writeProxyPolicyFile(cfg, "ch-1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "/run/loop/ch-1/proxy-policy.json", path)

	var got config.DockerProxyConfig
	require.NoError(s.T(), json.Unmarshal(captured, &got))
	require.Equal(s.T(), cfg.Gates.DockerProxy.DefaultDecision, got.DefaultDecision)
	require.Equal(s.T(), cfg.Gates.DockerProxy.HTTPRules, got.HTTPRules)
}

// --- End-to-end: Run() with proxy enabled ---

func (s *ProxySuite) proxyRunCfg() *config.Config {
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
	c.Gates.DockerProxy.Enabled = true
	c.Gates.DockerProxy.DefaultDecision = types.DecisionApprove
	return c
}

func (s *ProxySuite) installRunnerDefaults(cfg *config.Config) {
	s.runner = NewDockerRunner(s.client, cfg, nil)
	s.runner.sys = newDefaultMockSystem()
	s.runner.instanceID = "test-instance"
	s.runner.osRandRead = func(b []byte) (int, error) {
		for i := range b {
			b[i] = 0xaa
		}
		return len(b), nil
	}
	s.runner.osTimeLocalName = func() string { return "Local" }
	s.client.On("NetworkEnsure", mock.Anything, mock.Anything).Maybe().Return(nil)
}

func (s *ProxySuite) expectRunCompletes(ctx context.Context, cid string) {
	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)
	s.client.On("ContainerStart", ctx, cid).Return(nil)
	s.client.On("ContainerWait", ctx, cid).Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerLogs", ctx, cid).Return(
		newStringReader(`{"type":"result","result":"ok","session_id":"s1","is_error":false}`), nil,
	)
}

// findEnv returns the value of a "KEY=..." entry in env, or "" if missing.
func findEnv(env []string, key string) string {
	prefix := key + "="
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, prefix); ok {
			return v
		}
	}
	return ""
}

func (s *ProxySuite) TestRunProxyEnabledAddsBindsAndEnvAndToken() {
	cfg := s.proxyRunCfg()
	s.installRunnerDefaults(cfg)
	s.runner.SetDockerProxyDeps("/run/loop", "/var/run/docker.sock")

	resolver := agentgate.NewMultiManagerResolver()
	s.runner.SetGateDeps(resolver, &stubGateRouter{bot: stubGateBot{}}, types.RateLimits{})

	ctx := context.Background()
	var captured *ContainerConfig
	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cc *ContainerConfig) bool {
		captured = cc
		return true
	}), "loop-ch-1-aaaaaa").Return("cid-real", nil)
	s.expectRunCompletes(ctx, "cid-real")

	_, err := s.runner.Run(ctx, &agent.AgentRequest{ChannelID: "ch-1"})
	require.NoError(s.T(), err)

	require.NotNil(s.T(), captured)
	require.True(s.T(), slices.Contains(captured.Binds, hostDockerBind),
		"binds should include %q; got %v", hostDockerBind, captured.Binds)
	wantPolicy := "/run/loop/ch-1/proxy-policy.json:/etc/loop/proxy-policy.json:ro"
	require.True(s.T(), slices.Contains(captured.Binds, wantPolicy),
		"binds should include %q; got %v", wantPolicy, captured.Binds)
	require.Equal(s.T(), "1", findEnv(captured.Env, "LOOP_DOCKERPROXY_ENABLED"))
	require.Equal(s.T(), "/etc/loop/proxy-policy.json", findEnv(captured.Env, "LOOP_DOCKERPROXY_POLICY_FILE"))
	require.Equal(s.T(), "/var/run/docker.sock.host", findEnv(captured.Env, "LOOP_DOCKERPROXY_UPSTREAM"))
	require.Equal(s.T(), "ch-1", findEnv(captured.Env, "LOOP_CHANNEL_ID"))
	token := findEnv(captured.Env, "LOOP_GATE_TOKEN")
	require.Len(s.T(), token, 64, "token should be 32 bytes hex-encoded")
	require.Equal(s.T(), "loop-ch-1-aaaaaa", findEnv(captured.Env, "LOOP_CONTAINER_ID"),
		"loop-dockerproxy NewServer rejects empty CID; runner must stamp the container name")

	gotCid, mgr, gotChannel, ok := resolver.ByToken(token)
	require.True(s.T(), ok)
	require.Equal(s.T(), "cid-real", gotCid)
	require.NotNil(s.T(), mgr)
	require.Equal(s.T(), "ch-1", gotChannel)
}

func (s *ProxySuite) TestRunProxyDisabledNoBindsNoToken() {
	cfg := s.proxyRunCfg()
	cfg.Gates.DockerProxy.Enabled = false
	s.installRunnerDefaults(cfg)
	s.runner.SetDockerProxyDeps("/run/loop", "")

	ctx := context.Background()
	var captured *ContainerConfig
	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cc *ContainerConfig) bool {
		captured = cc
		return true
	}), "loop-ch-1-aaaaaa").Return("cid-real", nil)
	s.expectRunCompletes(ctx, "cid-real")

	_, err := s.runner.Run(ctx, &agent.AgentRequest{ChannelID: "ch-1"})
	require.NoError(s.T(), err)

	require.NotNil(s.T(), captured)
	require.False(s.T(), slices.ContainsFunc(captured.Binds, func(b string) bool {
		return strings.HasSuffix(b, ":/var/run/docker.sock.host:ro")
	}))
	require.False(s.T(), slices.ContainsFunc(captured.Env, func(e string) bool {
		return strings.HasPrefix(e, "LOOP_DOCKERPROXY_ENABLED=")
	}))
	require.False(s.T(), slices.ContainsFunc(captured.Env, func(e string) bool {
		return strings.HasPrefix(e, "LOOP_GATE_TOKEN=")
	}))
}

// TestRunProxyDefaultsHostSockWhenEmpty covers the "hostSock == """ branch
// in createAndStartContainer — when SetDockerProxyDeps leaves the host
// socket path blank, createAndStartContainer falls back to /var/run/docker.sock.
func (s *ProxySuite) TestRunProxyDefaultsHostSockWhenEmpty() {
	cfg := s.proxyRunCfg()
	s.installRunnerDefaults(cfg)
	s.runner.SetDockerProxyDeps("/run/loop", "") // empty hostSock → default

	resolver := agentgate.NewMultiManagerResolver()
	s.runner.SetGateDeps(resolver, &stubGateRouter{bot: stubGateBot{}}, types.RateLimits{})

	ctx := context.Background()
	var captured *ContainerConfig
	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cc *ContainerConfig) bool {
		captured = cc
		return true
	}), "loop-ch-1-aaaaaa").Return("cid-real", nil)
	s.expectRunCompletes(ctx, "cid-real")

	_, err := s.runner.Run(ctx, &agent.AgentRequest{ChannelID: "ch-1"})
	require.NoError(s.T(), err)
	require.True(s.T(), slices.Contains(captured.Binds, hostDockerBind),
		"binds should include default %q; got %v", hostDockerBind, captured.Binds)
}

// TestRunGateTokenErrorFailsRun covers the newGateToken error branch in
// createAndStartContainer. The token is generated unconditionally, so
// disabling the gate/proxy still exercises it.
func (s *ProxySuite) TestRunGateTokenErrorFailsRun() {
	cfg := s.proxyRunCfg()
	cfg.Gates.DockerProxy.Enabled = false
	s.installRunnerDefaults(cfg)
	s.runner.osRandRead = func([]byte) (int, error) { return 0, errors.New("entropy") }

	ctx := context.Background()
	_, err := s.runner.Run(ctx, &agent.AgentRequest{ChannelID: "ch-1"})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "generating gate token")
}

// TestRunProxyPolicyWriteErrorFailsRun covers the writeProxyPolicyFile
// error branch in createAndStartContainer.
func (s *ProxySuite) TestRunProxyPolicyWriteErrorFailsRun() {
	cfg := s.proxyRunCfg()
	s.installRunnerDefaults(cfg)
	s.runner.SetDockerProxyDeps("/run/loop", "/var/run/docker.sock")
	// Swap sys for one whose MkdirAll errors on the policy dir.
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

// --- SetGateDeps ---

func (s *ProxySuite) TestSetGateDepsStoresFields() {
	resolver := agentgate.NewMultiManagerResolver()
	router := &stubGateRouter{bot: stubGateBot{}}
	limits := types.RateLimits{Pending: 7, PerMinute: 11, Total: 13}

	s.runner.SetGateDeps(resolver, router, limits)
	require.Same(s.T(), resolver, s.runner.gateResolver)
	require.Equal(s.T(), router, s.runner.gateBotRouter)
	require.Equal(s.T(), limits, s.runner.gateRateLimits)
}

// --- ContainerRemove ---

func (s *ProxySuite) TestContainerRemoveDeregistersFromResolver() {
	resolver := agentgate.NewMultiManagerResolver()
	mgr := agentgate.NewManager(&stubGateRouter{bot: stubGateBot{}}, types.RateLimits{})
	resolver.AddWithToken("cid-1", "tok-1", mgr, "ch-1")
	s.runner.gateResolver = resolver

	ctx := context.Background()
	s.client.On("ContainerRemove", ctx, "cid-1").Return(nil)

	require.NoError(s.T(), s.runner.ContainerRemove(ctx, "cid-1"))
	_, _, _, ok := resolver.ByToken("tok-1")
	require.False(s.T(), ok, "token should be freed after ContainerRemove")
}

func (s *ProxySuite) TestContainerRemoveNoResolverIsFine() {
	ctx := context.Background()
	s.client.On("ContainerRemove", ctx, "cid-1").Return(nil)
	require.NoError(s.T(), s.runner.ContainerRemove(ctx, "cid-1"))
}

func (s *ProxySuite) TestContainerRemoveClientErrorPropagates() {
	boom := errors.New("docker remove failed")
	ctx := context.Background()
	s.client.On("ContainerRemove", ctx, "cid-1").Return(boom)
	require.ErrorIs(s.T(), s.runner.ContainerRemove(ctx, "cid-1"), boom)
}

// --- filterProxySockConflicts ---

func (s *ProxySuite) TestFilterProxySockConflictsDropsTargetBind() {
	in := []string{
		"/home/u/.claude:/home/u/.claude",
		"/var/run/docker.sock:/var/run/docker.sock",
		"/some/other:/var/run/docker.sock:rw",
		"/var/run/docker.sock:/var/run/docker.sock.host:ro",
	}
	out := filterProxySockConflicts(in)
	require.Equal(s.T(), []string{
		"/home/u/.claude:/home/u/.claude",
		"/var/run/docker.sock:/var/run/docker.sock.host:ro",
	}, out)
}

func (s *ProxySuite) TestFilterProxySockConflictsKeepsMalformed() {
	in := []string{"not-a-mount", "/a:/b"}
	require.Equal(s.T(), in, filterProxySockConflicts(in))
}

// TestRunProxyStripsUserDockerSockMount guards against the address-already-
// in-use bind failure: a user-configured /var/run/docker.sock mount must be
// filtered out before the proxy's own binds are appended.
func (s *ProxySuite) TestRunProxyStripsUserDockerSockMount() {
	cfg := s.proxyRunCfg()
	cfg.Mounts = []string{"/var/run/docker.sock:/var/run/docker.sock"}
	s.installRunnerDefaults(cfg)
	s.runner.SetDockerProxyDeps("/run/loop", "/var/run/docker.sock")

	resolver := agentgate.NewMultiManagerResolver()
	s.runner.SetGateDeps(resolver, &stubGateRouter{bot: stubGateBot{}}, types.RateLimits{})

	ctx := context.Background()
	var captured *ContainerConfig
	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cc *ContainerConfig) bool {
		captured = cc
		return true
	}), "loop-ch-1-aaaaaa").Return("cid-real", nil)
	s.expectRunCompletes(ctx, "cid-real")

	_, err := s.runner.Run(ctx, &agent.AgentRequest{ChannelID: "ch-1"})
	require.NoError(s.T(), err)
	require.NotNil(s.T(), captured)
	require.False(s.T(), slices.Contains(captured.Binds, "/var/run/docker.sock:/var/run/docker.sock"),
		"user mount targeting /var/run/docker.sock must be stripped when proxy is enabled; got %v", captured.Binds)
	require.True(s.T(), slices.Contains(captured.Binds, hostDockerBind),
		"proxy's own host-sock bind must still be present; got %v", captured.Binds)
}

// --- SecurityOpt ---

// TestRunGateEnabledSetsSeccompAndPtraceCap guards against the EPERM failure
// when the in-container seccomp installer runs under Docker's default outer
// profile, and against the separate EPERM from the gate failing to read the
// traced child's memory. The runner must drop the outer seccomp profile AND
// add SYS_PTRACE so process_vm_readv works across the root→agent uid drop.
func (s *ProxySuite) TestRunGateEnabledSetsSeccompAndPtraceCap() {
	cfg := s.proxyRunCfg()
	cfg.Gates.Agentgate.Enabled = true
	s.installRunnerDefaults(cfg)
	s.runner.SetDockerProxyDeps("/run/loop", "/var/run/docker.sock")

	resolver := agentgate.NewMultiManagerResolver()
	s.runner.SetGateDeps(resolver, &stubGateRouter{bot: stubGateBot{}}, types.RateLimits{})

	ctx := context.Background()
	var captured *ContainerConfig
	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cc *ContainerConfig) bool {
		captured = cc
		return true
	}), "loop-ch-1-aaaaaa").Return("cid-real", nil)
	s.expectRunCompletes(ctx, "cid-real")

	_, err := s.runner.Run(ctx, &agent.AgentRequest{ChannelID: "ch-1"})
	require.NoError(s.T(), err)
	require.NotNil(s.T(), captured)
	require.Equal(s.T(), []string{"seccomp=unconfined"}, captured.SecurityOpt)
	require.Equal(s.T(), []string{"SYS_PTRACE"}, captured.CapAdd)
}

func (s *ProxySuite) TestRunGateDisabledLeavesSandboxUntouched() {
	cfg := s.proxyRunCfg()
	cfg.Gates.Agentgate.Enabled = false
	cfg.Gates.DockerProxy.Enabled = false
	s.installRunnerDefaults(cfg)

	ctx := context.Background()
	var captured *ContainerConfig
	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cc *ContainerConfig) bool {
		captured = cc
		return true
	}), "loop-ch-1-aaaaaa").Return("cid-real", nil)
	s.expectRunCompletes(ctx, "cid-real")

	_, err := s.runner.Run(ctx, &agent.AgentRequest{ChannelID: "ch-1"})
	require.NoError(s.T(), err)
	require.NotNil(s.T(), captured)
	require.Empty(s.T(), captured.SecurityOpt)
	require.Empty(s.T(), captured.CapAdd)
}
