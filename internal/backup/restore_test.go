package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/testutil"
)

type RestoreSuite struct {
	suite.Suite
	fs       *fakeFS
	restorer *Restorer
	home     string
	loopDir  string
	project  string
	dbPath   string
	file     string
	identity *age.X25519Identity
	calls    []string
	stopErr  error
	startErr error
}

func TestRestoreSuite(t *testing.T) {
	suite.Run(t, new(RestoreSuite))
}

func (s *RestoreSuite) SetupTest() {
	s.fs = &fakeFS{}
	s.restorer = NewRestorer()
	s.restorer.fs = s.fs
	s.restorer.now = func() time.Time { return time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC) }
	s.home = s.T().TempDir()
	s.loopDir = filepath.Join(s.home, ".loop")
	s.project = filepath.Join(s.home, "src", "app")
	s.dbPath = filepath.Join(s.loopDir, "loop.db")
	s.file = filepath.Join(s.T().TempDir(), "loop-20261014T090000Z.zip")
	id, err := age.GenerateX25519Identity()
	require.NoError(s.T(), err)
	s.identity = id
	s.calls = nil
	s.stopErr, s.startErr = nil, nil
	putFile(s.T(), s.dbPath, "the current database")
}

func (s *RestoreSuite) options() RestoreOptions {
	return RestoreOptions{
		File: s.file,
		Identity: func() ([]age.Identity, error) {
			s.calls = append(s.calls, "identity")
			return []age.Identity{s.identity}, nil
		},
		DBPath:  s.dbPath,
		LoopDir: s.loopDir,
		StopDaemon: func() error {
			s.calls = append(s.calls, "stop")
			return s.stopErr
		},
		StartDaemon: func() error {
			s.calls = append(s.calls, "start")
			return s.startErr
		},
	}
}

type entry struct {
	name, content string
}

// manifestOf is a manifest with the global playgrounds and s.project's.
func (s *RestoreSuite) manifestOf() string {
	data, err := json.Marshal(Manifest{
		Database: dbEntry,
		Playgrounds: []Playground{
			{Folder: globalPlaygrounds, Global: true},
			{Folder: projectPlaygrounds + "/app", Project: s.project},
		},
	})
	require.NoError(s.T(), err)
	return string(data)
}

// write writes a zip of entries to s.file, encrypted to s.identity when
// encrypt is set.
func (s *RestoreSuite) write(encrypt bool, entries ...entry) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e.name)
		require.NoError(s.T(), err)
		_, err = w.Write([]byte(e.content))
		require.NoError(s.T(), err)
	}
	require.NoError(s.T(), zw.Close())
	data := buf.Bytes()
	if encrypt {
		var out bytes.Buffer
		w, err := age.Encrypt(&out, s.identity.Recipient())
		require.NoError(s.T(), err)
		_, err = w.Write(data)
		require.NoError(s.T(), err)
		require.NoError(s.T(), w.Close())
		data = out.Bytes()
	}
	require.NoError(s.T(), os.WriteFile(s.file, data, 0o600))
}

// valid writes a backup with a database and a playground of each kind.
func (s *RestoreSuite) valid(encrypt bool) {
	s.write(encrypt,
		entry{dbEntry, sqliteHeader + " the backup's database"},
		entry{globalPlaygrounds + "/snake/index.html", "snake"},
		entry{globalPlaygrounds + "/snake/js/game.js", "play()"},
		entry{globalPlaygrounds + "/chess/index.html", "chess from the backup"},
		entry{projectPlaygrounds + "/app/chart/index.html", "chart"},
		entry{projectPlaygrounds + "/other/x/index.html", "not listed"},
		entry{manifestEntry, s.manifestOf()},
	)
}

func (s *RestoreSuite) read(path string) string {
	data, err := os.ReadFile(path)
	require.NoError(s.T(), err)
	return string(data)
}

