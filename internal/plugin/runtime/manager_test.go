package runtime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin"
)

type mockDocker struct {
	mock.Mock
}

func (m *mockDocker) ContainerCreate(ctx context.Context, config *containertypes.Config, hostConfig *containertypes.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (containertypes.CreateResponse, error) {
	args := m.Called(ctx, config, hostConfig, networkingConfig, platform, containerName)
	return args.Get(0).(containertypes.CreateResponse), args.Error(1)
}

func (m *mockDocker) ContainerStart(ctx context.Context, id string, options containertypes.StartOptions) error {
	return m.Called(ctx, id, options).Error(0)
}

func (m *mockDocker) ContainerRemove(ctx context.Context, id string, options containertypes.RemoveOptions) error {
	return m.Called(ctx, id, options).Error(0)
}

func (m *mockDocker) ContainerInspect(ctx context.Context, id string) (containertypes.InspectResponse, error) {
	args := m.Called(ctx, id)
	return args.Get(0).(containertypes.InspectResponse), args.Error(1)
}

func (m *mockDocker) ContainerList(ctx context.Context, options containertypes.ListOptions) ([]containertypes.Summary, error) {
	args := m.Called(ctx, options)
	v, _ := args.Get(0).([]containertypes.Summary)
	return v, args.Error(1)
}

func (m *mockDocker) ContainerLogs(ctx context.Context, id string, options containertypes.LogsOptions) (io.ReadCloser, error) {
	args := m.Called(ctx, id, options)
	rc, _ := args.Get(0).(io.ReadCloser)
	return rc, args.Error(1)
}

func (m *mockDocker) VolumeRemove(ctx context.Context, id string, force bool) error {
	return m.Called(ctx, id, force).Error(0)
}

func (m *mockDocker) ImageInspect(ctx context.Context, id string, _ ...client.ImageInspectOption) (image.InspectResponse, error) {
	args := m.Called(ctx, id)
	return args.Get(0).(image.InspectResponse), args.Error(1)
}

type mockStore struct {
	mock.Mock
}

func (m *mockStore) UpsertPluginContainer(ctx context.Context, c *db.PluginContainer) error {
	return m.Called(ctx, c).Error(0)
}

func (m *mockStore) DeletePluginContainer(ctx context.Context, pluginID, channelID string) error {
	return m.Called(ctx, pluginID, channelID).Error(0)
}

type mockTokens struct {
	mock.Mock
}

func (m *mockTokens) IssuePlugin(containerID, channelID, dirPath, pluginID string) (string, error) {
	args := m.Called(containerID, channelID, dirPath, pluginID)
	return args.String(0), args.Error(1)
}

func (m *mockTokens) Revoke(containerID string) error {
	return m.Called(containerID).Error(0)
}

type mockGate struct {
	mock.Mock
}

func (m *mockGate) WaitBuilds(ctx context.Context, images []string, onWait func(string)) error {
	onWait(images[0])
	return m.Called(ctx, images).Error(0)
}

type ManagerSuite struct {
	suite.Suite
	ctx     context.Context
	docker  *mockDocker
	store   *mockStore
	tokens  *mockTokens
	m       *Manager
	now     time.Time
	healthy bool
	plugin  Plugin
	channel Channel
}

func TestManagerSuite(t *testing.T) {
	suite.Run(t, new(ManagerSuite))
}

func (s *ManagerSuite) SetupTest() {
	s.ctx = context.Background()
	s.docker = new(mockDocker)
	s.store = new(mockStore)
	s.tokens = new(mockTokens)
	s.m = NewManager(s.docker, s.store, s.tokens, Config{
		Root:        s.T().TempDir(),
		APIURL:      "http://host.docker.internal:8222",
		IdleTimeout: 10 * time.Minute,
		StartWait:   time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.now = time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	s.m.now = func() time.Time { return s.now }
	s.m.getenv = func(string) string { return "" }
	s.m.getuid = func() int { return 1000 }
	s.m.getgid = func() int { return 1001 }
	s.m.readRand = func(b []byte) (int, error) { return len(b), nil }
	s.healthy = true
	s.m.healthy = func(context.Context, string, string) bool { return s.healthy }
	s.m.pollInterval = time.Millisecond
	s.plugin = Plugin{Manifest: &plugin.Manifest{ID: "cogtest"}, Image: "example.com/cogtest@sha256:aa"}
	s.channel = Channel{ID: "ch1", DirPath: "/work"}
}

func (s *ManagerSuite) TearDownTest() {
	s.docker.AssertExpectations(s.T())
	s.store.AssertExpectations(s.T())
	s.tokens.AssertExpectations(s.T())
}

const name = "loop-plugin-cogtest-ch1"

func running(on bool, port string) containertypes.InspectResponse {
	info := containertypes.InspectResponse{ContainerJSONBase: &containertypes.ContainerJSONBase{State: &containertypes.State{Running: on, ExitCode: 3}}}
	if port != "" {
		info.NetworkSettings = &containertypes.NetworkSettings{}
		info.NetworkSettings.Ports = nat.PortMap{portKey: {{HostPort: port}}}
	}
	return info
}

func (s *ManagerSuite) expectNoLeftover() {
	s.docker.On("ContainerList", s.ctx, mock.MatchedBy(func(o containertypes.ListOptions) bool { return o.Filters.Get("name")[0] == "^/"+name+"$" })).Return(nil, nil).Once()
}

// expectStart expects a container, id, to be created and started.
func (s *ManagerSuite) expectStart(id, port string) {
	s.expectNoLeftover()
	s.docker.On("ContainerCreate", s.ctx, mock.Anything, mock.Anything, (*network.NetworkingConfig)(nil), (*ocispec.Platform)(nil), name).
		Return(containertypes.CreateResponse{ID: id}, nil).Once()
	s.tokens.On("IssuePlugin", id, "ch1", "/work", "cogtest").Return("tok-"+id, nil).Once()
	s.docker.On("ContainerStart", s.ctx, id, containertypes.StartOptions{}).Return(nil).Once()
	s.docker.On("ContainerInspect", s.ctx, id).Return(running(true, port), nil).Once()
}

// logStream is a container log stream with out on stdout and errOut on
// stderr, framed as Docker sends it for a container without a TTY.
func logStream(out, errOut string) io.ReadCloser {
	var buf bytes.Buffer
	_, _ = stdcopy.NewStdWriter(&buf, stdcopy.Stdout).Write([]byte(out))
	_, _ = stdcopy.NewStdWriter(&buf, stdcopy.Stderr).Write([]byte(errOut))
	return io.NopCloser(&buf)
}

var logsOptions = containertypes.LogsOptions{ShowStdout: true, ShowStderr: true, Tail: "20"}

func (s *ManagerSuite) expectDiscard(id string) {
	s.docker.On("ContainerRemove", s.ctx, id, containertypes.RemoveOptions{Force: true}).Return(nil).Once()
	s.tokens.On("Revoke", id).Return(nil).Once()
}

func (s *ManagerSuite) expectRow(status, containerID string, port int, lastErr string) {
	s.store.On("UpsertPluginContainer", s.ctx, &db.PluginContainer{PluginID: "cogtest", ChannelID: "ch1", ContainerID: containerID, HostPort: port, Status: status, LastError: lastErr}).Return(nil).Once()
}

func (s *ManagerSuite) TestEnsureStartsThenReuses() {
	s.plugin.Manifest.Resources = &plugin.Resources{Memory: "512m", CPUs: 1}
	s.m.cfg.Proxy.HTTPProxy = "http://proxy:3128"
	gate := new(mockGate)
	gate.On("WaitBuilds", s.ctx, []string{s.plugin.Image}).Return(nil).Once()
	s.m.SetImageGate(gate)
	s.expectStart("c1", "40001")
	s.expectRow(db.PluginContainerRunning, "c1", 40001, "")

	ep, err := s.m.Ensure(s.ctx, s.plugin, s.channel)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "http://127.0.0.1:40001", ep.URL)
	require.Len(s.T(), ep.Secret, 64)
	gate.AssertExpectations(s.T())

	call := s.docker.Calls[1]
	require.Equal(s.T(), "ContainerCreate", call.Method)
	cfg := call.Arguments.Get(1).(*containertypes.Config)
	host := call.Arguments.Get(2).(*containertypes.HostConfig)
	require.Equal(s.T(), s.plugin.Image, cfg.Image)
	require.Equal(s.T(), map[string]string{LabelKey: "cogtest", channelLabelKey: "ch1", rootLabelKey: s.m.rootLabel()}, cfg.Labels)
	require.Subset(s.T(), cfg.Env, []string{
		"LOOP_PLUGIN_ID=cogtest", "LOOP_CHANNEL_ID=ch1", "LOOP_CHANNEL_DIR=/work", "LOOP_PLUGIN_PORT=8080",
		"LOOP_API_URL=http://host.docker.internal:8222", "LOOP_PLUGIN_TOKEN_FILE=/run/loop/plugin-token",
		"LOOP_PLUGIN_SECRET_FILE=/run/loop/loop-secret", "LOOP_DATA_DIR=/data", "HTTP_PROXY=http://proxy:3128",
	})
	require.True(s.T(), host.ReadonlyRootfs)
	require.Equal(s.T(), []string{"no-new-privileges"}, host.SecurityOpt)
	require.Equal(s.T(), []string{"ALL"}, []string(host.CapDrop))
	require.Equal(s.T(), int64(512<<20), host.Memory)
	require.Equal(s.T(), int64(cpuPeriod), host.CPUQuota)
	require.Equal(s.T(), int64(pidsLimit), *host.PidsLimit)
	require.Equal(s.T(), "1000:1001", cfg.User)
	run := filepath.Join(s.m.cfg.Root, "cogtest", "run", "ch1")
	data := filepath.Join(s.m.cfg.Root, "cogtest", "data", "ch1")
	require.DirExists(s.T(), data)
	require.Equal(s.T(), []mount.Mount{
		{Type: mount.TypeBind, Source: data, Target: "/data"},
		{Type: mount.TypeBind, Source: run, Target: "/run/loop", ReadOnly: true},
	}, host.Mounts)

	tok, err := os.ReadFile(filepath.Join(run, tokenFile))
	require.NoError(s.T(), err)
	require.Equal(s.T(), "tok-c1", string(tok))
	secret, err := os.ReadFile(filepath.Join(run, secretFile))
	require.NoError(s.T(), err)
	require.Equal(s.T(), ep.Secret, string(secret))

	// A running container is reused, and its use pushes back its idle stop.
	s.docker.On("ContainerInspect", s.ctx, "c1").Return(running(true, "40001"), nil).Once()
	s.now = s.now.Add(time.Minute)
	again, err := s.m.Ensure(s.ctx, s.plugin, s.channel)
	require.NoError(s.T(), err)
	require.Equal(s.T(), ep, again)
	require.Equal(s.T(), s.now, s.m.running[key{"cogtest", "ch1"}].lastUsed)
}

func (s *ManagerSuite) TestEnsureReplacesExitedContainer() {
	s.expectStart("c1", "40001")
	s.expectRow(db.PluginContainerRunning, "c1", 40001, "")
	_, err := s.m.Ensure(s.ctx, s.plugin, s.channel)
	require.NoError(s.T(), err)

	s.docker.On("ContainerInspect", s.ctx, "c1").Return(containertypes.InspectResponse{}, errors.New("gone")).Once()
	s.expectDiscard("c1")
	s.expectStart("c2", "40002")
	s.expectRow(db.PluginContainerRunning, "c2", 40002, "")
	ep, err := s.m.Ensure(s.ctx, s.plugin, s.channel)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "http://127.0.0.1:40002", ep.URL)
}

func (s *ManagerSuite) TestEnsureRemovesLeftoverByName() {
	s.docker.On("ContainerList", s.ctx, mock.Anything).Return([]containertypes.Summary{{ID: "old"}}, nil).Once()
	s.expectDiscard("old")
	s.docker.On("ContainerCreate", s.ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything, name).Return(containertypes.CreateResponse{ID: "c1"}, nil).Once()
	s.tokens.On("IssuePlugin", "c1", "ch1", "/work", "cogtest").Return("tok", nil).Once()
	s.docker.On("ContainerStart", s.ctx, "c1", mock.Anything).Return(nil).Once()
	s.docker.On("ContainerInspect", s.ctx, "c1").Return(running(true, "40001"), nil).Once()
	s.expectRow(db.PluginContainerRunning, "c1", 40001, "")
	_, err := s.m.Ensure(s.ctx, s.plugin, s.channel)
	require.NoError(s.T(), err)
}

func (s *ManagerSuite) TestEnsureFailures() {
	boom := errors.New("boom")
	created := func() {
		s.expectNoLeftover()
		s.docker.On("ContainerCreate", s.ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything, name).Return(containertypes.CreateResponse{ID: "c1"}, nil).Once()
	}
	issued := func() {
		created()
		s.tokens.On("IssuePlugin", "c1", "ch1", "/work", "cogtest").Return("tok", nil).Once()
	}
	started := func(info containertypes.InspectResponse) {
		issued()
		s.docker.On("ContainerStart", s.ctx, "c1", mock.Anything).Return(nil).Once()
		s.docker.On("ContainerInspect", s.ctx, "c1").Return(info, nil).Once()
	}
	tests := []struct {
		name    string
		setup   func()
		discard bool
		wantErr string
	}{
		{name: "image gate", setup: func() {
			s.expectNoLeftover()
			gate := new(mockGate)
			gate.On("WaitBuilds", s.ctx, mock.Anything).Return(boom)
			s.m.SetImageGate(gate)
		}, wantErr: "waiting for the image build: boom"},
		{name: "run dir", setup: func() {
			s.expectNoLeftover()
			require.NoError(s.T(), os.WriteFile(filepath.Join(s.m.cfg.Root, "cogtest"), nil, 0o644))
		}, wantErr: "creating the plugin run dir"},
		{name: "root daemon", setup: func() {
			s.expectNoLeftover()
			s.m.getuid = func() int { return 0 }
		}, wantErr: "the Loop daemon's user, which must not be root"},
		{name: "data dir owner", setup: func() {
			s.expectNoLeftover()
			s.m.getuid = func() int { return 0 }
			s.m.getenv = envOf(map[string]string{"LOOP_HOST_UID": "1000", "LOOP_HOST_GID": "1000"})
			s.m.chown = func(string, int, int) error { return boom }
		}, wantErr: "handing the plugin data dir to uid 1000: boom"},
		{name: "data dir", setup: func() {
			s.expectNoLeftover()
			require.NoError(s.T(), os.MkdirAll(filepath.Join(s.m.cfg.Root, "cogtest"), 0o755))
			require.NoError(s.T(), os.WriteFile(filepath.Join(s.m.cfg.Root, "cogtest", "data"), nil, 0o644))
		}, wantErr: "creating the plugin data dir"},
		{name: "secret", setup: func() {
			s.expectNoLeftover()
			s.m.readRand = func([]byte) (int, error) { return 0, boom }
		}, wantErr: "generating the plugin secret: boom"},
		{name: "create", setup: func() {
			s.expectNoLeftover()
			s.docker.On("ContainerCreate", s.ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything, name).Return(containertypes.CreateResponse{}, boom).Once()
		}, wantErr: "creating the plugin container: boom"},
		{name: "token", discard: true, setup: func() {
			created()
			s.tokens.On("IssuePlugin", "c1", "ch1", "/work", "cogtest").Return("", boom).Once()
		}, wantErr: "issuing the plugin token: boom"},
		{name: "write token", discard: true, setup: func() {
			issued()
			require.NoError(s.T(), os.MkdirAll(filepath.Join(s.m.cfg.Root, "cogtest", "run", "ch1", tokenFile), 0o755))
		}, wantErr: "writing the plugin token"},
		{name: "write secret", discard: true, setup: func() {
			issued()
			require.NoError(s.T(), os.MkdirAll(filepath.Join(s.m.cfg.Root, "cogtest", "run", "ch1", secretFile), 0o755))
		}, wantErr: "writing the plugin secret"},
		{name: "start", discard: true, setup: func() {
			issued()
			s.docker.On("ContainerStart", s.ctx, "c1", mock.Anything).Return(boom).Once()
		}, wantErr: "starting the plugin container: boom"},
		{name: "inspect", discard: true, setup: func() {
			issued()
			s.docker.On("ContainerStart", s.ctx, "c1", mock.Anything).Return(nil).Once()
			s.docker.On("ContainerInspect", s.ctx, "c1").Return(containertypes.InspectResponse{}, boom).Once()
		}, wantErr: "inspecting the plugin container: boom"},
		{name: "no port", discard: true, setup: func() { started(running(true, "")) }, wantErr: "no host port for 8080/tcp"},
		{name: "exits", discard: true, setup: func() {
			started(running(true, "40001"))
			s.healthy = false
			s.docker.On("ContainerInspect", s.ctx, "c1").Return(running(false, ""), nil).Once()
			s.docker.On("ContainerLogs", s.ctx, "c1", logsOptions).Return(logStream("listening\n", "panic: no config\n"), nil).Once()
		}, wantErr: "exited with code 3 before becoming healthy; its last log lines:\nlistening\npanic: no config"},
		{name: "never healthy", discard: true, setup: func() {
			started(running(true, "40001"))
			s.healthy = false
			s.m.cfg.StartWait = 0
			s.docker.On("ContainerInspect", s.ctx, "c1").Return(running(true, ""), nil).Once()
			s.docker.On("ContainerLogs", s.ctx, "c1", logsOptions).Return(nil, boom).Once()
		}, wantErr: "not healthy after 0s"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			tc.setup()
			if tc.discard {
				s.expectDiscard("c1")
			}
			s.store.On("UpsertPluginContainer", s.ctx, mock.MatchedBy(func(c *db.PluginContainer) bool {
				return c.Status == db.PluginContainerStopped && c.LastError != ""
			})).Return(nil).Once()
			_, err := s.m.Ensure(s.ctx, s.plugin, s.channel)
			require.ErrorContains(s.T(), err, tc.wantErr)
			require.Empty(s.T(), s.m.running)
			s.TearDownTest()
		})
	}
}

