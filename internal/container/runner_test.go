package container

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/agent"
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

func (s *RunnerSuite) TestNewDockerRunner() {
	runner := NewDockerRunner(s.client, s.cfg, nil)
	require.NotNil(s.T(), runner)
	require.Equal(s.T(), s.client, runner.client)
	require.Equal(s.T(), s.cfg, runner.cfg.Load())
}

func (s *RunnerSuite) TestRunHappyPath() {
	ctx := context.Background()
	req := &agent.AgentRequest{
		SessionID:    "sess-1",
		Messages:     []agent.AgentMessage{{Role: "user", Content: "hello"}},
		SystemPrompt: "You are helpful",
		ChannelID:    "ch-1",
	}

	s.setupMockRun(ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		hasResume := slices.Contains(cfg.Cmd, "--resume") && slices.Contains(cfg.Cmd, "sess-1")
		hasBinds := slices.Contains(cfg.Binds, "/home/testuser/.loop/ch-1/work:/home/testuser/.loop/ch-1/work")
		hasHome := slices.Contains(cfg.Env, "HOME=/home/testuser")
		hasHostUser := slices.Contains(cfg.Env, "HOST_USER=testuser")
		hasHostUID := slices.Contains(cfg.Env, "HOST_UID=1000")
		hasHostGID := slices.Contains(cfg.Env, "HOST_GID=1000")
		hasTZ := slices.ContainsFunc(cfg.Env, func(e string) bool {
			return len(e) > 3 && e[:3] == "TZ="
		})
		return hasResume && hasBinds && hasHome && hasHostUser && hasHostUID && hasHostGID && hasTZ
	}), testContainerName, `{"type":"result","result":"Hello! How can I help?","session_id":"sess-new-1","is_error":false}`)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Hello! How can I help?", resp.Response)
	require.Equal(s.T(), "sess-new-1", resp.SessionID)

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunForkSession() {
	ctx := context.Background()
	req := &agent.AgentRequest{
		SessionID:   "sess-parent",
		ForkSession: true,
		Messages:    []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID:   "ch-1",
	}

	s.setupMockRun(ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		hasResume := slices.Contains(cfg.Cmd, "--resume") && slices.Contains(cfg.Cmd, "sess-parent")
		hasFork := slices.Contains(cfg.Cmd, "--fork-session")
		return hasResume && hasFork
	}), testContainerName, `{"type":"result","result":"Forked!","session_id":"sess-forked","is_error":false}`)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Forked!", resp.Response)
	require.Equal(s.T(), "sess-forked", resp.SessionID)

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunForkSessionCompactOnPromptTooLong() {
	ctx := context.Background()
	req := &agent.AgentRequest{
		SessionID:   "sess-parent",
		ForkSession: true,
		Messages:    []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID:   "ch-1",
	}

	// First attempt: fork → "Prompt is too long"
	forkJSON := `{"type":"result","result":"Prompt is too long","session_id":"sess-forked","is_error":true}`
	forkReader := bytes.NewReader([]byte(forkJSON))
	forkWait := make(chan WaitResponse, 1)
	forkWait <- WaitResponse{StatusCode: 0}
	forkErr := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return slices.Contains(cfg.Cmd, "--fork-session") && slices.Contains(cfg.Cmd, "sess-parent")
	}), "loop-ch-1-aabbcc").Return("container-fork", nil).Once()
	s.client.On("ContainerLogs", ctx, "container-fork").Return(forkReader, nil)
	s.client.On("ContainerStart", ctx, "container-fork").Return(nil)
	s.client.On("ContainerWait", ctx, "container-fork").Return((<-chan WaitResponse)(forkWait), (<-chan error)(forkErr))

	// Compact: run /compact on the forked session
	compactJSON := `{"type":"result","result":"Conversation compacted","session_id":"sess-compacted","is_error":false}`
	compactReader := bytes.NewReader([]byte(compactJSON))
	compactWait := make(chan WaitResponse, 1)
	compactWait <- WaitResponse{StatusCode: 0}
	compactErrCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		hasResume := slices.Contains(cfg.Cmd, "--resume") && slices.Contains(cfg.Cmd, "sess-forked")
		noFork := !slices.Contains(cfg.Cmd, "--fork-session")
		hasCompact := slices.Contains(cfg.Cmd, "/compact")
		return hasResume && noFork && hasCompact
	}), "loop-ch-1-aabbcc").Return("container-compact", nil).Once()
	s.client.On("ContainerLogs", ctx, "container-compact").Return(compactReader, nil)
	s.client.On("ContainerStart", ctx, "container-compact").Return(nil)
	s.client.On("ContainerWait", ctx, "container-compact").Return((<-chan WaitResponse)(compactWait), (<-chan error)(compactErrCh))

	// Retry: resume compacted session with original prompt
	retryJSON := `{"type":"result","result":"Hi from compacted session!","session_id":"sess-final","is_error":false}`
	retryReader := bytes.NewReader([]byte(retryJSON))
	retryWait := make(chan WaitResponse, 1)
	retryWait <- WaitResponse{StatusCode: 0}
	retryErrCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		hasResume := slices.Contains(cfg.Cmd, "--resume") && slices.Contains(cfg.Cmd, "sess-compacted")
		noFork := !slices.Contains(cfg.Cmd, "--fork-session")
		return hasResume && noFork
	}), "loop-ch-1-aabbcc").Return("container-retry", nil).Once()
	s.client.On("ContainerLogs", ctx, "container-retry").Return(retryReader, nil)
	s.client.On("ContainerStart", ctx, "container-retry").Return(nil)
	s.client.On("ContainerWait", ctx, "container-retry").Return((<-chan WaitResponse)(retryWait), (<-chan error)(retryErrCh))

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Hi from compacted session!", resp.Response)
	require.Equal(s.T(), "sess-final", resp.SessionID)

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunForkSessionCompactFails() {
	ctx := context.Background()
	req := &agent.AgentRequest{
		SessionID:   "sess-parent",
		ForkSession: true,
		Messages:    []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID:   "ch-1",
		Prompt:      "user: hello",
	}

	// First attempt: fork → "Prompt is too long"
	forkJSON := `{"type":"result","result":"Prompt is too long","session_id":"sess-forked","is_error":true}`
	forkReader := bytes.NewReader([]byte(forkJSON))
	forkWait := make(chan WaitResponse, 1)
	forkWait <- WaitResponse{StatusCode: 0}
	forkErr := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return slices.Contains(cfg.Cmd, "--fork-session")
	}), "loop-ch-1-aabbcc").Return("container-fork", nil).Once()
	s.client.On("ContainerLogs", ctx, "container-fork").Return(forkReader, nil)
	s.client.On("ContainerStart", ctx, "container-fork").Return(nil)
	s.client.On("ContainerWait", ctx, "container-fork").Return((<-chan WaitResponse)(forkWait), (<-chan error)(forkErr))

	// Compact fails: container create error
	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return slices.Contains(cfg.Cmd, "/compact")
	}), "loop-ch-1-aabbcc").Return("", errors.New("docker error")).Once()

	resp, err := s.runner.Run(ctx, req)
	require.Error(s.T(), err)
	require.Nil(s.T(), resp)
	require.Contains(s.T(), err.Error(), "compacting session")

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunSessionCompactOnPromptTooLong() {
	ctx := context.Background()
	req := &agent.AgentRequest{
		SessionID: "sess-long",
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
		Prompt:    "user: hello",
	}

	// First attempt: regular resume → "Prompt is too long"
	failJSON := `{"type":"result","result":"Prompt is too long","session_id":"sess-long","is_error":true}`
	failReader := bytes.NewReader([]byte(failJSON))
	failWait := make(chan WaitResponse, 1)
	failWait <- WaitResponse{StatusCode: 0}
	failErr := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return slices.Contains(cfg.Cmd, "--resume") && slices.Contains(cfg.Cmd, "sess-long") &&
			!slices.Contains(cfg.Cmd, "/compact")
	}), "loop-ch-1-aabbcc").Return("container-fail", nil).Once()
	s.client.On("ContainerLogs", ctx, "container-fail").Return(failReader, nil)
	s.client.On("ContainerStart", ctx, "container-fail").Return(nil)
	s.client.On("ContainerWait", ctx, "container-fail").Return((<-chan WaitResponse)(failWait), (<-chan error)(failErr))

	// Compact
	compactJSON := `{"type":"result","result":"Compacted","session_id":"sess-compacted","is_error":false}`
	compactReader := bytes.NewReader([]byte(compactJSON))
	compactWait := make(chan WaitResponse, 1)
	compactWait <- WaitResponse{StatusCode: 0}
	compactErrCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return slices.Contains(cfg.Cmd, "--resume") && slices.Contains(cfg.Cmd, "sess-long") &&
			slices.Contains(cfg.Cmd, "/compact")
	}), "loop-ch-1-aabbcc").Return("container-compact", nil).Once()
	s.client.On("ContainerLogs", ctx, "container-compact").Return(compactReader, nil)
	s.client.On("ContainerStart", ctx, "container-compact").Return(nil)
	s.client.On("ContainerWait", ctx, "container-compact").Return((<-chan WaitResponse)(compactWait), (<-chan error)(compactErrCh))

	// Retry with compacted session
	retryJSON := `{"type":"result","result":"Hello!","session_id":"sess-compacted","is_error":false}`
	retryReader := bytes.NewReader([]byte(retryJSON))
	retryWait := make(chan WaitResponse, 1)
	retryWait <- WaitResponse{StatusCode: 0}
	retryErrCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return slices.Contains(cfg.Cmd, "--resume") && slices.Contains(cfg.Cmd, "sess-compacted") &&
			!slices.Contains(cfg.Cmd, "/compact")
	}), "loop-ch-1-aabbcc").Return("container-retry", nil).Once()
	s.client.On("ContainerLogs", ctx, "container-retry").Return(retryReader, nil)
	s.client.On("ContainerStart", ctx, "container-retry").Return(nil)
	s.client.On("ContainerWait", ctx, "container-retry").Return((<-chan WaitResponse)(retryWait), (<-chan error)(retryErrCh))

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Hello!", resp.Response)
	require.Equal(s.T(), "sess-compacted", resp.SessionID)

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunUsesExplicitPromptOverLastMessage() {
	ctx := context.Background()
	req := &agent.AgentRequest{
		SessionID: "sess-1",
		Messages: []agent.AgentMessage{
			{Role: "user", Content: "Alice: first message"},
			{Role: "user", Content: "Bob: second message"},
		},
		ChannelID: "ch-1",
		Prompt:    "Alice: first message",
	}

	s.setupMockRun(ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		// The last element of Cmd is the prompt — it should be the explicit Prompt,
		// NOT the last message content.
		lastArg := cfg.Cmd[len(cfg.Cmd)-1]
		return lastArg == "Alice: first message"
	}), testContainerName, `{"type":"result","result":"Hello!","session_id":"sess-new","is_error":false}`)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Hello!", resp.Response)

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunWorktreeParentDirMount() {
	ctx := context.Background()
	req := &agent.AgentRequest{
		SessionID:     "sess-1",
		Messages:      []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID:     "ch-1",
		DirPath:       "/projects/myapp/.worktrees/wt1",
		ParentDirPath: "/projects/myapp",
	}

	s.setupMockRun(ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		// Parent dir mounted (includes the worktree subdir), workDir not separately mounted.
		hasParent := slices.Contains(cfg.Binds, "/projects/myapp:/projects/myapp")
		noSeparateWorkDir := !slices.Contains(cfg.Binds, "/projects/myapp/.worktrees/wt1:/projects/myapp/.worktrees/wt1")
		return hasParent && noSeparateWorkDir && cfg.WorkingDir == "/projects/myapp/.worktrees/wt1"
	}), "loop-wt1-aabbcc",
		`{"type":"result","result":"ok","session_id":"s1","is_error":false}`)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp.Response)
	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunWorktreeUsesWorktreeProjectConfig() {
	// When ParentDirPath is set, loadWorktreeProjectConfig should be called instead of loadProjectConfig.
	ctx := context.Background()
	req := &agent.AgentRequest{
		ChannelID:     "ch-1",
		DirPath:       "/projects/myapp/.worktrees/wt1",
		ParentDirPath: "/projects/myapp",
	}

	worktreeCfgCalled := false
	s.runner.loadWorktreeProjectConfig = func(worktreeDir, parentDir string, cfg *config.Config) (*config.Config, error) {
		require.Equal(s.T(), "/projects/myapp/.worktrees/wt1", worktreeDir)
		require.Equal(s.T(), "/projects/myapp", parentDir)
		worktreeCfgCalled = true
		return cfg, nil
	}

	s.setupMockRun(ctx, mock.Anything, "loop-wt1-aabbcc",
		`{"type":"result","result":"ok","session_id":"s1","is_error":false}`)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp.Response)
	require.True(s.T(), worktreeCfgCalled, "loadWorktreeProjectConfig should have been called")
	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunWorktreeProjectConfigError() {
	ctx := context.Background()
	req := &agent.AgentRequest{
		ChannelID:     "ch-1",
		DirPath:       "/projects/myapp/.worktrees/wt1",
		ParentDirPath: "/projects/myapp",
	}

	s.runner.loadWorktreeProjectConfig = func(_, _ string, _ *config.Config) (*config.Config, error) {
		return nil, errors.New("permission denied")
	}

	resp, err := s.runner.Run(ctx, req)
	require.Error(s.T(), err)
	require.Nil(s.T(), resp)
	require.Contains(s.T(), err.Error(), "loading project config")
}

func (s *RunnerSuite) TestRunCreateFails() {
	ctx := context.Background()
	req := &agent.AgentRequest{ChannelID: "ch-1"}

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), "loop-ch-1-aabbcc").Return("", errors.New("docker create failed"))

	resp, err := s.runner.Run(ctx, req)
	require.Nil(s.T(), resp)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating container")

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunLogsFails() {
	ctx := context.Background()
	req := &agent.AgentRequest{ChannelID: "ch-1"}

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), "loop-ch-1-aabbcc").Return("container-123", nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerLogs", ctx, "container-123").Return(nil, errors.New("logs failed"))

	resp, err := s.runner.Run(ctx, req)
	require.Nil(s.T(), resp)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading container logs")

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunStartFails() {
	ctx := context.Background()
	req := &agent.AgentRequest{ChannelID: "ch-1"}

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), "loop-ch-1-aabbcc").Return("container-123", nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(errors.New("start failed"))

	resp, err := s.runner.Run(ctx, req)
	require.Nil(s.T(), resp)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "starting container")

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunTimeout() {
	ctx, cancel := context.WithCancel(context.Background())

	req := &agent.AgentRequest{ChannelID: "ch-1"}

	waitCh := make(chan WaitResponse)
	errCh := make(chan error)

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), "loop-ch-1-aabbcc").Return("container-123", nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerStop", mock.Anything, "container-123").Return(nil)

	// Cancel context to simulate timeout
	cancel()

	resp, err := s.runner.Run(ctx, req)
	require.Nil(s.T(), resp)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "container execution timed out")

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunWaitError() {
	ctx := context.Background()
	req := &agent.AgentRequest{ChannelID: "ch-1"}

	waitCh := make(chan WaitResponse, 1)
	errCh := make(chan error, 1)
	errCh <- errors.New("wait error")

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), "loop-ch-1-aabbcc").Return("container-123", nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))

	resp, err := s.runner.Run(ctx, req)
	require.Nil(s.T(), resp)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "waiting for container")

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunWaitErrChNil() {
	ctx := context.Background()

	reader := strings.NewReader(`{"type":"result","result":"Ok","session_id":"s-1"}` + "\n")

	req := &agent.AgentRequest{ChannelID: "ch-1"}

	waitCh := make(chan WaitResponse) // never written to
	errCh := make(chan error, 1)
	errCh <- nil // nil error on errCh

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), "loop-ch-1-aabbcc").Return("container-123", nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerLogs", ctx, "container-123").Return(reader, nil)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Ok", resp.Response)

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunContainerExitError() {
	ctx := context.Background()
	req := &agent.AgentRequest{ChannelID: "ch-1"}

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 1, Error: errors.New("exit code 1")}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), "loop-ch-1-aabbcc").Return("container-123", nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))

	resp, err := s.runner.Run(ctx, req)
	require.Nil(s.T(), resp)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "container exited with error")

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunOutputErrors() {
	tests := []struct {
		name      string
		reader    io.Reader
		exitCode  int64
		wantErr   string
		checkResp func(*testing.T, *agent.AgentResponse)
	}{
		{
			name:    "read output error",
			reader:  &errReader{err: errors.New("read error")},
			wantErr: "reading container output",
		},
		{
			name:    "JSON parse error",
			reader:  bytes.NewReader([]byte("not valid json")),
			wantErr: "parsing claude response",
		},
		{
			name:    "Claude error",
			reader:  bytes.NewReader([]byte(`{"type":"result","result":"something went wrong","session_id":"sess-err","is_error":true}`)),
			wantErr: "claude returned error",
			checkResp: func(t *testing.T, resp *agent.AgentResponse) {
				require.NotNil(t, resp)
				require.Equal(t, "sess-err", resp.SessionID)
				require.Equal(t, "something went wrong", resp.Error)
			},
		},
		{
			name:     "non-zero exit code",
			reader:   bytes.NewReader([]byte("")),
			exitCode: 1,
			wantErr:  "container exited with code 1",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.client = new(MockDockerClient)
			s.runner = NewDockerRunner(s.client, s.cfg, nil)
			s.applyMockDefaults()

			ctx := context.Background()
			req := &agent.AgentRequest{ChannelID: "ch-1"}

			waitCh := make(chan WaitResponse, 1)
			waitCh <- WaitResponse{StatusCode: tt.exitCode}
			errCh := make(chan error, 1)

			s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), testContainerName).Return(testContainerID, nil)
			s.client.On("ContainerLogs", ctx, testContainerID).Return(tt.reader, nil)
			s.client.On("ContainerStart", ctx, testContainerID).Return(nil)
			s.client.On("ContainerWait", ctx, testContainerID).Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))

			resp, err := s.runner.Run(ctx, req)
			require.Error(s.T(), err)
			require.Contains(s.T(), err.Error(), tt.wantErr)
			if tt.checkResp != nil {
				tt.checkResp(s.T(), resp)
			} else {
				require.Nil(s.T(), resp)
			}

			s.client.AssertExpectations(s.T())
		})
	}
}

