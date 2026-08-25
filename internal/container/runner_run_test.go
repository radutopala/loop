package container

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
)

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

// TestRunDropsResumeWhenTranscriptMissing covers the case that wedged a
// channel in practice: Claude Code pruned the transcript its session id
// points at, so --resume would fail every turn from then on. The runner
// drops the flags and starts fresh instead.
func (s *RunnerSuite) TestRunDropsResumeWhenTranscriptMissing() {
	var buf bytes.Buffer
	s.runner.SetLogger(slog.New(slog.NewTextHandler(&buf, nil)))
	var gotWorkDir, gotSession string
	s.runner.transcriptMissing = func(workDir, sessionID string) bool {
		gotWorkDir, gotSession = workDir, sessionID
		return true
	}

	ctx := context.Background()
	req := &agent.AgentRequest{
		SessionID:   "sess-pruned",
		ForkSession: true,
		Messages:    []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID:   "ch-1",
	}

	s.setupMockRun(ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return !slices.Contains(cfg.Cmd, "--resume") && !slices.Contains(cfg.Cmd, "--fork-session")
	}), testContainerName, `{"type":"result","result":"Fresh!","session_id":"sess-new","is_error":false}`)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "sess-new", resp.SessionID)
	require.Equal(s.T(), "/home/testuser/.loop/ch-1/work", gotWorkDir)
	require.Equal(s.T(), "sess-pruned", gotSession)
	require.Contains(s.T(), buf.String(), "session transcript not found")
	// The caller's request is left alone — the drop applies to this run only.
	require.Equal(s.T(), "sess-pruned", req.SessionID)
	require.True(s.T(), req.ForkSession)

	s.client.AssertExpectations(s.T())
}

// TestRunDropsResumeWithoutLogger is the same drop with no logger wired up:
// SetLogger is optional, and a nil logger must not panic.
func (s *RunnerSuite) TestRunDropsResumeWithoutLogger() {
	s.runner.transcriptMissing = func(string, string) bool { return true }

	ctx := context.Background()
	req := &agent.AgentRequest{
		SessionID: "sess-pruned",
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
	}

	s.setupMockRun(ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return !slices.Contains(cfg.Cmd, "--resume")
	}), testContainerName, `{"type":"result","result":"Fresh!","session_id":"sess-new","is_error":false}`)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "sess-new", resp.SessionID)

	s.client.AssertExpectations(s.T())
}
