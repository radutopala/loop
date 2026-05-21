package container

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/testutil"
)

// MockDockerClient implements DockerClient for testing.
type MockDockerClient struct {
	mock.Mock
}

func (m *MockDockerClient) ContainerCreate(ctx context.Context, cfg *ContainerConfig, name string) (string, error) {
	args := m.Called(ctx, cfg, name)
	return args.String(0), args.Error(1)
}

func (m *MockDockerClient) ContainerLogs(ctx context.Context, containerID string) (io.Reader, error) {
	args := m.Called(ctx, containerID)
	var r io.Reader
	if v := args.Get(0); v != nil {
		r = v.(io.Reader)
	}
	return r, args.Error(1)
}

func (m *MockDockerClient) ContainerLogsFollow(ctx context.Context, containerID string) (io.ReadCloser, error) {
	args := m.Called(ctx, containerID)
	var r io.ReadCloser
	if v := args.Get(0); v != nil {
		r = v.(io.ReadCloser)
	}
	return r, args.Error(1)
}

func (m *MockDockerClient) ContainerStart(ctx context.Context, containerID string) error {
	args := m.Called(ctx, containerID)
	return args.Error(0)
}

func (m *MockDockerClient) ContainerWait(ctx context.Context, containerID string) (<-chan WaitResponse, <-chan error) {
	args := m.Called(ctx, containerID)
	return args.Get(0).(<-chan WaitResponse), args.Get(1).(<-chan error)
}

func (m *MockDockerClient) ContainerRemove(ctx context.Context, containerID string) error {
	args := m.Called(ctx, containerID)
	return args.Error(0)
}

func (m *MockDockerClient) ContainerStop(ctx context.Context, containerID string) error {
	args := m.Called(ctx, containerID)
	return args.Error(0)
}

func (m *MockDockerClient) ImageList(ctx context.Context, image string) ([]string, error) {
	args := m.Called(ctx, image)
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockDockerClient) ImagePull(ctx context.Context, image string) error {
	args := m.Called(ctx, image)
	return args.Error(0)
}

func (m *MockDockerClient) ImageBuild(ctx context.Context, contextDir, tag string) error {
	args := m.Called(ctx, contextDir, tag)
	return args.Error(0)
}

func (m *MockDockerClient) ImageBuildFile(ctx context.Context, contextDir, dockerfile, tag string) error {
	args := m.Called(ctx, contextDir, dockerfile, tag)
	return args.Error(0)
}

func (m *MockDockerClient) PruneBuildCache(ctx context.Context, unusedFor time.Duration) error {
	return m.Called(ctx, unusedFor).Error(0)
}

func (m *MockDockerClient) RemoveImageAndContainers(ctx context.Context, imageName string) error {
	args := m.Called(ctx, imageName)
	return args.Error(0)
}

func (m *MockDockerClient) ImageInspectLabels(ctx context.Context, imageName string) (map[string]string, error) {
	args := m.Called(ctx, imageName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]string), args.Error(1)
}

func (m *MockDockerClient) ContainerList(ctx context.Context, labelKey, labelValue string) ([]string, error) {
	args := m.Called(ctx, labelKey, labelValue)
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockDockerClient) ListContainerInfos(ctx context.Context) ([]*ContainerInfo, error) {
	args := m.Called(ctx)
	return args.Get(0).([]*ContainerInfo), args.Error(1)
}

func (m *MockDockerClient) CopyToContainer(ctx context.Context, containerID, dstPath string, content io.Reader) error {
	args := m.Called(ctx, containerID, dstPath, content)
	return args.Error(0)
}

func (m *MockDockerClient) NetworkEnsure(ctx context.Context, name string) error {
	args := m.Called(ctx, name)
	return args.Error(0)
}

func (m *MockDockerClient) SetLoopVersion(v string) {}

// MockContainerRegistry implements ContainerRegistry for testing.
type MockContainerRegistry struct {
	mock.Mock
}

func (m *MockContainerRegistry) Register(info *ContainerInfo) *ContainerInfo {
	m.Called(info)
	return info
}
func (m *MockContainerRegistry) Unregister(containerID string) { m.Called(containerID) }
func (m *MockContainerRegistry) UpdateStatus(containerID string, s ContainerStatus) {
	m.Called(containerID, s)
}
func (m *MockContainerRegistry) List() []*ContainerInfo                { return nil }
func (m *MockContainerRegistry) ListByChannel(string) []*ContainerInfo { return nil }
func (m *MockContainerRegistry) FindByChannelAndType(string, ContainerType) *ContainerInfo {
	return nil
}
func (m *MockContainerRegistry) RunningChannelIDs(context.Context) map[string]struct{} {
	return nil
}
func (m *MockContainerRegistry) RemoveContainer(ctx context.Context, containerID string) error {
	args := m.Called(ctx, containerID)
	return args.Error(0)
}
func (m *MockContainerRegistry) ScheduleRemove(containerID string, delay time.Duration) {
	m.Called(containerID, delay)
}
func (m *MockContainerRegistry) FindOrCreateShell(context.Context, string, string, string) (string, error) {
	return "", nil
}