func (s *RunnerSuite) TestRunAuthConfig() {
	tests := []struct {
		name       string
		oauthToken string
		apiKey     string
		checkEnv   func(*ContainerConfig) bool
	}{
		{
			name:       "OAuth token",
			oauthToken: "sk-ant-test-token",
			checkEnv: func(cfg *ContainerConfig) bool {
				return slices.Contains(cfg.Env, "CLAUDE_CODE_OAUTH_TOKEN=sk-ant-test-token") &&
					!slices.Contains(cfg.Cmd, "--resume")
			},
		},
		{
			name:   "API key",
			apiKey: "sk-ant-api-key-123",
			checkEnv: func(cfg *ContainerConfig) bool {
				return slices.Contains(cfg.Env, "ANTHROPIC_API_KEY=sk-ant-api-key-123") &&
					!slices.ContainsFunc(cfg.Env, func(e string) bool {
						return strings.HasPrefix(e, "CLAUDE_CODE_OAUTH_TOKEN=")
					})
			},
		},
		{
			name:       "OAuth takes precedence over API key",
			oauthToken: "sk-ant-oauth-token",
			apiKey:     "sk-ant-api-key-123",
			checkEnv: func(cfg *ContainerConfig) bool {
				return slices.Contains(cfg.Env, "CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oauth-token") &&
					!slices.ContainsFunc(cfg.Env, func(e string) bool {
						return strings.HasPrefix(e, "ANTHROPIC_API_KEY=")
					})
			},
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.client = new(MockDockerClient)
			s.cfg.ClaudeCodeOAuthToken = tt.oauthToken
			s.cfg.AnthropicAPIKey = tt.apiKey
			s.runner = NewDockerRunner(s.client, s.cfg, nil)
			s.applyMockDefaults()

			ctx := context.Background()
			req := &agent.AgentRequest{
				Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
				ChannelID: "ch-1",
			}

			s.setupMockRun(ctx, mock.MatchedBy(tt.checkEnv), testContainerName,
				`{"type":"result","result":"response","session_id":"sess-new","is_error":false}`)

			resp, err := s.runner.Run(ctx, req)
			require.NoError(s.T(), err)
			require.Equal(s.T(), "response", resp.Response)
			require.Equal(s.T(), "sess-new", resp.SessionID)

			s.client.AssertExpectations(s.T())
		})
	}
}

func (s *RunnerSuite) TestRunProxyEnv() {
	tests := []struct {
		name     string
		envs     map[string]string
		checkEnv func(*ContainerConfig) bool
	}{
		{
			name: "all proxy envs forwarded",
			envs: map[string]string{
				"HTTP_PROXY":  "http://proxy:8080",
				"HTTPS_PROXY": "http://proxy:8443",
				"NO_PROXY":    "localhost,127.0.0.1",
			},
			checkEnv: func(cfg *ContainerConfig) bool {
				return slices.Contains(cfg.Env, "HTTP_PROXY=http://proxy:8080") &&
					slices.Contains(cfg.Env, "HTTPS_PROXY=http://proxy:8443") &&
					slices.Contains(cfg.Env, "NO_PROXY=localhost,127.0.0.1,host.docker.internal,::1")
			},
		},
		{
			name: "NO_PROXY auto-added",
			envs: map[string]string{
				"HTTP_PROXY": "http://proxy:8080",
			},
			checkEnv: func(cfg *ContainerConfig) bool {
				return slices.Contains(cfg.Env, "HTTP_PROXY=http://proxy:8080") &&
					slices.Contains(cfg.Env, "NO_PROXY=host.docker.internal,localhost,127.0.0.1,::1") &&
					slices.Contains(cfg.Env, "no_proxy=host.docker.internal,localhost,127.0.0.1,::1")
			},
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.client = new(MockDockerClient)
			s.runner = NewDockerRunner(s.client, s.cfg, nil)
			s.applyMockDefaults()
			// Re-register Getenv expectations with specific env vars first, catch-all last.
			s.sys.Override("Getenv", "USER").Return("testuser")
			for k, v := range tt.envs {
				s.sys.On("Getenv", k).Return(v)
			}
			s.sys.On("Getenv", mock.Anything).Return("")

			ctx := context.Background()
			req := &agent.AgentRequest{
				Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
				ChannelID: "ch-1",
			}

			s.setupMockRun(ctx, mock.MatchedBy(tt.checkEnv), testContainerName, testJSONOK)

			resp, err := s.runner.Run(ctx, req)
			require.NoError(s.T(), err)
			require.Equal(s.T(), "ok", resp.Response)

			s.client.AssertExpectations(s.T())
		})
	}
}

func (s *RunnerSuite) TestRunConfigEnvsForwarding() {
	s.cfg.Envs = map[string]string{
		"CUSTOM_VAR": "custom-value",
		"GOMODCACHE": "~/go/pkg/mod",
	}

	ctx := context.Background()
	req := &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
	}

	s.setupMockRun(ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return slices.Contains(cfg.Env, "CUSTOM_VAR=custom-value") &&
			slices.Contains(cfg.Env, "GOMODCACHE=/home/testuser/go/pkg/mod")
	}), testContainerName, testJSONOK)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp.Response)

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunConfigEnvsExpandError() {
	s.cfg.Envs = map[string]string{
		"BAD_VAR": "~/some/path",
	}
	s.sys.Override("UserHomeDir").Return("/home/testuser", nil).Once()
	s.sys.On("UserHomeDir").Return("", errors.New("home error"))

	ctx := context.Background()
	req := &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
	}

	_, err := s.runner.Run(ctx, req)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "expanding env")
}

func (s *RunnerSuite) TestCleanup() {
	ctx := context.Background()

	s.client.On("ContainerList", ctx, InstanceLabelKey, "test-instance").Return([]string{"c1", "c2"}, nil)
	s.client.On("ContainerRemove", ctx, "c1").Return(nil)
	s.client.On("ContainerRemove", ctx, "c2").Return(nil)

	err := s.runner.Cleanup(ctx)
	require.NoError(s.T(), err)

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestCleanupListError() {
	ctx := context.Background()

	s.client.On("ContainerList", ctx, InstanceLabelKey, "test-instance").Return([]string(nil), errors.New("list error"))

	err := s.runner.Cleanup(ctx)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "listing containers")

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestCleanupRemoveError() {
	ctx := context.Background()

	s.client.On("ContainerList", ctx, InstanceLabelKey, "test-instance").Return([]string{"c1", "c2"}, nil)
	s.client.On("ContainerRemove", ctx, "c1").Return(errors.New("remove failed"))
	s.client.On("ContainerRemove", ctx, "c2").Return(nil)

	err := s.runner.Cleanup(ctx)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "removing container c1")

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestCleanupNoContainers() {
	ctx := context.Background()

	s.client.On("ContainerList", ctx, InstanceLabelKey, "test-instance").Return([]string{}, nil)

	err := s.runner.Cleanup(ctx)
	require.NoError(s.T(), err)

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunRetryWithSession() {
	ctx := context.Background()
	req := &agent.AgentRequest{
		SessionID: "stale-sess",
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
	}

	// First attempt (with session) — non-JSON output, exit code 1
	failReader := bytes.NewReader([]byte("No session found"))
	failWaitCh := make(chan WaitResponse, 1)
	failWaitCh <- WaitResponse{StatusCode: 1}
	failErrCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return slices.Contains(cfg.Cmd, "--resume") && slices.Contains(cfg.Cmd, "stale-sess")
	}), "loop-ch-1-aabbcc").Return("container-fail", nil).Once()
	s.client.On("ContainerLogs", ctx, "container-fail").Return(failReader, nil)
	s.client.On("ContainerStart", ctx, "container-fail").Return(nil)
	s.client.On("ContainerWait", ctx, "container-fail").Return((<-chan WaitResponse)(failWaitCh), (<-chan error)(failErrCh))

	// Retry (with session, prompt only) — succeeds
	okJSON := `{"type":"result","result":"Hello!","session_id":"new-sess","is_error":false}`
	okReader := bytes.NewReader([]byte(okJSON))
	okWaitCh := make(chan WaitResponse, 1)
	okWaitCh <- WaitResponse{StatusCode: 0}
	okErrCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return slices.Contains(cfg.Cmd, "--resume") && slices.Contains(cfg.Cmd, "stale-sess")
	}), "loop-ch-1-aabbcc").Return("container-ok", nil).Once()
	s.client.On("ContainerLogs", ctx, "container-ok").Return(okReader, nil)
	s.client.On("ContainerStart", ctx, "container-ok").Return(nil)
	s.client.On("ContainerWait", ctx, "container-ok").Return((<-chan WaitResponse)(okWaitCh), (<-chan error)(okErrCh))

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Hello!", resp.Response)
	require.Equal(s.T(), "new-sess", resp.SessionID)

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunRetryAlsoFails() {
	ctx := context.Background()
	req := &agent.AgentRequest{
		SessionID: "stale-sess",
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
	}

	// First attempt (with session) — fails
	failReader1 := bytes.NewReader([]byte("No session found"))
	failWaitCh1 := make(chan WaitResponse, 1)
	failWaitCh1 <- WaitResponse{StatusCode: 1}
	failErrCh1 := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return slices.Contains(cfg.Cmd, "--resume") && slices.Contains(cfg.Cmd, "stale-sess")
	}), "loop-ch-1-aabbcc").Return("container-fail1", nil).Once()
	s.client.On("ContainerLogs", ctx, "container-fail1").Return(failReader1, nil)
	s.client.On("ContainerStart", ctx, "container-fail1").Return(nil)
	s.client.On("ContainerWait", ctx, "container-fail1").Return((<-chan WaitResponse)(failWaitCh1), (<-chan error)(failErrCh1))

	// Retry (with session, prompt only) — also fails
	failReader2 := bytes.NewReader([]byte("some other error"))
	failWaitCh2 := make(chan WaitResponse, 1)
	failWaitCh2 <- WaitResponse{StatusCode: 1}
	failErrCh2 := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return slices.Contains(cfg.Cmd, "--resume") && slices.Contains(cfg.Cmd, "stale-sess")
	}), "loop-ch-1-aabbcc").Return("container-fail2", nil).Once()
	s.client.On("ContainerLogs", ctx, "container-fail2").Return(failReader2, nil)
	s.client.On("ContainerStart", ctx, "container-fail2").Return(nil)
	s.client.On("ContainerWait", ctx, "container-fail2").Return((<-chan WaitResponse)(failWaitCh2), (<-chan error)(failErrCh2))

	resp, err := s.runner.Run(ctx, req)
	require.Nil(s.T(), resp)
	require.Error(s.T(), err)
	// Returns original error (from first attempt)
	require.Contains(s.T(), err.Error(), "container exited with code 1")

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunHomeDirError() {
	s.sys.Override("UserHomeDir").Return("", errors.New("home dir error"))

	ctx := context.Background()
	req := &agent.AgentRequest{ChannelID: "ch-1"}

	resp, err := s.runner.Run(ctx, req)
	require.Nil(s.T(), resp)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting home directory")
}

func (s *RunnerSuite) TestRunMkdirAllError() {
	s.sys.Override("MkdirAll", mock.Anything, mock.Anything).Return(errors.New("mkdir fail"))

	ctx := context.Background()
	req := &agent.AgentRequest{ChannelID: "ch-1"}

	resp, err := s.runner.Run(ctx, req)
	require.Nil(s.T(), resp)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating work dir")
}

func (s *RunnerSuite) TestRunMkdirAllMCPSubdirError() {
	// workDir mkdir succeeds, but .loop subdir fails inside writeMCPConfig.
	s.sys.Override("MkdirAll", mock.Anything, mock.Anything).Return(nil).Once()
	s.sys.On("MkdirAll", mock.Anything, mock.Anything).Return(errors.New("mkdir subdir fail"))

	ctx := context.Background()
	req := &agent.AgentRequest{ChannelID: "ch-1"}

	resp, err := s.runner.Run(ctx, req)
	require.Nil(s.T(), resp)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating host directory")
}

// Default implementations for MkdirAll, Getenv, and UserHomeDir are now
// provided by osutil.RealSystem (tested in osutil package).

func (s *RunnerSuite) TestAddAuthEnv() {
	tests := []struct {
		name       string
		oauthToken string
		apiKey     string
		want       []string
	}{
		{
			name:       "OAuth token set",
			oauthToken: "oauth-tok",
			want:       []string{"BASE=1", "CLAUDE_CODE_OAUTH_TOKEN=oauth-tok"},
		},
		{
			name:   "API key set",
			apiKey: "api-key",
			want:   []string{"BASE=1", "ANTHROPIC_API_KEY=api-key"},
		},
		{
			name:       "OAuth takes precedence",
			oauthToken: "oauth-tok",
			apiKey:     "api-key",
			want:       []string{"BASE=1", "CLAUDE_CODE_OAUTH_TOKEN=oauth-tok"},
		},
		{
			name: "neither set",
			want: []string{"BASE=1"},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			cfg := &config.Config{
				ClaudeCodeOAuthToken: tc.oauthToken,
				AnthropicAPIKey:      tc.apiKey,
			}
			result := addAuthEnv([]string{"BASE=1"}, cfg)
			require.Equal(s.T(), tc.want, result)
		})
	}
}

func (s *RunnerSuite) TestAddProxyEnv() {
	tests := []struct {
		name string
		envs map[string]string
		want []string
	}{
		{
			name: "no proxy vars",
			envs: map[string]string{},
			want: []string{"BASE=1"},
		},
		{
			name: "HTTP_PROXY forwarded with NO_PROXY added",
			envs: map[string]string{"HTTP_PROXY": "http://proxy:8080"},
			want: []string{"BASE=1", "HTTP_PROXY=http://proxy:8080", "NO_PROXY=host.docker.internal,localhost,127.0.0.1,::1", "no_proxy=host.docker.internal,localhost,127.0.0.1,::1"},
		},
		{
			name: "localhost rewritten to docker host",
			envs: map[string]string{"HTTP_PROXY": "http://localhost:3128"},
			want: []string{"BASE=1", "HTTP_PROXY=http://host.docker.internal:3128", "NO_PROXY=host.docker.internal,localhost,127.0.0.1,::1", "no_proxy=host.docker.internal,localhost,127.0.0.1,::1"},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			sys := new(testutil.MockSystem)
			for k, v := range tc.envs {
				sys.On("Getenv", k).Return(v)
			}
			sys.On("Getenv", mock.Anything).Return("")
			s.runner.sys = sys

			result := s.runner.addProxyEnv([]string{"BASE=1"})
			require.Equal(s.T(), tc.want, result)

			s.runner.sys = s.sys // restore
		})
	}
}

func (s *RunnerSuite) TestLocalhostToDockerHost() {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"bare port", ":3128", "http://host.docker.internal:3128"},
		{"localhost with port", "http://localhost:3128", "http://host.docker.internal:3128"},
		{"127.0.0.1 with port", "http://127.0.0.1:3128", "http://host.docker.internal:3128"},
		{"https localhost", "https://localhost:3128", "https://host.docker.internal:3128"},
		{"localhost no port", "http://localhost", "http://host.docker.internal"},
		{"127.0.0.1 no port", "http://127.0.0.1", "http://host.docker.internal"},
		{"localhost with path", "http://localhost/proxy", "http://host.docker.internal/proxy"},
		{"remote proxy unchanged", "http://proxy.corp:8080", "http://proxy.corp:8080"},
		{"empty string", "", ""},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, localhostToDockerHost(tc.input))
		})
	}
}

