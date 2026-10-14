package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/testutil"
)

// fakeFS does everything on disk, failing a step when fail returns an
// error for it: op is the method's name, or write, sync or close for a
// file's, and path the path it works on.
type fakeFS struct {
	osFS
	fail func(op, path string) error
	// failAfter fails file writes once that many bytes were written, when
	// it's set.
	failAfter int
	written   int
	entries   []fs.DirEntry
	// walkErr makes WalkDir report an unreadable entry and one whose Info
	// fails before walking.
	walkErr error
}

func (f *fakeFS) err(op, path string) error {
	if f.fail == nil {
		return nil
	}
	return f.fail(op, path)
}

func (f *fakeFS) MkdirAll(path string, perm fs.FileMode) error {
	if err := f.err("MkdirAll", path); err != nil {
		return err
	}
	return f.osFS.MkdirAll(path, perm)
}

func (f *fakeFS) CreateTemp(dir, pattern string) (writeFile, error) {
	if err := f.err("CreateTemp", dir); err != nil {
		return nil, err
	}
	file, err := f.osFS.CreateTemp(dir, pattern)
	if err != nil {
		return nil, err
	}
	return &fakeFile{writeFile: file, fs: f}, nil
}

func (f *fakeFS) Create(path string) (writeFile, error) {
	if err := f.err("Create", path); err != nil {
		return nil, err
	}
	file, err := f.osFS.Create(path)
	if err != nil {
		return nil, err
	}
	return &fakeFile{writeFile: file, fs: f}, nil
}

func (f *fakeFS) Open(path string) (io.ReadCloser, error) {
	if err := f.err("Open", path); err != nil {
		return nil, err
	}
	return f.osFS.Open(path)
}

func (f *fakeFS) OpenReaderAt(path string) (readerAtFile, error) {
	if err := f.err("OpenReaderAt", path); err != nil {
		return nil, err
	}
	file, err := f.osFS.OpenReaderAt(path)
	if err != nil {
		return nil, err
	}
	return &fakeReaderAt{readerAtFile: file, fs: f, path: path}, nil
}

type fakeReaderAt struct {
	readerAtFile
	fs   *fakeFS
	path string
}

func (f *fakeReaderAt) Stat() (fs.FileInfo, error) {
	if err := f.fs.err("Stat", f.path); err != nil {
		return nil, err
	}
	return f.readerAtFile.Stat()
}

func (f *fakeFS) Rename(from, to string) error {
	if err := f.err("Rename", to); err != nil {
		return err
	}
	return f.osFS.Rename(from, to)
}

func (f *fakeFS) Remove(path string) error {
	if err := f.err("Remove", path); err != nil {
		return err
	}
	return f.osFS.Remove(path)
}

func (f *fakeFS) ReadDir(path string) ([]fs.DirEntry, error) {
	if err := f.err("ReadDir", path); err != nil {
		return nil, err
	}
	if f.entries != nil {
		return f.entries, nil
	}
	return f.osFS.ReadDir(path)
}

func (f *fakeFS) WalkDir(root string, fn fs.WalkDirFunc) error {
	if f.walkErr != nil {
		if err := fn(filepath.Join(root, "unreadable"), nil, f.walkErr); err != nil {
			return err
		}
		if err := fn(filepath.Join(root, "gone"), fakeEntry{name: "gone"}, nil); err != nil {
			return err
		}
	}
	return f.osFS.WalkDir(root, fn)
}

type fakeFile struct {
	writeFile
	fs *fakeFS
}

func (f *fakeFile) Write(p []byte) (int, error) {
	if err := f.fs.err("write", f.Name()); err != nil {
		return 0, err
	}
	if f.fs.failAfter > 0 && f.fs.written+len(p) > f.fs.failAfter {
		return 0, errors.New("disk full")
	}
	f.fs.written += len(p)
	return f.writeFile.Write(p)
}

func (f *fakeFile) Sync() error {
	return errors.Join(f.writeFile.Sync(), f.fs.err("sync", f.Name()))
}

func (f *fakeFile) Close() error {
	return errors.Join(f.writeFile.Close(), f.fs.err("close", f.Name()))
}