func (s *RestoreSuite) TestRestores() {
	for _, encrypt := range []bool{false, true} {
		s.Run(map[bool]string{false: "plain", true: "encrypted"}[encrypt], func() {
			s.SetupTest()
			s.valid(encrypt)
			putFile(s.T(), s.dbPath+"-wal", "wal")
			putFile(s.T(), filepath.Join(s.loopDir, "playground", "chess", "index.html"), "chess now")

			res, err := s.restorer.Restore(s.options())
			require.NoError(s.T(), err)
			want := []string{"stop", "start"}
			if encrypt {
				want = []string{"identity", "stop", "start"}
			}
			require.Equal(s.T(), want, s.calls)

			require.Equal(s.T(), sqliteHeader+" the backup's database", s.read(s.dbPath))
			aside := s.dbPath + ".before-restore-20261014T100000Z"
			require.Equal(s.T(), []string{aside, aside + "-wal"}, res.MovedAside)
			require.Equal(s.T(), "the current database", s.read(aside))
			require.Equal(s.T(), "wal", s.read(aside+"-wal"))
			require.NoFileExists(s.T(), s.dbPath+"-wal")

			require.Equal(s.T(), []string{
				filepath.Join(s.loopDir, "playground", "snake"),
				filepath.Join(s.project, ".loop", "playground", "chart"),
			}, res.Restored)
			require.Equal(s.T(), []string{filepath.Join(s.loopDir, "playground", "chess")}, res.Kept)
			require.Equal(s.T(), "play()", s.read(filepath.Join(s.loopDir, "playground", "snake", "js", "game.js")))
			require.Equal(s.T(), "chart", s.read(filepath.Join(s.project, ".loop", "playground", "chart", "index.html")))
			require.Equal(s.T(), "chess now", s.read(filepath.Join(s.loopDir, "playground", "chess", "index.html")))

			// Nothing unpacked is left next to the database.
			entries, err := os.ReadDir(s.loopDir)
			require.NoError(s.T(), err)
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			require.ElementsMatch(s.T(), []string{"loop.db", filepath.Base(aside), filepath.Base(aside) + "-wal", "playground"}, names)
		})
	}
}

func (s *RestoreSuite) TestRestoresTwiceInASecond() {
	s.valid(false)
	aside := s.dbPath + ".before-restore-20261014T100000Z"
	// An earlier restore in this second moved a database and its WAL aside;
	// one before it left only a WAL under the next name.
	putFile(s.T(), aside, "first")
	putFile(s.T(), aside+"-2-wal", "second's wal")

	res, err := s.restorer.Restore(s.options())
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{aside + "-3"}, res.MovedAside)
	require.Equal(s.T(), "the current database", s.read(aside+"-3"))
	require.Equal(s.T(), "first", s.read(aside))
	require.Equal(s.T(), "second's wal", s.read(aside+"-2-wal"))
}

