package container

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
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

func (m *MockDockerClient) ContainerList(ctx context.Context, labelKey, labelValue string) ([]string, error) {
	args := m.Called(ctx, labelKey, labelValue)
	return args.Get(0).([]string), args.Error(1)
}

type RunnerSuite struct {
	suite.Suite
	client            *MockDockerClient
	runner            *DockerRunner
	cfg               *config.Config
	origMkdirAll      func(string, os.FileMode) error
	origGetenv        func(string) string
	origWriteFile     func(string, []byte, os.FileMode) error
	origUserHomeDir   func() (string, error)
	origOsStat        func(string) (os.FileInfo, error)
	origExecCommand   func(string, ...string) *exec.Cmd
	origTimeAfterFunc func(time.Duration, func()) *time.Timer
	origRandRead      func([]byte) (int, error)
	origReadlink      func(string) (string, error)
	origReadFile      func(string) ([]byte, error)
	origTimeLocalName func() string
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
				getenv = func(key string) string {
					if key == "TZ" {
						return "America/New_York"
					}
					return ""
				}
			},
			expected: "America/New_York",
		},
		{
			name: "from readlink",
			setup: func() {
				readlink = func(string) (string, error) {
					return "/var/db/timezone/zoneinfo/Europe/Bucharest", nil
				}
			},
			expected: "Europe/Bucharest",
		},
		{
			name: "from /etc/timezone",
			setup: func() {
				readFile = func(path string) ([]byte, error) {
					if path == "/etc/timezone" {
						return []byte("Asia/Tokyo\n"), nil
					}
					return nil, os.ErrNotExist
				}
			},
			expected: "Asia/Tokyo",
		},
		{
			name: "from time.Local name",
			setup: func() {
				timeLocalName = func() string { return "Europe/Berlin" }
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
			// Reset to defaults: no TZ, no readFile, no readlink
			getenv = func(string) string { return "" }
			readFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
			readlink = func(string) (string, error) { return "", os.ErrNotExist }
			timeLocalName = func() string { return "Local" }
			tt.setup()
			require.Equal(s.T(), tt.expected, localTimezone())
		})
	}
}

func TestRunnerSuite(t *testing.T) {
	suite.Run(t, new(RunnerSuite))
}

func (s *RunnerSuite) SetupTest() {
	s.origMkdirAll = mkdirAll
	mkdirAll = func(_ string, _ os.FileMode) error { return nil }
	s.origGetenv = getenv
	getenv = func(key string) string {
		if key == "USER" {
			return "testuser"
		}
		return ""
	}
	s.origWriteFile = writeFile
	writeFile = func(_ string, _ []byte, _ os.FileMode) error { return nil }
	s.origUserHomeDir = userHomeDir
	userHomeDir = func() (string, error) { return "/home/testuser", nil }
	s.origOsStat = osStat
	osStat = func(_ string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	s.origExecCommand = execCommand
	execCommand = func(_ string, _ ...string) *exec.Cmd {
		return exec.Command("echo", "")
	}
	s.origTimeAfterFunc = timeAfterFunc
	timeAfterFunc = func(d time.Duration, f func()) *time.Timer {
		f() // execute immediately in tests
		return time.NewTimer(0)
	}
	s.origRandRead = randRead
	randRead = func(b []byte) (int, error) {
		copy(b, []byte{0xaa, 0xbb, 0xcc})
		return len(b), nil
	}
	s.origReadlink = readlink
	readlink = func(string) (string, error) { return "", os.ErrNotExist }
	s.origReadFile = readFile
	readFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	s.origTimeLocalName = timeLocalName
	timeLocalName = func() string { return "Local" }
	s.client = new(MockDockerClient)
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
	s.runner = NewDockerRunner(s.client, s.cfg)
}

func (s *RunnerSuite) TearDownTest() {
	mkdirAll = s.origMkdirAll
	getenv = s.origGetenv
	writeFile = s.origWriteFile
	userHomeDir = s.origUserHomeDir
	osStat = s.origOsStat
	execCommand = s.origExecCommand
	timeAfterFunc = s.origTimeAfterFunc
	randRead = s.origRandRead
	readlink = s.origReadlink
	readFile = s.origReadFile
	timeLocalName = s.origTimeLocalName
}

// setupMockRun sets up mocks for a successful non-streaming container Run cycle.
func (s *RunnerSuite) setupMockRun(ctx context.Context, createMatcher any, containerName, jsonOutput string) {
	reader := bytes.NewReader([]byte(jsonOutput))
	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, createMatcher, containerName).Return(testContainerID, nil)
	s.client.On("ContainerLogs", ctx, testContainerID).Return(reader, nil)
	s.client.On("ContainerStart", ctx, testContainerID).Return(nil)
	s.client.On("ContainerWait", ctx, testContainerID).Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerRemove", ctx, testContainerID).Return(nil)
}