// failOp fails op on paths containing part.
func failOp(op, part string) func(string, string) error {
	return func(o, path string) error {
		if o == op && strings.Contains(path, part) {
			return errors.New("boom")
		}
		return nil
	}
}

// fakeEntry is a dir entry whose Info fails.
type fakeEntry struct {
	name string
	typ  fs.FileMode
}

func (e fakeEntry) Name() string               { return e.name }
func (e fakeEntry) IsDir() bool                { return e.typ.IsDir() }
func (e fakeEntry) Type() fs.FileMode          { return e.typ }
func (e fakeEntry) Info() (fs.FileInfo, error) { return nil, errors.New("gone") }

type BackupSuite struct {
	suite.Suite
	store    *testutil.MockStore
	fs       *fakeFS
	cfg      config.BackupConfig
	cfgErr   error
	home     string
	loopDir  string
	work     string
	dir      string
	projects []string
	now      time.Time
	runner   *Runner
	snapped  []byte
	identity *age.X25519Identity
}

func TestBackupSuite(t *testing.T) {
	suite.Run(t, new(BackupSuite))
}

func (s *BackupSuite) SetupTest() {
	s.store = new(testutil.MockStore)
	s.fs = &fakeFS{}
	s.home = s.T().TempDir()
	s.loopDir = filepath.Join(s.home, ".loop")
	s.work = s.T().TempDir()
	s.dir = filepath.Join(s.home, "Dropbox", "loop-backups")
	s.cfg = config.BackupConfig{
		Enabled:     true,
		Path:        "~/Dropbox/loop-backups",
		TimesPerDay: 4,
		Retention:   config.BackupRetention{KeepLast: 2},
	}
	s.cfgErr = nil
	s.projects = nil
	s.now = time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	s.snapped = []byte(sqliteHeader + " the database")
	id, err := age.GenerateX25519Identity()
	require.NoError(s.T(), err)
	s.identity = id
	s.runner = NewRunner(Deps{
		Store: s.store,
		Config: func() (*config.Config, error) {
			return &config.Config{Backup: s.cfg}, s.cfgErr
		},
		LoopDir:  s.loopDir,
		WorkDir:  s.work,
		Home:     s.home,
		Version:  "v2026.10.1",
		Projects: func(context.Context) []string { return s.projects },
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	s.runner.now = func() time.Time { return s.now }
	s.runner.fs = s.fs
}

// snapshots makes the store write the snapshot file it's asked for.
func (s *BackupSuite) snapshots() {
	s.store.On("BackupInto", mock.Anything, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		require.NoError(s.T(), os.WriteFile(args.String(1), s.snapped, 0o600))
	})
}

func (s *BackupSuite) files() []string {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	require.NoError(s.T(), err)
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func putFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// unzip returns the files of the backup at path, decrypting it with
// s.identity when it's encrypted.
func (s *BackupSuite) unzip(path string) map[string]string {
	data, err := os.ReadFile(path)
	require.NoError(s.T(), err)
	if strings.HasSuffix(path, ageSuffix) {
		dec, err := age.Decrypt(bytes.NewReader(data), s.identity)
		require.NoError(s.T(), err)
		data, err = io.ReadAll(dec)
		require.NoError(s.T(), err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(s.T(), err)
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		require.NoError(s.T(), err)
		content, err := io.ReadAll(rc)
		require.NoError(s.T(), err)
		require.NoError(s.T(), rc.Close())
		out[f.Name] = string(content)
	}
	return out
}

func (s *BackupSuite) manifest(files map[string]string) Manifest {
	var m Manifest
	require.NoError(s.T(), json.Unmarshal([]byte(files[manifestEntry]), &m))
	return m
}

func (s *BackupSuite) TestScheduleWritesAndPrunes() {
	s.snapshots()
	ctx := context.Background()
	putFile(s.T(), filepath.Join(s.loopDir, "playground", "snake", "index.html"), "<h1>snake</h1>")
	putFile(s.T(), filepath.Join(s.loopDir, "playground", "snake", "js", "game.js"), "play()")
	// Two projects of the same name, and one without playgrounds.
	one := filepath.Join(s.home, "a", "app")
	two := filepath.Join(s.home, "b", "app")
	putFile(s.T(), filepath.Join(one, ".loop", "playground", "chart", "index.html"), "one")
	putFile(s.T(), filepath.Join(two, ".loop", "playground", "chart", "index.html"), "two")
	require.NoError(s.T(), os.Symlink(filepath.Join(one, "nowhere"), filepath.Join(two, ".loop", "playground", "chart", "link")))
	s.projects = []string{one, two, filepath.Join(s.home, "c", "tool")}

	// The first backup is due right away and creates the directory.
	s.runner.runIfDue(ctx)
	require.Equal(s.T(), []string{"loop-20261014T090000Z.zip"}, s.files())
	files := s.unzip(filepath.Join(s.dir, "loop-20261014T090000Z.zip"))
	require.Equal(s.T(), map[string]string{
		"loop.db":                                    string(s.snapped),
		"playground/global/snake/index.html":         "<h1>snake</h1>",
		"playground/global/snake/js/game.js":         "play()",
		"playground/projects/app/chart/index.html":   "one",
		"playground/projects/app-2/chart/index.html": "two",
		"manifest.json":                              files[manifestEntry],
	}, files)
	require.Equal(s.T(), Manifest{
		CreatedAt: s.now,
		Version:   "v2026.10.1",
		Database:  "loop.db",
		Playgrounds: []Playground{
			{Folder: "playground/global", Global: true},
			{Folder: "playground/projects/app", Project: one},
			{Folder: "playground/projects/app-2", Project: two},
		},
	}, s.manifest(files))
	// The snapshot is gone from the work dir.
	left, err := os.ReadDir(s.work)
	require.NoError(s.T(), err)
	require.Empty(s.T(), left)

	// Not again until a quarter of a day has passed.
	s.now = s.now.Add(6*time.Hour - time.Second)
	s.runner.runIfDue(ctx)
	require.Len(s.T(), s.files(), 1)

	// A partial file of a backup that didn't finish and a file that isn't a
	// backup sit next to them.
	putFile(s.T(), filepath.Join(s.dir, ".loop-123.partial"), "")
	putFile(s.T(), filepath.Join(s.dir, "notes.txt"), "")
	for range 2 {
		s.now = s.now.Add(6 * time.Hour)
		s.runner.runIfDue(ctx)
	}
	// Two kept, the oldest and the partial file dropped, the other file left.
	require.Equal(s.T(), []string{"loop-20261014T205959Z.zip", "loop-20261015T025959Z.zip", "notes.txt"}, s.files())

	st := s.runner.Status()
	require.True(s.T(), st.Enabled)
	require.Equal(s.T(), s.dir, st.Dir)
	require.Equal(s.T(), 2, st.Count)
	require.Equal(s.T(), "loop-20261015T025959Z.zip", st.Last.File)
	require.False(s.T(), st.Last.Encrypted)
	require.Positive(s.T(), st.Last.Size)
	require.Greater(s.T(), st.TotalSize, st.Last.Size)
	require.Equal(s.T(), s.now.Add(6*time.Hour), *st.NextAt)
	require.Empty(s.T(), st.Error)
	require.Empty(s.T(), st.Recipient)
}

func (s *BackupSuite) TestEncrypted() {
	s.snapshots()
	s.cfg.Recipient = s.identity.Recipient().String()
	s.runner.runIfDue(context.Background())
	require.Equal(s.T(), []string{"loop-20261014T090000Z.zip.age"}, s.files())
	files := s.unzip(filepath.Join(s.dir, "loop-20261014T090000Z.zip.age"))
	require.Equal(s.T(), string(s.snapped), files[dbEntry])
	require.Empty(s.T(), s.manifest(files).Playgrounds)

	st := s.runner.Status()
	require.True(s.T(), st.Last.Encrypted)
	require.Equal(s.T(), s.cfg.Recipient, st.Recipient)

	// A wrong key can't read it.
	other, err := age.GenerateX25519Identity()
	require.NoError(s.T(), err)
	data, err := os.ReadFile(st.Last.Path)
	require.NoError(s.T(), err)
	_, err = age.Decrypt(bytes.NewReader(data), other)
	require.Error(s.T(), err)
}

func (s *BackupSuite) TestInvalidRecipient() {
	s.cfg.Recipient = "age1nope"
	require.ErrorContains(s.T(), s.runner.Start(), "the backup public key isn't an age public key (age1...)")
	require.Contains(s.T(), s.runner.Status().Error, "isn't an age public key")
	s.runner.runIfDue(context.Background())
	s.store.AssertNotCalled(s.T(), "BackupInto", mock.Anything, mock.Anything)
}

func (s *BackupSuite) TestNeverOverwrites() {
	s.snapshots()
	s.Run("a backup of the same second", func() {
		putFile(s.T(), filepath.Join(s.dir, "loop-20261014T090000Z.zip.age"), "older")
		require.True(s.T(), s.runner.begin())
		sets, err := s.runner.settings()
		require.NoError(s.T(), err)
		_, err = s.runner.write(context.Background(), sets)
		require.EqualError(s.T(), err, "a backup for 2026-10-14T09:00:00Z already exists")
		require.NoError(s.T(), os.Remove(filepath.Join(s.dir, "loop-20261014T090000Z.zip.age")))
	})
	s.Run("something else of the name", func() {
		require.NoError(s.T(), os.MkdirAll(filepath.Join(s.dir, "loop-20261014T090000Z.zip"), 0o700))
		require.True(s.T(), s.runner.begin())
		sets, err := s.runner.settings()
		require.NoError(s.T(), err)
		_, err = s.runner.write(context.Background(), sets)
		require.EqualError(s.T(), err, "writing the backup: loop-20261014T090000Z.zip already exists")
		require.Equal(s.T(), []string{"loop-20261014T090000Z.zip"}, s.files())
	})
}

func (s *BackupSuite) TestScheduleSkips() {
	tests := []struct {
		name  string
		setup func()
	}{
		{"disabled", func() { s.cfg.Enabled = false }},
		{"no path", func() { s.cfg.Path = "" }},
		{"relative path", func() { s.cfg.Path = "backups" }},
		{"config error", func() { s.cfgErr = errors.New("bad config") }},
		{"already running", func() { s.runner.running = true }},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			tt.setup()
			s.runner.runIfDue(context.Background())
			s.store.AssertNotCalled(s.T(), "BackupInto", mock.Anything, mock.Anything)
		})
	}
}

func (s *BackupSuite) TestFailureWaitsBeforeRetrying() {
	s.store.On("BackupInto", mock.Anything, mock.Anything).Return(errors.New("disk full")).Once()
	s.runner.runIfDue(context.Background())
	st := s.runner.Status()
	require.Equal(s.T(), "snapshotting the database: disk full", st.Error)
	require.Equal(s.T(), s.now, *st.ErrorAt)
	require.Equal(s.T(), s.now.Add(retryAfter), *st.NextAt)
	require.False(s.T(), st.Running)

	s.now = s.now.Add(retryAfter - time.Second)
	s.runner.runIfDue(context.Background())
	s.store.AssertNumberOfCalls(s.T(), "BackupInto", 1)

	// A success clears the error.
	s.snapshots()
	s.now = s.now.Add(time.Second)
	s.runner.runIfDue(context.Background())
	st = s.runner.Status()
	require.Empty(s.T(), st.Error)
	require.Nil(s.T(), st.ErrorAt)
	require.Equal(s.T(), 1, st.Count)
}

func (s *BackupSuite) TestWriteFailures() {
	random := make([]byte, 256<<10)
	_, err := rand.Read(random)
	require.NoError(s.T(), err)
	playground := func() {
		putFile(s.T(), filepath.Join(s.loopDir, "playground", "noise", "data.bin"), string(random))
	}
	tests := []struct {
		name  string
		setup func()
		want  string
	}{
		{"mkdir", func() { s.fs.fail = failOp("MkdirAll", "") }, "creating %DIR%: boom"},
		{"open", func() { s.fs.fail = failOp("Open", ".backup-") }, "writing the backup: adding the database: boom"},
		{"create", func() { s.fs.fail = failOp("CreateTemp", "") }, "creating the backup: boom"},
		{"write", func() { s.fs.fail = failOp("write", "") }, "writing the backup: boom"},
		{"write the database", func() {
			s.snapped = append([]byte(sqliteHeader), random...)
			s.fs.failAfter = 64 << 10
		}, "writing the backup: adding the database: disk full"},
		{"write a playground", func() {
			playground()
			s.fs.failAfter = 64 << 10
		}, "writing the backup: adding %LOOP%/playground: disk full"},
		{"sync", func() { s.fs.fail = failOp("sync", "") }, "writing the backup: boom"},
		{"close", func() { s.fs.fail = failOp("close", "") }, "writing the backup: boom"},
		{"rename", func() { s.fs.fail = failOp("Rename", "") }, "writing the backup: boom"},
		{"encrypt", func() {
			s.cfg.Recipient = s.identity.Recipient().String()
			s.runner.encrypt = func(io.Writer, ...age.Recipient) (io.WriteCloser, error) {
				return nil, errors.New("no randomness")
			}
		}, "writing the backup: encrypting: no randomness"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			tt.setup()
			s.snapshots()
			require.True(s.T(), s.runner.begin())
			sets, err := s.runner.settings()
			require.NoError(s.T(), err)
			_, err = s.runner.write(context.Background(), sets)
			want := strings.ReplaceAll(tt.want, "%DIR%", s.dir)
			require.EqualError(s.T(), err, strings.ReplaceAll(want, "%LOOP%", s.loopDir))
			// Nothing but the directory is left behind.
			require.Empty(s.T(), s.files())
		})
	}
}

