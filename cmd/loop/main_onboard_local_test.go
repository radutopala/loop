package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
)

// --- onboard:local ---

func (s *MainSuite) TestOnboardLocalSuccess() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return []ensureResult{{Platform: "local", ChannelID: "ch-test", Created: true}}, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.NoError(s.T(), err)

	mcpPath := filepath.Join(tmpDir, ".mcp.json")
	data, err := os.ReadFile(mcpPath)
	require.NoError(s.T(), err)

	var result map[string]any
	require.NoError(s.T(), json.Unmarshal(data, &result))

	servers := result["mcpServers"].(map[string]any)
	loop := servers["loop"].(map[string]any)
	require.Equal(s.T(), "loop", loop["command"])

	args := loop["args"].([]any)
	require.Equal(s.T(), "mcp", args[0])
	require.Equal(s.T(), "--dir", args[1])
	require.Equal(s.T(), tmpDir, args[2])
	require.Equal(s.T(), "--api-url", args[3])
	require.Equal(s.T(), "http://localhost:8222", args[4])
	require.Equal(s.T(), "--platform", args[5])
	require.Equal(s.T(), "local", args[6])
	require.Equal(s.T(), "--log", args[7])
	require.Equal(s.T(), filepath.Join(tmpDir, ".loop", "mcp.log"), args[8])
}

func (s *MainSuite) TestOnboardLocalWithMemoryEnabled() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return []ensureResult{{Platform: "local", ChannelID: "ch-test", Created: true}}, nil
	}
	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{Memory: config.MemoryConfig{Enabled: true}}, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.NoError(s.T(), err)

	data, err := os.ReadFile(filepath.Join(tmpDir, ".mcp.json"))
	require.NoError(s.T(), err)

	var result map[string]any
	require.NoError(s.T(), json.Unmarshal(data, &result))

	servers := result["mcpServers"].(map[string]any)
	loop := servers["loop"].(map[string]any)
	args := loop["args"].([]any)
	require.Equal(s.T(), "--memory", args[len(args)-1])
}

func (s *MainSuite) TestOnboardLocalMergesExisting() {
	tmpDir := s.T().TempDir()
	existing := `{"mcpServers":{"other":{"command":"other-cmd"}}}`
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, ".mcp.json"), []byte(existing), 0644))

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return []ensureResult{{Platform: "local", ChannelID: "ch-test", Created: true}}, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.NoError(s.T(), err)

	data, err := os.ReadFile(filepath.Join(tmpDir, ".mcp.json"))
	require.NoError(s.T(), err)

	var result map[string]any
	require.NoError(s.T(), json.Unmarshal(data, &result))

	servers := result["mcpServers"].(map[string]any)
	require.Contains(s.T(), servers, "other", "existing server should be preserved")
	require.Contains(s.T(), servers, "loop", "loop server should be added")
}

func (s *MainSuite) TestOnboardLocalAlreadyRegisteredUpdatesArgs() {
	tmpDir := s.T().TempDir()
	existing := `{"mcpServers":{"loop":{"command":"loop","args":["mcp"]}}}`
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, ".mcp.json"), []byte(existing), 0644))

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return []ensureResult{{Platform: "local", ChannelID: "ch-test", Created: true}}, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.NoError(s.T(), err)

	// Verify file was updated with rebuilt args
	data, err := os.ReadFile(filepath.Join(tmpDir, ".mcp.json"))
	require.NoError(s.T(), err)
	var result map[string]any
	require.NoError(s.T(), json.Unmarshal(data, &result))
	servers := result["mcpServers"].(map[string]any)
	loop := servers["loop"].(map[string]any)
	args := loop["args"].([]any)
	require.Equal(s.T(), "mcp", args[0])
	require.Equal(s.T(), "--dir", args[1])
}

func (s *MainSuite) TestOnboardLocalInvalidExistingJSON() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, ".mcp.json"), []byte("not json"), 0644))

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parsing existing .mcp.json")
}

func (s *MainSuite) TestOnboardLocalGetwdError() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return("", errors.New("getwd error"))

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting working directory")
}

func (s *MainSuite) TestOnboardLocalWriteError() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	sys.Override("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("write error"))

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing .mcp.json")
}

