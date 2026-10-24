package fsmigrate

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/osutil"
)

// A project's .loop is the agent's to write. A migration of the project
// configs reaches each through its project dir: a config.json, or the temp
// file written beside it, that a symlink leads out of the project is
// skipped, and the file it points to is left as it was.
func (s *FSMigrateSuite) TestProjectConfigMigrationsStayInTheProject() {
	const before = `{"claude_dangerously_load_development_channels": true}`
	host := s.T().TempDir()
	hostFile := filepath.Join(host, "file")
	require.NoError(s.T(), os.WriteFile(hostFile, []byte(before), 0o644))

	loopDir := filepath.Join(s.T().TempDir(), ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0o755))
	project := func(write func(loop string)) string {
		dir := s.T().TempDir()
		require.NoError(s.T(), os.Mkdir(filepath.Join(dir, ".loop"), 0o755))
		write(filepath.Join(dir, ".loop"))
		return dir
	}
	plain := project(func(loop string) {
		require.NoError(s.T(), os.WriteFile(filepath.Join(loop, "config.json"), []byte(before), 0o644))
	})
	linkedConfig := project(func(loop string) {
		require.NoError(s.T(), os.Symlink(hostFile, filepath.Join(loop, "config.json")))
	})
	linkedTmp := project(func(loop string) {
		require.NoError(s.T(), os.WriteFile(filepath.Join(loop, "config.json"), []byte(before), 0o644))
		require.NoError(s.T(), os.Symlink(hostFile, filepath.Join(loop, "config.json.tmp")))
	})

	c := &Ctx{
		Sys:         osutil.RealSystem{},
		LoopDir:     loopDir,
		ProjectDirs: []string{linkedConfig, linkedTmp, plain},
		ProjectSys:  func(dir string) System { return osutil.RootFS{Root: dir} },
	}
	require.NoError(s.T(), dropDevChannelsKey(context.Background(), c))

	read := func(path string) string {
		data, err := os.ReadFile(path)
		require.NoError(s.T(), err)
		return string(data)
	}
	require.Equal(s.T(), "{}", read(filepath.Join(plain, ".loop", "config.json")))
	require.Equal(s.T(), before, read(filepath.Join(linkedTmp, ".loop", "config.json")))
	require.Equal(s.T(), before, read(hostFile))
}

// Other errors from a project's config still stop the migration.
func (s *FSMigrateSuite) TestProjectConfigMigrationErrors() {
	boom := errors.New("boom")
	sys := newFakeSystem()
	sys.files["/work/project/.loop/config.json"] = []byte(`{"claude_dangerously_load_development_channels": true}`)
	c := &Ctx{
		Sys:         sys,
		LoopDir:     "/loop",
		ProjectDirs: []string{"/work/project"},
		ProjectSys:  func(string) System { return failingWrites{sys, boom} },
	}
	require.ErrorIs(s.T(), dropDevChannelsKey(context.Background(), c), boom)
}

type failingWrites struct {
	System
	err error
}

func (f failingWrites) WriteFile(string, []byte, os.FileMode) error { return f.err }