func (s *BackupSuite) TestUnreadablePlaygroundFilesAreLeftOut() {
	s.snapshots()
	putFile(s.T(), filepath.Join(s.loopDir, "playground", "snake", "index.html"), "<h1>snake</h1>")
	putFile(s.T(), filepath.Join(s.loopDir, "playground", "snake", "secret.txt"), "locked")
	s.fs.fail = failOp("Open", "secret.txt")
	s.fs.walkErr = errors.New("permission denied")
	s.runner.runIfDue(context.Background())
	files := s.unzip(filepath.Join(s.dir, "loop-20261014T090000Z.zip"))
	require.Contains(s.T(), files, "playground/global/snake/index.html")
	require.NotContains(s.T(), files, "playground/global/snake/secret.txt")
	require.Empty(s.T(), s.runner.Status().Error)
}

func (s *BackupSuite) TestPruneFailuresAreLogged() {
	s.snapshots()
	s.Run("listing", func() {
		s.fs.fail = failOp("ReadDir", "")
		s.runner.runIfDue(context.Background())
		s.fs.fail = nil
		require.Len(s.T(), s.files(), 1)
		require.Empty(s.T(), s.runner.Status().Error)
	})
	s.Run("removing", func() {
		s.cfg.Retention.KeepLast = 1
		s.now = s.now.Add(24 * time.Hour)
		putFile(s.T(), filepath.Join(s.dir, ".loop-1.partial"), "")
		s.fs.fail = failOp("Remove", s.dir)
		s.runner.runIfDue(context.Background())
		s.fs.fail = nil
		require.Len(s.T(), s.files(), 3)
		require.Empty(s.T(), s.runner.Status().Error)
	})
}