func (s *MainSuite) TestOnboardLocalCmdRunE() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return []ensureResult{{Platform: "local", ChannelID: "ch-test", Created: true}}, nil
	}

	cmd := s.app.newOnboardLocalCmd()
	cmd.SetArgs([]string{"--api-url", "http://custom:9999"})
	err := cmd.Execute()
	require.NoError(s.T(), err)

	data, err := os.ReadFile(filepath.Join(tmpDir, ".mcp.json"))
	require.NoError(s.T(), err)

	var result map[string]any
	require.NoError(s.T(), json.Unmarshal(data, &result))

	servers := result["mcpServers"].(map[string]any)
	loop := servers["loop"].(map[string]any)
	args := loop["args"].([]any)
	require.Equal(s.T(), "http://custom:9999", args[4])
	require.Equal(s.T(), "--platform", args[5])
	require.Equal(s.T(), "local", args[6])
	require.Equal(s.T(), "--log", args[7])
	require.Equal(s.T(), filepath.Join(tmpDir, ".loop", "mcp.log"), args[8])
}

func (s *MainSuite) TestOnboardLocalEnsuresChannels() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)

	var calledAPIURL, calledDir string
	s.app.ensureAllChannelsFn = func(apiURL, dir string) ([]ensureResult, error) {
		calledAPIURL = apiURL
		calledDir = dir
		return []ensureResult{{Platform: "local", ChannelID: "ch-123", Created: true}}, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "http://localhost:8222", calledAPIURL)
	require.Equal(s.T(), tmpDir, calledDir)
}

func (s *MainSuite) TestOnboardLocalEnsureChannelsFailsGracefully() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return nil, errors.New("server not running")
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.NoError(s.T(), err, "onboardLocal should succeed even when ensureAllChannels fails")
}

func (s *MainSuite) TestOnboardLocalWithPlatformFlag() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)

	var calledAPIURL, calledDir, calledPlatform string
	s.app.ensureChannelFn = func(apiURL, dir, platform string) (string, error) {
		calledAPIURL = apiURL
		calledDir = dir
		calledPlatform = platform
		return "ch-local-123", nil
	}
	ensureAllCalled := false
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		ensureAllCalled = true
		return nil, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "local")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "http://localhost:8222", calledAPIURL)
	require.Equal(s.T(), tmpDir, calledDir)
	require.Equal(s.T(), "local", calledPlatform)
	require.False(s.T(), ensureAllCalled, "ensureAllChannelsFunc should NOT be called when --platform is set")
}

func (s *MainSuite) TestOnboardLocalWithPlatformFlagEnsureError() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)

	s.app.ensureChannelFn = func(_, _, _ string) (string, error) {
		return "", errors.New("server not running")
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "local")
	require.NoError(s.T(), err, "onboardLocal should succeed even when ensureChannel fails")
}

func (s *MainSuite) TestOnboardLocalAlreadyRegisteredStillEnsuresChannels() {
	tmpDir := s.T().TempDir()
	existing := `{"mcpServers":{"loop":{"command":"loop","args":["mcp","--dir","` + tmpDir + `","--api-url","http://localhost:8222"]}}}`
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, ".mcp.json"), []byte(existing), 0644))

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)

	called := false
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		called = true
		return []ensureResult{{Platform: "local", ChannelID: "ch-456", Created: false}}, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.NoError(s.T(), err)
	require.True(s.T(), called, "ensureAllChannelsFunc should be called even when loop is already registered")
}

func (s *MainSuite) TestOnboardLocalProjectConfigWritten() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return []ensureResult{{Platform: "local", ChannelID: "ch-test", Created: true}}, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.NoError(s.T(), err)

	projectConfigPath := filepath.Join(tmpDir, ".loop", "config.json")
	data, err := os.ReadFile(projectConfigPath)
	require.NoError(s.T(), err)
	require.Equal(s.T(), string(config.ProjectExampleConfig), string(data))
}

func (s *MainSuite) TestOnboardLocalProjectConfigAlreadyExists() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(loopDir, "config.json"), []byte(`{"claude_model":"custom"}`), 0644))

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return []ensureResult{{Platform: "local", ChannelID: "ch-test", Created: true}}, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.NoError(s.T(), err)

	// Verify existing config was NOT overwritten
	data, err := os.ReadFile(filepath.Join(loopDir, "config.json"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), `{"claude_model":"custom"}`, string(data))
}

func (s *MainSuite) TestOnboardLocalProjectConfigMkdirError() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	sys.Override("MkdirAll", mock.Anything, mock.Anything).Return(errors.New("mkdir error"))
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return []ensureResult{{Platform: "local", ChannelID: "ch-test", Created: true}}, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating .loop directory")
}

