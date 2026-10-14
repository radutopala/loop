package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/daemon"
)

// restoreFixture is a Loop dir with a database and a backup to restore.
type restoreFixture struct {
	loopDir  string
	dbPath   string
	file     string
	identity *age.X25519Identity
	calls    []string
}

// newRestoreFixture writes a backup holding a database and a global
// playground, encrypted when encrypt is set, and points s.app at a Loop dir
// whose daemon status is status.
func (s *MainSuite) newRestoreFixture(encrypt bool, status string) *restoreFixture {
	f := &restoreFixture{loopDir: filepath.Join(s.T().TempDir(), ".loop")}
	f.dbPath = filepath.Join(f.loopDir, "loop.db")
	require.NoError(s.T(), os.MkdirAll(f.loopDir, 0o700))
	require.NoError(s.T(), os.WriteFile(f.dbPath, []byte("current"), 0o600))

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range map[string]string{
		"loop.db":                            "SQLite format 3\x00 restored",
		"manifest.json":                      `{"created_at":"2026-10-14T09:00:00Z","version":"v2026.10.1","playgrounds":[{"folder":"playground/global","global":true}]}`,
		"playground/global/snake/index.html": "snake",
	} {
		w, err := zw.Create(name)
		require.NoError(s.T(), err)
		_, err = w.Write([]byte(content))
		require.NoError(s.T(), err)
	}
	require.NoError(s.T(), zw.Close())
	data := buf.Bytes()
	f.file = filepath.Join(s.T().TempDir(), "loop-20261014T090000Z.zip")
	id, err := age.GenerateX25519Identity()
	require.NoError(s.T(), err)
	f.identity = id
	if encrypt {
		var out bytes.Buffer
		w, err := age.Encrypt(&out, id.Recipient())
		require.NoError(s.T(), err)
		_, err = w.Write(data)
		require.NoError(s.T(), err)
		require.NoError(s.T(), w.Close())
		data = out.Bytes()
		f.file += ".age"
	}
	require.NoError(s.T(), os.WriteFile(f.file, data, 0o600))

	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{DBPath: f.dbPath, LoopDir: f.loopDir, LogFile: "loop.log"}, nil
	}
	s.app.newSystem = func() daemon.System { return daemon.RealSystem{} }
	s.app.daemonStatus = func(daemon.System) (string, error) { return status, nil }
	s.app.daemonStop = func(daemon.System) error {
		f.calls = append(f.calls, "stop")
		return nil
	}
	s.app.daemonStart = func(_ daemon.System, logFile string) error {
		f.calls = append(f.calls, "start "+logFile)
		return nil
	}
	return f
}

func (s *MainSuite) runRestore(stdin string, args ...string) (string, string, error) {
	cmd := s.app.newBackupRestoreCmd()
	var out, errOut bytes.Buffer
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func (s *MainSuite) TestBackupRestore() {
	s.Run("restarts a running daemon", func() {
		f := s.newRestoreFixture(false, "running")
		out, _, err := s.runRestore("", f.file)
		require.NoError(s.T(), err)
		require.Equal(s.T(), []string{"stop", "start loop.log"}, f.calls)
		data, err := os.ReadFile(f.dbPath)
		require.NoError(s.T(), err)
		require.Equal(s.T(), "SQLite format 3\x00 restored", string(data))
		require.Contains(s.T(), out, "(Loop v2026.10.1).")
		require.Contains(s.T(), out, "Moved the replaced database to "+f.dbPath+".before-restore-")
		require.Contains(s.T(), out, "Restored the playground "+filepath.Join(f.loopDir, "playground", "snake"))
		require.NotContains(s.T(), out, "wasn't running")

		// Restoring it again keeps the playground that's now there.
		out, _, err = s.runRestore("", f.file)
		require.NoError(s.T(), err)
		require.Contains(s.T(), out, "Kept the existing playground "+filepath.Join(f.loopDir, "playground", "snake"))
	})
	s.Run("leaves a stopped daemon stopped", func() {
		f := s.newRestoreFixture(false, "not installed")
		out, _, err := s.runRestore("", f.file)
		require.NoError(s.T(), err)
		require.Empty(s.T(), f.calls)
		require.Contains(s.T(), out, "The daemon wasn't running; start it with loop daemon:start.")
	})
	s.Run("pasted key", func() {
		f := s.newRestoreFixture(true, "running")
		_, errOut, err := s.runRestore(f.identity.String()+"\n", f.file)
		require.NoError(s.T(), err)
		require.Contains(s.T(), errOut, "Paste its private key")
		require.Equal(s.T(), []string{"stop", "start loop.log"}, f.calls)
	})
	s.Run("key file", func() {
		f := s.newRestoreFixture(true, "running")
		keyFile := filepath.Join(s.T().TempDir(), "key.txt")
		require.NoError(s.T(), os.WriteFile(keyFile, []byte("# created: 2026-10-14\n# public key: "+f.identity.Recipient().String()+"\n"+f.identity.String()+"\n"), 0o600))
		_, errOut, err := s.runRestore("", "--identity", keyFile, f.file)
		require.NoError(s.T(), err)
		require.Empty(s.T(), errOut)
	})
	s.Run("missing key file", func() {
		f := s.newRestoreFixture(true, "running")
		_, _, err := s.runRestore("", "-i", filepath.Join(s.T().TempDir(), "nope"), f.file)
		require.ErrorContains(s.T(), err, "reading the private key: ")
		require.Empty(s.T(), f.calls)
	})
	s.Run("not a key", func() {
		f := s.newRestoreFixture(true, "running")
		_, _, err := s.runRestore("hello\n", f.file)
		require.ErrorContains(s.T(), err, "reading the private key: ")
		require.Empty(s.T(), f.calls)
	})
	s.Run("config error", func() {
		s.app.configLoad = func() (*config.Config, error) { return nil, errors.New("config fail") }
		_, _, err := s.runRestore("", "backup.zip")
		require.EqualError(s.T(), err, "config fail")
	})
	s.Run("bad backup", func() {
		f := s.newRestoreFixture(false, "running")
		_, _, err := s.runRestore("", f.file+".gone")
		require.ErrorContains(s.T(), err, "opening the backup")
		require.Empty(s.T(), f.calls)
	})
}