func (s *BackupSuite) TestStart() {
	s.Run("no path", func() {
		s.SetupTest()
		s.cfg.Path = ""
		require.ErrorIs(s.T(), s.runner.Start(), ErrNoPath)
	})
	s.Run("config error", func() {
		s.SetupTest()
		s.cfgErr = errors.New("bad config")
		require.EqualError(s.T(), s.runner.Start(), "loading config: bad config")
	})
	s.Run("already running", func() {
		s.SetupTest()
		s.runner.running = true
		require.ErrorIs(s.T(), s.runner.Start(), ErrRunning)
	})
	s.Run("writes even when not scheduled", func() {
		s.SetupTest()
		s.cfg.Enabled = false
		s.snapshots()
		require.NoError(s.T(), s.runner.Start())
		require.Eventually(s.T(), func() bool { return !s.runner.Status().Running }, 5*time.Second, 10*time.Millisecond)
		st := s.runner.Status()
		require.Equal(s.T(), 1, st.Count)
		require.Nil(s.T(), st.NextAt)
	})
	s.Run("failure", func() {
		s.SetupTest()
		s.store.On("BackupInto", mock.Anything, mock.Anything).Return(errors.New("disk full"))
		require.NoError(s.T(), s.runner.Start())
		require.Eventually(s.T(), func() bool { return s.runner.Status().Error != "" }, 5*time.Second, 10*time.Millisecond)
	})
}

