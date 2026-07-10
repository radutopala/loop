package container

import (
	"archive/tar"
	"context"
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

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
)

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
		// ~/.claude.json has the consent flags merged in while preserving the
		// original content (the auth token).
		return strings.Contains(string(data), `"oauth_token":"tok-123"`) &&
			strings.Contains(string(data), `"bypassPermissionsModeAccepted":true`)
	})).Return(nil)

	err := s.runner.copyFiles(ctx, containerID, []string{"~/.claude.json"}, "/work")
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

	err := s.runner.copyFiles(ctx, containerID, []string{"~/.claude.json", "~/.npmrc"}, "/work")
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{".claude.json", ".npmrc"}, tarNames)
	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestCopyFilesNotExists() {
	ctx := context.Background()
	// osReadFile already returns os.ErrNotExist by default in SetupTest

	err := s.runner.copyFiles(ctx, "cid-nofile", []string{"~/.claude.json"}, "/work")
	require.NoError(s.T(), err)
	s.client.AssertNotCalled(s.T(), "CopyToContainer")
}

func (s *RunnerSuite) TestCopyFilesCopyError() {
	ctx := context.Background()
	containerID := "cid-copyerr"

	s.sys.Override("ReadFile", "/home/testuser/.claude.json").Return([]byte(`{}`), nil)
	s.sys.On("ReadFile", mock.Anything).Return(nil, os.ErrNotExist)

	s.client.On("CopyToContainer", ctx, containerID, "/home/testuser", mock.Anything).Return(errors.New("copy failed"))

	err := s.runner.copyFiles(ctx, containerID, []string{"~/.claude.json"}, "/work")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "copy failed")
	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestCopyFilesExpandError() {
	ctx := context.Background()

	s.sys.Override("UserHomeDir").Return("", errors.New("no home"))

	err := s.runner.copyFiles(ctx, "cid-nohome", []string{"~/.claude.json"}, "/work")
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

	err := s.runner.copyFiles(ctx, "cid-readerr", []string{"~/.claude.json"}, "/work")
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

	err := s.runner.copyFiles(ctx, containerID, []string{"/etc/some.conf"}, "/work")
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
			require.Equal(s.T(), tc.expected+claudeExitTrailer, got)
		})
	}
}

func (s *RunnerSuite) TestBuildInteractiveClaudeCmdWithAgentID() {
	// Default config: even with an agent ID, the development-channels flag is
	// off until the opt-in config switch is set.
	cfg := &config.Config{ClaudeBinPath: "claude"}
	got := BuildInteractiveClaudeCmd(cfg, "ch-1", "/work", "", "agent-0", false)
	require.Equal(s.T(), "CLAUDE_CODE_NO_FLICKER=1 claude --mcp-config /work/.loop/mcp-ch-1-agent-0.json --dangerously-skip-permissions"+claudeExitTrailer, got)
}

func (s *RunnerSuite) TestBuildInteractiveClaudeCmdWithAgentIDAndDevChannels() {
	cfg := &config.Config{ClaudeBinPath: "claude", ClaudeDangerouslyLoadDevelopmentChannels: true}
	got := BuildInteractiveClaudeCmd(cfg, "ch-1", "/work", "", "agent-0", false)
	require.Equal(s.T(), "CLAUDE_CODE_NO_FLICKER=1 claude --mcp-config /work/.loop/mcp-ch-1-agent-0.json --dangerously-skip-permissions --dangerously-load-development-channels server:loop"+claudeExitTrailer, got)
}

func (s *RunnerSuite) TestBuildInteractiveClaudeCmdDevChannelsWithoutAgentID() {
	// The flag requires BOTH an agent ID and the config opt-in; the config
	// alone is not enough.
	cfg := &config.Config{ClaudeBinPath: "claude", ClaudeDangerouslyLoadDevelopmentChannels: true}
	got := BuildInteractiveClaudeCmd(cfg, "ch-1", "/work", "", "", false)
	require.NotContains(s.T(), got, "--dangerously-load-development-channels")
}

// TestBuildInteractiveClaudeCmdGateEnabled proves the gate-on branch prepends
// `loop syscallwrap --` so an interactive claude launched from a docker-exec
// shell runs under the same seccomp filter the stream-mode path installs via
// entrypoint.sh.
func (s *RunnerSuite) TestBuildInteractiveClaudeCmdGateEnabled() {
	cfg := &config.Config{ClaudeBinPath: "claude"}
	cfg.Gates.Agentgate.Enabled = true
	got := BuildInteractiveClaudeCmd(cfg, "ch-1", "/work", "", "", false)
	require.Equal(s.T(), "CLAUDE_CODE_NO_FLICKER=1 loop syscallwrap -- claude --mcp-config /work/.loop/mcp-ch-1.json --dangerously-skip-permissions"+claudeExitTrailer, got)
}