func (s *RestoreSuite) TestRestoresWhatRunnerWrites() {
	putFile(s.T(), filepath.Join(s.home, "src", "app", ".loop", "playground", "chart", "index.html"), "chart")
	store := new(testutil.MockStore)
	store.On("BackupInto", mock.Anything, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		putFile(s.T(), args.String(1), sqliteHeader+" snapshot")
	})
	dir := filepath.Join(s.home, "backups")
	runner := NewRunner(Deps{
		Store: store,
		Config: func() (*config.Config, error) {
			return &config.Config{Backup: config.BackupConfig{Path: dir, Recipient: s.identity.Recipient().String()}}, nil
		},
		LoopDir:  s.loopDir,
		WorkDir:  s.T().TempDir(),
		Projects: func(context.Context) []string { return []string{s.project} },
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	require.True(s.T(), runner.begin())
	sets, err := runner.settings()
	require.NoError(s.T(), err)
	b, err := runner.write(context.Background(), sets)
	require.NoError(s.T(), err)
	require.NoError(s.T(), os.RemoveAll(filepath.Join(s.project, ".loop")))

	s.file = b.Path
	res, err := NewRestorer().Restore(s.options())
	require.NoError(s.T(), err)
	require.Equal(s.T(), sqliteHeader+" snapshot", s.read(s.dbPath))
	require.Equal(s.T(), "chart", s.read(filepath.Join(s.project, ".loop", "playground", "chart", "index.html")))
	require.Equal(s.T(), b.At, res.Manifest.CreatedAt)
}

func (s *RestoreSuite) TestBadBackupsChangeNothing() {
	tests := []struct {
		name  string
		setup func()
		want  string
	}{
		{"missing", func() { s.file += ".gone" }, "opening the backup: open %FILE%: no such file or directory"},
		{"stat", func() {
			s.valid(false)
			s.fs.fail = failOp("Stat", "")
		}, "opening the backup: boom"},
		{"not a zip", func() { putFile(s.T(), s.file, "hello") }, "reading the backup: zip: not a valid zip file"},
		{"no key", func() {
			s.valid(true)
			s.identity = nil
		}, "no key"},
		{"wrong key", func() {
			s.valid(true)
			other, err := age.GenerateX25519Identity()
			require.NoError(s.T(), err)
			s.identity = other
		}, "decrypting the backup: identity did not match any of the recipients: incorrect identity for recipient block"},
		{"no manifest", func() {
			s.write(false, entry{dbEntry, sqliteHeader})
		}, "not a Loop backup: open manifest.json: file does not exist"},
		{"bad manifest", func() {
			s.write(false, entry{manifestEntry, "{"})
		}, "reading manifest.json: unexpected end of JSON input"},
		{"no database", func() {
			s.write(false, entry{manifestEntry, "{}"})
		}, "not a Loop backup: open loop.db: file does not exist"},
		{"not a database", func() {
			s.write(false, entry{manifestEntry, "{}"}, entry{dbEntry, "hello"})
		}, "the backup's loop.db isn't a SQLite database"},
		{"unpacking", func() {
			s.valid(false)
			s.fs.fail = failOp("CreateTemp", "")
		}, "unpacking the database: boom"},
		{"writing", func() {
			s.valid(false)
			s.fs.fail = failOp("write", "")
		}, "unpacking the database: boom"},
		{"stopping", func() {
			s.valid(false)
			s.stopErr = errors.New("launchctl failed")
		}, "stopping the daemon: launchctl failed"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			tt.setup()
			opts := s.options()
			if s.identity == nil {
				opts.Identity = func() ([]age.Identity, error) { return nil, errors.New("no key") }
			}
			_, err := s.restorer.Restore(opts)
			require.EqualError(s.T(), err, strings.ReplaceAll(tt.want, "%FILE%", s.file))
			require.NotContains(s.T(), s.calls, "start")
			require.Equal(s.T(), "the current database", s.read(s.dbPath))
			entries, err := os.ReadDir(s.loopDir)
			require.NoError(s.T(), err)
			require.Len(s.T(), entries, 1)
		})
	}
}

func (s *RestoreSuite) TestManifestFolders() {
	tests := []struct {
		name string
		p    Playground
	}{
		{"global with a project", Playground{Folder: globalPlaygrounds, Global: true, Project: "/src/app"}},
		{"relative project", Playground{Folder: projectPlaygrounds + "/app", Project: "src/app"}},
		{"unclean folder", Playground{Folder: projectPlaygrounds + "/app/../../x", Project: "/src/app"}},
		{"outside the playgrounds", Playground{Folder: "loop.db", Global: true}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			data, err := json.Marshal(Manifest{Playgrounds: []Playground{tt.p}})
			require.NoError(s.T(), err)
			s.write(false, entry{manifestEntry, string(data)}, entry{dbEntry, sqliteHeader})
			_, err = s.restorer.Restore(s.options())
			require.ErrorContains(s.T(), err, "manifest.json lists an unexpected playground folder")
			require.Empty(s.T(), s.calls)
		})
	}
}