func (s *ManagerSuite) TestEnsureCanceledWhileWaiting() {
	ctx, cancel := context.WithCancel(s.ctx)
	s.ctx = ctx
	s.expectStart("c1", "40001")
	s.m.cfg.StartWait = time.Hour
	s.m.pollInterval = time.Hour
	s.m.healthy = func(context.Context, string, string) bool { cancel(); return false }
	s.docker.On("ContainerInspect", s.ctx, "c1").Return(running(true, ""), nil).Once()
	s.docker.On("ContainerLogs", s.ctx, "c1", logsOptions).Return(logStream("", " \n"), nil).Once()
	s.expectDiscard("c1")
	s.store.On("UpsertPluginContainer", s.ctx, mock.Anything).Return(nil).Once()
	_, err := s.m.Ensure(s.ctx, s.plugin, s.channel)
	require.EqualError(s.T(), err, context.Canceled.Error(), "blank logs add nothing")
}

func (s *ManagerSuite) TestEnsureBacksOffThenFails() {
	boom := errors.New("boom")
	fail := func() {
		s.expectNoLeftover()
		s.docker.On("ContainerCreate", s.ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything, name).Return(containertypes.CreateResponse{}, boom).Once()
	}
	fail()
	s.expectRow(db.PluginContainerStopped, "", 0, "creating the plugin container: boom")
	_, err := s.m.Ensure(s.ctx, s.plugin, s.channel)
	require.ErrorContains(s.T(), err, "boom")

	_, err = s.m.Ensure(s.ctx, s.plugin, s.channel)
	require.EqualError(s.T(), err, "plugin cogtest failed to start in channel ch1, retrying in 1s: creating the plugin container: boom")

	s.now = s.now.Add(time.Second)
	fail()
	s.expectRow(db.PluginContainerStopped, "", 0, "creating the plugin container: boom")
	_, err = s.m.Ensure(s.ctx, s.plugin, s.channel)
	require.ErrorContains(s.T(), err, "boom")
	_, err = s.m.Ensure(s.ctx, s.plugin, s.channel)
	require.ErrorContains(s.T(), err, "retrying in 2s")

	s.now = s.now.Add(2 * time.Second)
	fail()
	s.expectRow(db.PluginContainerFailed, "", 0, "creating the plugin container: boom")
	_, err = s.m.Ensure(s.ctx, s.plugin, s.channel)
	require.ErrorContains(s.T(), err, "boom")

	s.now = s.now.Add(time.Hour)
	_, err = s.m.Ensure(s.ctx, s.plugin, s.channel)
	require.EqualError(s.T(), err, "plugin cogtest failed to start in channel ch1 3 times: creating the plugin container: boom")

	s.m.Reset("cogtest", "ch1")
	s.expectStart("c1", "40001")
	s.expectRow(db.PluginContainerRunning, "c1", 40001, "")
	_, err = s.m.Ensure(s.ctx, s.plugin, s.channel)
	require.NoError(s.T(), err)
	require.Empty(s.T(), s.m.failures, "a start clears the failures")
}