// TestBuildInteractiveClaudeCmdGateDisabled confirms the baseline (no prefix)
// when the gate is explicitly off.
func (s *RunnerSuite) TestBuildInteractiveClaudeCmdGateDisabled() {
	cfg := &config.Config{ClaudeBinPath: "claude"}
	cfg.Gates.Agentgate.Enabled = false
	got := BuildInteractiveClaudeCmd(cfg, "ch-1", "/work", "", "", false)
	require.NotContains(s.T(), got, "loop syscallwrap")
	require.Equal(s.T(), "CLAUDE_CODE_NO_FLICKER=1 claude --mcp-config /work/.loop/mcp-ch-1.json --dangerously-skip-permissions"+claudeExitTrailer, got)
}

func (s *RunnerSuite) TestBuildBaseClaudeCmdFlags() {
	cfg := &config.Config{ClaudeBinPath: "claude"}

	// Baseline: --dangerously-skip-permissions, no --permission-mode,
	// no --dangerously-load-development-channels.
	cmd := buildBaseClaudeCmd(cfg, "/work/.loop/mcp-ch-1.json", "", "", false, false, nil)
	got := strings.Join(cmd, " ")
	require.Contains(s.T(), got, "--dangerously-skip-permissions")
	require.NotContains(s.T(), got, "--permission-mode")
	require.NotContains(s.T(), got, "--dangerously-load-development-channels")

	// With agent ID but default config: the development-channels flag is
	// off until the opt-in is set.
	cmd = buildBaseClaudeCmd(cfg, "/work/.loop/mcp-ch-1.json", "", "agent-0", false, false, nil)
	got = strings.Join(cmd, " ")
	require.NotContains(s.T(), got, "--dangerously-load-development-channels")

	// With agent ID + opt-in config: --dangerously-load-development-channels server:loop is added.
	cfg.ClaudeDangerouslyLoadDevelopmentChannels = true
	cmd = buildBaseClaudeCmd(cfg, "/work/.loop/mcp-ch-1.json", "", "agent-0", false, false, nil)
	got = strings.Join(cmd, " ")
	require.Contains(s.T(), got, "--dangerously-load-development-channels server:loop")

	// Opt-in alone (no agent ID) still omits the flag.
	cmd = buildBaseClaudeCmd(cfg, "/work/.loop/mcp-ch-1.json", "", "", false, false, nil)
	got = strings.Join(cmd, " ")
	require.NotContains(s.T(), got, "--dangerously-load-development-channels")
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

func (s *RunnerSuite) TestBuildClaudeCmdPermissionPromptTool() {
	cfg := &config.Config{ClaudeBinPath: "claude"}
	req := &agent.AgentRequest{
		ChannelID: "ch-1",
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hi"}},
	}
	cmd := buildClaudeCmd(cfg, "/work/.loop/mcp-ch-1.json", req)
	// Batch mode must configure a permission-prompt-tool so Claude exposes the
	// interactive tools (AskUserQuestion/EnterPlanMode/ExitPlanMode) in --print
	// mode; it names a registered MCP tool (empty does not work).
	idx := slices.Index(cmd, "--permission-prompt-tool")
	require.GreaterOrEqual(s.T(), idx, 0, "batch cmd must set --permission-prompt-tool")
	require.Equal(s.T(), "mcp__loop__permission_prompt", cmd[idx+1])
	// The interactive terminal command must NOT carry it — interactive mode
	// already exposes these tools.
	got := BuildInteractiveClaudeCmd(cfg, "ch-1", "/work", "", "", false)
	require.NotContains(s.T(), got, "--permission-prompt-tool")
}

func (s *RunnerSuite) TestBuildClaudeCmdDisallowedTools() {
	req := &agent.AgentRequest{
		ChannelID: "ch-1",
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
	}

	// With a disallowed-tools list: a single comma-joined --disallowedTools
	// value (so the variadic flag can't swallow the trailing prompt), and the
	// prompt stays the last argument.
	cfg := &config.Config{
		ClaudeBinPath:              "claude",
		ClaudeBatchDisallowedTools: []string{"ScheduleWakeup", "CronCreate"},
	}
	cmd := buildClaudeCmd(cfg, "/work/.loop/mcp-ch-1.json", req)
	got := strings.Join(cmd, " ")
	require.Contains(s.T(), got, "--disallowedTools ScheduleWakeup,CronCreate")
	require.Equal(s.T(), "user: hello\n", cmd[len(cmd)-1])
	// The flag value is one comma-joined token, not separate args.
	idx := slices.Index(cmd, "--disallowedTools")
	require.GreaterOrEqual(s.T(), idx, 0)
	require.Equal(s.T(), "ScheduleWakeup,CronCreate", cmd[idx+1])
	// Critical: --disallowedTools is variadic, so it MUST come before --print
	// (a flag terminates the variadic). Emitted adjacent to the trailing prompt
	// it would swallow the prompt into bogus tool names.
	printIdx := slices.Index(cmd, "--print")
	require.Less(s.T(), idx, printIdx, "--disallowedTools must precede --print")

	// Empty list: no --disallowedTools flag at all.
	cfg.ClaudeBatchDisallowedTools = nil
	cmd = buildClaudeCmd(cfg, "/work/.loop/mcp-ch-1.json", req)
	require.NotContains(s.T(), strings.Join(cmd, " "), "--disallowedTools")
}

func (s *RunnerSuite) TestBuildInteractiveClaudeCmdNoDisallowedTools() {
	// The interactive terminal path must NOT carry --disallowedTools even when
	// the batch list is configured — denial is batch-only.
	cfg := &config.Config{
		ClaudeBinPath:              "claude",
		ClaudeBatchDisallowedTools: []string{"ScheduleWakeup"},
	}
	got := BuildInteractiveClaudeCmd(cfg, "ch-1", "/work", "", "", false)
	require.NotContains(s.T(), got, "--disallowedTools")
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
			require.Equal(s.T(), "CLAUDE_CODE_NO_FLICKER=1 claude --mcp-config "+expectedMCP+" --dangerously-skip-permissions"+tc.wantExtra+claudeExitTrailer, got)
		})
	}
}

