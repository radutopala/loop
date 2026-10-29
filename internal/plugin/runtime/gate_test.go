package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agentgate"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/types"
)

func imageInfo(user string, entrypoint, cmd []string) image.InspectResponse {
	return image.InspectResponse{Config: &dockerspec.DockerOCIImageConfig{ImageConfig: ocispec.ImageConfig{User: user, Entrypoint: entrypoint, Cmd: cmd}}}
}

func (s *ManagerSuite) useGate(binErr error) string {
	bin := filepath.Join(s.T().TempDir(), "loop")
	s.m.cfg.Gate = &GateConfig{
		Binary:             func(context.Context) (string, error) { return bin, binErr },
		AuditRetentionDays: 7,
		AuditVerbose:       true,
	}
	return bin
}

func (s *ManagerSuite) TestEnsureUnderGate() {
	bin := s.useGate(nil)
	s.plugin.Manifest.Gate = &plugin.Gate{Files: []types.FileRule{{Paths: []string{"/data/secret"}, Decision: types.DecisionDeny}}}
	s.docker.On("ImageInspect", s.ctx, s.plugin.Image).Return(imageInfo("1000:1000", []string{"/plugin"}, []string{"serve", "-v"}), nil).Once()
	s.expectStart("c1", "40001")
	s.expectRow(db.PluginContainerRunning, "c1", 40001, "")

	_, err := s.m.Ensure(s.ctx, s.plugin, s.channel)
	require.NoError(s.T(), err)

	call := s.docker.Calls[2]
	require.Equal(s.T(), "ContainerCreate", call.Method)
	cfg := call.Arguments.Get(1).(*containertypes.Config)
	host := call.Arguments.Get(2).(*containertypes.HostConfig)
	require.Equal(s.T(), "0", cfg.User)
	require.Equal(s.T(), []string{"/opt/loop/loop", "syscallwrap", "--", "/plugin", "serve", "-v"}, []string(cfg.Entrypoint))
	require.Nil(s.T(), cfg.Cmd)
	require.Subset(s.T(), cfg.Env, []string{
		"LOOP_GATE_ENABLED=1", "LOOP_GATE_POLICY_FILE=/etc/loop/gate-policy.json", "LOOP_GATE_APPROVER=deny",
		"LOOP_HOST_USER=1000:1001", "LOOP_GATE_AUDIT_DIR=/var/log/loop-gate", "LOOP_GATE_AUDIT_RETENTION_DAYS=7",
		"LOOP_GATE_AUDIT_VERBOSE=1", "LOOP_PLUGIN_ID=cogtest",
	})
	require.Equal(s.T(), []string{"no-new-privileges"}, host.SecurityOpt)
	require.Equal(s.T(), []string{"ALL"}, []string(host.CapDrop))
	require.Equal(s.T(), []string{"SYS_PTRACE", "SETUID", "SETGID", "DAC_OVERRIDE"}, []string(host.CapAdd))
	policy := filepath.Join(s.m.cfg.Root, "cogtest", "gate", "ch1", "gate-policy.json")
	audit := filepath.Join(s.m.cfg.Root, "cogtest", "audit", "ch1")
	require.Equal(s.T(), []mount.Mount{
		{Type: mount.TypeBind, Source: bin, Target: "/opt/loop/loop", ReadOnly: true},
		{Type: mount.TypeBind, Source: policy, Target: "/etc/loop/gate-policy.json", ReadOnly: true},
		{Type: mount.TypeBind, Source: audit, Target: "/var/log/loop-gate"},
	}, host.Mounts[2:])
	require.DirExists(s.T(), audit)

	// The policy compiles as the gate loads it, and puts the manifest's
	// rules before Loop's baseline.
	raw, err := os.ReadFile(policy)
	require.NoError(s.T(), err)
	var file gatePolicyFile
	require.NoError(s.T(), json.Unmarshal(raw, &file))
	p, err := agentgate.CompilePolicy(file.DefaultDecision, file.PathRules, file.CommandRules, file.FileRules)
	require.NoError(s.T(), err)
	tests := []struct {
		op, path string
		want     types.Decision
	}{
		{"read", "/data/secret", types.DecisionDeny},
		{"write", "/data/notes.json", types.DecisionAllow},
		{"create", "/tmp/x", types.DecisionAllow},
		{"write", "/dev/null", types.DecisionAllow},
		{"read", "/etc/passwd", types.DecisionAllow},
		{"write", "/etc/passwd", types.DecisionDeny},
		{"delete", "/run/loop/plugin-token", types.DecisionDeny},
	}
	for _, tt := range tests {
		require.Equal(s.T(), tt.want, p.MatchFile(tt.op, tt.path).Decision, "%s %s", tt.op, tt.path)
	}
	require.Equal(s.T(), types.DecisionAllow, p.MatchCommand("node", []string{"x.js"}).Decision)
	require.Equal(s.T(), types.DecisionDeny, p.MatchPath("/var/run/docker.sock").Decision)
	for _, path := range []string{"/data/bin/x", "/tmp/x", "/dev/shm/x"} {
		r, ok := p.MatchExecFile(path)
		require.True(s.T(), ok, path)
		require.Equal(s.T(), types.DecisionDeny, r.Decision, path)
	}
	_, ok := p.MatchExecFile("/plugin")
	require.False(s.T(), ok, "the image's own files run")
}