// startOne starts ch's container, cid, and returns its key.
func (s *ManagerSuite) startOne(ch, cid string) key {
	c := Channel{ID: ch, DirPath: "/work"}
	n := ContainerName("cogtest", ch)
	s.docker.On("ContainerList", s.ctx, mock.MatchedBy(func(o containertypes.ListOptions) bool { return o.Filters.Get("name")[0] == "^/"+n+"$" })).Return(nil, nil).Once()
	s.docker.On("ContainerCreate", s.ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything, n).Return(containertypes.CreateResponse{ID: cid}, nil).Once()
	s.tokens.On("IssuePlugin", cid, ch, "/work", "cogtest").Return("tok", nil).Once()
	s.docker.On("ContainerStart", s.ctx, cid, mock.Anything).Return(nil).Once()
	s.docker.On("ContainerInspect", s.ctx, cid).Return(running(true, "40001"), nil).Once()
	s.store.On("UpsertPluginContainer", s.ctx, &db.PluginContainer{PluginID: "cogtest", ChannelID: ch, ContainerID: cid, HostPort: 40001, Status: db.PluginContainerRunning}).Return(nil).Once()
	_, err := s.m.Ensure(s.ctx, s.plugin, c)
	require.NoError(s.T(), err)
	return key{"cogtest", ch}
}

