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

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
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

func (s *RunnerSuite) TestLocalTimezoneFromTZEnv() {
	origGetenv := getenv
	defer func() { getenv = origGetenv }()
	getenv = func(key string) string {
		if key == "TZ" {
			return "America/New_York"
		}
		return ""
	}
	require.Equal(s.T(), "America/New_York", localTimezone())
}

func (s *RunnerSuite) TestLocalTimezoneFromReadlink() {
	origGetenv := getenv
	origReadFile := readFile
	origReadlink := readlink
	defer func() { getenv = origGetenv; readFile = origReadFile; readlink = origReadlink }()
	getenv = func(string) string { return "" }
	readFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	readlink = func(string) (string, error) {
		return "/var/db/timezone/zoneinfo/Europe/Bucharest", nil
	}
	require.Equal(s.T(), "Europe/Bucharest", localTimezone())
}

func (s *RunnerSuite) TestLocalTimezoneFromEtcTimezone() {
	origGetenv := getenv
	origReadFile := readFile
	defer func() { getenv = origGetenv; readFile = origReadFile }()
	getenv = func(string) string { return "" }
	readFile = func(path string) ([]byte, error) {
		if path == "/etc/timezone" {
			return []byte("Asia/Tokyo\n"), nil
		}
		return nil, os.ErrNotExist
	}
	require.Equal(s.T(), "Asia/Tokyo", localTimezone())
}

func (s *RunnerSuite) TestLocalTimezoneFromLocationName() {
	origGetenv := getenv
	origTimeLocalName := timeLocalName
	defer func() { getenv = origGetenv; timeLocalName = origTimeLocalName }()
	getenv = func(string) string { return "" }
	timeLocalName = func() string { return "Europe/Berlin" }
	require.Equal(s.T(), "Europe/Berlin", localTimezone())
}