func (s *RunnerSuite) TestEnsureNoProxy() {
	tests := []struct {
		name       string
		env        []string
		extraHosts []string
		want       []string
	}{
		{
			"appends to existing NO_PROXY",
			[]string{"HTTP_PROXY=http://proxy:8080", "NO_PROXY=localhost,127.0.0.1"},
			nil,
			[]string{"HTTP_PROXY=http://proxy:8080", "NO_PROXY=localhost,127.0.0.1,host.docker.internal,::1"},
		},
		{
			"appends to existing no_proxy",
			[]string{"http_proxy=http://proxy:8080", "no_proxy=localhost"},
			nil,
			[]string{"http_proxy=http://proxy:8080", "no_proxy=localhost,host.docker.internal,127.0.0.1,::1"},
		},
		{
			"adds both NO_PROXY and no_proxy when missing",
			[]string{"HTTP_PROXY=http://proxy:8080"},
			nil,
			[]string{"HTTP_PROXY=http://proxy:8080", "NO_PROXY=host.docker.internal,localhost,127.0.0.1,::1", "no_proxy=host.docker.internal,localhost,127.0.0.1,::1"},
		},
		{
			"no-op when already present",
			[]string{"NO_PROXY=host.docker.internal,localhost,127.0.0.1,::1,other"},
			nil,
			[]string{"NO_PROXY=host.docker.internal,localhost,127.0.0.1,::1,other"},
		},
		{
			"empty NO_PROXY value",
			[]string{"NO_PROXY="},
			nil,
			[]string{"NO_PROXY=host.docker.internal,localhost,127.0.0.1,::1"},
		},
		{
			"extra hosts added to NO_PROXY",
			[]string{"HTTP_PROXY=http://proxy:8080", "NO_PROXY=localhost"},
			[]string{"loop-chrome-ch1"},
			[]string{"HTTP_PROXY=http://proxy:8080", "NO_PROXY=localhost,host.docker.internal,127.0.0.1,::1,loop-chrome-ch1"},
		},
		{
			"extra hosts added when NO_PROXY missing",
			[]string{"HTTP_PROXY=http://proxy:8080"},
			[]string{"loop-chrome-ch1"},
			[]string{"HTTP_PROXY=http://proxy:8080", "NO_PROXY=host.docker.internal,localhost,127.0.0.1,::1,loop-chrome-ch1", "no_proxy=host.docker.internal,localhost,127.0.0.1,::1,loop-chrome-ch1"},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			result := ensureNoProxy(tc.env, tc.extraHosts...)
			require.Equal(s.T(), tc.want, result)
		})
	}
}

func (s *RunnerSuite) TestBuildMCPConfig() {
	cfg := buildMCPConfig("ch-1", "http://host.docker.internal:8222", "/home/user/project", "", "", false, true, nil)
	require.Len(s.T(), cfg.MCPServers, 2)
	ls := cfg.MCPServers["loop"]
	require.Equal(s.T(), "/usr/local/bin/loop", ls.Command)
	require.Equal(s.T(), []string{"mcp", "--channel-id", "ch-1", "--api-url", "http://host.docker.internal:8222", "--log", "/home/user/project/.loop/mcp.log"}, ls.Args)
	require.Nil(s.T(), ls.Env)

	bs := cfg.MCPServers["loop-browser"]
	require.Equal(s.T(), "/usr/local/bin/loop", bs.Command)
	require.Equal(s.T(), []string{"mcp-browser", "--log", "/home/user/project/.loop/mcp-browser.log", "--api-url", "http://host.docker.internal:8222", "--channel-id", "ch-1"}, bs.Args)
}

func (s *RunnerSuite) TestBuildMCPConfigBrowserDisabled() {
	cfg := buildMCPConfig("ch-1", "http://host.docker.internal:8222", "/home/user/project", "", "", false, false, nil)
	require.Len(s.T(), cfg.MCPServers, 1)
	_, hasBrowser := cfg.MCPServers["loop-browser"]
	require.False(s.T(), hasBrowser)
	_, hasLoop := cfg.MCPServers["loop"]
	require.True(s.T(), hasLoop)
}

func (s *RunnerSuite) TestBuildMCPConfigWithAuthorID() {
	cfg := buildMCPConfig("ch-1", "http://host.docker.internal:8222", "/home/user/project", "user-42", "", false, true, nil)
	require.Len(s.T(), cfg.MCPServers, 2)
	ls := cfg.MCPServers["loop"]
	require.Equal(s.T(), "/usr/local/bin/loop", ls.Command)
	require.Equal(s.T(), []string{"mcp", "--channel-id", "ch-1", "--api-url", "http://host.docker.internal:8222", "--log", "/home/user/project/.loop/mcp.log", "--author-id", "user-42"}, ls.Args)
}

func (s *RunnerSuite) TestBuildMCPConfigWithUserServers() {
	userServers := map[string]config.MCPServerConfig{
		"custom-tool": {
			Command: "/path/to/binary",
			Args:    []string{"--flag"},
			Env:     map[string]string{"API_KEY": "secret"},
		},
	}
	cfg := buildMCPConfig("ch-1", "http://host.docker.internal:8222", "/home/user/project", "", "", false, true, userServers)
	require.Len(s.T(), cfg.MCPServers, 3)

	custom := cfg.MCPServers["custom-tool"]
	require.Equal(s.T(), "/path/to/binary", custom.Command)
	require.Equal(s.T(), []string{"--flag"}, custom.Args)
	require.Equal(s.T(), map[string]string{"API_KEY": "secret"}, custom.Env)

	ls := cfg.MCPServers["loop"]
	require.Equal(s.T(), "/usr/local/bin/loop", ls.Command)
}

func (s *RunnerSuite) TestBuildMCPConfigUserLoopPreserved() {
	userServers := map[string]config.MCPServerConfig{
		"loop": {
			Command: "/user/custom/loop",
			Args:    []string{"--custom-flag"},
		},
	}
	cfg := buildMCPConfig("ch-1", "http://host.docker.internal:8222", "/home/user/project", "", "", false, true, userServers)
	require.Len(s.T(), cfg.MCPServers, 2)
	ls := cfg.MCPServers["loop"]
	require.Equal(s.T(), "/user/custom/loop", ls.Command)
	require.Equal(s.T(), []string{"--custom-flag"}, ls.Args)
}

func (s *RunnerSuite) TestBuildMCPConfigUserBrowserPreserved() {
	userServers := map[string]config.MCPServerConfig{
		"loop-browser": {
			Command: "/user/custom/browser",
			Args:    []string{"--port", "9999"},
		},
	}
	cfg := buildMCPConfig("ch-1", "http://host.docker.internal:8222", "/home/user/project", "", "", false, true, userServers)
	require.Len(s.T(), cfg.MCPServers, 2)
	bs := cfg.MCPServers["loop-browser"]
	require.Equal(s.T(), "/user/custom/browser", bs.Command)
	require.Equal(s.T(), []string{"--port", "9999"}, bs.Args)
}

func (s *RunnerSuite) TestBuildMCPConfigWithMemory() {
	cfg := buildMCPConfig("ch-1", "http://host.docker.internal:8222", "/home/user/project", "", "", true, true, nil)
	require.Len(s.T(), cfg.MCPServers, 2)
	ls := cfg.MCPServers["loop"]
	require.Contains(s.T(), ls.Args, "--memory")
}

func (s *RunnerSuite) TestBuildMCPConfigWithAgentID() {
	cfg := buildMCPConfig("ch-1", "http://host.docker.internal:8222", "/home/user/project", "", "agent-0", false, true, nil)
	require.Len(s.T(), cfg.MCPServers, 2)
	ls := cfg.MCPServers["loop"]
	require.Contains(s.T(), ls.Args, "--agent-id")
	require.Contains(s.T(), ls.Args, "agent-0")
}

func (s *RunnerSuite) TestRunBrowserDisabledNoNetwork() {
	s.cfg.Browser.Enabled = false
	s.runner = NewDockerRunner(s.client, s.cfg, nil)
	s.applyMockDefaults()
	ctx := context.Background()

	s.setupMockRun(ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return cfg.NetworkName == "" && cfg.Hostname == ""
	}), testContainerName, testJSONOK)

	resp, err := s.runner.Run(ctx, &agent.AgentRequest{
		ChannelID: "ch-1",
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hi"}},
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp.Response)
}

func (s *RunnerSuite) TestRunBrowserEnabledNoNetwork() {
	// Even with browser enabled, the agent container no longer joins a Docker
	// network — the mcp-browser server proxies actions through the host API instead.
	s.cfg.Browser.Enabled = true
	s.runner = NewDockerRunner(s.client, s.cfg, nil)
	s.applyMockDefaults()
	ctx := context.Background()

	s.setupMockRun(ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return cfg.NetworkName == "" && cfg.Hostname == ""
	}), testContainerName, testJSONOK)

	resp, err := s.runner.Run(ctx, &agent.AgentRequest{
		ChannelID: "ch-1",
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hi"}},
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp.Response)
}

func (s *RunnerSuite) TestRunWithScreenshotDirBind() {
	// Screenshot directory is always bind-mounted read-only.
	ctx := context.Background()

	screenshotDir := filepath.Join(s.cfg.LoopDir, "screenshots")

	s.setupMockRun(ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		hasScreenshotBind := slices.Contains(cfg.Binds, screenshotDir+":"+screenshotDir+":ro")
		return hasScreenshotBind
	}), testContainerName, testJSONOK)

	resp, err := s.runner.Run(ctx, &agent.AgentRequest{
		ChannelID: "ch-1",
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hi"}},
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp.Response)
}

func (s *RunnerSuite) TestRunWithDirPath() {
	ctx := context.Background()
	req := &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
		DirPath:   "/home/user/project",
	}

	s.setupMockRun(ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return slices.Contains(cfg.Binds, "/home/user/project:/home/user/project")
	}), "loop-project-aabbcc", testJSONOK)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp.Response)

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunMCPConfigWriteError() {
	s.sys.Override("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("write failed"))

	ctx := context.Background()
	req := &agent.AgentRequest{ChannelID: "ch-1"}

	resp, err := s.runner.Run(ctx, req)
	require.Nil(s.T(), resp)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing mcp config")
}

func (s *RunnerSuite) TestRunMCPConfigWritten() {
	var writtenPath string
	var writtenData []byte
	s.sys.Override("WriteFile", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			writtenPath = args.String(0)
			writtenData = args.Get(1).([]byte)
		}).Return(nil)

	ctx := context.Background()
	req := &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
	}

	s.setupMockRun(ctx, mock.AnythingOfType("*container.ContainerConfig"), testContainerName, testJSONOK)

	_, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)

	require.Equal(s.T(), "/home/testuser/.loop/ch-1/work/.loop/mcp-ch-1.json", writtenPath)

	var cfg mcpConfig
	require.NoError(s.T(), json.Unmarshal(writtenData, &cfg))
	require.Contains(s.T(), cfg.MCPServers, "loop")
	ls := cfg.MCPServers["loop"]
	require.Equal(s.T(), "/usr/local/bin/loop", ls.Command)
	require.Equal(s.T(), []string{"mcp", "--channel-id", "ch-1", "--api-url", "http://host.docker.internal:8222", "--log", "/home/testuser/.loop/ch-1/work/.loop/mcp.log"}, ls.Args)

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunMCPConfigRemovedAfterRun() {
	var removedPath string
	s.sys.Override("Remove", mock.Anything).
		Run(func(args mock.Arguments) {
			removedPath = args.String(0)
		}).Return(nil)

	ctx := context.Background()
	req := &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
	}

	s.setupMockRun(ctx, mock.AnythingOfType("*container.ContainerConfig"), testContainerName, testJSONOK)

	_, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)

	require.Equal(s.T(), "/home/testuser/.loop/ch-1/work/.loop/mcp-ch-1.json", removedPath)
	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunKeepMCPConfigsSkipsRemoval() {
	s.cfg.KeepMCPConfigs = true

	ctx := context.Background()
	req := &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
	}

	s.setupMockRun(ctx, mock.AnythingOfType("*container.ContainerConfig"), testContainerName, testJSONOK)

	_, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)

	// Remove should NOT have been called — KeepMCPConfigs is true.
	s.sys.AssertNotCalled(s.T(), "Remove", mock.Anything)
	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunMCPConfigIncludesAgentID() {
	var writtenPath string
	var writtenData []byte
	s.sys.Override("WriteFile", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			writtenPath = args.String(0)
			writtenData = args.Get(1).([]byte)
		}).Return(nil)

	ctx := context.Background()
	req := &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
		AgentID:   "chat",
	}

	s.setupMockRun(ctx, mock.AnythingOfType("*container.ContainerConfig"), testContainerName, testJSONOK)

	_, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)

	// Per-agent MCP config path includes the agent ID.
	require.Equal(s.T(), "/home/testuser/.loop/ch-1/work/.loop/mcp-ch-1-chat.json", writtenPath)

	var cfg mcpConfig
	require.NoError(s.T(), json.Unmarshal(writtenData, &cfg))
	require.Contains(s.T(), cfg.MCPServers, "loop")
	ls := cfg.MCPServers["loop"]
	require.Contains(s.T(), ls.Args, "--agent-id")
	require.Contains(s.T(), ls.Args, "chat")

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunAgentIDAddsChannelFlag() {
	ctx := context.Background()
	req := &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
		AgentID:   "chat",
	}

	s.setupMockRun(ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return slices.Contains(cfg.Cmd, "--dangerously-load-development-channels")
	}), testContainerName, testJSONOK)

	_, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	s.client.AssertExpectations(s.T())
}

// Default implementation for WriteFile is now provided by osutil.RealSystem
// (tested in osutil package).

// errReader always returns an error on Read.
type errReader struct {
	err error
}

func (r *errReader) Read([]byte) (int, error) {
	return 0, r.err
}

// --- Tests for scanStreamJSON ---

func TestParseStreamJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantResp *claudeResponse
		wantErr  string
	}{
		{
			name:  "single result line",
			input: `{"type":"result","result":"hello","session_id":"s1","is_error":false}`,
			wantResp: &claudeResponse{
				Type:      "result",
				Result:    "hello",
				SessionID: "s1",
			},
		},
		{
			name: "multiple events with result last",
			input: `{"type":"system","data":"init"}
{"type":"assistant","message":"thinking..."}
{"type":"result","result":"done","session_id":"s2","is_error":false}`,
			wantResp: &claudeResponse{
				Type:      "result",
				Result:    "done",
				SessionID: "s2",
			},
		},
		{
			name:  "skips blank lines and non-JSON",
			input: "\nsome garbage\n\n" + `{"type":"result","result":"ok","session_id":"s3","is_error":false}` + "\n",
			wantResp: &claudeResponse{
				Type:      "result",
				Result:    "ok",
				SessionID: "s3",
			},
		},
		{
			name:    "no result event",
			input:   `{"type":"assistant","message":"hi"}`,
			wantErr: "no result event found",
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: "no result event found",
		},
		{
			name:  "large intermediate line exceeding default scanner buffer",
			input: `{"type":"assistant","message":"` + strings.Repeat("x", 128*1024) + `"}` + "\n" + `{"type":"result","result":"done","session_id":"s4","is_error":false}`,
			wantResp: &claudeResponse{
				Type:      "result",
				Result:    "done",
				SessionID: "s4",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := scanStreamJSON(strings.NewReader(tc.input), streamCallbacks{})
			if tc.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErr)
				require.Nil(t, resp)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.wantResp, resp)
			}
		})
	}
}

func TestParseStreamJSONReaderError(t *testing.T) {
	resp, err := scanStreamJSON(&errReader{err: errors.New("read error")}, streamCallbacks{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "reading container output")
	require.Nil(t, resp)
}

// --- Tests for new mount processing functions ---

func (s *RunnerSuite) TestExpandPath() {
	// Default UserHomeDir from newDefaultMockSystem returns "/home/testuser"

	tests := []struct {
		name     string
		input    string
		expected string
		wantErr  bool
	}{
		{
			name:     "expand tilde path",
			input:    "~/.claude",
			expected: "/home/testuser/.claude",
			wantErr:  false,
		},
		{
			name:     "absolute path unchanged",
			input:    "/absolute/path",
			expected: "/absolute/path",
			wantErr:  false,
		},
		{
			name:     "relative path unchanged",
			input:    "relative/path",
			expected: "relative/path",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			result, err := s.runner.expandPath(tt.input)
			if tt.wantErr {
				require.Error(s.T(), err)
			} else {
				require.NoError(s.T(), err)
				require.Equal(s.T(), tt.expected, result)
			}
		})
	}
}

func (s *RunnerSuite) TestExpandPathHomeDirError() {
	s.sys.Override("UserHomeDir").Return("", errors.New("home dir error"))

	_, err := s.runner.expandPath("~/.claude")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "home dir error")
}

func TestParseMountSpec(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantHost  string
		wantCont  string
		wantMode  string
		wantStr   string
		wantError bool
	}{
		{"host:container", "/src:/dst", "/src", "/dst", "", "/src:/dst", false},
		{"with mode", "/src:/dst:ro", "/src", "/dst", "ro", "/src:/dst:ro", false},
		{"named volume", "myvolume:/cache", "myvolume", "/cache", "", "myvolume:/cache", false},
		{"named volume with mode", "myvolume:/cache:rw", "myvolume", "/cache", "rw", "myvolume:/cache:rw", false},
		{"invalid no colon", "/src", "", "", "", "", true},
		{"invalid empty", "", "", "", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms, err := parseMountSpec(tt.input)
			if tt.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantHost, ms.Host)
			require.Equal(t, tt.wantCont, ms.Container)
			require.Equal(t, tt.wantMode, ms.Mode)
			require.Equal(t, tt.wantStr, ms.String())
		})
	}
}

func (s *RunnerSuite) TestProcessMount() {
	// Default UserHomeDir from newDefaultMockSystem returns "/home/testuser"
	s.sys.Override("Stat", "/home/testuser/.claude").Return(nil, nil)
	s.sys.On("Stat", "/home/testuser/.gitconfig").Return(nil, nil)
	s.sys.On("Stat", mock.Anything).Return(nil, os.ErrNotExist)

	tests := []struct {
		name     string
		input    string
		expected string
		wantErr  bool
	}{
		{
			name:     "valid mount with tilde expansion on both sides",
			input:    "~/.claude:~/.claude",
			expected: "/home/testuser/.claude:/home/testuser/.claude",
			wantErr:  false,
		},
		{
			name:     "valid mount with readonly flag",
			input:    "~/.gitconfig:~/.gitconfig:ro",
			expected: "/home/testuser/.gitconfig:/home/testuser/.gitconfig:ro",
			wantErr:  false,
		},
		{
			name:     "non-existent path returns empty",
			input:    "~/.nonexistent:/target",
			expected: "",
			wantErr:  false,
		},
		{
			name:     "named volume",
			input:    "gomodcache:/go/pkg/mod",
			expected: "gomodcache:/go/pkg/mod",
			wantErr:  false,
		},
		{
			name:     "named volume with mode",
			input:    "gobuildcache:/root/.cache/go-build:rw",
			expected: "gobuildcache:/root/.cache/go-build:rw",
			wantErr:  false,
		},
		{
			name:     "named volume with tilde container path",
			input:    "npmcache:~/.npm",
			expected: "npmcache:/home/testuser/.npm",
			wantErr:  false,
		},
		{
			name:     "invalid format",
			input:    "invalid",
			expected: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			result, err := s.runner.processMount(tt.input)
			if tt.wantErr {
				require.Error(s.T(), err)
			} else {
				require.NoError(s.T(), err)
				require.Equal(s.T(), tt.expected, result)
			}
		})
	}
}