func (s *ManagerSuite) expectStopped(ch, cid string) {
	s.expectDiscardErr(cid, nil)
	s.store.On("UpsertPluginContainer", s.ctx, &db.PluginContainer{PluginID: "cogtest", ChannelID: ch, Status: db.PluginContainerStopped}).Return(nil).Once()
}

func (s *ManagerSuite) expectDiscardErr(cid string, err error) {
	s.docker.On("ContainerRemove", s.ctx, cid, containertypes.RemoveOptions{Force: true}).Return(err).Once()
	s.tokens.On("Revoke", cid).Return(err).Once()
}

func (s *ManagerSuite) TestStop() {
	s.m.Stop(s.ctx, "cogtest", "ch1") // nothing running
	s.startOne("ch1", "c1")
	// Failures to remove or revoke are logged, not returned.
	s.expectDiscardErr("c1", errors.New("boom"))
	s.store.On("UpsertPluginContainer", s.ctx, mock.Anything).Return(errors.New("db down")).Once()
	s.m.Stop(s.ctx, "cogtest", "ch1")
	require.Empty(s.T(), s.m.running)
}

func (s *ManagerSuite) TestStopIdleAndStopAll() {
	s.startOne("ch1", "c1")
	s.now = s.now.Add(5 * time.Minute)
	s.startOne("ch2", "c2")
	s.now = s.now.Add(6 * time.Minute)

	s.expectStopped("ch1", "c1")
	require.True(s.T(), s.m.StopIdle(s.ctx))
	require.Len(s.T(), s.m.running, 1)
	require.False(s.T(), s.m.StopIdle(s.ctx))

	s.expectStopped("ch2", "c2")
	s.m.StopAll(s.ctx)
	require.Empty(s.T(), s.m.running)
}