func (s *RunnerSuite) TestLocalTimezoneFallbackUTC() {
	origGetenv := getenv
	origReadFile := readFile
	origReadlink := readlink
	defer func() { getenv = origGetenv; readFile = origReadFile; readlink = origReadlink }()
	getenv = func(string) string { return "" }
	readFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	readlink = func(string) (string, error) { return "", os.ErrNotExist }
	require.Equal(s.T(), "UTC", localTimezone())
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

	jsonOutput := `{"type":"result","result":"Hello! How can I help?","session_id":"sess-new-1","is_error":false}`
	reader := bytes.NewReader([]byte(jsonOutput))

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		hasResume := slices.Contains(cfg.Cmd, "--resume") && slices.Contains(cfg.Cmd, "sess-1")
		hasBinds := len(cfg.Binds) == 1 &&
			cfg.Binds[0] == "/home/testuser/.loop/ch-1/work:/home/testuser/.loop/ch-1/work"
		hasHome := slices.Contains(cfg.Env, "HOME=/home/testuser")
		hasHostUser := slices.Contains(cfg.Env, "HOST_USER=testuser")
		hasTZ := slices.ContainsFunc(cfg.Env, func(e string) bool {
			return len(e) > 3 && e[:3] == "TZ="
		})
		return hasResume && hasBinds && hasHome && hasHostUser && hasTZ
	}), "loop-ch-1-aabbcc").Return("container-123", nil)
	s.client.On("ContainerLogs", ctx, "container-123").Return(reader, nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Hello! How can I help?", resp.Response)
	require.Equal(s.T(), "sess-new-1", resp.SessionID)

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

	jsonOutput := `{"type":"result","result":"Hello!","session_id":"sess-new","is_error":false}`
	reader := bytes.NewReader([]byte(jsonOutput))

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		// The last element of Cmd is the prompt — it should be the explicit Prompt,
		// NOT the last message content.
		lastArg := cfg.Cmd[len(cfg.Cmd)-1]
		return lastArg == "Alice: first message"
	}), "loop-ch-1-aabbcc").Return("container-123", nil)
	s.client.On("ContainerLogs", ctx, "container-123").Return(reader, nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

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

func (s *RunnerSuite) TestRunReadOutputError() {
	ctx := context.Background()
	req := &agent.AgentRequest{ChannelID: "ch-1"}

	reader := &errReader{err: errors.New("read error")}

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), "loop-ch-1-aabbcc").Return("container-123", nil)
	s.client.On("ContainerLogs", ctx, "container-123").Return(reader, nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

	resp, err := s.runner.Run(ctx, req)
	require.Nil(s.T(), resp)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading container output")

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunWithOAuthToken() {
	ctx := context.Background()
	s.cfg.ClaudeCodeOAuthToken = "sk-ant-test-token"

	req := &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
	}

	jsonOutput := `{"type":"result","result":"response","session_id":"sess-new","is_error":false}`
	reader := bytes.NewReader([]byte(jsonOutput))

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		hasOAuth := slices.Contains(cfg.Env, "CLAUDE_CODE_OAUTH_TOKEN=sk-ant-test-token")
		noResume := !slices.Contains(cfg.Cmd, "--resume")
		return hasOAuth && noResume
	}), "loop-ch-1-aabbcc").Return("container-123", nil)
	s.client.On("ContainerLogs", ctx, "container-123").Return(reader, nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "response", resp.Response)
	require.Equal(s.T(), "sess-new", resp.SessionID)

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunNoSessionID() {
	ctx := context.Background()
	req := &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
	}

	jsonOutput := `{"type":"result","result":"Hi there","session_id":"brand-new-sess","is_error":false}`
	reader := bytes.NewReader([]byte(jsonOutput))

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return !slices.Contains(cfg.Cmd, "--resume")
	}), "loop-ch-1-aabbcc").Return("container-123", nil)
	s.client.On("ContainerLogs", ctx, "container-123").Return(reader, nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Hi there", resp.Response)
	require.Equal(s.T(), "brand-new-sess", resp.SessionID)

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunProxyEnvForwarding() {
	getenv = func(key string) string {
		switch key {
		case "USER":
			return "testuser"
		case "HTTP_PROXY":
			return "http://proxy:8080"
		case "HTTPS_PROXY":
			return "http://proxy:8443"
		case "NO_PROXY":
			return "localhost,127.0.0.1"
		}
		return ""
	}

	ctx := context.Background()
	req := &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
	}

	jsonOutput := `{"type":"result","result":"ok","session_id":"s1","is_error":false}`
	reader := bytes.NewReader([]byte(jsonOutput))

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return slices.Contains(cfg.Env, "HTTP_PROXY=http://proxy:8080") &&
			slices.Contains(cfg.Env, "HTTPS_PROXY=http://proxy:8443") &&
			slices.Contains(cfg.Env, "NO_PROXY=localhost,127.0.0.1")
	}), "loop-ch-1-aabbcc").Return("container-123", nil)
	s.client.On("ContainerLogs", ctx, "container-123").Return(reader, nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp.Response)

	s.client.AssertExpectations(s.T())
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

	jsonOutput := `{"type":"result","result":"ok","session_id":"s1","is_error":false}`
	reader := bytes.NewReader([]byte(jsonOutput))

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return slices.Contains(cfg.Env, "CUSTOM_VAR=custom-value") &&
			slices.Contains(cfg.Env, "GOMODCACHE=/home/testuser/go/pkg/mod")
	}), "loop-ch-1-aabbcc").Return("container-123", nil)
	s.client.On("ContainerLogs", ctx, "container-123").Return(reader, nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

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

func (s *RunnerSuite) TestRunJSONParseError() {
	ctx := context.Background()
	req := &agent.AgentRequest{ChannelID: "ch-1"}

	reader := bytes.NewReader([]byte("not valid json"))

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), "loop-ch-1-aabbcc").Return("container-123", nil)
	s.client.On("ContainerLogs", ctx, "container-123").Return(reader, nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

	resp, err := s.runner.Run(ctx, req)
	require.Nil(s.T(), resp)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parsing claude response")

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunClaudeError() {
	ctx := context.Background()
	req := &agent.AgentRequest{ChannelID: "ch-1"}

	jsonOutput := `{"type":"result","result":"something went wrong","session_id":"sess-err","is_error":true}`
	reader := bytes.NewReader([]byte(jsonOutput))

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), "loop-ch-1-aabbcc").Return("container-123", nil)
	s.client.On("ContainerLogs", ctx, "container-123").Return(reader, nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

	resp, err := s.runner.Run(ctx, req)
	require.Nil(s.T(), resp)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "claude returned error")
	require.Contains(s.T(), err.Error(), "something went wrong")

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunNonZeroExitCode() {
	ctx := context.Background()
	req := &agent.AgentRequest{ChannelID: "ch-1"}

	reader := bytes.NewReader([]byte(""))

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 1}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), "loop-ch-1-aabbcc").Return("container-123", nil)
	s.client.On("ContainerLogs", ctx, "container-123").Return(reader, nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

	resp, err := s.runner.Run(ctx, req)
	require.Nil(s.T(), resp)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "container exited with code 1")

	s.client.AssertExpectations(s.T())
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