func (s *RunnerSuite) TestNewDockerRunner() {
	runner := NewDockerRunner(s.client, s.cfg)
	require.NotNil(s.T(), runner)
	require.Equal(s.T(), s.client, runner.client)
	require.Equal(s.T(), s.cfg, runner.cfg)
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
		hasBinds := len(cfg.Binds) == 1 &&
			cfg.Binds[0] == "/home/testuser/.loop/ch-1/work:/home/testuser/.loop/ch-1/work"
		hasHome := slices.Contains(cfg.Env, "HOME=/home/testuser")
		hasHostUser := slices.Contains(cfg.Env, "HOST_USER=testuser")
		hasTZ := slices.ContainsFunc(cfg.Env, func(e string) bool {
			return len(e) > 3 && e[:3] == "TZ="
		})
		return hasResume && hasBinds && hasHome && hasHostUser && hasTZ
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
	s.client.On("ContainerRemove", ctx, "container-fork").Return(nil)

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
	s.client.On("ContainerRemove", ctx, "container-compact").Return(nil)

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
	s.client.On("ContainerRemove", ctx, "container-retry").Return(nil)

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
	s.client.On("ContainerRemove", ctx, "container-fork").Return(nil)

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
	s.client.On("ContainerRemove", ctx, "container-fail").Return(nil)

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
	s.client.On("ContainerRemove", ctx, "container-compact").Return(nil)

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
	s.client.On("ContainerRemove", ctx, "container-retry").Return(nil)

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
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

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
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

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
	// scheduleRemove uses context.Background(), not the cancelled ctx
	s.client.On("ContainerRemove", mock.Anything, "container-123").Return(nil)

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
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

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
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

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
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

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
			s.runner = NewDockerRunner(s.client, s.cfg)

			ctx := context.Background()
			req := &agent.AgentRequest{ChannelID: "ch-1"}

			waitCh := make(chan WaitResponse, 1)
			waitCh <- WaitResponse{StatusCode: tt.exitCode}
			errCh := make(chan error, 1)

			s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), testContainerName).Return(testContainerID, nil)
			s.client.On("ContainerLogs", ctx, testContainerID).Return(tt.reader, nil)
			s.client.On("ContainerStart", ctx, testContainerID).Return(nil)
			s.client.On("ContainerWait", ctx, testContainerID).Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
			s.client.On("ContainerRemove", ctx, testContainerID).Return(nil)

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
			s.runner = NewDockerRunner(s.client, s.cfg)

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
					slices.Contains(cfg.Env, "NO_PROXY=localhost,127.0.0.1,host.docker.internal")
			},
		},
		{
			name: "NO_PROXY auto-added",
			envs: map[string]string{
				"HTTP_PROXY": "http://proxy:8080",
			},
			checkEnv: func(cfg *ContainerConfig) bool {
				return slices.Contains(cfg.Env, "HTTP_PROXY=http://proxy:8080") &&
					slices.Contains(cfg.Env, "NO_PROXY=host.docker.internal") &&
					slices.Contains(cfg.Env, "no_proxy=host.docker.internal")
			},
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.client = new(MockDockerClient)
			s.runner = NewDockerRunner(s.client, s.cfg)
			getenv = func(key string) string {
				if key == "USER" {
					return "testuser"
				}
				return tt.envs[key]
			}

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
	callCount := 0
	userHomeDir = func() (string, error) {
		callCount++
		if callCount == 1 {
			return "/home/testuser", nil // hostHome succeeds
		}
		return "", errors.New("home error") // env expansion fails
	}

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

	s.client.On("ContainerList", ctx, "app", containerLabel).Return([]string{"c1", "c2"}, nil)
	s.client.On("ContainerRemove", ctx, "c1").Return(nil)
	s.client.On("ContainerRemove", ctx, "c2").Return(nil)

	err := s.runner.Cleanup(ctx)
	require.NoError(s.T(), err)

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestCleanupListError() {
	ctx := context.Background()

	s.client.On("ContainerList", ctx, "app", containerLabel).Return([]string(nil), errors.New("list error"))

	err := s.runner.Cleanup(ctx)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "listing containers")

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestCleanupRemoveError() {
	ctx := context.Background()

	s.client.On("ContainerList", ctx, "app", containerLabel).Return([]string{"c1", "c2"}, nil)
	s.client.On("ContainerRemove", ctx, "c1").Return(errors.New("remove failed"))
	s.client.On("ContainerRemove", ctx, "c2").Return(nil)

	err := s.runner.Cleanup(ctx)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "removing container c1")

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestCleanupNoContainers() {
	ctx := context.Background()

	s.client.On("ContainerList", ctx, "app", containerLabel).Return([]string{}, nil)

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
	s.client.On("ContainerRemove", ctx, "container-fail").Return(nil)

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
	s.client.On("ContainerRemove", ctx, "container-ok").Return(nil)

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
	s.client.On("ContainerRemove", ctx, "container-fail1").Return(nil)

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
	s.client.On("ContainerRemove", ctx, "container-fail2").Return(nil)

	resp, err := s.runner.Run(ctx, req)
	require.Nil(s.T(), resp)
	require.Error(s.T(), err)
	// Returns original error (from first attempt)
	require.Contains(s.T(), err.Error(), "container exited with code 1")

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunHomeDirError() {
	userHomeDir = func() (string, error) { return "", errors.New("home dir error") }

	ctx := context.Background()
	req := &agent.AgentRequest{ChannelID: "ch-1"}

	resp, err := s.runner.Run(ctx, req)
	require.Nil(s.T(), resp)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting home directory")
}

func (s *RunnerSuite) TestRunMkdirAllError() {
	mkdirAll = func(_ string, _ os.FileMode) error { return errors.New("mkdir fail") }

	ctx := context.Background()
	req := &agent.AgentRequest{ChannelID: "ch-1"}

	resp, err := s.runner.Run(ctx, req)
	require.Nil(s.T(), resp)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating host directory")
}

func (s *RunnerSuite) TestDefaultMkdirAll() {
	tmpDir := s.T().TempDir()
	err := s.origMkdirAll(tmpDir+"/a/b", 0o755)
	require.NoError(s.T(), err)
}

func (s *RunnerSuite) TestDefaultGetenv() {
	result := s.origGetenv("PATH")
	require.NotEmpty(s.T(), result)
}

func (s *RunnerSuite) TestDefaultUserHomeDir() {
	home, err := s.origUserHomeDir()
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), home)
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
		name string
		env  []string
		want []string
	}{
		{
			"appends to existing NO_PROXY",
			[]string{"HTTP_PROXY=http://proxy:8080", "NO_PROXY=localhost,127.0.0.1"},
			[]string{"HTTP_PROXY=http://proxy:8080", "NO_PROXY=localhost,127.0.0.1,host.docker.internal"},
		},
		{
			"appends to existing no_proxy",
			[]string{"http_proxy=http://proxy:8080", "no_proxy=localhost"},
			[]string{"http_proxy=http://proxy:8080", "no_proxy=localhost,host.docker.internal"},
		},
		{
			"adds both NO_PROXY and no_proxy when missing",
			[]string{"HTTP_PROXY=http://proxy:8080"},
			[]string{"HTTP_PROXY=http://proxy:8080", "NO_PROXY=host.docker.internal", "no_proxy=host.docker.internal"},
		},
		{
			"no-op when already present",
			[]string{"NO_PROXY=host.docker.internal,other"},
			[]string{"NO_PROXY=host.docker.internal,other"},
		},
		{
			"empty NO_PROXY value",
			[]string{"NO_PROXY="},
			[]string{"NO_PROXY=host.docker.internal"},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			result := ensureNoProxy(tc.env)
			require.Equal(s.T(), tc.want, result)
		})
	}
}