func (s *ManagerSuite) TestStopPlugin() {
	s.startOne("ch1", "c1")
	other := key{"other", "ch1"}
	s.m.running[other] = &instance{containerID: "o1"}
	s.m.failures[key{"cogtest", "ch2"}] = &failure{count: 3}
	s.m.failures[other] = &failure{count: 1}

	s.expectStopped("ch1", "c1")
	s.m.StopPlugin(s.ctx, "cogtest")
	require.Equal(s.T(), map[key]*instance{other: {containerID: "o1"}}, s.m.running)
	require.Equal(s.T(), map[key]*failure{other: {count: 1}}, s.m.failures)
}

func (s *ManagerSuite) TestRunning() {
	require.Empty(s.T(), s.m.Running())
	s.startOne("ch1", "c1")
	s.startOne("ch2", "c2")
	require.ElementsMatch(s.T(), []Running{
		{PluginID: "cogtest", Channel: Channel{ID: "ch1", DirPath: "/work"}},
		{PluginID: "cogtest", Channel: Channel{ID: "ch2", DirPath: "/work"}},
	}, s.m.Running())
}

// envOf is a getenv over vars.
func envOf(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func (s *ManagerSuite) TestEnsureUser() {
	tests := []struct {
		name      string
		uid       int
		env       map[string]string
		wantUser  string
		wantChown bool
	}{
		{name: "daemon's", uid: 1000, wantUser: "1000:1001"},
		{name: "LOOP_HOST_UID", uid: 1000, env: map[string]string{"LOOP_HOST_UID": "2000", "LOOP_HOST_GID": "2001"}, wantUser: "2000:2001"},
		{name: "half set", uid: 1000, env: map[string]string{"LOOP_HOST_UID": "2000"}, wantUser: "1000:1001"},
		{name: "root daemon", uid: 0, env: map[string]string{"LOOP_HOST_UID": "2000", "LOOP_HOST_GID": "2001"}, wantUser: "2000:2001", wantChown: true},
		{name: "windows", uid: -1},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.m.getuid = func() int { return tt.uid }
			s.m.getenv = envOf(tt.env)
			var chowned []any
			s.m.chown = func(name string, uid, gid int) error {
				chowned = []any{name, uid, gid}
				return nil
			}
			s.expectStart("c1", "40001")
			s.expectRow(db.PluginContainerRunning, "c1", 40001, "")
			_, err := s.m.Ensure(s.ctx, s.plugin, s.channel)
			require.NoError(s.T(), err)
			cfg := s.docker.Calls[1].Arguments.Get(1).(*containertypes.Config)
			require.Equal(s.T(), tt.wantUser, cfg.User)
			if tt.wantChown {
				require.Equal(s.T(), []any{filepath.Join(s.m.cfg.Root, "cogtest", "data", "ch1"), 2000, 2001}, chowned)
			} else {
				require.Nil(s.T(), chowned)
			}
			s.TearDownTest()
		})
	}
}