func TestIsNamedVolume(t *testing.T) {
	tests := []struct {
		source   string
		expected bool
	}{
		{"gomodcache", true},
		{"my-volume", true},
		{"/absolute/path", false},
		{"~/home/path", false},
		{"./relative/path", false},
		{"relative/path", false},
		{"", true}, // edge case but won't reach here due to mount format validation
	}
	for _, tt := range tests {
		t.Run(tt.source, func(t *testing.T) {
			require.Equal(t, tt.expected, config.IsNamedVolume(tt.source))
		})
	}
}

func (s *RunnerSuite) TestProcessMountExpandPathError() {
	s.sys.Override("UserHomeDir").Return("", errors.New("home dir error"))

	result, err := s.runner.processMount("~/.claude:~/.claude")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "expanding path")
	require.Empty(s.T(), result)
}

func (s *RunnerSuite) TestProcessMountContainerPathExpandError() {
	s.sys.Override("UserHomeDir").Return("/home/testuser", nil).Once()
	s.sys.On("UserHomeDir").Return("", errors.New("home dir error"))

	s.sys.Override("Stat", mock.Anything).Return(nil, nil)

	// Host uses ~ (triggers first osUserHomeDir call), container uses ~ (triggers second call that fails)
	result, err := s.runner.processMount("~/.claude:~/.claude")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "expanding container path")
	require.Empty(s.T(), result)
}

func (s *RunnerSuite) TestProcessMountNamedVolumeContainerPathExpandError() {
	s.sys.Override("UserHomeDir").Return("", errors.New("home dir error"))

	result, err := s.runner.processMount("myvolume:~/.cache")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "expanding container path")
	require.Empty(s.T(), result)
}

func (s *RunnerSuite) TestRunWithInvalidMount() {
	s.cfg.Mounts = []string{"invalid-mount-no-colon"}

	ctx := context.Background()
	req := &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
	}

	s.setupMockRun(ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		// Invalid mount should be skipped; workDir + screenshots + playground
		return len(cfg.Binds) == 3
	}), testContainerName, testJSONOK)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp.Response)

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunWithCustomMounts() {
	// Default UserHomeDir from newDefaultMockSystem returns "/home/testuser"
	s.sys.Override("Stat", "/home/testuser/.claude").Return(nil, nil)
	s.sys.On("Stat", "/home/testuser/.gitconfig").Return(nil, nil)
	s.sys.On("Stat", mock.Anything).Return(nil, os.ErrNotExist)

	s.cfg.Mounts = []string{
		"~/.claude:~/.claude",
		"~/.gitconfig:~/.gitconfig:ro",
		"~/.ssh:~/.ssh:ro", // This will be skipped as non-existent
	}

	ctx := context.Background()
	req := &agent.AgentRequest{
		SessionID:    "sess-1",
		Messages:     []agent.AgentMessage{{Role: "user", Content: "hello"}},
		SystemPrompt: "You are helpful",
		ChannelID:    "ch-1",
	}

	s.setupMockRun(ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		hasClaudeBind := slices.Contains(cfg.Binds, "/home/testuser/.claude:/home/testuser/.claude")
		hasGitBind := slices.Contains(cfg.Binds, "/home/testuser/.gitconfig:/home/testuser/.gitconfig:ro")
		hasWorkBind := slices.Contains(cfg.Binds, "/home/testuser/.loop/ch-1/work:/home/testuser/.loop/ch-1/work")
		return hasClaudeBind && hasGitBind && hasWorkBind &&
			cfg.WorkingDir == "/home/testuser/.loop/ch-1/work"
	}), testContainerName, `{"type":"result","result":"Hello!","session_id":"sess-new-1","is_error":false}`)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), resp)

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunNamedVolumesChownDirs() {
	// Default UserHomeDir and Stat from newDefaultMockSystem are sufficient

	s.cfg.Mounts = []string{
		"loop-gomodcache:/go/pkg/mod",
		"loop-npmcache:~/.npm",
	}

	ctx := context.Background()
	req := &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
	}

	s.setupMockRun(ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		hasChownDirs := false
		for _, e := range cfg.Env {
			if val, ok := strings.CutPrefix(e, "CHOWN_PATHS="); ok {
				hasChownDirs = strings.Contains(val, "/go/pkg/mod") &&
					strings.Contains(val, "/home/testuser/.npm")
			}
		}
		hasGomod := slices.Contains(cfg.Binds, "loop-gomodcache:/go/pkg/mod")
		hasNpm := slices.Contains(cfg.Binds, "loop-npmcache:/home/testuser/.npm")
		return hasChownDirs && hasGomod && hasNpm
	}), testContainerName, testJSONOK)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp.Response)

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestGitExcludesMount() {
	tests := []struct {
		name       string
		gitOutput  string
		gitErr     bool
		homeDir    string
		homeDirErr bool
		fileExists bool
		expected   string
	}{
		{
			name:       "tilde path with existing file",
			gitOutput:  "~/.gitignore_global\n",
			homeDir:    "/home/testuser",
			fileExists: true,
			expected:   "/home/testuser/.gitignore_global:/home/testuser/.gitignore_global:ro",
		},
		{
			name:       "absolute path stays as-is",
			gitOutput:  "/Users/testuser/.gitignore_global\n",
			homeDir:    "/Users/testuser",
			fileExists: true,
			expected:   "/Users/testuser/.gitignore_global:/Users/testuser/.gitignore_global:ro",
		},
		{
			name:     "git config returns error",
			gitErr:   true,
			expected: "",
		},
		{
			name:      "git config returns empty",
			gitOutput: "\n",
			expected:  "",
		},
		{
			name:       "file does not exist",
			gitOutput:  "~/.gitignore_global\n",
			homeDir:    "/home/testuser",
			fileExists: false,
			expected:   "",
		},
		{
			name:       "home dir error with tilde path",
			gitOutput:  "~/.gitignore_global\n",
			homeDirErr: true,
			expected:   "",
		},
	}

	for _, tc := range tests {
		s.Run(tc.name, func() {
			if tc.gitErr {
				s.sys.Override("ExecCommandOutput", mock.Anything, mock.Anything).Return(nil, errors.New("exit status 1"))
			} else {
				s.sys.Override("ExecCommandOutput", mock.Anything, mock.Anything).Return([]byte(tc.gitOutput), nil)
			}

			if tc.homeDirErr {
				s.sys.Override("UserHomeDir").Return("", errors.New("home dir error"))
			} else {
				s.sys.Override("UserHomeDir").Return(tc.homeDir, nil)
			}

			if tc.fileExists {
				s.sys.Override("Stat", mock.Anything).Return(nil, nil)
			} else {
				s.sys.Override("Stat", mock.Anything).Return(nil, os.ErrNotExist)
			}

			result := s.runner.gitExcludesMount()
			require.Equal(s.T(), tc.expected, result)
		})
	}
}

func (s *RunnerSuite) TestRunClaudeModelConfig() {
	tests := []struct {
		name        string
		model       string
		checkConfig func(*ContainerConfig) bool
	}{
		{
			name:  "with model",
			model: "claude-sonnet-4-5-20250929",
			checkConfig: func(cfg *ContainerConfig) bool {
				modelIdx := slices.Index(cfg.Cmd, "--model")
				return modelIdx != -1 && cfg.Cmd[modelIdx+1] == "claude-sonnet-4-5-20250929"
			},
		},
		{
			name: "without model",
			checkConfig: func(cfg *ContainerConfig) bool {
				return !slices.Contains(cfg.Cmd, "--model")
			},
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.client = new(MockDockerClient)
			s.cfg.ClaudeModel = tt.model
			s.runner = NewDockerRunner(s.client, s.cfg, nil)
			s.applyMockDefaults()

			ctx := context.Background()
			req := &agent.AgentRequest{
				Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
				ChannelID: "ch-1",
			}

			s.setupMockRun(ctx, mock.MatchedBy(tt.checkConfig), testContainerName, testJSONOK)

			resp, err := s.runner.Run(ctx, req)
			require.NoError(s.T(), err)
			require.Equal(s.T(), "ok", resp.Response)

			s.client.AssertExpectations(s.T())
		})
	}
}

func (s *RunnerSuite) TestRunWithGitExcludesMount() {
	// Default UserHomeDir from newDefaultMockSystem returns "/home/testuser"
	s.sys.Override("ExecCommandOutput", mock.Anything, mock.Anything).Return([]byte("~/.gitignore_global\n"), nil)
	s.sys.Override("Stat", "/home/testuser/.gitignore_global").Return(nil, nil)
	s.sys.On("Stat", mock.Anything).Return(nil, os.ErrNotExist)

	ctx := context.Background()
	req := &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
	}

	s.setupMockRun(ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return slices.Contains(cfg.Binds, "/home/testuser/.gitignore_global:/home/testuser/.gitignore_global:ro")
	}), testContainerName, testJSONOK)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp.Response)

	s.client.AssertExpectations(s.T())
}

// Default implementation for ExecCommand is now provided by osutil.RealSystem
// (tested in osutil package).

func (s *RunnerSuite) TestRunProjectConfigError() {
	ctx := context.Background()
	req := &agent.AgentRequest{
		SessionID:    "sess-1",
		Messages:     []agent.AgentMessage{{Role: "user", Content: "hello"}},
		SystemPrompt: "You are helpful",
		ChannelID:    "ch-1",
		DirPath:      "/project/path",
	}

	s.runner.loadProjectConfig = func(_ string, _ *config.Config) (*config.Config, error) {
		return nil, errors.New("permission denied")
	}

	resp, err := s.runner.Run(ctx, req)
	require.Error(s.T(), err)
	require.Nil(s.T(), resp)
	require.Contains(s.T(), err.Error(), "loading project config")
}

func (s *RunnerSuite) TestDefaultRandRead() {
	r := NewDockerRunner(s.client, s.cfg, nil)
	b := make([]byte, 3)
	n, err := r.osRandRead(b)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 3, n)
}

// --- Tests for SanitizeName ---

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"lowercase conversion", "MyProject", "myproject"},
		{"special chars to hyphens", "my_project!@#$%", "my-project"},
		{"consecutive hyphens collapsed", "my---project", "my-project"},
		{"leading/trailing hyphens trimmed", "---my-project---", "my-project"},
		{"dots replaced", "my.project.v2", "my-project-v2"},
		{"spaces replaced", "my project", "my-project"},
		{"already clean", "my-project", "my-project"},
		{"numbers preserved", "project123", "project123"},
		{"long name truncated to 40", strings.Repeat("a", 50), strings.Repeat("a", 40)},
		{"truncation trims trailing hyphens", strings.Repeat("a", 39) + "-b", strings.Repeat("a", 39)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := SanitizeName(tc.input)
			require.Equal(t, tc.expected, result)
		})
	}
}

// --- Tests for containerName ---

func (s *RunnerSuite) TestContainerName() {
	s.runner.osRandRead = func(b []byte) (int, error) {
		copy(b, []byte{0xde, 0xad, 0x42})
		return len(b), nil
	}

	tests := []struct {
		name      string
		channelID string
		dirPath   string
		expected  string
	}{
		{"dirPath set uses filepath.Base", "ch-1", "/home/user/my-project", "loop-my-project-dead42"},
		{"dirPath empty uses channelID", "ch-123", "", "loop-ch-123-dead42"},
		{"dirPath with special chars", "ch-1", "/home/user/My Project!", "loop-my-project-dead42"},
		{"long dirPath base truncated", "ch-1", "/home/user/" + strings.Repeat("x", 50), "loop-" + strings.Repeat("x", 40) + "-dead42"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			result := s.runner.containerName(tc.channelID, tc.dirPath)
			require.Equal(s.T(), tc.expected, result)
		})
	}
}

// --- Tests for assistantMessage.extractText ---

func TestAssistantMessageExtractText(t *testing.T) {
	tests := []struct {
		name     string
		msg      assistantMessage
		expected string
	}{
		{
			name: "single text block",
			msg: func() assistantMessage {
				var m assistantMessage
				m.Message.Content = append(m.Message.Content, assistantContentBlock{Type: "text", Text: "Hello!"})
				return m
			}(),
			expected: "Hello!",
		},
		{
			name: "multiple text blocks joined",
			msg: func() assistantMessage {
				var m assistantMessage
				m.Message.Content = append(m.Message.Content,
					assistantContentBlock{Type: "text", Text: "Line one"},
					assistantContentBlock{Type: "text", Text: "Line two"},
				)
				return m
			}(),
			expected: "Line one\nLine two",
		},
		{
			name: "tool_use only returns empty",
			msg: func() assistantMessage {
				var m assistantMessage
				m.Message.Content = append(m.Message.Content, assistantContentBlock{Type: "tool_use", Text: ""})
				return m
			}(),
			expected: "",
		},
		{
			name: "mixed content skips non-text",
			msg: func() assistantMessage {
				var m assistantMessage
				m.Message.Content = append(m.Message.Content,
					assistantContentBlock{Type: "tool_use", Text: ""},
					assistantContentBlock{Type: "text", Text: "Result"},
				)
				return m
			}(),
			expected: "Result",
		},
		{
			name:     "empty content",
			msg:      assistantMessage{},
			expected: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.msg.extractText()
			require.Equal(t, tc.expected, result)
		})
	}
}

func TestExtractToolUses(t *testing.T) {
	t.Run("extracts tool_use blocks", func(t *testing.T) {
		input := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}},{"type":"text","text":"Running tests"}]}}`
		var msg assistantMessage
		require.NoError(t, json.Unmarshal([]byte(input), &msg))
		tools := msg.extractToolUses()
		require.Len(t, tools, 1)
		require.Equal(t, "Bash", tools[0].Name)
		require.Equal(t, "go test ./...", tools[0].Input)
	})

	t.Run("no tool_use blocks", func(t *testing.T) {
		input := `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello"}]}}`
		var msg assistantMessage
		require.NoError(t, json.Unmarshal([]byte(input), &msg))
		require.Empty(t, msg.extractToolUses())
	})

	t.Run("empty name skipped", func(t *testing.T) {
		input := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"","input":{}}]}}`
		var msg assistantMessage
		require.NoError(t, json.Unmarshal([]byte(input), &msg))
		require.Empty(t, msg.extractToolUses())
	})
}

func TestSummarizeToolInput(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		input    string
		expected string
	}{
		{"Bash command", "Bash", `{"command":"go build ./..."}`, "go build ./..."},
		{"Read file", "Read", `{"file_path":"/tmp/foo.go"}`, "/tmp/foo.go"},
		{"Edit file", "Edit", `{"file_path":"/tmp/bar.go"}`, "/tmp/bar.go"},
		{"Write file", "Write", `{"file_path":"/tmp/baz.go"}`, "/tmp/baz.go"},
		{"Glob pattern", "Glob", `{"pattern":"**/*.ts"}`, "**/*.ts"},
		{"Grep pattern", "Grep", `{"pattern":"TODO"}`, "TODO"},
		{"Agent desc", "Agent", `{"description":"search code"}`, "search code"},
		{"fallback key", "WebSearch", `{"query":"golang testing"}`, "golang testing"},
		{"empty input", "Bash", `{}`, ""},
		{"invalid json", "Bash", `not json`, ""},
		{"empty raw", "Bash", ``, ""},
		{"long command truncated", "Bash", `{"command":"` + strings.Repeat("x", 200) + `"}`, strings.Repeat("x", 120) + "..."},
		{"long fallback truncated", "WebSearch", `{"query":"` + strings.Repeat("y", 200) + `"}`, strings.Repeat("y", 120) + "..."},
		{"AskUserQuestion raw", "AskUserQuestion", `{"questions":[{"question":"What?"}]}`, `{"questions":[{"question":"What?"}]}`},
		{"ExitPlanMode raw", "ExitPlanMode", `{"plan":"# My Plan","planFilePath":"/tmp/p.md"}`, `{"plan":"# My Plan","planFilePath":"/tmp/p.md"}`},
		{"TodoWrite raw", "TodoWrite", `{"todos":[{"content":"Do thing","status":"pending","activeForm":"Doing thing"}]}`, `{"todos":[{"content":"Do thing","status":"pending","activeForm":"Doing thing"}]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := summarizeToolInput(tc.toolName, json.RawMessage(tc.input))
			require.Equal(t, tc.expected, result)
		})
	}
}

func TestScanStreamJSONOnToolUse(t *testing.T) {
	input := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test"}}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
	var tools []string
	cb := streamCallbacks{
		onToolUse: func(toolUseID, name, input string) {
			tools = append(tools, name+":"+input)
		},
	}
	resp, err := scanStreamJSON(strings.NewReader(input), cb)
	require.NoError(t, err)
	require.Equal(t, "OK", resp.Result)
	require.Equal(t, []string{"Bash:go test"}, tools)
}