func (s *RunnerSuite) TestBuildMCPConfig() {
	cfg := buildMCPConfig("ch-1", "http://host.docker.internal:8222", "/home/user/project", "", false, nil)
	require.Len(s.T(), cfg.MCPServers, 1)
	ls := cfg.MCPServers["loop"]
	require.Equal(s.T(), "/usr/local/bin/loop", ls.Command)
	require.Equal(s.T(), []string{"mcp", "--channel-id", "ch-1", "--api-url", "http://host.docker.internal:8222", "--log", "/home/user/project/.loop/mcp.log"}, ls.Args)
	require.Nil(s.T(), ls.Env)
}

func (s *RunnerSuite) TestBuildMCPConfigWithAuthorID() {
	cfg := buildMCPConfig("ch-1", "http://host.docker.internal:8222", "/home/user/project", "user-42", false, nil)
	require.Len(s.T(), cfg.MCPServers, 1)
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
	cfg := buildMCPConfig("ch-1", "http://host.docker.internal:8222", "/home/user/project", "", false, userServers)
	require.Len(s.T(), cfg.MCPServers, 2)

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
	cfg := buildMCPConfig("ch-1", "http://host.docker.internal:8222", "/home/user/project", "", false, userServers)
	require.Len(s.T(), cfg.MCPServers, 1)
	ls := cfg.MCPServers["loop"]
	require.Equal(s.T(), "/user/custom/loop", ls.Command)
	require.Equal(s.T(), []string{"--custom-flag"}, ls.Args)
}

