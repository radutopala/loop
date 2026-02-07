package container

import (
	"bytes"
	"context"
	"errors"
	"io"
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

func (m *MockDockerClient) ContainerList(ctx context.Context, labelKey, labelValue string) ([]string, error) {
	args := m.Called(ctx, labelKey, labelValue)
	return args.Get(0).([]string), args.Error(1)
}

type RunnerSuite struct {
	suite.Suite
	client *MockDockerClient
	runner *DockerRunner
	cfg    *config.Config
}

func TestRunnerSuite(t *testing.T) {
	suite.Run(t, new(RunnerSuite))
}

func (s *RunnerSuite) SetupTest() {
	s.client = new(MockDockerClient)
	s.cfg = &config.Config{
		ContainerImage:    "loop-agent:latest",
		ContainerMemoryMB: 512,
		ContainerCPUs:     1.0,
		ContainerTimeout:  30 * time.Second,
		APIAddr:           ":8222",
		WorkDir:           "/home/testuser/.loop/work",
	}
	s.runner = NewDockerRunner(s.client, s.cfg)
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
		hasBinds := len(cfg.Binds) == 2 && cfg.Binds[0] == sessionVolume && cfg.Binds[1] == "/home/testuser/.loop/work:/work"
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

// errReader always returns an error on Read.
type errReader struct {
	err error
}

func (r *errReader) Read([]byte) (int, error) {
	return 0, r.err
}