func (s *ManagerSuite) TestGatePolicyExecDenyComesFirst() {
	g := &plugin.Gate{Files: []types.FileRule{{Paths: []string{"/data/**"}, Operations: []string{agentgate.OpExec}, Decision: types.DecisionAllow}}}
	file := pluginGatePolicy(g)
	p, err := agentgate.CompilePolicy(file.DefaultDecision, file.PathRules, file.CommandRules, file.FileRules)
	require.NoError(s.T(), err)
	r, ok := p.MatchExecFile("/data/x")
	require.True(s.T(), ok)
	require.Equal(s.T(), types.DecisionDeny, r.Decision, "a manifest can't allow running its data")
}

func (s *ManagerSuite) TestGatePolicyCommands() {
	g := &plugin.Gate{Commands: []types.CommandRule{{Commands: []string{"curl"}, Decision: types.DecisionDeny}}}
	file := pluginGatePolicy(g)
	require.Equal(s.T(), g.Commands[0], file.CommandRules[0])
	require.Equal(s.T(), types.CommandRule{Decision: types.DecisionAllow}, file.CommandRules[1])
	require.Equal(s.T(), types.DecisionDeny, file.DefaultDecision)
}

func (s *ManagerSuite) TestEnsureUnderGateQuiet() {
	s.useGate(nil)
	s.m.cfg.Gate.AuditVerbose = false
	s.docker.On("ImageInspect", s.ctx, s.plugin.Image).Return(imageInfo("1000", nil, []string{"/plugin"}), nil).Once()
	s.expectStart("c1", "40001")
	s.expectRow(db.PluginContainerRunning, "c1", 40001, "")
	_, err := s.m.Ensure(s.ctx, s.plugin, s.channel)
	require.NoError(s.T(), err)
	cfg := s.docker.Calls[2].Arguments.Get(1).(*containertypes.Config)
	require.NotContains(s.T(), cfg.Env, "LOOP_GATE_AUDIT_VERBOSE=1")
	require.Equal(s.T(), []string{"/opt/loop/loop", "syscallwrap", "--", "/plugin"}, []string(cfg.Entrypoint))
}

func (s *ManagerSuite) TestEnsureUnderGatePullsMissingImage() {
	s.useGate(nil)
	s.docker.On("ImageInspect", s.ctx, s.plugin.Image).Return(image.InspectResponse{}, errImageGone).Once()
	s.expectPull()
	s.docker.On("ImageInspect", s.ctx, s.plugin.Image).Return(imageInfo("1000", nil, []string{"/plugin"}), nil).Once()
	s.expectStart("c1", "40001")
	s.expectRow(db.PluginContainerRunning, "c1", 40001, "")
	_, err := s.m.Ensure(s.ctx, s.plugin, s.channel)
	require.NoError(s.T(), err)
}

func (s *ManagerSuite) TestEnsureGateFailures() {
	boom := errors.New("boom")
	inspect := func(info image.InspectResponse, err error) func() {
		return func() { s.docker.On("ImageInspect", s.ctx, s.plugin.Image).Return(info, err).Once() }
	}
	ok := imageInfo("1000", []string{"/plugin"}, nil)
	// blockRoot puts a file where a dir under the plugin's root should be.
	blockRoot := func(dir string) func() {
		return func() {
			inspect(ok, nil)()
			require.NoError(s.T(), os.MkdirAll(filepath.Join(s.m.cfg.Root, "cogtest"), 0o755))
			require.NoError(s.T(), os.WriteFile(filepath.Join(s.m.cfg.Root, "cogtest", dir), nil, 0o644))
		}
	}
	tests := []struct {
		name    string
		binErr  error
		setup   func()
		wantErr string
	}{
		{name: "binary", binErr: boom, setup: func() {}, wantErr: "finding the plugin gate: boom"},
		{name: "inspect", setup: inspect(image.InspectResponse{}, boom), wantErr: "inspecting the plugin image: boom"},
		{name: "pull missing image", setup: func() {
			inspect(image.InspectResponse{}, errImageGone)()
			s.docker.On("ImagePull", s.ctx, s.plugin.Image, image.PullOptions{}).Return(nil, boom).Once()
		}, wantErr: "pulling the missing plugin image: pulling example.com/cogtest@sha256:aa: boom"},
		{name: "no config", setup: inspect(image.InspectResponse{}, nil), wantErr: "the plugin image has no USER to run as"},
		{name: "no user", setup: func() {
			// Without a daemon uid, as on Windows, the image's USER is used.
			s.m.getuid = func() int { return -1 }
			inspect(imageInfo("", []string{"/plugin"}, nil), nil)()
		}, wantErr: "the plugin image has no USER to run as"},
		{name: "no command", setup: inspect(imageInfo("1000", nil, nil), nil), wantErr: "the plugin image has no command"},
		{name: "gate dir", setup: blockRoot("gate"), wantErr: "creating the plugin gate dir"},
		{name: "audit dir", setup: blockRoot("audit"), wantErr: "creating the plugin gate audit dir"},
		{name: "policy file", setup: func() {
			inspect(ok, nil)()
			require.NoError(s.T(), os.MkdirAll(filepath.Join(s.m.cfg.Root, "cogtest", "gate", "ch1", "gate-policy.json"), 0o755))
		}, wantErr: "writing the plugin gate policy"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.useGate(tt.binErr)
			s.expectNoLeftover()
			tt.setup()
			s.store.On("UpsertPluginContainer", s.ctx, mock.Anything).Return(nil).Once()
			_, err := s.m.Ensure(s.ctx, s.plugin, s.channel)
			require.ErrorContains(s.T(), err, tt.wantErr)
			s.docker.AssertNotCalled(s.T(), "ContainerCreate", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
			s.TearDownTest()
		})
	}
}