func (s *MainSuite) TestOnboardLocalProjectConfigWriteError() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	writeCall := sys.Override("WriteFile", mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)
	writeCount := 0
	writeCall.RunFn = func(args mock.Arguments) {
		writeCount++
		if writeCount == 2 {
			writeCall.ReturnArguments = mock.Arguments{errors.New("write config error")}
			return
		}
		writeCall.ReturnArguments = mock.Arguments{os.WriteFile(args.String(0), args.Get(1).([]byte), args.Get(2).(os.FileMode))}
	}
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return []ensureResult{{Platform: "local", ChannelID: "ch-test", Created: true}}, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing project config")
}

func (s *MainSuite) TestOnboardLocalTemplatesDirError() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	mkdirCall := sys.Override("MkdirAll", mock.Anything, mock.Anything).Maybe().Return(nil)
	mkdirCalls := 0
	mkdirCall.RunFn = func(args mock.Arguments) {
		mkdirCalls++
		if mkdirCalls == 2 { // Second mkdir is templates dir (after .loop dir)
			mkdirCall.ReturnArguments = mock.Arguments{errors.New("templates mkdir error")}
			return
		}
		mkdirCall.ReturnArguments = mock.Arguments{os.MkdirAll(args.String(0), args.Get(1).(os.FileMode))}
	}
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return []ensureResult{{Platform: "local", ChannelID: "ch-test", Created: true}}, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating templates directory")
}

func (s *MainSuite) TestOnboardLocalTemplatesDirCreated() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return []ensureResult{{Platform: "local", ChannelID: "ch-test", Created: true}}, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.NoError(s.T(), err)

	// Verify templates directory was created
	templatesDir := filepath.Join(tmpDir, ".loop", "templates")
	info, err := os.Stat(templatesDir)
	require.NoError(s.T(), err)
	require.True(s.T(), info.IsDir())
}

func (s *MainSuite) TestOnboardLocalShortcutsDirError() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	mkdirCall := sys.Override("MkdirAll", mock.Anything, mock.Anything).Maybe().Return(nil)
	mkdirCalls := 0
	mkdirCall.RunFn = func(args mock.Arguments) {
		mkdirCalls++
		if mkdirCalls == 3 { // Third mkdir is shortcuts dir (after .loop dir and templates dir)
			mkdirCall.ReturnArguments = mock.Arguments{errors.New("shortcuts mkdir error")}
			return
		}
		mkdirCall.ReturnArguments = mock.Arguments{os.MkdirAll(args.String(0), args.Get(1).(os.FileMode))}
	}
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return []ensureResult{{Platform: "local", ChannelID: "ch-test", Created: true}}, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating shortcuts directory")
}

func (s *MainSuite) TestOnboardLocalShortcutsDirCreated() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return []ensureResult{{Platform: "local", ChannelID: "ch-test", Created: true}}, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.NoError(s.T(), err)

	// Verify shortcuts directory was created
	shortcutsDir := filepath.Join(tmpDir, ".loop", "shortcuts")
	info, err := os.Stat(shortcutsDir)
	require.NoError(s.T(), err)
	require.True(s.T(), info.IsDir())
}

func (s *MainSuite) TestOnboardLocalWithOwnerID() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return []ensureResult{{Platform: "local", ChannelID: "ch-test", Created: true}}, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "U99887766", "")
	require.NoError(s.T(), err)

	projectConfigPath := filepath.Join(tmpDir, ".loop", "config.json")
	data, err := os.ReadFile(projectConfigPath)
	require.NoError(s.T(), err)

	content := string(data)
	require.Contains(s.T(), content, `"permissions": {`)
	require.Contains(s.T(), content, `"U99887766"`)
	require.NotContains(s.T(), content, `//  "owners"`)
}

func (s *MainSuite) TestOnboardLocalCmdWithOwnerIDFlag() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return []ensureResult{{Platform: "local", ChannelID: "ch-test", Created: true}}, nil
	}

	cmd := s.app.newOnboardLocalCmd()
	cmd.SetArgs([]string{"--owner-id", "ULOCAL123"})
	err := cmd.Execute()
	require.NoError(s.T(), err)

	projectConfigPath := filepath.Join(tmpDir, ".loop", "config.json")
	data, err := os.ReadFile(projectConfigPath)
	require.NoError(s.T(), err)

	content := string(data)
	require.Contains(s.T(), content, `"ULOCAL123"`)
	require.Contains(s.T(), content, `"permissions": {`)
}
