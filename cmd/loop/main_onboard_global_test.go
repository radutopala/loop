package main

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- onboard:global ---

func (s *MainSuite) TestNewOnboardGlobalCmd() {
	cmd := s.app.newOnboardGlobalCmd()
	require.Equal(s.T(), "onboard:global", cmd.Use)
	require.Equal(s.T(), []string{"o:global", "setup"}, cmd.Aliases)
	require.NotNil(s.T(), cmd.RunE)
	require.NotNil(s.T(), cmd.Flags().Lookup("force"))
	f := cmd.Flags().Lookup("owner-id")
	require.NotNil(s.T(), f)
	require.Equal(s.T(), "", f.DefValue)
}

func (s *MainSuite) TestNewOnboardLocalCmd() {
	cmd := s.app.newOnboardLocalCmd()
	require.Equal(s.T(), "onboard:local", cmd.Use)
	require.Equal(s.T(), []string{"o:local", "init"}, cmd.Aliases)
	require.NotNil(s.T(), cmd.RunE)
	f := cmd.Flags().Lookup("api-url")
	require.NotNil(s.T(), f)
	require.Equal(s.T(), "http://localhost:8222", f.DefValue)
	ownerF := cmd.Flags().Lookup("owner-id")
	require.NotNil(s.T(), ownerF)
	require.Equal(s.T(), "", ownerF.DefValue)
}

func (s *MainSuite) TestOnboardGlobalSuccess() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return(tmpDir, nil)

	err := s.app.onboardGlobal(false, "")
	require.NoError(s.T(), err)

	configPath := filepath.Join(tmpDir, ".loop", "config.json")
	data, err := os.ReadFile(configPath)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(data), "discord_token")
	require.Contains(s.T(), string(data), "task_templates")

	// Verify container files were written
	dockerfilePath := filepath.Join(tmpDir, ".loop", "container", "Dockerfile")
	dockerfileData, err := os.ReadFile(dockerfilePath)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(dockerfileData), "FROM golang:")

	entrypointPath := filepath.Join(tmpDir, ".loop", "container", "entrypoint.sh")
	entrypointData, err := os.ReadFile(entrypointPath)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(entrypointData), `gosu "$AGENT_USER" "$@"`)

	setupPath := filepath.Join(tmpDir, ".loop", "container", "setup.sh")
	setupData, err := os.ReadFile(setupPath)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(setupData), "#!/bin/bash")

	bashrcPath := filepath.Join(tmpDir, ".loop", ".bashrc")
	bashrcData, err := os.ReadFile(bashrcPath)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(bashrcData), "Shell aliases")

	// Verify Slack manifest was written
	manifestPath := filepath.Join(tmpDir, ".loop", "slack-manifest.json")
	manifestData, err := os.ReadFile(manifestPath)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(manifestData), "LoopBot")
	require.Contains(s.T(), string(manifestData), "socket_mode_enabled")

	// Verify templates directory was created
	templatesDir := filepath.Join(tmpDir, ".loop", "templates")
	info, err := os.Stat(templatesDir)
	require.NoError(s.T(), err)
	require.True(s.T(), info.IsDir())

	// Verify embedded templates were written
	heartbeatPath := filepath.Join(templatesDir, "heartbeat.md")
	heartbeatData, err := os.ReadFile(heartbeatPath)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(heartbeatData), "heartbeat check")

	tkAutoWorkerPath := filepath.Join(templatesDir, "tk-auto-worker.md")
	tkAutoWorkerData, err := os.ReadFile(tkAutoWorkerPath)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(tkAutoWorkerData), "ticket dispatcher")

	// Verify playground examples were written
	playgroundDir := filepath.Join(tmpDir, ".loop", "playground")
	pgInfo, err := os.Stat(playgroundDir)
	require.NoError(s.T(), err)
	require.True(s.T(), pgInfo.IsDir())

	pongIndex := filepath.Join(playgroundDir, "pong", "index.html")
	pongData, err := os.ReadFile(pongIndex)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), pongData)

	tetrisReadme := filepath.Join(playgroundDir, "tetris", "README.md")
	tetrisData, err := os.ReadFile(tetrisReadme)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(tetrisData), "title:")
}

func (s *MainSuite) TestOnboardGlobalConfigAlreadyExists() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	configPath := filepath.Join(loopDir, "config.json")

	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(configPath, []byte("existing"), 0600))

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return(tmpDir, nil)

	err := s.app.onboardGlobal(false, "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "config already exists")
	require.Contains(s.T(), err.Error(), "--force")

	// Verify original content is unchanged
	data, err := os.ReadFile(configPath)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "existing", string(data))
}