func (s *ManagerSuite) TestEnsureDoesNotMountTheChannelDir() {
	// Plugins read the channel's files through Loop, never from a mount.
	s.channel.DirPath = "/work"
	s.expectNoLeftover()
	s.docker.On("ContainerCreate", s.ctx, mock.Anything, mock.Anything, (*network.NetworkingConfig)(nil), (*ocispec.Platform)(nil), name).
		Return(containertypes.CreateResponse{ID: "c1"}, nil).Once()
	s.tokens.On("IssuePlugin", "c1", "ch1", "/work", "cogtest").Return("tok", nil).Once()
	s.docker.On("ContainerStart", s.ctx, "c1", containertypes.StartOptions{}).Return(nil).Once()
	s.docker.On("ContainerInspect", s.ctx, "c1").Return(running(true, "40001"), nil).Once()
	s.expectRow(db.PluginContainerRunning, "c1", 40001, "")
	_, err := s.m.Ensure(s.ctx, s.plugin, s.channel)
	require.NoError(s.T(), err)
	host := s.docker.Calls[1].Arguments.Get(2).(*containertypes.HostConfig)
	for _, m := range host.Mounts {
		require.NotEqual(s.T(), "/work", m.Source)
	}
}

func (s *ManagerSuite) TestRemove() {
	run := filepath.Join(s.m.cfg.Root, "cogtest", "run", "ch1")
	gate := filepath.Join(s.m.cfg.Root, "cogtest", "gate", "ch1")
	audit := filepath.Join(s.m.cfg.Root, "cogtest", "audit", "ch1")
	data := filepath.Join(s.m.cfg.Root, "cogtest", "data", "ch1")
	require.NoError(s.T(), os.MkdirAll(gate, 0o755))
	require.NoError(s.T(), os.MkdirAll(audit, 0o755))
	s.startOne("ch1", "c1")
	require.NoError(s.T(), os.WriteFile(filepath.Join(data, "plugin.db"), nil, 0o644))
	s.m.failures[key{"cogtest", "ch1"}] = &failure{count: 1}
	s.expectStopped("ch1", "c1")
	s.expectNoLeftover()
	s.store.On("DeletePluginContainer", s.ctx, "cogtest", "ch1").Return(nil).Once()
	require.NoError(s.T(), s.m.Remove(s.ctx, "cogtest", "ch1", true))
	require.NoDirExists(s.T(), run)
	require.NoDirExists(s.T(), gate)
	require.NoDirExists(s.T(), audit)
	require.NoDirExists(s.T(), data)
	require.Empty(s.T(), s.m.failures)
}