func (s *RunnerSuite) TestRunRetryWithoutSession() {
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

	// Retry (without session) — succeeds
	okJSON := `{"type":"result","result":"Hello!","session_id":"new-sess","is_error":false}`
	okReader := bytes.NewReader([]byte(okJSON))
	okWaitCh := make(chan WaitResponse, 1)
	okWaitCh <- WaitResponse{StatusCode: 0}
	okErrCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return !slices.Contains(cfg.Cmd, "--resume")
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

	// Retry (without session) — also fails
	failReader2 := bytes.NewReader([]byte("some other error"))
	failWaitCh2 := make(chan WaitResponse, 1)
	failWaitCh2 <- WaitResponse{StatusCode: 1}
	failErrCh2 := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return !slices.Contains(cfg.Cmd, "--resume")
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

func (s *RunnerSuite) TestBuildMCPConfig() {
	cfg := buildMCPConfig("ch-1", "http://host.docker.internal:8222", "/home/user/project", nil)
	require.Len(s.T(), cfg.MCPServers, 1)
	ls := cfg.MCPServers["loop"]
	require.Equal(s.T(), "/usr/local/bin/loop", ls.Command)
	require.Equal(s.T(), []string{"mcp", "--channel-id", "ch-1", "--api-url", "http://host.docker.internal:8222", "--log", "/home/user/project/.loop/mcp.log"}, ls.Args)
	require.Nil(s.T(), ls.Env)
}

func (s *RunnerSuite) TestBuildMCPConfigWithUserServers() {
	userServers := map[string]config.MCPServerConfig{
		"custom-tool": {
			Command: "/path/to/binary",
			Args:    []string{"--flag"},
			Env:     map[string]string{"API_KEY": "secret"},
		},
	}
	cfg := buildMCPConfig("ch-1", "http://host.docker.internal:8222", "/home/user/project", userServers)
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
	cfg := buildMCPConfig("ch-1", "http://host.docker.internal:8222", "/home/user/project", userServers)
	require.Len(s.T(), cfg.MCPServers, 1)
	ls := cfg.MCPServers["loop"]
	require.Equal(s.T(), "/user/custom/loop", ls.Command)
	require.Equal(s.T(), []string{"--custom-flag"}, ls.Args)
}

func (s *RunnerSuite) TestRunWithDirPath() {
	ctx := context.Background()
	req := &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
		DirPath:   "/home/user/project",
	}

	jsonOutput := `{"type":"result","result":"ok","session_id":"s1","is_error":false}`
	reader := bytes.NewReader([]byte(jsonOutput))

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return len(cfg.Binds) == 1 &&
			cfg.Binds[0] == "/home/user/project:/home/user/project"
	}), "loop-project-aabbcc").Return("container-123", nil)
	s.client.On("ContainerLogs", ctx, "container-123").Return(reader, nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp.Response)

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunWithoutDirPathUsesDefault() {
	ctx := context.Background()
	req := &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
	}

	jsonOutput := `{"type":"result","result":"ok","session_id":"s1","is_error":false}`
	reader := bytes.NewReader([]byte(jsonOutput))

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return len(cfg.Binds) == 1 &&
			cfg.Binds[0] == "/home/testuser/.loop/ch-1/work:/home/testuser/.loop/ch-1/work"
	}), "loop-ch-1-aabbcc").Return("container-123", nil)
	s.client.On("ContainerLogs", ctx, "container-123").Return(reader, nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

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

	jsonOutput := `{"type":"result","result":"ok","session_id":"s1","is_error":false}`
	reader := bytes.NewReader([]byte(jsonOutput))

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), "loop-ch-1-aabbcc").Return("container-123", nil)
	s.client.On("ContainerLogs", ctx, "container-123").Return(reader, nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

	_, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)

	require.Equal(s.T(), "/home/testuser/.loop/ch-1/work/.loop/mcp.json", writtenPath)

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
			require.Equal(t, tt.expected, isNamedVolume(tt.source))
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

	jsonOutput := `{"type":"result","result":"ok","session_id":"s1","is_error":false}`
	reader := bytes.NewReader([]byte(jsonOutput))

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		// Invalid mount should be skipped, only workDir bind
		return len(cfg.Binds) == 1
	}), "loop-ch-1-aabbcc").Return("container-123", nil)
	s.client.On("ContainerLogs", ctx, "container-123").Return(reader, nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

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

	jsonOutput := `{"type":"result","result":"Hello!","session_id":"sess-new-1","is_error":false}`
	reader := bytes.NewReader([]byte(jsonOutput))

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		// Check that binds include custom mounts and preserve paths
		expectedBinds := []string{
			"/home/testuser/.claude:/home/testuser/.claude",
			"/home/testuser/.gitconfig:/home/testuser/.gitconfig:ro",
			"/home/testuser/.loop/ch-1/work:/home/testuser/.loop/ch-1/work",
		}
		bindsMatch := slices.Equal(cfg.Binds, expectedBinds)
		workDirMatch := cfg.WorkingDir == "/home/testuser/.loop/ch-1/work"
		return bindsMatch && workDirMatch
	}), "loop-ch-1-aabbcc").Return("container-123", nil)
	s.client.On("ContainerLogs", ctx, "container-123").Return(reader, nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

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

	jsonOutput := `{"type":"result","result":"ok","session_id":"s1","is_error":false}`
	reader := bytes.NewReader([]byte(jsonOutput))

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		hasChownDirs := false
		for _, e := range cfg.Env {
			if strings.HasPrefix(e, "CHOWN_DIRS=") {
				val := strings.TrimPrefix(e, "CHOWN_DIRS=")
				hasChownDirs = strings.Contains(val, "/go/pkg/mod") &&
					strings.Contains(val, "/home/testuser/.npm")
			}
		}
		hasGomod := slices.Contains(cfg.Binds, "loop-gomodcache:/go/pkg/mod")
		hasNpm := slices.Contains(cfg.Binds, "loop-npmcache:/home/testuser/.npm")
		return hasChownDirs && hasGomod && hasNpm
	}), "loop-ch-1-aabbcc").Return("container-123", nil)
	s.client.On("ContainerLogs", ctx, "container-123").Return(reader, nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp.Response)

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunWithDirPathPreservesPath() {
	ctx := context.Background()
	req := &agent.AgentRequest{
		SessionID:    "sess-1",
		Messages:     []agent.AgentMessage{{Role: "user", Content: "hello"}},
		SystemPrompt: "You are helpful",
		ChannelID:    "ch-1",
		DirPath:      "/custom/project/path",
	}

	jsonOutput := `{"type":"result","result":"Hello!","session_id":"sess-new-1","is_error":false}`
	reader := bytes.NewReader([]byte(jsonOutput))

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		// Verify workDir is mounted at same path and WorkingDir is set correctly
		workDirBind := "/custom/project/path:/custom/project/path"
		hasCorrectBinds := len(cfg.Binds) == 1 && slices.Contains(cfg.Binds, workDirBind)
		hasCorrectWorkingDir := cfg.WorkingDir == "/custom/project/path"
		return hasCorrectBinds && hasCorrectWorkingDir
	}), "loop-path-aabbcc").Return("container-123", nil)
	s.client.On("ContainerLogs", ctx, "container-123").Return(reader, nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), resp)

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