func (s *MainSuite) TestOnboardGlobalForceOverwrite() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	configPath := filepath.Join(loopDir, "config.json")

	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(configPath, []byte("old config"), 0600))

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return(tmpDir, nil)

	err := s.app.onboardGlobal(true, "")
	require.NoError(s.T(), err)

	// Verify config was overwritten
	data, err := os.ReadFile(configPath)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(data), "discord_token")
	require.Contains(s.T(), string(data), "task_templates")
	require.NotContains(s.T(), string(data), "old config")
}

func (s *MainSuite) TestOnboardGlobalHomeDirError() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("", errors.New("home dir error"))

	err := s.app.onboardGlobal(false, "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting home directory")
}

func (s *MainSuite) TestOnboardGlobalMkdirErrors() {
	tests := []struct {
		name      string
		failCallN int
		wantErr   string
	}{
		{"loop directory", 1, "creating loop directory"},
		{"container directory", 2, "creating container directory"},
		{"templates directory", 3, "creating templates directory"},
		{"shortcuts directory", 4, "creating shortcuts directory"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			tmpDir := s.T().TempDir()
			sys := newPassthroughMock()
			s.app.sys = sys
			sys.Override("UserHomeDir").Return(tmpDir, nil)
			mkdirCall := sys.Override("MkdirAll", mock.Anything, mock.Anything).Maybe().Return(nil)
			calls := 0
			mkdirCall.RunFn = func(args mock.Arguments) {
				calls++
				if calls == tt.failCallN {
					mkdirCall.ReturnArguments = mock.Arguments{errors.New("mkdir error")}
					return
				}
				mkdirCall.ReturnArguments = mock.Arguments{os.MkdirAll(args.String(0), args.Get(1).(os.FileMode))}
			}

			err := s.app.onboardGlobal(false, "")
			require.Error(s.T(), err)
			require.Contains(s.T(), err.Error(), tt.wantErr)
		})
	}
}

func (s *MainSuite) TestOnboardGlobalCmdWithForceFlag() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	configPath := filepath.Join(loopDir, "config.json")

	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(configPath, []byte("old"), 0600))

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return(tmpDir, nil)

	cmd := s.app.newOnboardGlobalCmd()
	cmd.SetArgs([]string{"--force"})
	err := cmd.Execute()
	require.NoError(s.T(), err)

	data, err := os.ReadFile(configPath)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(data), "discord_token")
}

func (s *MainSuite) TestOnboardGlobalBashrcSkipsIfExists() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	bashrcPath := filepath.Join(loopDir, ".bashrc")
	require.NoError(s.T(), os.WriteFile(bashrcPath, []byte("existing aliases"), 0644))

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return(tmpDir, nil)

	err := s.app.onboardGlobal(true, "") // force overwrites config but not .bashrc
	require.NoError(s.T(), err)

	data, err := os.ReadFile(bashrcPath)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "existing aliases", string(data))
}

func (s *MainSuite) TestOnboardGlobalSetupSkipsIfExists() {
	tmpDir := s.T().TempDir()
	containerDir := filepath.Join(tmpDir, ".loop", "container")
	require.NoError(s.T(), os.MkdirAll(containerDir, 0755))
	setupPath := filepath.Join(containerDir, "setup.sh")
	require.NoError(s.T(), os.WriteFile(setupPath, []byte("existing setup"), 0644))

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return(tmpDir, nil)

	err := s.app.onboardGlobal(true, "") // force overwrites config but not setup.sh
	require.NoError(s.T(), err)

	data, err := os.ReadFile(setupPath)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "existing setup", string(data))
}

func (s *MainSuite) TestOnboardGlobalWriteErrors() {
	tests := []struct {
		name      string
		failCallN int
		wantErr   string
	}{
		{"config file", 1, "writing config file"},
		{".bashrc", 2, "writing .bashrc"},
		{"Dockerfile", 3, "writing container Dockerfile"},
		{"chrome Dockerfile", 4, "writing chrome Dockerfile"},
		{"chrome entrypoint", 5, "writing chrome entrypoint"},
		{"entrypoint", 6, "writing container entrypoint"},
		{"agent-bashrc", 7, "writing container agent-bashrc"},
		{"setup script", 8, "writing container setup script"},
		{"Slack manifest", 9, "writing Slack manifest"},
		{"template", 10, "writing template"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			tmpDir := s.T().TempDir()
			sys := newPassthroughMock()
			s.app.sys = sys
			sys.Override("UserHomeDir").Return(tmpDir, nil)
			writeCall := sys.Override("WriteFile", mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)
			calls := 0
			writeCall.RunFn = func(args mock.Arguments) {
				calls++
				if calls == tt.failCallN {
					writeCall.ReturnArguments = mock.Arguments{errors.New("write error")}
					return
				}
				writeCall.ReturnArguments = mock.Arguments{os.WriteFile(args.String(0), args.Get(1).([]byte), args.Get(2).(os.FileMode))}
			}

			err := s.app.onboardGlobal(false, "")
			require.Error(s.T(), err)
			require.Contains(s.T(), err.Error(), tt.wantErr)
		})
	}
}