func (s *RestoreSuite) TestSwapFailuresPutTheDatabaseBack() {
	failed := false
	tests := []struct {
		name string
		fail func(string, string) error
		want string
	}{
		{"moving the WAL aside", failOp("Rename", "-wal"), "moving the database aside: boom"},
		{"moving the database aside", failOp("Rename", "loop.db"), "moving the database aside: boom"},
		{"putting the backup's in place", func(op, path string) error {
			// Only the backup's move in fails, not the moves back.
			if op == "Rename" && filepath.Base(path) == "loop.db" && !failed {
				failed = true
				return errors.New("boom")
			}
			return nil
		}, "putting the backup's database in place: boom"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.valid(false)
			putFile(s.T(), s.dbPath+"-wal", "wal")
			s.fs.fail = tt.fail
			res, err := s.restorer.Restore(s.options())
			require.EqualError(s.T(), err, tt.want)
			require.Nil(s.T(), res)
			require.Equal(s.T(), []string{"stop", "start"}, s.calls)
			require.Equal(s.T(), "the current database", s.read(s.dbPath))
			require.Equal(s.T(), "wal", s.read(s.dbPath+"-wal"))
			entries, err := os.ReadDir(s.loopDir)
			require.NoError(s.T(), err)
			require.Len(s.T(), entries, 2)
		})
	}
}

func (s *RestoreSuite) TestPlaygroundFailures() {
	tests := []struct {
		name  string
		setup func()
		want  string
	}{
		{"mkdir", func() { s.fs.fail = failOp("MkdirAll", "snake") }, "restoring the playground %LOOP%/playground/snake: boom"},
		{"create", func() { s.fs.fail = failOp("Create", "snake") }, "restoring the playground %LOOP%/playground/snake: boom"},
		{"write", func() { s.fs.fail = failOp("write", "snake") }, "restoring the playground %LOOP%/playground/snake: boom"},
		{"unreadable entry", func() {
			var buf bytes.Buffer
			zw := zip.NewWriter(&buf)
			for _, e := range []entry{{dbEntry, sqliteHeader + " the backup's database"}, {manifestEntry, s.manifestOf()}} {
				w, err := zw.Create(e.name)
				require.NoError(s.T(), err)
				_, err = w.Write([]byte(e.content))
				require.NoError(s.T(), err)
			}
			_, err := zw.CreateRaw(&zip.FileHeader{Name: globalPlaygrounds + "/snake/index.html", Method: 99})
			require.NoError(s.T(), err)
			require.NoError(s.T(), zw.Close())
			require.NoError(s.T(), os.WriteFile(s.file, buf.Bytes(), 0o600))
		}, "restoring the playground %LOOP%/playground/snake: zip: unsupported compression algorithm"},
		{"starting the daemon", func() { s.startErr = errors.New("launchctl failed") }, "starting the daemon: launchctl failed"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.valid(false)
			tt.setup()
			_, err := s.restorer.Restore(s.options())
			require.EqualError(s.T(), err, strings.ReplaceAll(tt.want, "%LOOP%", s.loopDir))
			// The database is restored and the daemon started regardless.
			require.Equal(s.T(), sqliteHeader+" the backup's database", s.read(s.dbPath))
			require.Equal(s.T(), []string{"stop", "start"}, s.calls)
		})
	}
}

func (s *RestoreSuite) TestUnsafePath() {
	s.write(false,
		entry{dbEntry, sqliteHeader},
		entry{globalPlaygrounds + "/snake/../../../evil", "x"},
		entry{manifestEntry, s.manifestOf()},
	)
	_, err := s.restorer.Restore(s.options())
	require.EqualError(s.T(), err, `the backup has an unsafe path "playground/global/snake/../../../evil"`)
	require.NoFileExists(s.T(), filepath.Join(s.home, "evil"))
}

func (s *RestoreSuite) TestSkipsDirEntries() {
	s.write(false,
		entry{dbEntry, sqliteHeader},
		entry{globalPlaygrounds + "/", ""},
		entry{globalPlaygrounds + "/snake/", ""},
		entry{globalPlaygrounds + "/snake/index.html", "snake"},
		entry{manifestEntry, s.manifestOf()},
	)
	res, err := s.restorer.Restore(s.options())
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{filepath.Join(s.loopDir, "playground", "snake")}, res.Restored)
}