func (s *RunnerSuite) TestRunWithClaudeModel() {
	s.cfg.ClaudeModel = "claude-sonnet-4-5-20250929"

	ctx := context.Background()
	req := &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
	}

	jsonOutput := `{"type":"result","result":"ok","session_id":"s1","is_error":false}`
	reader := bytes.NewReader([]byte(jsonOutput))

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		modelIdx := slices.Index(cfg.Cmd, "--model")
		if modelIdx == -1 {
			return false
		}
		return cfg.Cmd[modelIdx+1] == "claude-sonnet-4-5-20250929"
	}), "loop-ch-1-aabbcc").Return("container-123", nil)
	s.client.On("ContainerLogs", ctx, "container-123").Return(reader, nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp.Response)

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunWithoutClaudeModel() {
	ctx := context.Background()
	req := &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
	}

	jsonOutput := `{"type":"result","result":"ok","session_id":"s1","is_error":false}`
	reader := bytes.NewReader([]byte(jsonOutput))

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return !slices.Contains(cfg.Cmd, "--model")
	}), "loop-ch-1-aabbcc").Return("container-123", nil)
	s.client.On("ContainerLogs", ctx, "container-123").Return(reader, nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp.Response)

	s.client.AssertExpectations(s.T())
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

	jsonOutput := `{"type":"result","result":"ok","session_id":"s1","is_error":false}`
	reader := bytes.NewReader([]byte(jsonOutput))

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return slices.Contains(cfg.Binds, "/home/testuser/.gitignore_global:/home/testuser/.gitignore_global:ro")
	}), "loop-ch-1-aabbcc").Return("container-123", nil)
	s.client.On("ContainerLogs", ctx, "container-123").Return(reader, nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

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