func (s *MainSuite) TestOnboardGlobalTemplatesSkipIfExist() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	templatesDir := filepath.Join(loopDir, "templates")

	require.NoError(s.T(), os.MkdirAll(templatesDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(templatesDir, "heartbeat.md"), []byte("custom heartbeat"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(templatesDir, "tk-auto-worker.md"), []byte("custom worker"), 0644))

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return(tmpDir, nil)

	err := s.app.onboardGlobal(true, "") // force overwrites config but not templates
	require.NoError(s.T(), err)

	data, err := os.ReadFile(filepath.Join(templatesDir, "heartbeat.md"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), "custom heartbeat", string(data))

	data, err = os.ReadFile(filepath.Join(templatesDir, "tk-auto-worker.md"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), "custom worker", string(data))
}

func (s *MainSuite) TestDumpPlaygroundExamplesSuccess() {
	dir := s.T().TempDir()
	s.app.sys = newPassthroughMock()
	err := s.app.dumpPlaygroundExamples(dir)
	require.NoError(s.T(), err)

	// Verify at least one example was written.
	entries, err := os.ReadDir(dir)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), entries)

	// Verify pong has files.
	pongHTML, err := os.ReadFile(filepath.Join(dir, "pong", "index.html"))
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), pongHTML)
}

func (s *MainSuite) TestDumpPlaygroundExamplesMkdirError() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("MkdirAll", mock.Anything, mock.Anything).Return(errors.New("mkdir error"))

	err := s.app.dumpPlaygroundExamples("/tmp/nonexistent")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating playground directory")
}

func (s *MainSuite) TestDumpPlaygroundExamplesWriteError() {
	dir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	writeCall := sys.Override("WriteFile", mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)
	calls := 0
	writeCall.RunFn = func(args mock.Arguments) {
		calls++
		if calls == 1 {
			writeCall.ReturnArguments = mock.Arguments{errors.New("write error")}
			return
		}
		writeCall.ReturnArguments = mock.Arguments{os.WriteFile(args.String(0), args.Get(1).([]byte), args.Get(2).(os.FileMode))}
	}

	err := s.app.dumpPlaygroundExamples(dir)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing playground file")
}

func (s *MainSuite) TestDumpPlaygroundExamplesExampleMkdirError() {
	dir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	mkdirCalls := 0
	mkdirCall := sys.Override("MkdirAll", mock.Anything, mock.Anything).Maybe().Return(nil)
	mkdirCall.RunFn = func(args mock.Arguments) {
		mkdirCalls++
		if mkdirCalls == 2 { // first is the playground dir itself, second is the first example
			mkdirCall.ReturnArguments = mock.Arguments{errors.New("mkdir error")}
			return
		}
		mkdirCall.ReturnArguments = mock.Arguments{os.MkdirAll(args.String(0), args.Get(1).(os.FileMode))}
	}

	err := s.app.dumpPlaygroundExamples(dir)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating playground example")
}

func (s *MainSuite) TestOnboardGlobalShortcutsDumpError() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return(tmpDir, nil)
	s.app.shortcutsFS = brokenShortcutsReadDirFS{}

	err := s.app.onboardGlobal(false, "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading embedded shortcuts")
}

func (s *MainSuite) TestOnboardGlobalPlaygroundDumpError() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return(tmpDir, nil)

	// Let all prior writes succeed, then fail MkdirAll for the playground example subdirs.
	mkdirCalls := 0
	mkdirCall := sys.Override("MkdirAll", mock.Anything, mock.Anything).Maybe().Return(nil)
	mkdirCall.RunFn = func(args mock.Arguments) {
		mkdirCalls++
		path := args.String(0)
		// The playground base dir is created first, then the first example subdir.
		// Fail on the first example subdir (contains "/playground/" and a subdir name).
		if mkdirCalls > 1 && filepath.Dir(path) == filepath.Join(tmpDir, ".loop", "playground") {
			mkdirCall.ReturnArguments = mock.Arguments{errors.New("playground mkdir error")}
			return
		}
		mkdirCall.ReturnArguments = mock.Arguments{os.MkdirAll(path, args.Get(1).(os.FileMode))}
	}

	err := s.app.onboardGlobal(false, "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating playground example")
}

