package container

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
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

func (m *MockDockerClient) ContainerAttach(ctx context.Context, containerID string) (io.Reader, error) {
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
	client        *MockDockerClient
	runner        *DockerRunner
	cfg           *config.Config
	origMkdirAll  func(string, os.FileMode) error
	origGetenv    func(string) string
	origWriteFile func(string, []byte, os.FileMode) error
}

func TestRunnerSuite(t *testing.T) {
	suite.Run(t, new(RunnerSuite))
}

func (s *RunnerSuite) SetupTest() {
	s.origMkdirAll = mkdirAll
	mkdirAll = func(_ string, _ os.FileMode) error { return nil }
	s.origGetenv = getenv
	getenv = func(_ string) string { return "" }
	s.origWriteFile = writeFile
	writeFile = func(_ string, _ []byte, _ os.FileMode) error { return nil }
	s.client = new(MockDockerClient)
	s.cfg = &config.Config{
		ContainerImage:    "loop-agent:latest",
		ContainerMemoryMB: 512,
		ContainerCPUs:     1.0,
		ContainerTimeout:  30 * time.Second,
		APIAddr:           ":8222",
		LoopDir:           "/home/testuser/.loop",
	}
	s.runner = NewDockerRunner(s.client, s.cfg)
}

func (s *RunnerSuite) TearDownTest() {
	mkdirAll = s.origMkdirAll
	getenv = s.origGetenv
	writeFile = s.origWriteFile
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
		hasSessionEnv := false
		for _, e := range cfg.Env {
			if e == "SESSION_ID=sess-1" {
				hasSessionEnv = true
			}
		}
		hasBinds := len(cfg.Binds) == 2 &&
			cfg.Binds[0] == "/home/testuser/.loop/ch-1/work:/home/testuser/.loop/ch-1/work" &&
			cfg.Binds[1] == "/home/testuser/.loop/ch-1/mcp:/home/testuser/.loop/ch-1/mcp"
		return hasSessionEnv && hasBinds
	}), "").Return("container-123", nil)
	s.client.On("ContainerAttach", ctx, "container-123").Return(reader, nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Hello! How can I help?", resp.Response)
	require.Equal(s.T(), "sess-new-1", resp.SessionID)

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunCreateFails() {
	ctx := context.Background()
	req := &agent.AgentRequest{ChannelID: "ch-1"}

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), "").Return("", errors.New("docker create failed"))

	resp, err := s.runner.Run(ctx, req)
	require.Nil(s.T(), resp)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating container")

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunAttachFails() {
	ctx := context.Background()
	req := &agent.AgentRequest{ChannelID: "ch-1"}

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), "").Return("container-123", nil)
	s.client.On("ContainerAttach", ctx, "container-123").Return(nil, errors.New("attach failed"))
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

	resp, err := s.runner.Run(ctx, req)
	require.Nil(s.T(), resp)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "attaching to container")

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunStartFails() {
	ctx := context.Background()
	req := &agent.AgentRequest{ChannelID: "ch-1"}

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), "").Return("container-123", nil)
	s.client.On("ContainerAttach", ctx, "container-123").Return(bytes.NewReader(nil), nil)
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

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), "").Return("container-123", nil)
	s.client.On("ContainerAttach", ctx, "container-123").Return(bytes.NewReader(nil), nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

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

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), "").Return("container-123", nil)
	s.client.On("ContainerAttach", ctx, "container-123").Return(bytes.NewReader(nil), nil)
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

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), "").Return("container-123", nil)
	s.client.On("ContainerAttach", ctx, "container-123").Return(bytes.NewReader(nil), nil)
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

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), "").Return("container-123", nil)
	s.client.On("ContainerAttach", ctx, "container-123").Return(reader, nil)
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
		hasOAuth := false
		for _, e := range cfg.Env {
			if e == "CLAUDE_CODE_OAUTH_TOKEN=sk-ant-test-token" {
				hasOAuth = true
			}
			if strings.HasPrefix(e, "SESSION_ID=") {
				return false // SESSION_ID should NOT be set for empty session
			}
		}
		return hasOAuth
	}), "").Return("container-123", nil)
	s.client.On("ContainerAttach", ctx, "container-123").Return(reader, nil)
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
		for _, e := range cfg.Env {
			if strings.HasPrefix(e, "SESSION_ID=") {
				return false
			}
		}
		return true
	}), "").Return("container-123", nil)
	s.client.On("ContainerAttach", ctx, "container-123").Return(reader, nil)
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

	jsonOutput := `{"result":"ok","session_id":"s1","is_error":false}`
	reader := bytes.NewReader([]byte(jsonOutput))

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return slices.Contains(cfg.Env, "HTTP_PROXY=http://proxy:8080") &&
			slices.Contains(cfg.Env, "HTTPS_PROXY=http://proxy:8443") &&
			slices.Contains(cfg.Env, "NO_PROXY=localhost,127.0.0.1")
	}), "").Return("container-123", nil)
	s.client.On("ContainerAttach", ctx, "container-123").Return(reader, nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp.Response)

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunJSONParseError() {
	ctx := context.Background()
	req := &agent.AgentRequest{ChannelID: "ch-1"}

	reader := bytes.NewReader([]byte("not valid json"))

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), "").Return("container-123", nil)
	s.client.On("ContainerAttach", ctx, "container-123").Return(reader, nil)
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

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), "").Return("container-123", nil)
	s.client.On("ContainerAttach", ctx, "container-123").Return(reader, nil)
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

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), "").Return("container-123", nil)
	s.client.On("ContainerAttach", ctx, "container-123").Return(reader, nil)
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
		return slices.Contains(cfg.Env, "SESSION_ID=stale-sess")
	}), "").Return("container-fail", nil).Once()
	s.client.On("ContainerAttach", ctx, "container-fail").Return(failReader, nil)
	s.client.On("ContainerStart", ctx, "container-fail").Return(nil)
	s.client.On("ContainerWait", ctx, "container-fail").Return((<-chan WaitResponse)(failWaitCh), (<-chan error)(failErrCh))
	s.client.On("ContainerRemove", ctx, "container-fail").Return(nil)

	// Retry (without session) — succeeds
	okJSON := `{"result":"Hello!","session_id":"new-sess","is_error":false}`
	okReader := bytes.NewReader([]byte(okJSON))
	okWaitCh := make(chan WaitResponse, 1)
	okWaitCh <- WaitResponse{StatusCode: 0}
	okErrCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		for _, e := range cfg.Env {
			if strings.HasPrefix(e, "SESSION_ID=") {
				return false
			}
		}
		return true
	}), "").Return("container-ok", nil).Once()
	s.client.On("ContainerAttach", ctx, "container-ok").Return(okReader, nil)
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
		return slices.Contains(cfg.Env, "SESSION_ID=stale-sess")
	}), "").Return("container-fail1", nil).Once()
	s.client.On("ContainerAttach", ctx, "container-fail1").Return(failReader1, nil)
	s.client.On("ContainerStart", ctx, "container-fail1").Return(nil)
	s.client.On("ContainerWait", ctx, "container-fail1").Return((<-chan WaitResponse)(failWaitCh1), (<-chan error)(failErrCh1))
	s.client.On("ContainerRemove", ctx, "container-fail1").Return(nil)

	// Retry (without session) — also fails
	failReader2 := bytes.NewReader([]byte("some other error"))
	failWaitCh2 := make(chan WaitResponse, 1)
	failWaitCh2 <- WaitResponse{StatusCode: 1}
	failErrCh2 := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		for _, e := range cfg.Env {
			if strings.HasPrefix(e, "SESSION_ID=") {
				return false
			}
		}
		return true
	}), "").Return("container-fail2", nil).Once()
	s.client.On("ContainerAttach", ctx, "container-fail2").Return(failReader2, nil)
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
	cfg := buildMCPConfig("ch-1", "http://host.docker.internal:8222", nil)
	require.Len(s.T(), cfg.MCPServers, 1)
	ls := cfg.MCPServers["loop"]
	require.Equal(s.T(), "/usr/local/bin/loop", ls.Command)
	require.Equal(s.T(), []string{"mcp", "--channel-id", "ch-1", "--api-url", "http://host.docker.internal:8222", "--log", "/mcp/mcp.log"}, ls.Args)
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
	cfg := buildMCPConfig("ch-1", "http://host.docker.internal:8222", userServers)
	require.Len(s.T(), cfg.MCPServers, 2)

	custom := cfg.MCPServers["custom-tool"]
	require.Equal(s.T(), "/path/to/binary", custom.Command)
	require.Equal(s.T(), []string{"--flag"}, custom.Args)
	require.Equal(s.T(), map[string]string{"API_KEY": "secret"}, custom.Env)

	ls := cfg.MCPServers["loop"]
	require.Equal(s.T(), "/usr/local/bin/loop", ls.Command)
}