func (s *BackupSuite) TestStatus() {
	s.Run("no directory yet", func() {
		s.SetupTest()
		s.cfg.Retention = config.BackupRetention{KeepLast: 3, KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 6}
		st := s.runner.Status()
		require.Empty(s.T(), st.Error)
		require.Zero(s.T(), st.Count)
		require.Zero(s.T(), st.TotalSize)
		require.Nil(s.T(), st.Last)
		// The first backup is due now.
		require.Equal(s.T(), s.now, *st.NextAt)
		require.Equal(s.T(), 4, st.TimesPerDay)
		require.Equal(s.T(), []int{3, 7, 4, 6}, []int{st.KeepLast, st.KeepDaily, st.KeepWeekly, st.KeepMonthly})
	})
	s.Run("listing fails", func() {
		s.SetupTest()
		s.fs.fail = failOp("ReadDir", "")
		require.Equal(s.T(), "boom", s.runner.Status().Error)
	})
	s.Run("relative path", func() {
		s.SetupTest()
		s.cfg.Path = "backups"
		st := s.runner.Status()
		require.Equal(s.T(), `the backup path "backups" isn't absolute`, st.Error)
		require.Nil(s.T(), st.NextAt)
	})
	s.Run("absolute path", func() {
		s.SetupTest()
		s.cfg.Path = s.dir + "/"
		require.Equal(s.T(), s.dir, s.runner.Status().Dir)
	})
	s.Run("files that aren't backups", func() {
		s.SetupTest()
		s.fs.entries = []fs.DirEntry{
			fakeEntry{name: "loop-20261014T090000Z.zip"},
			fakeEntry{name: "loop-20261014T080000Z.zip.age"},
			fakeEntry{name: "loop-20261013T090000Z.zip", typ: fs.ModeDir},
			fakeEntry{name: "loop-yesterday.zip"},
			fakeEntry{name: "loop-20261014T090000Z.db.gz"},
			fakeEntry{name: "20261014T090000Z.zip"},
		}
		st := s.runner.Status()
		require.Equal(s.T(), 2, st.Count)
		require.Zero(s.T(), st.Last.Size)
		require.False(s.T(), st.Last.Encrypted)
	})
}