func (s *RunnerSuite) TestBuildMCPConfigWithMemory() {
	cfg := buildMCPConfig("ch-1", "http://host.docker.internal:8222", "/home/user/project", "", true, nil)
	require.Len(s.T(), cfg.MCPServers, 1)
	ls := cfg.MCPServers["loop"]
	require.Contains(s.T(), ls.Args, "--memory")
}

func (s *RunnerSuite) TestRunWithDirPath() {
	ctx := context.Background()
	req := &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
		DirPath:   "/home/user/project",
	}

	s.setupMockRun(ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return len(cfg.Binds) == 1 &&
			cfg.Binds[0] == "/home/user/project:/home/user/project"
	}), "loop-project-aabbcc", testJSONOK)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp.Response)

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunMCPConfigWriteError() {
	writeFile = func(_ string, _ []byte, _ os.FileMode) error {
		return errors.New("write failed")
	}

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
	writeFile = func(path string, data []byte, _ os.FileMode) error {
		writtenPath = path
		writtenData = data
		return nil
	}

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

func (s *RunnerSuite) TestDefaultWriteFile() {
	tmpDir := s.T().TempDir()
	err := s.origWriteFile(tmpDir+"/test.txt", []byte("hello"), 0o644)
	require.NoError(s.T(), err)
}

// errReader always returns an error on Read.
type errReader struct {
	err error
}

func (r *errReader) Read([]byte) (int, error) {
	return 0, r.err
}

// --- Tests for parseStreamJSON ---

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
			resp, err := parseStreamJSON(strings.NewReader(tc.input))
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
	resp, err := parseStreamJSON(&errReader{err: errors.New("read error")})
	require.Error(t, err)
	require.Contains(t, err.Error(), "reading container output")
	require.Nil(t, resp)
}

// --- Tests for new mount processing functions ---