func (m *MockDockerClient) LatestClaudeVersion() string {
	args := m.Called()
	return args.String(0)
}

type RunnerSuite struct {
	suite.Suite
	client *MockDockerClient
	sys    *testutil.MockSystem
	runner *DockerRunner
	cfg    *config.Config
}

const (
	testContainerID   = "container-123"
	testContainerName = "loop-ch-1-aabbcc"
	testJSONOK        = `{"type":"result","result":"ok","session_id":"s1","is_error":false}`
)

func (s *RunnerSuite) TestLocalTimezone() {
	tests := []struct {
		name     string
		setup    func()
		expected string
	}{
		{
			name: "from TZ env",
			setup: func() {
				s.sys.Override("Getenv", "TZ").Return("America/New_York")
				s.sys.On("Getenv", mock.Anything).Return("")
			},
			expected: "America/New_York",
		},
		{
			name: "from osReadlink",
			setup: func() {
				s.sys.Override("Readlink", mock.Anything).Return("/var/db/timezone/zoneinfo/Europe/Bucharest", nil)
			},
			expected: "Europe/Bucharest",
		},
		{
			name: "from /etc/timezone",
			setup: func() {
				s.sys.Override("ReadFile", "/etc/timezone").Return([]byte("Asia/Tokyo\n"), nil)
				s.sys.On("ReadFile", mock.Anything).Return(nil, os.ErrNotExist)
			},
			expected: "Asia/Tokyo",
		},
		{
			name: "from time.Local name",
			setup: func() {
				s.runner.osTimeLocalName = func() string { return "Europe/Berlin" }
			},
			expected: "Europe/Berlin",
		},
		{
			name:     "fallback UTC",
			setup:    func() {},
			expected: "UTC",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			// Reset to defaults: no TZ, no ReadFile, no Readlink
			s.sys = newDefaultMockSystem()
			s.runner.sys = s.sys
			s.runner.osTimeLocalName = func() string { return "Local" }
			tt.setup()
			require.Equal(s.T(), tt.expected, s.runner.localTimezone())
		})
	}
}

func TestRunnerSuite(t *testing.T) {
	suite.Run(t, new(RunnerSuite))
}

func (s *RunnerSuite) SetupTest() {
	s.client = new(MockDockerClient)
	s.client.On("NetworkEnsure", mock.Anything, mock.Anything).Maybe().Return(nil)
	s.sys = newDefaultMockSystem()
	s.cfg = &config.Config{
		ClaudeBinPath:      "claude",
		ContainerImage:     "loop-agent:latest",
		ContainerMemoryMB:  512,
		ContainerCPUs:      1.0,
		ContainerTimeout:   30 * time.Second,
		ContainerKeepAlive: 5 * time.Minute,
		APIAddr:            ":8222",
		LoopDir:            "/home/testuser/.loop",
	}
	s.cfg.Browser.Enabled = true
	s.runner = NewDockerRunner(s.client, s.cfg, nil)
	s.runner.sys = s.sys
	s.runner.instanceID = "test-instance"
	s.runner.osRandRead = func(b []byte) (int, error) {
		copy(b, []byte{0xaa, 0xbb, 0xcc})
		return len(b), nil
	}
	s.runner.osTimeLocalName = func() string { return "Local" }
}

// newDefaultMockSystem creates a MockSystem with default expectations for runner tests.
func newDefaultMockSystem() *testutil.MockSystem {
	sys := new(testutil.MockSystem)
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
	return sys
}

// applyMockDefaults sets mock fields on s.runner to match SetupTest defaults.
// Call this after creating a new runner via NewDockerRunner in subtests.
func (s *RunnerSuite) applyMockDefaults() {
	s.client.On("NetworkEnsure", mock.Anything, mock.Anything).Maybe().Return(nil)
	s.sys = newDefaultMockSystem()
	s.runner.sys = s.sys
	s.runner.osRandRead = func(b []byte) (int, error) {
		copy(b, []byte{0xaa, 0xbb, 0xcc})
		return len(b), nil
	}
	s.runner.osTimeLocalName = func() string { return "Local" }
}

// setupMockRun sets up mocks for a successful non-streaming container Run cycle.
func (s *RunnerSuite) setupMockRun(ctx context.Context, createMatcher any, containerName, jsonOutput string) {
	reader := bytes.NewReader([]byte(jsonOutput))
	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("NetworkEnsure", ctx, mock.Anything).Maybe().Return(nil)
	s.client.On("ContainerCreate", ctx, createMatcher, containerName).Return(testContainerID, nil)
	s.client.On("ContainerLogs", ctx, testContainerID).Return(reader, nil)
	s.client.On("ContainerStart", ctx, testContainerID).Return(nil)
	s.client.On("ContainerWait", ctx, testContainerID).Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
}