func (s *ManagerSuite) TestRemoveErrors() {
	boom := errors.New("boom")
	// A run dir that can't be removed, even by root: a file stands where
	// its parent dir should be.
	require.NoError(s.T(), os.WriteFile(filepath.Join(s.m.cfg.Root, "cogtest"), nil, 0o644))
	s.docker.On("ContainerList", s.ctx, mock.Anything).Return(nil, boom).Once()
	s.store.On("DeletePluginContainer", s.ctx, "cogtest", "ch1").Return(boom).Once()
	err := s.m.Remove(s.ctx, "cogtest", "ch1", true)
	require.ErrorContains(s.T(), err, "removing the plugin run dir")
	require.ErrorContains(s.T(), err, "removing the plugin gate dir")
	require.ErrorContains(s.T(), err, "removing the plugin gate audit dir")
	require.ErrorContains(s.T(), err, "deleting the plugin container row: boom")
	require.ErrorContains(s.T(), err, "removing the plugin data dir")
}

func (s *ManagerSuite) TestRemoveKeepsData() {
	data := filepath.Join(s.m.cfg.Root, "cogtest", "data", "ch1")
	require.NoError(s.T(), os.MkdirAll(data, 0o700))
	s.expectNoLeftover()
	s.store.On("DeletePluginContainer", s.ctx, "cogtest", "ch1").Return(nil).Once()
	require.NoError(s.T(), s.m.Remove(s.ctx, "cogtest", "ch1", false))
	require.DirExists(s.T(), data)
}