func (s *BackupSuite) TestRetain() {
	day := func(d, h int) time.Time { return time.Date(2026, 10, d, h, 0, 0, 0, time.UTC) }
	// Two backups a day from Aug 3 to Oct 14, newest first.
	var backups []Backup
	for at := day(14, 18); !at.Before(time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)); at = at.Add(-12 * time.Hour) {
		backups = append(backups, Backup{File: at.Format(timeLayout), At: at})
	}
	kept := func(r config.BackupRetention) []string {
		keep := retain(backups, r)
		var out []string
		for _, b := range backups {
			if keep[b.File] {
				out = append(out, b.File)
			}
		}
		return out
	}
	tests := []struct {
		name string
		r    config.BackupRetention
		want []string
	}{
		{"nothing kept but the newest", config.BackupRetention{}, []string{"20261014T180000Z"}},
		{"last", config.BackupRetention{KeepLast: 3}, []string{"20261014T180000Z", "20261014T060000Z", "20261013T180000Z"}},
		{"daily", config.BackupRetention{KeepDaily: 3}, []string{"20261014T180000Z", "20261013T180000Z", "20261012T180000Z"}},
		// Oct 14 is in ISO week 42, which starts on Monday Oct 12.
		{"weekly", config.BackupRetention{KeepWeekly: 3}, []string{"20261014T180000Z", "20261011T180000Z", "20261004T180000Z"}},
		{"monthly", config.BackupRetention{KeepMonthly: 6}, []string{"20261014T180000Z", "20260930T180000Z", "20260831T180000Z"}},
		{"tiers overlap", config.BackupRetention{KeepLast: 2, KeepDaily: 2, KeepWeekly: 2, KeepMonthly: 2}, []string{
			"20261014T180000Z", "20261014T060000Z", "20261013T180000Z", "20261011T180000Z", "20260930T180000Z",
		}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			require.Equal(s.T(), tt.want, kept(tt.r))
		})
	}
	require.Empty(s.T(), retain(nil, config.BackupRetention{KeepLast: 1}))
}