func TestScanStreamJSONOnActivity(t *testing.T) {
	t.Run("model detected from assistant events", func(t *testing.T) {
		input := `{"type":"assistant","message":{"model":"claude-opus-4-6","content":[{"type":"text","text":"Hello"}]}}
{"type":"assistant","message":{"model":"claude-opus-4-6","content":[{"type":"text","text":"World"}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
		var activities []string
		cb := streamCallbacks{
			onActivity: func(activity, detail string) {
				activities = append(activities, activity+":"+detail)
			},
		}
		resp, err := scanStreamJSON(strings.NewReader(input), cb)
		require.NoError(t, err)
		require.Equal(t, "OK", resp.Result)
		require.Equal(t, "claude-opus-4-6", resp.Model)
		// Model should only fire once (same model repeated)
		require.Equal(t, []string{"model:claude-opus-4-6"}, activities)
	})

	t.Run("model change fires again", func(t *testing.T) {
		input := `{"type":"assistant","message":{"model":"claude-opus-4-6","content":[{"type":"text","text":"Hello"}]}}
{"type":"assistant","message":{"model":"claude-haiku-4-5-20251001","content":[{"type":"text","text":"Sub"}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
		var activities []string
		cb := streamCallbacks{
			onActivity: func(activity, detail string) {
				activities = append(activities, activity+":"+detail)
			},
		}
		resp, err := scanStreamJSON(strings.NewReader(input), cb)
		require.NoError(t, err)
		// Last model wins
		require.Equal(t, "claude-haiku-4-5-20251001", resp.Model)
		require.Equal(t, []string{
			"model:claude-opus-4-6",
			"model:claude-haiku-4-5-20251001",
		}, activities)
	})

	t.Run("system events dispatched", func(t *testing.T) {
		input := `{"type":"system","subtype":"init","cwd":"/work"}
{"type":"system","subtype":"task_started","description":"Deep analysis"}
{"type":"system","subtype":"task_progress","description":"Reading files"}
{"type":"system","subtype":"status","status":"compacting"}
{"type":"assistant","message":{"model":"claude-opus-4-6","content":[{"type":"text","text":"Done"}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
		var activities []string
		cb := streamCallbacks{
			onActivity: func(activity, detail string) {
				activities = append(activities, activity+":"+detail)
			},
		}
		resp, err := scanStreamJSON(strings.NewReader(input), cb)
		require.NoError(t, err)
		require.Equal(t, "OK", resp.Result)
		require.Equal(t, []string{
			"subagent_started:Deep analysis",
			"subagent_progress:Reading files",
			"compacting:",
			"model:claude-opus-4-6",
		}, activities)
	})

	t.Run("result metadata parsed", func(t *testing.T) {
		input := `{"type":"assistant","message":{"model":"claude-opus-4-6","content":[{"type":"text","text":"Done"}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false,"duration_ms":5000,"num_turns":3,"stop_reason":"end_turn"}
`
		resp, err := scanStreamJSON(strings.NewReader(input), streamCallbacks{})
		require.NoError(t, err)
		require.Equal(t, 5000, resp.DurationMs)
		require.Equal(t, 3, resp.NumTurns)
		require.Equal(t, "end_turn", resp.StopReason)
		require.Equal(t, "claude-opus-4-6", resp.Model)
	})

	t.Run("malformed system event JSON skipped", func(t *testing.T) {
		// The line passes initial typeCheck unmarshal (has "type":"system") but fails
		// the second unmarshal into systemEvent because of a bad field value.
		input := `{"type":"system","subtype":123}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
		var activities []string
		resp, err := scanStreamJSON(strings.NewReader(input), streamCallbacks{
			onActivity: func(kind, desc string) {
				activities = append(activities, kind+":"+desc)
			},
		})
		require.NoError(t, err)
		require.Equal(t, "OK", resp.Result)
		require.Empty(t, activities) // malformed system event was skipped
	})

	t.Run("no activity callback ignores system events", func(t *testing.T) {
		input := `{"type":"system","subtype":"task_started","description":"test"}
{"type":"assistant","message":{"model":"claude-opus-4-6","content":[{"type":"text","text":"Done"}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
		// No onActivity — should not panic
		resp, err := scanStreamJSON(strings.NewReader(input), streamCallbacks{})
		require.NoError(t, err)
		require.Equal(t, "OK", resp.Result)
	})
}

// --- Tests for scanStreamJSON with onTurn ---

func TestParseStreamingJSON(t *testing.T) {
	t.Run("happy path with assistant and result events", func(t *testing.T) {
		input := `{"type":"system","subtype":"init"}
{"type":"assistant","message":{"content":[{"type":"text","text":"Let me check..."}]}}
{"type":"user","message":{"content":[{"type":"tool_result"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Here is the answer."}]}}
{"type":"result","result":"Here is the answer.","session_id":"sess-1","is_error":false}
`
		var turns []string
		onTurn := func(text string) {
			turns = append(turns, text)
		}

		resp, err := scanStreamJSON(strings.NewReader(input), streamCallbacks{onTurn: onTurn})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, "Here is the answer.", resp.Result)
		require.Equal(t, "sess-1", resp.SessionID)
		require.False(t, resp.IsError)
		require.Equal(t, []string{"Let me check...", "Here is the answer."}, turns)
	})

	t.Run("no result event", func(t *testing.T) {
		input := `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello"}]}}
`
		resp, err := scanStreamJSON(strings.NewReader(input), streamCallbacks{onTurn: func(string) {}})
		require.Error(t, err)
		require.Nil(t, resp)
		require.Contains(t, err.Error(), "no result event found")
	})

	t.Run("empty assistant text skipped", func(t *testing.T) {
		input := `{"type":"assistant","message":{"content":[{"type":"tool_use","text":""}]}}
{"type":"result","result":"Done.","session_id":"sess-2","is_error":false}
`
		var turns []string
		resp, err := scanStreamJSON(strings.NewReader(input), streamCallbacks{onTurn: func(text string) {
			turns = append(turns, text)
		}})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, "Done.", resp.Result)
		require.Empty(t, turns)
	})

	t.Run("non-JSON lines skipped", func(t *testing.T) {
		input := `not json at all
{"type":"result","result":"OK","session_id":"sess-3","is_error":false}
`
		resp, err := scanStreamJSON(strings.NewReader(input), streamCallbacks{onTurn: func(string) {}})
		require.NoError(t, err)
		require.Equal(t, "OK", resp.Result)
	})

	t.Run("empty lines skipped", func(t *testing.T) {
		input := `

{"type":"result","result":"OK","session_id":"sess-4","is_error":false}
`
		resp, err := scanStreamJSON(strings.NewReader(input), streamCallbacks{onTurn: func(string) {}})
		require.NoError(t, err)
		require.Equal(t, "OK", resp.Result)
	})

	t.Run("malformed assistant event skipped", func(t *testing.T) {
		input := `{"type":"assistant","message":"not an object"}
{"type":"result","result":"OK","session_id":"sess-5","is_error":false}
`
		var turns []string
		resp, err := scanStreamJSON(strings.NewReader(input), streamCallbacks{onTurn: func(text string) {
			turns = append(turns, text)
		}})
		require.NoError(t, err)
		require.Equal(t, "OK", resp.Result)
		require.Empty(t, turns)
	})

	t.Run("malformed result event skipped finds later result", func(t *testing.T) {
		input := `{"type":"result","result":123}
{"type":"result","result":"OK","session_id":"sess-6","is_error":false}
`
		resp, err := scanStreamJSON(strings.NewReader(input), streamCallbacks{onTurn: func(string) {}})
		require.NoError(t, err)
		require.Equal(t, "OK", resp.Result)
	})

	t.Run("error result", func(t *testing.T) {
		input := `{"type":"result","result":"something broke","session_id":"sess-err","is_error":true}
`
		resp, err := scanStreamJSON(strings.NewReader(input), streamCallbacks{onTurn: func(string) {}})
		require.NoError(t, err)
		require.True(t, resp.IsError)
		require.Equal(t, "something broke", resp.Result)
	})
}

// --- Tests for streaming path in Run ---

func (s *RunnerSuite) TestRunWithOnTurnStreaming() {
	ctx := context.Background()

	var streamedTurns []string
	req := &agent.AgentRequest{
		ChannelID: "ch-1",
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		OnTurn: func(text string) {
			streamedTurns = append(streamedTurns, text)
		},
	}

	// Build streaming log output with assistant + result events
	streamOutput := `{"type":"assistant","message":{"content":[{"type":"text","text":"Let me check..."}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Here is the answer."}]}}
{"type":"result","result":"Here is the answer.","session_id":"sess-stream","is_error":false}
`

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), testContainerName).Return(testContainerID, nil)
	s.client.On("ContainerStart", ctx, testContainerID).Return(nil)
	s.client.On("ContainerLogsFollow", ctx, testContainerID).Return(io.NopCloser(strings.NewReader(streamOutput)), nil)
	s.client.On("ContainerWait", ctx, testContainerID).Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Here is the answer.", resp.Response)
	require.Equal(s.T(), "sess-stream", resp.SessionID)
	require.Equal(s.T(), []string{"Let me check...", "Here is the answer."}, streamedTurns)

	// ContainerLogs should NOT be called in streaming path
	s.client.AssertNotCalled(s.T(), "ContainerLogs", mock.Anything, mock.Anything)
	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunWithOnTurnFollowError() {
	ctx := context.Background()
	req := &agent.AgentRequest{
		ChannelID: "ch-1",
		OnTurn:    func(string) {},
	}

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), testContainerName).Return(testContainerID, nil)
	s.client.On("ContainerStart", ctx, testContainerID).Return(nil)
	s.client.On("ContainerLogsFollow", ctx, testContainerID).Return(nil, errors.New("follow failed"))

	resp, err := s.runner.Run(ctx, req)
	require.Nil(s.T(), resp)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "following container logs")

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunWithOnTurnErrors() {
	tests := []struct {
		name         string
		streamOutput string
		exitCode     int64
		exitErr      error
		waitChanErr  error
		wantErr      string
		checkResp    func(*testing.T, *agent.AgentResponse)
	}{
		{
			name:         "Claude error",
			streamOutput: "{\"type\":\"result\",\"result\":\"something broke\",\"session_id\":\"sess-err\",\"is_error\":true}\n",
			wantErr:      "claude returned error",
			checkResp: func(t *testing.T, resp *agent.AgentResponse) {
				require.NotNil(t, resp)
				require.Equal(t, "something broke", resp.Error)
			},
		},
		{
			name:         "no result event",
			streamOutput: "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"Hello\"}]}}\n",
			wantErr:      "no result event found",
		},
		{
			name:         "non-zero exit code",
			streamOutput: "{\"type\":\"system\",\"subtype\":\"init\"}\n",
			exitCode:     1,
			wantErr:      "container exited with code 1",
		},
		{
			name:         "wait error",
			streamOutput: "{\"type\":\"result\",\"result\":\"OK\",\"session_id\":\"sess-1\",\"is_error\":false}\n",
			waitChanErr:  errors.New("wait error"),
			wantErr:      "waiting for container",
		},
		{
			name:         "container exit error",
			streamOutput: "{\"type\":\"result\",\"result\":\"OK\",\"session_id\":\"sess-1\",\"is_error\":false}\n",
			exitCode:     1,
			exitErr:      errors.New("oom killed"),
			wantErr:      "container exited with error",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.client = new(MockDockerClient)
			s.runner = NewDockerRunner(s.client, s.cfg, nil)
			s.applyMockDefaults()

			ctx := context.Background()
			req := &agent.AgentRequest{
				ChannelID: "ch-1",
				OnTurn:    func(string) {},
			}

			waitCh := make(chan WaitResponse, 1)
			errCh := make(chan error, 1)
			if tt.waitChanErr != nil {
				errCh <- tt.waitChanErr
			} else {
				waitCh <- WaitResponse{StatusCode: tt.exitCode, Error: tt.exitErr}
			}

			s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), testContainerName).Return(testContainerID, nil)
			s.client.On("ContainerStart", ctx, testContainerID).Return(nil)
			s.client.On("ContainerLogsFollow", ctx, testContainerID).Return(io.NopCloser(strings.NewReader(tt.streamOutput)), nil)
			s.client.On("ContainerWait", ctx, testContainerID).Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))

			resp, err := s.runner.Run(ctx, req)
			require.Error(s.T(), err)
			require.Contains(s.T(), err.Error(), tt.wantErr)
			if tt.checkResp != nil {
				tt.checkResp(s.T(), resp)
			} else {
				require.Nil(s.T(), resp)
			}

			s.client.AssertExpectations(s.T())
		})
	}
}

func (s *RunnerSuite) TestRunWithOnTurnTimeout() {
	ctx, cancel := context.WithCancel(context.Background())
	req := &agent.AgentRequest{
		ChannelID: "ch-1",
		OnTurn:    func(string) {},
	}

	// Create a reader that blocks until context is cancelled
	pr, pw := io.Pipe()
	go func() {
		<-ctx.Done()
		pw.CloseWithError(ctx.Err())
	}()

	waitCh := make(chan WaitResponse)
	errCh := make(chan error)

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), testContainerName).Return(testContainerID, nil)
	s.client.On("ContainerStart", ctx, testContainerID).Return(nil)
	s.client.On("ContainerLogsFollow", ctx, testContainerID).Return(pr, nil)
	s.client.On("ContainerWait", ctx, testContainerID).Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerStop", mock.Anything, testContainerID).Return(nil)

	// Cancel immediately to simulate timeout
	cancel()

	resp, err := s.runner.Run(ctx, req)
	require.Nil(s.T(), resp)
	require.Error(s.T(), err)
	// After pipe closes due to context cancel, scanStreamJSON returns
	// "no result event found" — then ContainerWait hits ctx.Done()
	require.Contains(s.T(), err.Error(), "container execution timed out")

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestCopyFilesSingleFile() {
	ctx := context.Background()
	containerID := "cid-copy"
	fileContent := []byte(`{"oauth_token":"tok-123"}`)

	s.sys.Override("ReadFile", "/home/testuser/.claude.json").Return(fileContent, nil)
	s.sys.On("ReadFile", mock.Anything).Return(nil, os.ErrNotExist)

	s.client.On("CopyToContainer", ctx, containerID, "/home/testuser", mock.MatchedBy(func(r io.Reader) bool {
		// Read the tar archive and verify contents.
		tr := tar.NewReader(r)
		hdr, err := tr.Next()
		if err != nil {
			return false
		}
		if hdr.Name != ".claude.json" || hdr.Mode != 0644 {
			return false
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return false
		}
		return string(data) == string(fileContent)
	})).Return(nil)

	err := s.runner.copyFiles(ctx, containerID, []string{"~/.claude.json"})
	require.NoError(s.T(), err)
	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestCopyFilesMultiple() {
	ctx := context.Background()
	containerID := "cid-multi"

	s.sys.Override("ReadFile", "/home/testuser/.claude.json").Return([]byte(`{"token":"t"}`), nil)
	s.sys.On("ReadFile", "/home/testuser/.npmrc").Return([]byte("registry=https://npm.pkg.github.com"), nil)
	s.sys.On("ReadFile", mock.Anything).Return(nil, os.ErrNotExist)

	var tarNames []string
	s.client.On("CopyToContainer", ctx, containerID, "/home/testuser", mock.Anything).
		Run(func(args mock.Arguments) {
			r := args.Get(3).(io.Reader)
			tr := tar.NewReader(r)
			hdr, _ := tr.Next()
			if hdr != nil {
				tarNames = append(tarNames, hdr.Name)
			}
		}).Return(nil).Times(2)

	err := s.runner.copyFiles(ctx, containerID, []string{"~/.claude.json", "~/.npmrc"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{".claude.json", ".npmrc"}, tarNames)
	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestCopyFilesNotExists() {
	ctx := context.Background()
	// osReadFile already returns os.ErrNotExist by default in SetupTest

	err := s.runner.copyFiles(ctx, "cid-nofile", []string{"~/.claude.json"})
	require.NoError(s.T(), err)
	s.client.AssertNotCalled(s.T(), "CopyToContainer")
}

func (s *RunnerSuite) TestCopyFilesCopyError() {
	ctx := context.Background()
	containerID := "cid-copyerr"

	s.sys.Override("ReadFile", "/home/testuser/.claude.json").Return([]byte(`{}`), nil)
	s.sys.On("ReadFile", mock.Anything).Return(nil, os.ErrNotExist)

	s.client.On("CopyToContainer", ctx, containerID, "/home/testuser", mock.Anything).Return(errors.New("copy failed"))

	err := s.runner.copyFiles(ctx, containerID, []string{"~/.claude.json"})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "copy failed")
	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestCopyFilesExpandError() {
	ctx := context.Background()

	s.sys.Override("UserHomeDir").Return("", errors.New("no home"))

	err := s.runner.copyFiles(ctx, "cid-nohome", []string{"~/.claude.json"})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "expanding path")
}

func (s *RunnerSuite) TestRunCopyFilesFails() {
	ctx := context.Background()
	s.cfg.CopyFiles = []string{"~/.claude.json"}
	req := &agent.AgentRequest{ChannelID: "ch-1"}

	s.sys.Override("ReadFile", "/home/testuser/.claude.json").Return(nil, errors.New("permission denied"))
	s.sys.On("ReadFile", mock.Anything).Return(nil, os.ErrNotExist)

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), "loop-ch-1-aabbcc").Return("container-123", nil)

	resp, err := s.runner.Run(ctx, req)
	require.Nil(s.T(), resp)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "copying files")

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestCopyFilesReadError() {
	ctx := context.Background()

	s.sys.Override("ReadFile", "/home/testuser/.claude.json").Return(nil, errors.New("permission denied"))
	s.sys.On("ReadFile", mock.Anything).Return(nil, os.ErrNotExist)

	err := s.runner.copyFiles(ctx, "cid-readerr", []string{"~/.claude.json"})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading ~/.claude.json")
}

func (s *RunnerSuite) TestCopyFilesAbsolutePath() {
	ctx := context.Background()
	containerID := "cid-abs"

	s.sys.Override("ReadFile", "/etc/some.conf").Return([]byte("config"), nil)
	s.sys.On("ReadFile", mock.Anything).Return(nil, os.ErrNotExist)

	s.client.On("CopyToContainer", ctx, containerID, "/etc", mock.MatchedBy(func(r io.Reader) bool {
		tr := tar.NewReader(r)
		hdr, _ := tr.Next()
		return hdr != nil && hdr.Name == "some.conf"
	})).Return(nil)

	err := s.runner.copyFiles(ctx, containerID, []string{"/etc/some.conf"})
	require.NoError(s.T(), err)
	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestFilterMountedCopyFiles() {
	home := "/home/testuser"
	// Default UserHomeDir from newDefaultMockSystem returns "/home/testuser"

	tests := []struct {
		name      string
		copyFiles []string
		binds     []string
		expected  []string
	}{
		{
			name:      "no overlap",
			copyFiles: []string{"~/.claude.json"},
			binds:     []string{home + "/.gitconfig:" + home + "/.gitconfig:ro"},
			expected:  []string{"~/.claude.json"},
		},
		{
			name:      "exact overlap filtered",
			copyFiles: []string{"~/.claude.json"},
			binds:     []string{home + "/.claude.json:" + home + "/.claude.json"},
			expected:  nil,
		},
		{
			name:      "partial overlap",
			copyFiles: []string{"~/.claude.json", "~/.npmrc"},
			binds:     []string{home + "/.claude.json:" + home + "/.claude.json"},
			expected:  []string{"~/.npmrc"},
		},
		{
			name:      "empty binds",
			copyFiles: []string{"~/.claude.json"},
			binds:     nil,
			expected:  []string{"~/.claude.json"},
		},
		{
			name:      "empty copy files",
			copyFiles: nil,
			binds:     []string{home + "/.claude.json:" + home + "/.claude.json"},
			expected:  nil,
		},
		{
			name:      "absolute path overlap",
			copyFiles: []string{"/etc/some.conf"},
			binds:     []string{"/etc/some.conf:/etc/some.conf:ro"},
			expected:  nil,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			result := s.runner.filterMountedCopyFiles(tt.copyFiles, tt.binds)
			require.Equal(s.T(), tt.expected, result)
		})
	}
}

func (s *RunnerSuite) TestFilterMountedCopyFilesExpandError() {
	s.sys.Override("UserHomeDir").Return("", errors.New("no home"))

	result := s.runner.filterMountedCopyFiles(
		[]string{"~/.claude.json", "/etc/some.conf"},
		[]string{"/etc/some.conf:/etc/some.conf:ro"},
	)
	// ~/ path kept because expandPath errors; /etc/some.conf filtered by bind match
	require.Equal(s.T(), []string{"~/.claude.json"}, result)
}

func (s *RunnerSuite) TestOsTimeLocalNameDefault() {
	// Call the real osTimeLocalName to cover the default function literal.
	r := NewDockerRunner(s.client, s.cfg, nil)
	loc := r.osTimeLocalName()
	require.NotEmpty(s.T(), loc)
}

func (s *RunnerSuite) TestCreateShellContainerHappyPath() {
	ctx := context.Background()

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return cfg.Image == "loop-agent:latest" &&
			slices.Equal(cfg.Cmd, []string{"sleep", "infinity"}) &&
			cfg.WorkingDir == "/home/testuser/.loop/ch-1/work" &&
			cfg.Labels["loop-channel"] == "ch-1"
	}), testContainerName).Return(testContainerID, nil)
	s.client.On("ContainerStart", ctx, testContainerID).Return(nil)

	id, err := s.runner.CreateShellContainer(ctx, "ch-1", "", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), testContainerID, id)
	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestCreateShellContainerMkdirError() {
	ctx := context.Background()
	s.sys.Override("MkdirAll", mock.Anything, mock.Anything).Return(errors.New("mkdir failed"))

	_, err := s.runner.CreateShellContainer(ctx, "ch-1", "", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating work dir")
}

func (s *RunnerSuite) TestCreateShellContainerCreateError() {
	ctx := context.Background()

	s.client.On("ContainerCreate", ctx, mock.Anything, mock.Anything).Return("", errors.New("create failed"))

	_, err := s.runner.CreateShellContainer(ctx, "ch-1", "", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating container")
}

func (s *RunnerSuite) TestCreateShellContainerEnvError() {
	ctx := context.Background()
	s.sys.Override("UserHomeDir").Return("", errors.New("no home"))

	_, err := s.runner.CreateShellContainer(ctx, "ch-1", "", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting home directory")
}

func (s *RunnerSuite) TestCreateShellContainerStartError() {
	ctx := context.Background()

	s.client.On("ContainerCreate", ctx, mock.Anything, testContainerName).Return(testContainerID, nil)
	s.client.On("ContainerStart", ctx, testContainerID).Return(errors.New("start failed"))

	_, err := s.runner.CreateShellContainer(ctx, "ch-1", "", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "starting container")
}

func (s *RunnerSuite) TestCreateShellContainerProjectConfigError() {
	ctx := context.Background()

	s.runner.loadProjectConfig = func(_ string, _ *config.Config) (*config.Config, error) {
		return nil, errors.New("permission denied")
	}

	_, err := s.runner.CreateShellContainer(ctx, "ch-1", "", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "loading project config")
}

func (s *RunnerSuite) TestBuildInteractiveClaudeCmd() {
	tests := []struct {
		name        string
		model       string
		binPath     string
		sessionID   string
		forkSession bool
		expected    string
	}{
		{
			name:     "default without model",
			binPath:  "claude",
			expected: "CLAUDE_CODE_NO_FLICKER=1 claude --mcp-config /work/.loop/mcp-ch-1.json --dangerously-skip-permissions",
		},
		{
			name:     "with model",
			model:    "claude-opus-4-6",
			binPath:  "claude",
			expected: "CLAUDE_CODE_NO_FLICKER=1 claude --mcp-config /work/.loop/mcp-ch-1.json --model claude-opus-4-6 --dangerously-skip-permissions",
		},
		{
			name:     "custom bin path",
			binPath:  "/usr/local/bin/claude",
			expected: "CLAUDE_CODE_NO_FLICKER=1 /usr/local/bin/claude --mcp-config /work/.loop/mcp-ch-1.json --dangerously-skip-permissions",
		},
		{
			name:      "with session uses resume",
			binPath:   "claude",
			sessionID: "sess-abc-123",
			expected:  "CLAUDE_CODE_NO_FLICKER=1 claude --mcp-config /work/.loop/mcp-ch-1.json --dangerously-skip-permissions --resume sess-abc-123",
		},
		{
			name:        "with session fork uses resume and fork-session",
			binPath:     "claude",
			sessionID:   "sess-parent",
			forkSession: true,
			expected:    "CLAUDE_CODE_NO_FLICKER=1 claude --mcp-config /work/.loop/mcp-ch-1.json --dangerously-skip-permissions --resume sess-parent --fork-session",
		},
		{
			name:      "with model and session uses resume",
			model:     "claude-opus-4-6",
			binPath:   "claude",
			sessionID: "sess-xyz",
			expected:  "CLAUDE_CODE_NO_FLICKER=1 claude --mcp-config /work/.loop/mcp-ch-1.json --model claude-opus-4-6 --dangerously-skip-permissions --resume sess-xyz",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			cfg := &config.Config{ClaudeBinPath: tc.binPath, ClaudeModel: tc.model}
			got := BuildInteractiveClaudeCmd(cfg, "ch-1", "/work", tc.sessionID, "", tc.forkSession)
			require.Equal(s.T(), tc.expected, got)
		})
	}
}

func (s *RunnerSuite) TestBuildInteractiveClaudeCmdWithAgentID() {
	cfg := &config.Config{ClaudeBinPath: "claude"}
	got := BuildInteractiveClaudeCmd(cfg, "ch-1", "/work", "", "agent-0", false)
	require.Equal(s.T(), "CLAUDE_CODE_NO_FLICKER=1 claude --mcp-config /work/.loop/mcp-ch-1-agent-0.json --dangerously-skip-permissions --dangerously-load-development-channels server:loop", got)
}

// TestBuildInteractiveClaudeCmdGateEnabled proves the gate-on branch prepends
// `loop syscallwrap --` so an interactive claude launched from a docker-exec
// shell runs under the same seccomp filter the stream-mode path installs via
// entrypoint.sh.
func (s *RunnerSuite) TestBuildInteractiveClaudeCmdGateEnabled() {
	cfg := &config.Config{ClaudeBinPath: "claude"}
	cfg.Gates.Agentgate.Enabled = true
	got := BuildInteractiveClaudeCmd(cfg, "ch-1", "/work", "", "", false)
	require.Equal(s.T(), "CLAUDE_CODE_NO_FLICKER=1 loop syscallwrap -- claude --mcp-config /work/.loop/mcp-ch-1.json --dangerously-skip-permissions", got)
}

// TestBuildInteractiveClaudeCmdGateDisabled confirms the baseline (no prefix)
// when the gate is explicitly off.
func (s *RunnerSuite) TestBuildInteractiveClaudeCmdGateDisabled() {
	cfg := &config.Config{ClaudeBinPath: "claude"}
	cfg.Gates.Agentgate.Enabled = false
	got := BuildInteractiveClaudeCmd(cfg, "ch-1", "/work", "", "", false)
	require.NotContains(s.T(), got, "loop syscallwrap")
	require.Equal(s.T(), "CLAUDE_CODE_NO_FLICKER=1 claude --mcp-config /work/.loop/mcp-ch-1.json --dangerously-skip-permissions", got)
}

func (s *RunnerSuite) TestBuildBaseClaudeCmdFlags() {
	cfg := &config.Config{ClaudeBinPath: "claude"}

	// Baseline: --dangerously-skip-permissions, no --permission-mode,
	// no --dangerously-load-development-channels.
	cmd := buildBaseClaudeCmd(cfg, "/work/.loop/mcp-ch-1.json", "", "", false, nil)
	got := strings.Join(cmd, " ")
	require.Contains(s.T(), got, "--dangerously-skip-permissions")
	require.NotContains(s.T(), got, "--permission-mode")
	require.NotContains(s.T(), got, "--dangerously-load-development-channels")

	// With agent ID: --dangerously-load-development-channels server:loop is added.
	cmd = buildBaseClaudeCmd(cfg, "/work/.loop/mcp-ch-1.json", "", "agent-0", false, nil)
	got = strings.Join(cmd, " ")
	require.Contains(s.T(), got, "--dangerously-load-development-channels server:loop")
}

func (s *RunnerSuite) TestBuildClaudeCmdPlanMode() {
	cfg := &config.Config{ClaudeBinPath: "claude"}

	// Without plan mode: no --append-system-prompt, no --permission-mode, no
	// plan-mode prefix on the prompt — the last argument is the raw prompt.
	req := &agent.AgentRequest{
		ChannelID: "ch-1",
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
	}
	cmd := buildClaudeCmd(cfg, "/work/.loop/mcp-ch-1.json", req)
	got := strings.Join(cmd, " ")
	require.Contains(s.T(), got, "--dangerously-skip-permissions")
	require.NotContains(s.T(), got, "--permission-mode")
	require.NotContains(s.T(), got, "--append-system-prompt")
	require.NotContains(s.T(), got, "EnterPlanMode")
	require.Equal(s.T(), "user: hello\n", cmd[len(cmd)-1])

	// With plan mode: the prompt is prefixed with the EnterPlanMode instruction;
	// no --append-system-prompt, no --permission-mode plan — EnterPlanMode
	// flips the session state, which triggers the harness's own plan-mode
	// attachment injection.
	req.PlanMode = true
	cmd = buildClaudeCmd(cfg, "/work/.loop/mcp-ch-1.json", req)
	got = strings.Join(cmd, " ")
	require.Contains(s.T(), got, "--dangerously-skip-permissions")
	require.NotContains(s.T(), got, "--permission-mode")
	require.NotContains(s.T(), got, "--append-system-prompt")
	promptArg := cmd[len(cmd)-1]
	require.True(s.T(), strings.HasPrefix(promptArg, "Call the EnterPlanMode tool"),
		"prompt should be prefixed with EnterPlanMode instruction, got: %q", promptArg)
	require.Contains(s.T(), promptArg, "user: hello")

	// Plan mode does not interfere with an explicit system prompt — it still
	// flows through --append-system-prompt unchanged.
	req.SystemPrompt = "Existing rules."
	cmd = buildClaudeCmd(cfg, "/work/.loop/mcp-ch-1.json", req)
	got = strings.Join(cmd, " ")
	require.Contains(s.T(), got, "--append-system-prompt Existing rules.")
	require.True(s.T(), strings.HasPrefix(cmd[len(cmd)-1], "Call the EnterPlanMode tool"))
}

func (s *RunnerSuite) TestClaudeCmdBuilder() {
	tests := []struct {
		name        string
		dirPath     string
		channelID   string
		sessionID   string
		forkSession bool
		loopDir     string
		wantDir     string
		wantExtra   string
	}{
		{
			name:      "with dirPath",
			dirPath:   "/projects/myapp",
			channelID: "ch-1",
			loopDir:   "/home/user/.loop",
			wantDir:   "/projects/myapp",
		},
		{
			name:      "empty dirPath falls back to loopDir",
			dirPath:   "",
			channelID: "ch-2",
			loopDir:   "/home/user/.loop",
			wantDir:   "/home/user/.loop/ch-2/work",
		},
		{
			name:      "with session ID adds resume",
			dirPath:   "/projects/myapp",
			channelID: "ch-1",
			sessionID: "sess-resume-1",
			loopDir:   "/home/user/.loop",
			wantDir:   "/projects/myapp",
			wantExtra: " --resume sess-resume-1",
		},
		{
			name:        "with session ID and fork adds resume and fork-session",
			dirPath:     "/projects/myapp",
			channelID:   "ch-thread",
			sessionID:   "sess-parent-1",
			forkSession: true,
			loopDir:     "/home/user/.loop",
			wantDir:     "/projects/myapp",
			wantExtra:   " --resume sess-parent-1 --fork-session",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			cfg := &config.Config{
				ClaudeBinPath: "claude",
				LoopDir:       tc.loopDir,
			}
			builder := NewClaudeCmdBuilder(cfg, nil)
			got := builder.BuildInteractiveCmd(tc.channelID, tc.dirPath, "", tc.sessionID, "", tc.forkSession)
			expectedMCP := tc.wantDir + "/.loop/mcp-" + tc.channelID + ".json"
			require.Equal(s.T(), "CLAUDE_CODE_NO_FLICKER=1 claude --mcp-config "+expectedMCP+" --dangerously-skip-permissions"+tc.wantExtra, got)
		})
	}
}

func (s *RunnerSuite) TestClaudeCmdBuilderProjectConfigModel() {
	// Create a temp dir with a .loop/config.json that sets claude_model.
	tmpDir := s.T().TempDir()
	loopConfigDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopConfigDir, 0755))
	require.NoError(s.T(), os.WriteFile(
		filepath.Join(loopConfigDir, "config.json"),
		[]byte(`{"claude_model": "claude-opus-4-6"}`),
		0644,
	))

	cfg := &config.Config{
		ClaudeBinPath: "claude",
		ClaudeModel:   "claude-sonnet-4-5-20250929",
		LoopDir:       "/home/user/.loop",
	}
	builder := NewClaudeCmdBuilder(cfg, nil)
	got := builder.BuildInteractiveCmd("ch-1", tmpDir, "", "", "", false)

	// Project config's claude_model should override the global one.
	expectedMCP := tmpDir + "/.loop/mcp-ch-1.json"
	require.Equal(s.T(), "CLAUDE_CODE_NO_FLICKER=1 claude --mcp-config "+expectedMCP+" --model claude-opus-4-6 --dangerously-skip-permissions", got)
}

func (s *RunnerSuite) TestClaudeCmdBuilderWritesAgentMCPConfig() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))

	cfg := &config.Config{
		ClaudeBinPath: "claude",
		LoopDir:       "/home/user/.loop",
		APIAddr:       ":8222",
		Memory:        config.MemoryConfig{Enabled: true},
	}
	builder := NewClaudeCmdBuilder(cfg, nil)
	got := builder.BuildInteractiveCmd("ch-1", tmpDir, "", "", "agent-0", false)

	// Command should reference the per-agent MCP config.
	expectedMCP := tmpDir + "/.loop/mcp-ch-1-agent-0.json"
	require.Contains(s.T(), got, "--mcp-config "+expectedMCP)
	require.Contains(s.T(), got, "--dangerously-load-development-channels server:loop")

	// Verify the per-agent MCP config was written with --agent-id.
	data, err := os.ReadFile(expectedMCP)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(data), "--agent-id")
	require.Contains(s.T(), string(data), "agent-0")
}

func (s *RunnerSuite) TestClaudeCmdBuilderNoAgentSkipsMCPWrite() {
	cfg := &config.Config{
		ClaudeBinPath: "claude",
		LoopDir:       "/home/user/.loop",
	}
	var writeCalled bool
	builder := NewClaudeCmdBuilder(cfg, nil)
	builder.writeFile = func(name string, data []byte, perm os.FileMode) error {
		writeCalled = true
		return nil
	}
	builder.BuildInteractiveCmd("ch-1", "/projects/app", "", "", "", false)
	require.False(s.T(), writeCalled, "should not write MCP config when agentID is empty")
}

func (s *RunnerSuite) TestClaudeCmdBuilderWorktreeProjectConfig() {
	// Create parent project dir with a .loop/config.json setting claude_model.
	parentDir := s.T().TempDir()
	parentLoopDir := filepath.Join(parentDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(parentLoopDir, 0755))
	require.NoError(s.T(), os.WriteFile(
		filepath.Join(parentLoopDir, "config.json"),
		[]byte(`{"claude_model": "claude-opus-4-6"}`),
		0644,
	))

	// worktreeDir has no config of its own.
	worktreeDir := s.T().TempDir()
	require.NoError(s.T(), os.MkdirAll(filepath.Join(worktreeDir, ".loop"), 0755))

	cfg := &config.Config{
		ClaudeBinPath: "claude",
		ClaudeModel:   "claude-sonnet-4-5-20250929",
		LoopDir:       "/home/user/.loop",
	}
	builder := NewClaudeCmdBuilder(cfg, nil)
	// Pass parentDirPath — should use loadWorktreeProjectConfig which
	// merges parent project config, picking up the model override.
	got := builder.BuildInteractiveCmd("ch-1", worktreeDir, parentDir, "", "", false)

	expectedMCP := worktreeDir + "/.loop/mcp-ch-1.json"
	require.Equal(s.T(), "CLAUDE_CODE_NO_FLICKER=1 claude --mcp-config "+expectedMCP+" --model claude-opus-4-6 --dangerously-skip-permissions", got)
}

func (s *RunnerSuite) TestCreateShellContainerWithCopyFiles() {
	ctx := context.Background()
	s.cfg.CopyFiles = []string{"~/.claude.json"}
	// Default Stat from newDefaultMockSystem returns os.ErrNotExist

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return slices.Equal(cfg.Cmd, []string{"sleep", "infinity"})
	}), testContainerName).Return(testContainerID, nil)
	s.client.On("ContainerStart", ctx, testContainerID).Return(nil)

	id, err := s.runner.CreateShellContainer(ctx, "ch-1", "", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), testContainerID, id)
}

func TestScanStreamJSONSkipsUserEvents(t *testing.T) {
	// "user" events (tool results) should be skipped — they can be very large (screenshots).
	input := `{"type":"assistant","message":{"content":[{"type":"text","text":"Taking screenshot"}]}}
{"type":"user","message":{"content":[{"type":"tool_result","content":"` + strings.Repeat("x", 100000) + `"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Done"}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
	var turns []string
	cb := streamCallbacks{
		onTurn: func(text string) { turns = append(turns, text) },
	}
	resp, err := scanStreamJSON(strings.NewReader(input), cb)
	require.NoError(t, err)
	require.Equal(t, "OK", resp.Result)
	require.Equal(t, []string{"Taking screenshot", "Done"}, turns)
}

func TestScanStreamJSONUserEventAtEOF(t *testing.T) {
	// "user" event as the last line without trailing newline.
	input := `{"type":"assistant","message":{"content":[{"type":"text","text":"Hi"}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
{"type":"user","message":{"content":[{"type":"tool_result","content":"big data"}]}}`
	resp, err := scanStreamJSON(strings.NewReader(input), streamCallbacks{})
	require.NoError(t, err)
	require.Equal(t, "OK", resp.Result)
}

func TestReadLineOrSkipEmptyInput(t *testing.T) {
	br := bufio.NewReaderSize(strings.NewReader(""), 64*1024)
	line, err := readLineOrSkip(br)
	require.ErrorIs(t, err, io.EOF)
	require.Nil(t, line)
}

type errorReader struct{ err error }

func (r *errorReader) Read([]byte) (int, error) { return 0, r.err }

func TestReadLineOrSkipReadError(t *testing.T) {
	// Reader that returns an error (not EOF) with no data.
	r := &errorReader{err: errors.New("read broken")}
	br := bufio.NewReaderSize(r, 64*1024)
	_, err := readLineOrSkip(br)
	require.Error(t, err)
}

func TestReadLineOrSkipUserEventUnderCap(t *testing.T) {
	// Small user event (well under userEventMaxBytes) — returned in full so
	// scanStreamJSON can dispatch the tool_result block.
	input := `{"type":"user","message":{"content":[{"type":"tool_result"}]}}` + "\n"
	br := bufio.NewReaderSize(strings.NewReader(input), 64*1024)
	line, err := readLineOrSkip(br)
	require.NoError(t, err)
	require.Equal(t, `{"type":"user","message":{"content":[{"type":"tool_result"}]}}`, string(line))
}

func TestReadLineOrSkipUserEventOverCap(t *testing.T) {
	// Multi-MB user event (over userEventMaxBytes) — drained without buffering,
	// returns nil so subsequent lines (e.g. result) still parse.
	input := `{"type":"user","message":{"content":[{"type":"tool_result","content":"` +
		strings.Repeat("x", userEventMaxBytes+1) + `"}]}}` + "\n" +
		`{"type":"result","result":"OK","session_id":"s1","is_error":false}` + "\n"
	br := bufio.NewReaderSize(strings.NewReader(input), 64*1024)
	line, err := readLineOrSkip(br)
	require.NoError(t, err)
	require.Nil(t, line)
	// Next call returns the result line in full.
	next, err := readLineOrSkip(br)
	require.NoError(t, err)
	require.Contains(t, string(next), `"type":"result"`)
}

func TestReadLineOrSkipLastLineNoNewline(t *testing.T) {
	// Last line without trailing newline — ReadBytes returns data + io.EOF.
	input := `{"type":"assistant","message":"hello"}`
	br := bufio.NewReaderSize(strings.NewReader(input), 64*1024)
	line, err := readLineOrSkip(br)
	require.NoError(t, err)
	require.Equal(t, `{"type":"assistant","message":"hello"}`, string(line))
}

// peekThenErrorReader returns data for the first Read (to fill Peek), then errors.
type peekThenErrorReader struct {
	data     string
	readOnce bool
}

func (r *peekThenErrorReader) Read(p []byte) (int, error) {
	if !r.readOnce {
		r.readOnce = true
		n := copy(p, r.data)
		return n, nil
	}
	return 0, errors.New("read error after peek")
}

func TestReadLineOrSkipReadErrorAfterPeek(t *testing.T) {
	// Peek succeeds (30 bytes of non-user data), but ReadBytes fails with no data.
	// Use a custom reader: first Read provides 30 bytes (no newline), second Read errors.
	r := &peekThenErrorReader{data: `{"type":"assistant","msg":"x"}`}
	br := bufio.NewReaderSize(r, 64*1024)
	line, err := readLineOrSkip(br)
	// ReadBytes will drain the buffer (data from peek), return it with the error.
	// Since len(line) > 0, we get the trimmed line back (not the error path at 1039).
	if err != nil {
		// If ReadBytes returned error with no data, that's the uncovered path.
		require.Nil(t, line)
	} else {
		require.NotNil(t, line)
	}
}

func TestScanStreamJSONUserEventWithNewline(t *testing.T) {
	// "user" event followed by newline — the normal case.
	input := `{"type":"user","message":{"content":[{"type":"tool_result","content":"tool output"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Got it"}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
	var turns []string
	cb := streamCallbacks{
		onTurn: func(text string) { turns = append(turns, text) },
	}
	resp, err := scanStreamJSON(strings.NewReader(input), cb)
	require.NoError(t, err)
	require.Equal(t, "OK", resp.Result)
	require.Equal(t, []string{"Got it"}, turns)
}

// --- buildContainerMounts extra dirs tests ---

func (s *RunnerSuite) TestBuildContainerMountsExtraDirs() {
	workDir := "/home/user/project"
	extraDirs := []string{"/home/user/lib", "/home/user/common"}

	binds, _ := s.runner.buildContainerMounts(nil, workDir, "", extraDirs)

	require.Contains(s.T(), binds, workDir+":"+workDir)
	require.Contains(s.T(), binds, "/home/user/lib:/home/user/lib")
	require.Contains(s.T(), binds, "/home/user/common:/home/user/common")
}

func (s *RunnerSuite) TestBuildContainerMountsExtraDirSameAsWorkDir() {
	workDir := "/home/user/project"
	extraDirs := []string{"/home/user/project", "/home/user/lib"}

	binds, _ := s.runner.buildContainerMounts(nil, workDir, "", extraDirs)

	// workDir should appear only once (from the default bind), the duplicate should be skipped.
	count := 0
	for _, b := range binds {
		if b == workDir+":"+workDir {
			count++
		}
	}
	require.Equal(s.T(), 1, count, "workDir should be mounted exactly once")
	require.Contains(s.T(), binds, "/home/user/lib:/home/user/lib")
}

func (s *RunnerSuite) TestBuildContainerMountsExtraDirUnderParent() {
	workDir := "/projects/myapp/.worktrees/wt1"
	parentDirPath := "/projects/myapp"
	extraDirs := []string{"/projects/myapp/subdir", "/external/lib"}

	binds, _ := s.runner.buildContainerMounts(nil, workDir, parentDirPath, extraDirs)

	// Parent dir is mounted (worktree case).
	require.Contains(s.T(), binds, parentDirPath+":"+parentDirPath)
	// Extra dir under parent is skipped (already covered by parent mount).
	require.NotContains(s.T(), binds, "/projects/myapp/subdir:/projects/myapp/subdir")
	// Extra dir same as parent is skipped.
	require.NotContains(s.T(), binds, parentDirPath+":"+parentDirPath+":"+parentDirPath+":"+parentDirPath)
	// External lib should be mounted.
	require.Contains(s.T(), binds, "/external/lib:/external/lib")
}

func (s *RunnerSuite) TestBuildContainerMountsExternalWorktree() {
	// External worktree (outside parent dir): workDir is NOT inside parentDirPath.
	workDir := "/Users/user/.external/worktrees/abc/myapp"
	parentDirPath := "/Users/user/dev/myapp"
	extraDirs := []string{"/Users/user/dev/myapp"}

	binds, _ := s.runner.buildContainerMounts(nil, workDir, parentDirPath, extraDirs)

	// Both workDir and parentDirPath should be mounted.
	require.Contains(s.T(), binds, workDir+":"+workDir)
	require.Contains(s.T(), binds, parentDirPath+":"+parentDirPath)
	// extra_dirs entry matching parentDirPath should not produce a duplicate.
	count := 0
	for _, b := range binds {
		if b == parentDirPath+":"+parentDirPath {
			count++
		}
	}
	require.Equal(s.T(), 1, count, "parent dir should be mounted exactly once")
}

func (s *RunnerSuite) TestBuildContainerMountsParentEqualsWorkDir() {
	// Defensive: if parentDirPath == workDir (e.g. a worktree task whose
	// thread was deleted), only one bind must be emitted — Docker rejects
	// duplicate mount targets.
	workDir := "/Users/user/dev/loop"
	parentDirPath := workDir

	binds, _ := s.runner.buildContainerMounts(nil, workDir, parentDirPath, nil)

	count := 0
	for _, b := range binds {
		if b == workDir+":"+workDir {
			count++
		}
	}
	require.Equal(s.T(), 1, count, "workDir should be mounted exactly once when it equals parentDirPath")
}

func (s *RunnerSuite) TestBuildContainerMountsExtraDirTildeExpansion() {
	workDir := "/home/user/project"
	extraDirs := []string{"~/lib", "/absolute/path"}

	binds, _ := s.runner.buildContainerMounts(nil, workDir, "", extraDirs)

	// ~ should be expanded to the home directory.
	require.Contains(s.T(), binds, "/home/testuser/lib:/home/testuser/lib")
	require.Contains(s.T(), binds, "/absolute/path:/absolute/path")
}

func (s *RunnerSuite) TestBuildContainerMountsExtraDirExpandError() {
	s.sys.Override("UserHomeDir").Return("", errors.New("no home"))

	workDir := "/home/user/project"
	extraDirs := []string{"~/broken", "/absolute/path"}

	binds, _ := s.runner.buildContainerMounts(nil, workDir, "", extraDirs)

	// ~/broken should be skipped because expandPath fails, /absolute/path should still appear.
	require.Contains(s.T(), binds, "/absolute/path:/absolute/path")
	for _, b := range binds {
		require.NotContains(s.T(), b, "broken")
	}
}

// --- buildBaseClaudeCmd extra dirs tests ---

func (s *RunnerSuite) TestBuildBaseClaudeCmdWithExtraDirs() {
	cfg := &config.Config{ClaudeBinPath: "claude"}
	extraDirs := []string{"/home/user/lib", "/home/user/common"}

	cmd := buildBaseClaudeCmd(cfg, "/work/.loop/mcp-ch-1.json", "", "", false, extraDirs)
	got := strings.Join(cmd, " ")

	require.Contains(s.T(), got, "--add-dir /home/user/lib")
	require.Contains(s.T(), got, "--add-dir /home/user/common")
}

func (s *RunnerSuite) TestBuildBaseClaudeCmdNoExtraDirs() {
	cfg := &config.Config{ClaudeBinPath: "claude"}

	cmd := buildBaseClaudeCmd(cfg, "/work/.loop/mcp-ch-1.json", "", "", false, nil)
	got := strings.Join(cmd, " ")

	require.NotContains(s.T(), got, "--add-dir")
}

func (s *RunnerSuite) TestBuildInteractiveClaudeCmdWithExtraDirs() {
	cfg := &config.Config{ClaudeBinPath: "claude", ExtraDirs: []string{"/extra/dir"}}
	got := BuildInteractiveClaudeCmd(cfg, "ch-1", "/work", "", "", false)
	require.Contains(s.T(), got, "--add-dir /extra/dir")
}

// --- Container registry integration tests ---

func (s *RunnerSuite) TestCleanupRegistryRemoveContainer() {
	ctx := context.Background()

	reg := new(MockContainerRegistry)
	s.runner.SetContainerRegistry(reg)

	s.client.On("ContainerList", ctx, InstanceLabelKey, "test-instance").Return([]string{"c1", "c2"}, nil)
	reg.On("RemoveContainer", ctx, "c1").Return(nil).Once()
	reg.On("RemoveContainer", ctx, "c2").Return(nil).Once()

	err := s.runner.Cleanup(ctx)
	require.NoError(s.T(), err)

	reg.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestCleanupRegistryRemoveContainerError() {
	ctx := context.Background()

	reg := new(MockContainerRegistry)
	s.runner.SetContainerRegistry(reg)

	s.client.On("ContainerList", ctx, InstanceLabelKey, "test-instance").Return([]string{"c1"}, nil)
	reg.On("RemoveContainer", ctx, "c1").Return(errors.New("remove failed")).Once()

	err := s.runner.Cleanup(ctx)
	require.Error(s.T(), err)

	reg.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestCreateShellContainerRegistersContainer() {
	ctx := context.Background()

	reg := new(MockContainerRegistry)
	s.runner.SetContainerRegistry(reg)

	reg.On("Register", mock.MatchedBy(func(info *ContainerInfo) bool {
		return info.ContainerID == testContainerID &&
			info.ChannelID == "ch-1" &&
			info.Type == ContainerTypeShell
	})).Once()

	s.client.On("ContainerCreate", ctx, mock.Anything, testContainerName).Return(testContainerID, nil)
	s.client.On("ContainerStart", ctx, testContainerID).Return(nil)

	id, err := s.runner.CreateShellContainer(ctx, "ch-1", "", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), testContainerID, id)

	reg.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunRegistersAgentContainer() {
	ctx := context.Background()
	req := &agent.AgentRequest{
		SessionID:    "sess-1",
		Messages:     []agent.AgentMessage{{Role: "user", Content: "hello"}},
		SystemPrompt: "You are helpful",
		ChannelID:    "ch-1",
	}

	reg := new(MockContainerRegistry)
	s.runner.SetContainerRegistry(reg)

	reg.On("Register", mock.MatchedBy(func(info *ContainerInfo) bool {
		return info.ContainerID == testContainerID &&
			info.ChannelID == "ch-1" &&
			info.Type == ContainerTypeAgent
	})).Once()
	reg.On("ScheduleRemove", testContainerID, 5*time.Minute).Once()

	s.setupMockRun(ctx, mock.Anything, testContainerName, testJSONOK)

	_, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)

	reg.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestCreateShellContainerNoRegistryOnError() {
	ctx := context.Background()

	reg := new(MockContainerRegistry)
	s.runner.SetContainerRegistry(reg)

	s.client.On("ContainerCreate", ctx, mock.Anything, mock.Anything).Return("", errors.New("create failed"))

	_, err := s.runner.CreateShellContainer(ctx, "ch-1", "", "")
	require.Error(s.T(), err)

	// Registry.Register should NOT be called on error.
	reg.AssertNotCalled(s.T(), "Register", mock.Anything)
}

func (s *RunnerSuite) TestCurrentConfigReloads() {
	s.runner.configLoad = func() (*config.Config, error) {
		return &config.Config{
			ClaudeBinPath:  "new-claude",
			ContainerImage: "new-image:latest",
			LoopDir:        "/new/loop",
		}, nil
	}

	cfg := s.runner.currentConfig()
	require.Equal(s.T(), "new-claude", cfg.ClaudeBinPath)
	require.Equal(s.T(), "new-image:latest", cfg.ContainerImage)
	// Verify it was stored as fallback.
	require.Equal(s.T(), "new-claude", s.runner.cfg.Load().ClaudeBinPath)
}

func (s *RunnerSuite) TestCurrentConfigFallbackOnError() {
	s.runner.configLoad = func() (*config.Config, error) {
		return nil, errors.New("reload failed")
	}

	cfg := s.runner.currentConfig()
	// Falls back to the original config from SetupTest.
	require.Equal(s.T(), "claude", cfg.ClaudeBinPath)
	require.Equal(s.T(), "loop-agent:latest", cfg.ContainerImage)
}

func (s *RunnerSuite) TestCurrentConfigNilLoader() {
	// configLoad is nil by default from SetupTest.
	cfg := s.runner.currentConfig()
	require.Equal(s.T(), s.cfg, cfg)
}

func (s *RunnerSuite) TestRunBashHappyPath() {
	ctx := context.Background()

	reg := new(MockContainerRegistry)
	s.runner.SetContainerRegistry(reg)
	reg.On("Register", mock.MatchedBy(func(info *ContainerInfo) bool {
		return info.ContainerID == testContainerID &&
			info.ChannelID == "ch-1" &&
			info.Type == ContainerTypeAgent &&
			info.ContainerName == testContainerName
	})).Once()
	reg.On("ScheduleRemove", testContainerID, 5*time.Minute).Once()

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return slices.Contains(cfg.Cmd, "/bin/sh") &&
			slices.Contains(cfg.Cmd, "-c") &&
			slices.Contains(cfg.Cmd, "echo hello")
	}), testContainerName).Return(testContainerID, nil)
	s.client.On("ContainerStart", ctx, testContainerID).Return(nil)
	s.client.On("ContainerWait", ctx, testContainerID).Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerLogs", ctx, testContainerID).Return(strings.NewReader("hello\n"), nil)

	output, err := s.runner.RunBash(ctx, "echo hello", "ch-1", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "hello\n", output)

	s.client.AssertExpectations(s.T())
	reg.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunBashCreateFails() {
	ctx := context.Background()

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), testContainerName).Return("", errors.New("docker create failed"))

	output, err := s.runner.RunBash(ctx, "echo hello", "ch-1", "")
	require.Error(s.T(), err)
	require.Empty(s.T(), output)
	require.Contains(s.T(), err.Error(), "creating container")

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunBashWaitError() {
	ctx := context.Background()

	reg := new(MockContainerRegistry)
	s.runner.SetContainerRegistry(reg)
	reg.On("Register", mock.MatchedBy(func(info *ContainerInfo) bool {
		return info.ContainerID == testContainerID &&
			info.ChannelID == "ch-1" &&
			info.Type == ContainerTypeAgent &&
			info.ContainerName == testContainerName
	})).Once()
	reg.On("ScheduleRemove", testContainerID, 5*time.Minute).Once()

	waitCh := make(chan WaitResponse, 1)
	errCh := make(chan error, 1)
	errCh <- errors.New("wait error")

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), testContainerName).Return(testContainerID, nil)
	s.client.On("ContainerStart", ctx, testContainerID).Return(nil)
	s.client.On("ContainerWait", ctx, testContainerID).Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))

	output, err := s.runner.RunBash(ctx, "echo hello", "ch-1", "")
	require.Error(s.T(), err)
	require.Empty(s.T(), output)
	require.Contains(s.T(), err.Error(), "waiting for container")

	s.client.AssertExpectations(s.T())
	reg.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunBashNonZeroExit() {
	ctx := context.Background()

	reg := new(MockContainerRegistry)
	s.runner.SetContainerRegistry(reg)
	reg.On("Register", mock.MatchedBy(func(info *ContainerInfo) bool {
		return info.ContainerID == testContainerID &&
			info.ChannelID == "ch-1" &&
			info.Type == ContainerTypeAgent &&
			info.ContainerName == testContainerName
	})).Once()
	reg.On("ScheduleRemove", testContainerID, 5*time.Minute).Once()

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 1}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), testContainerName).Return(testContainerID, nil)
	s.client.On("ContainerStart", ctx, testContainerID).Return(nil)
	s.client.On("ContainerWait", ctx, testContainerID).Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerLogs", ctx, testContainerID).Return(strings.NewReader("some output\n"), nil)

	output, err := s.runner.RunBash(ctx, "exit 1", "ch-1", "")
	require.Error(s.T(), err)
	require.Equal(s.T(), "some output\n", output)
	require.Contains(s.T(), err.Error(), "script exited with status 1")

	s.client.AssertExpectations(s.T())
	reg.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunBashLogsFails() {
	ctx := context.Background()

	reg := new(MockContainerRegistry)
	s.runner.SetContainerRegistry(reg)
	reg.On("Register", mock.MatchedBy(func(info *ContainerInfo) bool {
		return info.ContainerID == testContainerID &&
			info.ChannelID == "ch-1" &&
			info.Type == ContainerTypeAgent &&
			info.ContainerName == testContainerName
	})).Once()
	reg.On("ScheduleRemove", testContainerID, 5*time.Minute).Once()

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), testContainerName).Return(testContainerID, nil)
	s.client.On("ContainerStart", ctx, testContainerID).Return(nil)
	s.client.On("ContainerWait", ctx, testContainerID).Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerLogs", ctx, testContainerID).Return(nil, errors.New("logs failed"))

	output, err := s.runner.RunBash(ctx, "echo hello", "ch-1", "")
	require.Error(s.T(), err)
	require.Empty(s.T(), output)
	require.Contains(s.T(), err.Error(), "reading container logs")

	s.client.AssertExpectations(s.T())
	reg.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunBashContextCancelled() {
	ctx, cancel := context.WithCancel(context.Background())

	reg := new(MockContainerRegistry)
	s.runner.SetContainerRegistry(reg)
	reg.On("Register", mock.MatchedBy(func(info *ContainerInfo) bool {
		return info.ContainerID == testContainerID &&
			info.ChannelID == "ch-1" &&
			info.Type == ContainerTypeAgent &&
			info.ContainerName == testContainerName
	})).Once()
	reg.On("ScheduleRemove", testContainerID, 5*time.Minute).Once()

	waitCh := make(chan WaitResponse) // never written to
	errCh := make(chan error)         // never written to

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), testContainerName).Return(testContainerID, nil)
	s.client.On("ContainerStart", ctx, testContainerID).Return(nil)
	s.client.On("ContainerWait", ctx, testContainerID).Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))

	// Cancel context before waiting can complete.
	cancel()

	output, err := s.runner.RunBash(ctx, "sleep 999", "ch-1", "")
	require.Error(s.T(), err)
	require.Empty(s.T(), output)
	require.ErrorIs(s.T(), err, context.Canceled)

	s.client.AssertExpectations(s.T())
	reg.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunBashContainerError() {
	ctx := context.Background()

	reg := new(MockContainerRegistry)
	s.runner.SetContainerRegistry(reg)
	reg.On("Register", mock.MatchedBy(func(info *ContainerInfo) bool {
		return info.ContainerID == testContainerID &&
			info.ChannelID == "ch-1" &&
			info.Type == ContainerTypeAgent &&
			info.ContainerName == testContainerName
	})).Once()
	reg.On("ScheduleRemove", testContainerID, 5*time.Minute).Once()

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 1, Error: errors.New("OOM killed")}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), testContainerName).Return(testContainerID, nil)
	s.client.On("ContainerStart", ctx, testContainerID).Return(nil)
	s.client.On("ContainerWait", ctx, testContainerID).Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))

	output, err := s.runner.RunBash(ctx, "stress --vm 1", "ch-1", "")
	require.Error(s.T(), err)
	require.Empty(s.T(), output)
	require.Contains(s.T(), err.Error(), "container error")

	s.client.AssertExpectations(s.T())
	reg.AssertExpectations(s.T())
}

func TestClaudeCmdBuilderCurrentConfigReloads(t *testing.T) {
	initial := &config.Config{ClaudeBinPath: "old-claude"}
	b := NewClaudeCmdBuilder(initial, func() (*config.Config, error) {
		return &config.Config{ClaudeBinPath: "new-claude"}, nil
	})

	cfg := b.currentConfig()
	require.Equal(t, "new-claude", cfg.ClaudeBinPath)
	require.Equal(t, "new-claude", b.cfg.Load().ClaudeBinPath)
}

func TestClaudeCmdBuilderCurrentConfigFallbackOnError(t *testing.T) {
	initial := &config.Config{ClaudeBinPath: "original"}
	b := NewClaudeCmdBuilder(initial, func() (*config.Config, error) {
		return nil, errors.New("fail")
	})

	cfg := b.currentConfig()
	require.Equal(t, "original", cfg.ClaudeBinPath)
}

func TestClaudeCmdBuilderCurrentConfigNilLoader(t *testing.T) {
	initial := &config.Config{ClaudeBinPath: "frozen"}
	b := NewClaudeCmdBuilder(initial, nil)

	cfg := b.currentConfig()
	require.Equal(t, "frozen", cfg.ClaudeBinPath)
}

// --- Tests for thinking + tool_result extraction (peppy-mapping-pudding plan) ---

func TestExtractThinking(t *testing.T) {
	t.Run("single thinking block", func(t *testing.T) {
		input := `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"reasoning about the problem"}]}}`
		var msg assistantMessage
		require.NoError(t, json.Unmarshal([]byte(input), &msg))
		require.Equal(t, "reasoning about the problem", msg.extractThinking())
	})

	t.Run("multiple thinking blocks joined", func(t *testing.T) {
		input := `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"first"},{"type":"thinking","thinking":"second"}]}}`
		var msg assistantMessage
		require.NoError(t, json.Unmarshal([]byte(input), &msg))
		require.Equal(t, "first\nsecond", msg.extractThinking())
	})

	t.Run("mixed text + thinking returns only thinking", func(t *testing.T) {
		input := `{"type":"assistant","message":{"content":[{"type":"text","text":"answer"},{"type":"thinking","thinking":"hidden"}]}}`
		var msg assistantMessage
		require.NoError(t, json.Unmarshal([]byte(input), &msg))
		require.Equal(t, "hidden", msg.extractThinking())
		require.Equal(t, "answer", msg.extractText())
	})

	t.Run("no thinking blocks returns empty", func(t *testing.T) {
		input := `{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`
		var msg assistantMessage
		require.NoError(t, json.Unmarshal([]byte(input), &msg))
		require.Empty(t, msg.extractThinking())
	})

	t.Run("empty thinking string skipped", func(t *testing.T) {
		input := `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":""}]}}`
		var msg assistantMessage
		require.NoError(t, json.Unmarshal([]byte(input), &msg))
		require.Empty(t, msg.extractThinking())
	})
}

func TestExtractToolUsesIncludesID(t *testing.T) {
	input := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_abc","name":"Read","input":{"file_path":"/x"}}]}}`
	var msg assistantMessage
	require.NoError(t, json.Unmarshal([]byte(input), &msg))
	tools := msg.extractToolUses()
	require.Len(t, tools, 1)
	require.Equal(t, "toolu_abc", tools[0].ID)
	require.Equal(t, "Read", tools[0].Name)
	require.Equal(t, "/x", tools[0].Input)
}

func TestParseToolResultStringContent(t *testing.T) {
	body := parseToolResultContent(json.RawMessage(`"plain string output"`))
	require.Equal(t, "plain string output", body)
}

func TestParseToolResultMixedContent(t *testing.T) {
	body := parseToolResultContent(json.RawMessage(`[{"type":"text","text":"first"},{"type":"image","source":{"type":"base64"}},{"type":"text","text":"second"}]`))
	require.Equal(t, "first\nsecond", body)
}

func TestParseToolResultEmptyAndInvalid(t *testing.T) {
	require.Empty(t, parseToolResultContent(nil))
	require.Empty(t, parseToolResultContent(json.RawMessage(``)))
	require.Empty(t, parseToolResultContent(json.RawMessage(`{not valid}`)))
}

func TestScanStreamJSONOnThinking(t *testing.T) {
	input := `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"deep thoughts"},{"type":"text","text":"answer"}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
	var turns, thinks []string
	cb := streamCallbacks{
		onTurn:     func(text string) { turns = append(turns, text) },
		onThinking: func(text string) { thinks = append(thinks, text) },
	}
	resp, err := scanStreamJSON(strings.NewReader(input), cb)
	require.NoError(t, err)
	require.Equal(t, "OK", resp.Result)
	require.Equal(t, []string{"answer"}, turns)
	require.Equal(t, []string{"deep thoughts"}, thinks)
}

func TestScanStreamJSONOnToolResult(t *testing.T) {
	input := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"/x"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"file body"}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
	type capturedResult struct {
		toolUseID string
		output    string
		isError   bool
	}
	var results []capturedResult
	var tools []string
	cb := streamCallbacks{
		onToolUse: func(toolUseID, name, input string) {
			tools = append(tools, toolUseID+":"+name)
		},
		onToolResult: func(toolUseID, output string, isError bool) {
			results = append(results, capturedResult{toolUseID, output, isError})
		},
	}
	resp, err := scanStreamJSON(strings.NewReader(input), cb)
	require.NoError(t, err)
	require.Equal(t, "OK", resp.Result)
	require.Equal(t, []string{"toolu_1:Read"}, tools)
	require.Equal(t, []capturedResult{{"toolu_1", "file body", false}}, results)
}

func TestScanStreamJSONOnToolResultIsError(t *testing.T) {
	input := `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_2","content":"command failed","is_error":true}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
	var gotErr bool
	cb := streamCallbacks{
		onToolResult: func(toolUseID, output string, isError bool) {
			gotErr = isError
		},
	}
	_, err := scanStreamJSON(strings.NewReader(input), cb)
	require.NoError(t, err)
	require.True(t, gotErr)
}

func TestScanStreamJSONToolResultTruncated(t *testing.T) {
	big := strings.Repeat("x", toolResultMaxInline*2)
	input := `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"` + big + `"}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
	var output string
	cb := streamCallbacks{
		onToolResult: func(toolUseID, out string, isError bool) {
			output = out
		},
	}
	_, err := scanStreamJSON(strings.NewReader(input), cb)
	require.NoError(t, err)
	require.Len(t, output, toolResultMaxInline)
}

func TestScanStreamJSONOversizedUserEventStillCompletes(t *testing.T) {
	// A user line that exceeds userEventMaxBytes is drained without dispatching
	// onToolResult, but the surrounding result still parses.
	input := `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"` +
		strings.Repeat("z", userEventMaxBytes+1) + `"}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
	var dispatched bool
	cb := streamCallbacks{
		onToolResult: func(string, string, bool) { dispatched = true },
	}
	resp, err := scanStreamJSON(strings.NewReader(input), cb)
	require.NoError(t, err)
	require.Equal(t, "OK", resp.Result)
	require.False(t, dispatched, "oversized user event should be drained, not dispatched")
}

func TestScanStreamJSONOversizedUserEventNoTrailingNewline(t *testing.T) {
	// Same drain path as the trailing-newline variant but the oversized user
	// line ends with EOF directly — exercises the "over && EOF" return.
	input := `{"type":"result","result":"OK","session_id":"s1","is_error":false}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"` +
		strings.Repeat("z", userEventMaxBytes+1) + `"}]}}`
	var dispatched bool
	cb := streamCallbacks{
		onToolResult: func(string, string, bool) { dispatched = true },
	}
	resp, err := scanStreamJSON(strings.NewReader(input), cb)
	require.NoError(t, err)
	require.Equal(t, "OK", resp.Result)
	require.False(t, dispatched, "oversized user event without trailing newline should still drain")
}

func TestScanStreamJSONUserMessageMalformed(t *testing.T) {
	// type=user passes typeCheck but message field doesn't match userMessage
	// shape → unmarshal fails, parser continues and surfaces the result.
	input := `{"type":"user","message":42}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
	var dispatched bool
	cb := streamCallbacks{
		onToolResult: func(string, string, bool) { dispatched = true },
	}
	resp, err := scanStreamJSON(strings.NewReader(input), cb)
	require.NoError(t, err)
	require.Equal(t, "OK", resp.Result)
	require.False(t, dispatched)
}

func TestScanStreamJSONUserNonToolResultBlocksSkipped(t *testing.T) {
	// user content blocks that aren't tool_result are skipped without dispatch.
	input := `{"type":"user","message":{"content":[{"type":"text","text":"hi"}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
	var dispatched bool
	cb := streamCallbacks{
		onToolResult: func(string, string, bool) { dispatched = true },
	}
	resp, err := scanStreamJSON(strings.NewReader(input), cb)
	require.NoError(t, err)
	require.Equal(t, "OK", resp.Result)
	require.False(t, dispatched)
}

func TestScanStreamJSONInterleavedTextThinkingToolUse(t *testing.T) {
	// Regression: text + thinking + tool_use in one assistant turn each fires
	// the matching callback, in input order across turns.
	input := `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"plan"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"/x"}}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"done"}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
	var calls []string
	cb := streamCallbacks{
		onTurn:     func(t string) { calls = append(calls, "text:"+t) },
		onThinking: func(t string) { calls = append(calls, "think:"+t) },
		onToolUse:  func(id, name, _ string) { calls = append(calls, "tool:"+id+":"+name) },
	}
	_, err := scanStreamJSON(strings.NewReader(input), cb)
	require.NoError(t, err)
	require.Equal(t, []string{"think:plan", "tool:toolu_1:Read", "text:done"}, calls)
}