func (s *RunnerSuite) TestClaudeCmdBuilderBuildContinueCmd() {
	cfg := &config.Config{
		ClaudeBinPath: "claude",
		LoopDir:       "/home/user/.loop",
	}
	builder := NewClaudeCmdBuilder(cfg, nil)
	got := builder.BuildContinueCmd("ch-1", "/projects/myapp", "", "")

	// --continue is used, never --resume/--fork-session, regardless of any
	// stored channel session id (BuildContinueCmd never looks one up).
	require.Contains(s.T(), got, "--continue")
	require.NotContains(s.T(), got, "--resume")
	require.NotContains(s.T(), got, "--fork-session")

	expectedMCP := "/projects/myapp/.loop/mcp-ch-1.json"
	require.Equal(s.T(), "CLAUDE_CODE_NO_FLICKER=1 claude --mcp-config "+expectedMCP+" --dangerously-skip-permissions --continue"+claudeExitTrailer, got)
}

func (s *RunnerSuite) TestBuildBaseClaudeCmdContinueSessionIgnoresSessionID() {
	cfg := &config.Config{ClaudeBinPath: "claude"}
	// continueSession=true wins even when a sessionID is also supplied.
	cmd := buildBaseClaudeCmd(cfg, "/work/.loop/mcp-ch-1.json", "sess-should-be-ignored", "", false, true, nil)
	got := strings.Join(cmd, " ")
	require.Contains(s.T(), got, "--continue")
	require.NotContains(s.T(), got, "--resume")
	require.NotContains(s.T(), got, "sess-should-be-ignored")
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
	require.Equal(s.T(), "CLAUDE_CODE_NO_FLICKER=1 claude --mcp-config "+expectedMCP+" --model claude-opus-4-6 --dangerously-skip-permissions"+claudeExitTrailer, got)
}

func (s *RunnerSuite) TestClaudeCmdBuilderWritesAgentMCPConfig() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))

	cfg := &config.Config{
		ClaudeBinPath:                            "claude",
		LoopDir:                                  "/home/user/.loop",
		APIAddr:                                  ":8222",
		Memory:                                   config.MemoryConfig{Enabled: true},
		ClaudeDangerouslyLoadDevelopmentChannels: true,
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
	require.Equal(s.T(), "CLAUDE_CODE_NO_FLICKER=1 claude --mcp-config "+expectedMCP+" --model claude-opus-4-6 --dangerously-skip-permissions"+claudeExitTrailer, got)
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