func (s *BackupSuite) TestDefaultRetentionBound() {
	// A backup an hour for a year leaves at most the sum of the tiers.
	var backups []Backup
	at := time.Date(2026, 10, 14, 23, 0, 0, 0, time.UTC)
	for range 24 * 365 {
		backups = append(backups, Backup{File: at.Format(timeLayout), At: at})
		at = at.Add(-time.Hour)
	}
	keep := retain(backups, config.BackupRetention{
		KeepLast:    config.DefaultBackupKeepLast,
		KeepDaily:   config.DefaultBackupKeepDaily,
		KeepWeekly:  config.DefaultBackupKeepWeekly,
		KeepMonthly: config.DefaultBackupKeepMonthly,
	})
	require.LessOrEqual(s.T(), len(keep), 3+7+4+6)
	// The oldest is the newest of the sixth month back.
	oldest := ""
	for _, b := range backups {
		if keep[b.File] {
			oldest = b.File
		}
	}
	require.Equal(s.T(), "20260531T230000Z", oldest)
}

func (s *BackupSuite) TestRun() {
	s.snapshots()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { s.runner.Run(ctx, time.Millisecond) })
	require.Eventually(s.T(), func() bool { return s.runner.Status().Count == 1 }, 5*time.Second, time.Millisecond)
	// Ticks go on while nothing is due.
	time.Sleep(5 * time.Millisecond)
	cancel()
	wg.Wait()
	require.Len(s.T(), s.files(), 1)
}

func (s *BackupSuite) TestOSFS() {
	var f osFS
	dir := filepath.Join(s.T().TempDir(), "a")
	require.NoError(s.T(), f.MkdirAll(dir, 0o700))
	w, err := f.CreateTemp(dir, "x-*")
	require.NoError(s.T(), err)
	_, err = w.Write([]byte("hi"))
	require.NoError(s.T(), err)
	require.NoError(s.T(), w.Sync())
	require.NoError(s.T(), w.Close())
	require.NoError(s.T(), f.Rename(w.Name(), filepath.Join(dir, "y")))
	r, err := f.Open(filepath.Join(dir, "y"))
	require.NoError(s.T(), err)
	require.NoError(s.T(), r.Close())
	ra, err := f.OpenReaderAt(filepath.Join(dir, "y"))
	require.NoError(s.T(), err)
	require.NoError(s.T(), ra.Close())
	info, err := f.Lstat(filepath.Join(dir, "y"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), int64(2), info.Size())
	c, err := f.Create(filepath.Join(dir, "z"))
	require.NoError(s.T(), err)
	require.NoError(s.T(), c.Close())
	_, err = f.Create(filepath.Join(dir, "z"))
	require.ErrorIs(s.T(), err, fs.ErrExist)
	entries, err := f.ReadDir(dir)
	require.NoError(s.T(), err)
	require.Len(s.T(), entries, 2)
	var walked []string
	require.NoError(s.T(), f.WalkDir(dir, func(path string, _ fs.DirEntry, _ error) error {
		walked = append(walked, filepath.Base(path))
		return nil
	}))
	require.Equal(s.T(), []string{"a", "y", "z"}, walked)
	require.NoError(s.T(), f.Remove(filepath.Join(dir, "y")))
}