func TestExpandPath(t *testing.T) {
	origUserHomeDir := userHomeDir
	defer func() { userHomeDir = origUserHomeDir }()

	userHomeDir = func() (string, error) {
		return "/home/testuser", nil
	}

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
		t.Run(tt.name, func(t *testing.T) {
			result, err := expandPath(tt.input)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestExpandPathHomeDirError(t *testing.T) {
	origUserHomeDir := userHomeDir
	defer func() { userHomeDir = origUserHomeDir }()

	userHomeDir = func() (string, error) {
		return "", errors.New("home dir error")
	}

	_, err := expandPath("~/.claude")
	require.Error(t, err)
	require.Contains(t, err.Error(), "home dir error")
}

func TestProcessMount(t *testing.T) {
	origUserHomeDir := userHomeDir
	origOsStat := osStat
	defer func() {
		userHomeDir = origUserHomeDir
		osStat = origOsStat
	}()

	userHomeDir = func() (string, error) {
		return "/home/testuser", nil
	}

	osStat = func(name string) (os.FileInfo, error) {
		if name == "/home/testuser/.claude" || name == "/home/testuser/.gitconfig" {
			return nil, nil // Path exists
		}
		return nil, os.ErrNotExist
	}

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
		t.Run(tt.name, func(t *testing.T) {
			result, err := processMount(tt.input)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expected, result)
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

func TestProcessMountExpandPathError(t *testing.T) {
	origUserHomeDir := userHomeDir
	defer func() { userHomeDir = origUserHomeDir }()

	userHomeDir = func() (string, error) {
		return "", errors.New("home dir error")
	}

	result, err := processMount("~/.claude:~/.claude")
	require.Error(t, err)
	require.Contains(t, err.Error(), "expanding path")
	require.Empty(t, result)
}

func TestProcessMountContainerPathExpandError(t *testing.T) {
	origUserHomeDir := userHomeDir
	origOsStat := osStat
	defer func() {
		userHomeDir = origUserHomeDir
		osStat = origOsStat
	}()

	callCount := 0
	userHomeDir = func() (string, error) {
		callCount++
		if callCount == 1 {
			return "/home/testuser", nil // host tilde expansion succeeds
		}
		return "", errors.New("home dir error") // container tilde expansion fails
	}

	osStat = func(_ string) (os.FileInfo, error) {
		return nil, nil // path exists
	}

	// Host uses ~ (triggers first userHomeDir call), container uses ~ (triggers second call that fails)
	result, err := processMount("~/.claude:~/.claude")
	require.Error(t, err)
	require.Contains(t, err.Error(), "expanding container path")
	require.Empty(t, result)
}

func TestProcessMountNamedVolumeContainerPathExpandError(t *testing.T) {
	origUserHomeDir := userHomeDir
	defer func() { userHomeDir = origUserHomeDir }()

	userHomeDir = func() (string, error) {
		return "", errors.New("home dir error")
	}

	result, err := processMount("myvolume:~/.cache")
	require.Error(t, err)
	require.Contains(t, err.Error(), "expanding container path")
	require.Empty(t, result)
}

func (s *RunnerSuite) TestRunWithInvalidMount() {
	s.cfg.Mounts = []string{"invalid-mount-no-colon"}

	ctx := context.Background()
	req := &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
	}

	s.setupMockRun(ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		// Invalid mount should be skipped, only workDir bind
		return len(cfg.Binds) == 1
	}), testContainerName, testJSONOK)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp.Response)

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunWithCustomMounts() {
	origUserHomeDir := userHomeDir
	origOsStat := osStat
	defer func() {
		userHomeDir = origUserHomeDir
		osStat = origOsStat
	}()

	userHomeDir = func() (string, error) {
		return "/home/testuser", nil
	}

	osStat = func(name string) (os.FileInfo, error) {
		if name == "/home/testuser/.claude" || name == "/home/testuser/.gitconfig" {
			return nil, nil // Path exists
		}
		return nil, os.ErrNotExist
	}

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
		expectedBinds := []string{
			"/home/testuser/.claude:/home/testuser/.claude",
			"/home/testuser/.gitconfig:/home/testuser/.gitconfig:ro",
			"/home/testuser/.loop/ch-1/work:/home/testuser/.loop/ch-1/work",
		}
		return slices.Equal(cfg.Binds, expectedBinds) &&
			cfg.WorkingDir == "/home/testuser/.loop/ch-1/work"
	}), testContainerName, `{"type":"result","result":"Hello!","session_id":"sess-new-1","is_error":false}`)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), resp)

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunNamedVolumesChownDirs() {
	origUserHomeDir := userHomeDir
	origOsStat := osStat
	defer func() {
		userHomeDir = origUserHomeDir
		osStat = origOsStat
	}()

	userHomeDir = func() (string, error) {
		return "/home/testuser", nil
	}
	osStat = func(_ string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}

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
			if val, ok := strings.CutPrefix(e, "CHOWN_DIRS="); ok {
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

func TestGitExcludesMount(t *testing.T) {
	origExecCommand := execCommand
	origUserHomeDir := userHomeDir
	origOsStat := osStat
	defer func() {
		execCommand = origExecCommand
		userHomeDir = origUserHomeDir
		osStat = origOsStat
	}()

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
		t.Run(tc.name, func(t *testing.T) {
			if tc.gitErr {
				execCommand = func(_ string, _ ...string) *exec.Cmd {
					return exec.Command("false")
				}
			} else {
				execCommand = func(_ string, _ ...string) *exec.Cmd {
					return exec.Command("echo", "-n", tc.gitOutput)
				}
			}

			if tc.homeDirErr {
				userHomeDir = func() (string, error) {
					return "", errors.New("home dir error")
				}
			} else {
				userHomeDir = func() (string, error) {
					return tc.homeDir, nil
				}
			}

			osStat = func(_ string) (os.FileInfo, error) {
				if tc.fileExists {
					return nil, nil
				}
				return nil, os.ErrNotExist
			}

			result := gitExcludesMount()
			require.Equal(t, tc.expected, result)
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
			s.runner = NewDockerRunner(s.client, s.cfg)

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
	origUserHomeDir := userHomeDir
	origOsStat := osStat
	defer func() {
		userHomeDir = origUserHomeDir
		osStat = origOsStat
	}()

	userHomeDir = func() (string, error) {
		return "/home/testuser", nil
	}

	execCommand = func(_ string, _ ...string) *exec.Cmd {
		return exec.Command("echo", "-n", "~/.gitignore_global\n")
	}

	osStat = func(name string) (os.FileInfo, error) {
		if name == "/home/testuser/.gitignore_global" {
			return nil, nil
		}
		return nil, os.ErrNotExist
	}

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

func (s *RunnerSuite) TestDefaultExecCommand() {
	cmd := s.origExecCommand("echo", "hello")
	require.NotNil(s.T(), cmd)
}

func (s *RunnerSuite) TestScheduleRemove() {
	var capturedDuration time.Duration
	timeAfterFunc = func(d time.Duration, f func()) *time.Timer {
		capturedDuration = d
		f()
		return time.NewTimer(0)
	}

	s.client.On("ContainerRemove", mock.Anything, "container-xyz").Return(nil)

	s.runner.scheduleRemove("container-xyz")

	require.Equal(s.T(), 5*time.Minute, capturedDuration)
	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestScheduleRemoveIgnoresError() {
	timeAfterFunc = func(d time.Duration, f func()) *time.Timer {
		f()
		return time.NewTimer(0)
	}

	s.client.On("ContainerRemove", mock.Anything, "container-xyz").Return(errors.New("remove failed"))

	// Should not panic
	s.runner.scheduleRemove("container-xyz")
	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestDefaultTimeAfterFunc() {
	done := make(chan struct{})
	timer := s.origTimeAfterFunc(time.Millisecond, func() {
		close(done)
	})
	require.NotNil(s.T(), timer)
	select {
	case <-done:
		// success
	case <-time.After(time.Second):
		s.T().Fatal("timer callback was not called")
	}
}

func (s *RunnerSuite) TestRunProjectConfigError() {
	ctx := context.Background()
	req := &agent.AgentRequest{
		SessionID:    "sess-1",
		Messages:     []agent.AgentMessage{{Role: "user", Content: "hello"}},
		SystemPrompt: "You are helpful",
		ChannelID:    "ch-1",
		DirPath:      "/project/path",
	}

	// Mock readFile to simulate project config error
	origReadFile := config.TestSetReadFile(func(path string) ([]byte, error) {
		if strings.Contains(path, ".loop/config.json") {
			return nil, errors.New("permission denied")
		}
		return nil, os.ErrNotExist
	})
	defer config.TestSetReadFile(origReadFile)

	resp, err := s.runner.Run(ctx, req)
	require.Error(s.T(), err)
	require.Nil(s.T(), resp)
	require.Contains(s.T(), err.Error(), "loading project config")
}

func (s *RunnerSuite) TestDefaultRandRead() {
	b := make([]byte, 3)
	n, err := s.origRandRead(b)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 3, n)
}

// --- Tests for sanitizeName ---

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
			result := sanitizeName(tc.input)
			require.Equal(t, tc.expected, result)
		})
	}
}

// --- Tests for containerName ---

func TestContainerName(t *testing.T) {
	origRandRead := randRead
	defer func() { randRead = origRandRead }()

	randRead = func(b []byte) (int, error) {
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
		t.Run(tc.name, func(t *testing.T) {
			result := containerName(tc.channelID, tc.dirPath)
			require.Equal(t, tc.expected, result)
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
				m.Message.Content = append(m.Message.Content, struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}{Type: "text", Text: "Hello!"})
				return m
			}(),
			expected: "Hello!",
		},
		{
			name: "multiple text blocks joined",
			msg: func() assistantMessage {
				var m assistantMessage
				m.Message.Content = append(m.Message.Content,
					struct {
						Type string `json:"type"`
						Text string `json:"text"`
					}{Type: "text", Text: "Line one"},
					struct {
						Type string `json:"type"`
						Text string `json:"text"`
					}{Type: "text", Text: "Line two"},
				)
				return m
			}(),
			expected: "Line one\nLine two",
		},
		{
			name: "tool_use only returns empty",
			msg: func() assistantMessage {
				var m assistantMessage
				m.Message.Content = append(m.Message.Content, struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}{Type: "tool_use", Text: ""})
				return m
			}(),
			expected: "",
		},
		{
			name: "mixed content skips non-text",
			msg: func() assistantMessage {
				var m assistantMessage
				m.Message.Content = append(m.Message.Content,
					struct {
						Type string `json:"type"`
						Text string `json:"text"`
					}{Type: "tool_use", Text: ""},
					struct {
						Type string `json:"type"`
						Text string `json:"text"`
					}{Type: "text", Text: "Result"},
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

// --- Tests for parseStreamingJSON ---

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

		resp, err := parseStreamingJSON(strings.NewReader(input), onTurn)
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
		resp, err := parseStreamingJSON(strings.NewReader(input), func(string) {})
		require.Error(t, err)
		require.Nil(t, resp)
		require.Contains(t, err.Error(), "no result event found")
	})

	t.Run("empty assistant text skipped", func(t *testing.T) {
		input := `{"type":"assistant","message":{"content":[{"type":"tool_use","text":""}]}}
{"type":"result","result":"Done.","session_id":"sess-2","is_error":false}
`
		var turns []string
		resp, err := parseStreamingJSON(strings.NewReader(input), func(text string) {
			turns = append(turns, text)
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, "Done.", resp.Result)
		require.Empty(t, turns)
	})

	t.Run("non-JSON lines skipped", func(t *testing.T) {
		input := `not json at all
{"type":"result","result":"OK","session_id":"sess-3","is_error":false}
`
		resp, err := parseStreamingJSON(strings.NewReader(input), func(string) {})
		require.NoError(t, err)
		require.Equal(t, "OK", resp.Result)
	})

	t.Run("empty lines skipped", func(t *testing.T) {
		input := `

{"type":"result","result":"OK","session_id":"sess-4","is_error":false}
`
		resp, err := parseStreamingJSON(strings.NewReader(input), func(string) {})
		require.NoError(t, err)
		require.Equal(t, "OK", resp.Result)
	})

	t.Run("malformed assistant event skipped", func(t *testing.T) {
		input := `{"type":"assistant","message":"not an object"}
{"type":"result","result":"OK","session_id":"sess-5","is_error":false}
`
		var turns []string
		resp, err := parseStreamingJSON(strings.NewReader(input), func(text string) {
			turns = append(turns, text)
		})
		require.NoError(t, err)
		require.Equal(t, "OK", resp.Result)
		require.Empty(t, turns)
	})

	t.Run("malformed result event skipped finds later result", func(t *testing.T) {
		input := `{"type":"result","result":123}
{"type":"result","result":"OK","session_id":"sess-6","is_error":false}
`
		resp, err := parseStreamingJSON(strings.NewReader(input), func(string) {})
		require.NoError(t, err)
		require.Equal(t, "OK", resp.Result)
	})

	t.Run("error result", func(t *testing.T) {
		input := `{"type":"result","result":"something broke","session_id":"sess-err","is_error":true}
`
		resp, err := parseStreamingJSON(strings.NewReader(input), func(string) {})
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
	s.client.On("ContainerRemove", mock.Anything, testContainerID).Return(nil)

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
	s.client.On("ContainerRemove", mock.Anything, testContainerID).Return(nil)

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
			s.runner = NewDockerRunner(s.client, s.cfg)

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
			s.client.On("ContainerRemove", mock.Anything, testContainerID).Return(nil)

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
	s.client.On("ContainerRemove", mock.Anything, testContainerID).Return(nil)

	// Cancel immediately to simulate timeout
	cancel()

	resp, err := s.runner.Run(ctx, req)
	require.Nil(s.T(), resp)
	require.Error(s.T(), err)
	// After pipe closes due to context cancel, parseStreamingJSON returns
	// "no result event found" — then ContainerWait hits ctx.Done()
	require.Contains(s.T(), err.Error(), "container execution timed out")

	s.client.AssertExpectations(s.T())
}