func (s *ManagerSuite) TestRemoveLeftovers() {
	// Only this daemon's containers are listed: another daemon sharing the
	// Docker engine has another plugins root.
	other := NewManager(nil, nil, nil, Config{Root: s.T().TempDir()}, nil)
	require.NotEqual(s.T(), s.m.rootLabel(), other.rootLabel())
	require.Len(s.T(), s.m.rootLabel(), 16)
	labels := []string{LabelKey, rootLabelKey + "=" + s.m.rootLabel()}
	s.docker.On("ContainerList", s.ctx, mock.MatchedBy(func(o containertypes.ListOptions) bool {
		got := o.Filters.Get("label")
		slices.Sort(got)
		return o.All && slices.Equal(labels, got)
	})).
		Return([]containertypes.Summary{{ID: "old", Labels: map[string]string{LabelKey: "cogtest", channelLabelKey: "ch1"}}}, nil).Once()
	s.expectDiscard("old")
	s.expectRow(db.PluginContainerStopped, "", 0, "")
	require.NoError(s.T(), s.m.RemoveLeftovers(s.ctx))

	s.docker.On("ContainerList", s.ctx, mock.Anything).Return(nil, errors.New("boom")).Once()
	require.ErrorContains(s.T(), s.m.RemoveLeftovers(s.ctx), "listing plugin containers: boom")
}

func (s *ManagerSuite) TestResources() {
	tests := []struct {
		name       string
		r          *plugin.Resources
		wantMemory int64
		wantCPUs   float64
	}{
		{"none", nil, defaultMemory, defaultCPUs},
		{"empty", &plugin.Resources{}, defaultMemory, defaultCPUs},
		{"asked", &plugin.Resources{Memory: "1g", CPUs: 1.5}, 1 << 30, 1.5},
		{"capped", &plugin.Resources{Memory: "8g", CPUs: 16}, maxMemory, maxCPUs},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			memory, cpus := resources(tc.r)
			require.Equal(s.T(), tc.wantMemory, memory)
			require.InDelta(s.T(), tc.wantCPUs, cpus, 0)
		})
	}
}

func (s *ManagerSuite) TestCheckHealth() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" || r.Header.Get("Authorization") != "Bearer s3cret" {
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer srv.Close()
	m := NewManager(s.docker, s.store, s.tokens, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.True(s.T(), m.healthy(s.ctx, srv.URL, "s3cret"))
	require.False(s.T(), m.healthy(s.ctx, srv.URL, "wrong"))
	require.False(s.T(), m.healthy(s.ctx, "http://127.0.0.1:1", "s3cret"), "nothing listening")
	require.False(s.T(), m.healthy(s.ctx, "http://bad host", "s3cret"), "unparsable url")
}