func (s *RunnerSuite) TestBuildMCPConfigBuiltinOverridesUser() {
	userServers := map[string]config.MCPServerConfig{
		"loop": {
			Command: "/user/fake/binary",
			Args:    []string{"--user-flag"},
		},
	}
	cfg := buildMCPConfig("ch-1", "http://host.docker.internal:8222", userServers)
	require.Len(s.T(), cfg.MCPServers, 1)
	ls := cfg.MCPServers["loop"]
	require.Equal(s.T(), "/usr/local/bin/loop", ls.Command)
	require.Equal(s.T(), []string{"mcp", "--channel-id", "ch-1", "--api-url", "http://host.docker.internal:8222", "--log", "/mcp/mcp.log"}, ls.Args)
}

func (s *RunnerSuite) TestRunWithDirPath() {
	ctx := context.Background()
	req := &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
		DirPath:   "/home/user/project",
	}

	jsonOutput := `{"result":"ok","session_id":"s1","is_error":false}`
	reader := bytes.NewReader([]byte(jsonOutput))

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return len(cfg.Binds) == 2 &&
			cfg.Binds[0] == "/home/user/project:/home/user/project" &&
			cfg.Binds[1] == "/home/testuser/.loop/ch-1/mcp:/home/testuser/.loop/ch-1/mcp"
	}), "").Return("container-123", nil)
	s.client.On("ContainerAttach", ctx, "container-123").Return(reader, nil)
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

	jsonOutput := `{"result":"ok","session_id":"s1","is_error":false}`
	reader := bytes.NewReader([]byte(jsonOutput))

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return len(cfg.Binds) == 2 &&
			cfg.Binds[0] == "/home/testuser/.loop/ch-1/work:/home/testuser/.loop/ch-1/work" &&
			cfg.Binds[1] == "/home/testuser/.loop/ch-1/mcp:/home/testuser/.loop/ch-1/mcp"
	}), "").Return("container-123", nil)
	s.client.On("ContainerAttach", ctx, "container-123").Return(reader, nil)
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

	jsonOutput := `{"result":"ok","session_id":"s1","is_error":false}`
	reader := bytes.NewReader([]byte(jsonOutput))

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), "").Return("container-123", nil)
	s.client.On("ContainerAttach", ctx, "container-123").Return(reader, nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

	_, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)

	require.Equal(s.T(), "/home/testuser/.loop/ch-1/work/.mcp.json", writtenPath)

	var cfg mcpConfig
	require.NoError(s.T(), json.Unmarshal(writtenData, &cfg))
	require.Contains(s.T(), cfg.MCPServers, "loop")
	ls := cfg.MCPServers["loop"]
	require.Equal(s.T(), "/usr/local/bin/loop", ls.Command)
	require.Equal(s.T(), []string{"mcp", "--channel-id", "ch-1", "--api-url", "http://host.docker.internal:8222", "--log", "/mcp/mcp.log"}, ls.Args)

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
			name:     "valid mount with expansion",
			input:    "~/.claude:/home/agent/.claude",
			expected: "/home/testuser/.claude:/home/agent/.claude",
			wantErr:  false,
		},
		{
			name:     "valid mount with readonly flag",
			input:    "~/.gitconfig:/home/agent/.gitconfig:ro",
			expected: "/home/testuser/.gitconfig:/home/agent/.gitconfig:ro",
			wantErr:  false,
		},
		{
			name:     "non-existent path returns empty",
			input:    "~/.nonexistent:/target",
			expected: "",
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

func TestProcessMountExpandPathError(t *testing.T) {
	origUserHomeDir := userHomeDir
	defer func() { userHomeDir = origUserHomeDir }()

	userHomeDir = func() (string, error) {
		return "", errors.New("home dir error")
	}

	result, err := processMount("~/.claude:/home/agent/.claude")
	require.Error(t, err)
	require.Contains(t, err.Error(), "expanding path")
	require.Empty(t, result)
}

func (s *RunnerSuite) TestRunWithInvalidMount() {
	s.cfg.Mounts = []string{"invalid-mount-no-colon"}

	ctx := context.Background()
	req := &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
	}

	jsonOutput := `{"result":"ok","session_id":"s1","is_error":false}`
	reader := bytes.NewReader([]byte(jsonOutput))

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		// Invalid mount should be skipped, only workDir and mcpDir binds
		return len(cfg.Binds) == 2
	}), "").Return("container-123", nil)
	s.client.On("ContainerAttach", ctx, "container-123").Return(reader, nil)
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
		"~/.claude:/home/agent/.claude",
		"~/.gitconfig:/home/agent/.gitconfig:ro",
		"~/.ssh:/home/agent/.ssh:ro", // This will be skipped as non-existent
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
			"/home/testuser/.claude:/home/agent/.claude",
			"/home/testuser/.gitconfig:/home/agent/.gitconfig:ro",
			"/home/testuser/.loop/ch-1/work:/home/testuser/.loop/ch-1/work",
			"/home/testuser/.loop/ch-1/mcp:/home/testuser/.loop/ch-1/mcp",
		}
		bindsMatch := slices.Equal(cfg.Binds, expectedBinds)
		workDirMatch := cfg.WorkingDir == "/home/testuser/.loop/ch-1/work"
		return bindsMatch && workDirMatch
	}), "").Return("container-123", nil)
	s.client.On("ContainerAttach", ctx, "container-123").Return(reader, nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), resp)

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
		mcpDirBind := "/home/testuser/.loop/ch-1/mcp:/home/testuser/.loop/ch-1/mcp"
		hasCorrectBinds := slices.Contains(cfg.Binds, workDirBind) && slices.Contains(cfg.Binds, mcpDirBind)
		hasCorrectWorkingDir := cfg.WorkingDir == "/custom/project/path"
		return hasCorrectBinds && hasCorrectWorkingDir
	}), "").Return("container-123", nil)
	s.client.On("ContainerAttach", ctx, "container-123").Return(reader, nil)
	s.client.On("ContainerStart", ctx, "container-123").Return(nil)
	s.client.On("ContainerWait", ctx, "container-123").Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerRemove", ctx, "container-123").Return(nil)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), resp)

	s.client.AssertExpectations(s.T())
}
