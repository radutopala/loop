package container

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
)

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

func (s *RunnerSuite) TestRunClaudeEffortConfig() {
	tests := []struct {
		name        string
		effort      string
		checkConfig func(*ContainerConfig) bool
	}{
		{
			name:   "with effort",
			effort: "high",
			checkConfig: func(cfg *ContainerConfig) bool {
				effortIdx := slices.Index(cfg.Cmd, "--effort")
				return effortIdx != -1 && cfg.Cmd[effortIdx+1] == "high"
			},
		},
		{
			name: "without effort",
			checkConfig: func(cfg *ContainerConfig) bool {
				return !slices.Contains(cfg.Cmd, "--effort")
			},
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.client = new(MockDockerClient)
			s.cfg.ClaudeEffort = tt.effort
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

	cmd := buildBaseClaudeCmd(cfg, "/work/.loop/mcp-ch-1.json", "", "", false, false, extraDirs)
	got := strings.Join(cmd, " ")

	require.Contains(s.T(), got, "--add-dir /home/user/lib")
	require.Contains(s.T(), got, "--add-dir /home/user/common")
}

func (s *RunnerSuite) TestBuildBaseClaudeCmdNoExtraDirs() {
	cfg := &config.Config{ClaudeBinPath: "claude"}

	cmd := buildBaseClaudeCmd(cfg, "/work/.loop/mcp-ch-1.json", "", "", false, false, nil)
	got := strings.Join(cmd, " ")

	require.NotContains(s.T(), got, "--add-dir")
}

func (s *RunnerSuite) TestBuildInteractiveClaudeCmdWithExtraDirs() {
	cfg := &config.Config{ClaudeBinPath: "claude", ExtraDirs: []string{"/extra/dir"}}
	got := BuildInteractiveClaudeCmd(cfg, "ch-1", "/work", "", "", false)
	require.Contains(s.T(), got, "--add-dir /extra/dir")
}