func (s *MainSuite) TestOnboardGlobalPlaygroundSkipIfExist() {
	tmpDir := s.T().TempDir()
	playgroundDir := filepath.Join(tmpDir, ".loop", "playground", "pong")
	require.NoError(s.T(), os.MkdirAll(playgroundDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(playgroundDir, "index.html"), []byte("custom pong"), 0644))

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return(tmpDir, nil)

	err := s.app.onboardGlobal(true, "")
	require.NoError(s.T(), err)

	// Custom pong should be preserved.
	data, err := os.ReadFile(filepath.Join(playgroundDir, "index.html"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), "custom pong", string(data))
}

// brokenReadDirFS implements fs.ReadFileFS but fails on ReadDir.
type brokenReadDirFS struct{}

func (brokenReadDirFS) Open(string) (fs.File, error)    { return nil, errors.New("broken") }
func (brokenReadDirFS) ReadFile(string) ([]byte, error) { return nil, errors.New("broken") }

// brokenReadFileFS succeeds on ReadDir (returns one fake entry) but fails on ReadFile.
type brokenReadFileFS struct{ brokenReadDirFS }

func (brokenReadFileFS) Open(name string) (fs.File, error) {
	// fs.ReadDir calls Open; return a dir with one fake file entry.
	if name == "templates" {
		return &fakeDirFile{entries: []fs.DirEntry{&fakeEntry{name: "test.md"}}}, nil
	}
	return nil, errors.New("broken")
}

type fakeDirFile struct {
	entries []fs.DirEntry
	read    bool
}

func (f *fakeDirFile) Stat() (fs.FileInfo, error) { return nil, nil }
func (f *fakeDirFile) Read([]byte) (int, error)   { return 0, io.EOF }
func (f *fakeDirFile) Close() error               { return nil }
func (f *fakeDirFile) ReadDir(int) ([]fs.DirEntry, error) {
	if f.read {
		return nil, io.EOF
	}
	f.read = true
	return f.entries, nil
}

type fakeEntry struct{ name string }

func (e *fakeEntry) Name() string               { return e.name }
func (e *fakeEntry) IsDir() bool                { return false }
func (e *fakeEntry) Type() fs.FileMode          { return 0 }
func (e *fakeEntry) Info() (fs.FileInfo, error) { return nil, nil }

func (s *MainSuite) TestDumpTemplatesReadDirError() {
	s.app.templatesFS = brokenReadDirFS{}

	err := s.app.dumpTemplates(s.T().TempDir())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading embedded templates")
}

func (s *MainSuite) TestDumpTemplatesReadFileError() {
	s.app.templatesFS = brokenReadFileFS{}

	err := s.app.dumpTemplates(s.T().TempDir())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading embedded template test.md")
}

func (s *MainSuite) TestOnboardGlobalWithOwnerID() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return(tmpDir, nil)

	err := s.app.onboardGlobal(false, "U99887766")
	require.NoError(s.T(), err)

	configPath := filepath.Join(tmpDir, ".loop", "config.json")
	data, err := os.ReadFile(configPath)
	require.NoError(s.T(), err)

	content := string(data)
	// Verify the permissions block is uncommented with the real owner ID
	require.Contains(s.T(), content, `"permissions": {`)
	require.Contains(s.T(), content, `"U99887766"`)
	require.NotContains(s.T(), content, `//  "owners"`)
	require.NotContains(s.T(), content, `U12345678`)
}

func (s *MainSuite) TestOnboardGlobalCmdWithOwnerIDFlag() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return(tmpDir, nil)

	cmd := s.app.newOnboardGlobalCmd()
	cmd.SetArgs([]string{"--owner-id", "UTEST12345"})
	err := cmd.Execute()
	require.NoError(s.T(), err)

	configPath := filepath.Join(tmpDir, ".loop", "config.json")
	data, err := os.ReadFile(configPath)
	require.NoError(s.T(), err)

	content := string(data)
	require.Contains(s.T(), content, `"UTEST12345"`)
	require.Contains(s.T(), content, `"permissions": {`)
}
