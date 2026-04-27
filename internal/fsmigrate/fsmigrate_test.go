package fsmigrate

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	containerimage "github.com/radutopala/loop/internal/container/image"
)

type FSMigrateSuite struct {
	suite.Suite
}

func TestFSMigrateSuite(t *testing.T) {
	suite.Run(t, new(FSMigrateSuite))
}

// fakeSystem is a minimal in-memory System for tests. Calls are recorded;
// individual operations may be configured to return errors.
type fakeSystem struct {
	files       map[string][]byte
	dirs        map[string]bool
	mkdirErr    error
	writeErr    map[string]error
	statErr     map[string]error
	readErr     map[string]error
	removeErr   error
	statMissing map[string]bool
}

func newFakeSystem() *fakeSystem {
	return &fakeSystem{
		files:       map[string][]byte{},
		dirs:        map[string]bool{},
		writeErr:    map[string]error{},
		statErr:     map[string]error{},
		readErr:     map[string]error{},
		statMissing: map[string]bool{},
	}
}

func (f *fakeSystem) Stat(name string) (os.FileInfo, error) {
	if err, ok := f.statErr[name]; ok {
		return nil, err
	}
	if f.statMissing[name] {
		return nil, os.ErrNotExist
	}
	if _, ok := f.files[name]; ok {
		return nil, nil //nolint:nilnil // tests do not inspect the FileInfo
	}
	if f.dirs[name] {
		return nil, nil //nolint:nilnil
	}
	return nil, os.ErrNotExist
}

func (f *fakeSystem) MkdirAll(path string, _ os.FileMode) error {
	if f.mkdirErr != nil {
		return f.mkdirErr
	}
	f.dirs[path] = true
	return nil
}

func (f *fakeSystem) WriteFile(name string, data []byte, _ os.FileMode) error {
	if err, ok := f.writeErr[name]; ok {
		return err
	}
	f.files[name] = data
	return nil
}

func (f *fakeSystem) ReadFile(name string) ([]byte, error) {
	if err, ok := f.readErr[name]; ok {
		return nil, err
	}
	if data, ok := f.files[name]; ok {
		return data, nil
	}
	return nil, os.ErrNotExist
}

func (f *fakeSystem) Remove(name string) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	delete(f.files, name)
	return nil
}

// --- Run tests ---

func (s *FSMigrateSuite) TestRunAllNew() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS fs_migrations`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	for i := 1; i < len(migrations); i++ {
		mock.ExpectQuery(`SELECT COUNT`).
			WithArgs(i).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		mock.ExpectExec(`INSERT INTO fs_migrations`).
			WithArgs(i).
			WillReturnResult(sqlmock.NewResult(int64(i), 1))
	}

	sys := newFakeSystem()
	// Mark setup.sh as missing so refreshContainerFiles writes it.
	sys.statMissing[filepath.Join("/loop", "container", "setup.sh")] = true
	err = Run(context.Background(), db, &Ctx{Sys: sys, LoopDir: "/loop", Version: "v1"})
	require.NoError(s.T(), err)
	require.NoError(s.T(), mock.ExpectationsWereMet())

	// Verify the embedded files were written by migration 1.
	require.Equal(s.T(), containerimage.Dockerfile, sys.files[filepath.Join("/loop", "container", "Dockerfile")])
	require.Equal(s.T(), containerimage.AgentBashrc, sys.files[filepath.Join("/loop", "container", "agent-bashrc")])
	require.Equal(s.T(), containerimage.Setup, sys.files[filepath.Join("/loop", "container", "setup.sh")])
}

func (s *FSMigrateSuite) TestRunAlreadyApplied() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS fs_migrations`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	for i := 1; i < len(migrations); i++ {
		mock.ExpectQuery(`SELECT COUNT`).
			WithArgs(i).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	}

	sys := newFakeSystem()
	err = Run(context.Background(), db, &Ctx{Sys: sys, LoopDir: "/loop", Version: "v1"})
	require.NoError(s.T(), err)
	require.NoError(s.T(), mock.ExpectationsWereMet())
	// No Apply should have run, so no files written.
	require.Empty(s.T(), sys.files)
	require.Empty(s.T(), sys.dirs)
}

func (s *FSMigrateSuite) TestRunBootstrapTableError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS fs_migrations`).
		WillReturnError(sql.ErrConnDone)

	err = Run(context.Background(), db, &Ctx{Sys: newFakeSystem(), LoopDir: "/loop"})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating fs_migrations table")
}

func (s *FSMigrateSuite) TestRunCheckVersionError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS fs_migrations`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT COUNT`).
		WithArgs(1).
		WillReturnError(sql.ErrConnDone)

	err = Run(context.Background(), db, &Ctx{Sys: newFakeSystem(), LoopDir: "/loop"})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "checking fs migration version")
}

func (s *FSMigrateSuite) TestRunApplyError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS fs_migrations`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT COUNT`).
		WithArgs(1).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	sys := newFakeSystem()
	sys.mkdirErr = errors.New("disk full")

	err = Run(context.Background(), db, &Ctx{Sys: sys, LoopDir: "/loop"})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "applying fs migration 1")
	require.Contains(s.T(), err.Error(), "disk full")
}

func (s *FSMigrateSuite) TestRunRecordError() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS fs_migrations`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT COUNT`).
		WithArgs(1).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec(`INSERT INTO fs_migrations`).
		WithArgs(1).
		WillReturnError(sql.ErrConnDone)

	sys := newFakeSystem()
	sys.statMissing[filepath.Join("/loop", "container", "setup.sh")] = true
	err = Run(context.Background(), db, &Ctx{Sys: sys, LoopDir: "/loop"})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "recording fs migration 1")
}

// --- refreshContainerFiles tests ---

func (s *FSMigrateSuite) TestRefreshContainerFilesWritesAll() {
	sys := newFakeSystem()
	sys.statMissing[filepath.Join("/loop", "container", "setup.sh")] = true

	err := refreshContainerFiles(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)

	want := []string{"Dockerfile", "entrypoint.sh", "agent-bashrc", "chrome.Dockerfile", "chrome-entrypoint.sh", "setup.sh"}
	for _, name := range want {
		require.Contains(s.T(), sys.files, filepath.Join("/loop", "container", name), "missing file: %s", name)
	}
	require.True(s.T(), sys.dirs[filepath.Join("/loop", "container")])
}

func (s *FSMigrateSuite) TestRefreshContainerFilesMkdirError() {
	sys := newFakeSystem()
	sys.mkdirErr = errors.New("permission denied")

	err := refreshContainerFiles(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating container directory")
}

func (s *FSMigrateSuite) TestRefreshContainerFilesWriteOverwriteError() {
	sys := newFakeSystem()
	target := filepath.Join("/loop", "container", "Dockerfile")
	sys.writeErr[target] = errors.New("io error")

	err := refreshContainerFiles(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing Dockerfile")
}

func (s *FSMigrateSuite) TestRefreshContainerFilesBacksUpUserEdits() {
	sys := newFakeSystem()
	dockerfilePath := filepath.Join("/loop", "container", "Dockerfile")
	bashrcPath := filepath.Join("/loop", "container", "agent-bashrc")
	// Dockerfile pre-existing with user edits — must be backed up.
	sys.files[dockerfilePath] = []byte("# user-modified Dockerfile")
	// agent-bashrc pre-existing, identical to embedded — no backup expected.
	sys.files[bashrcPath] = containerimage.AgentBashrc

	err := refreshContainerFiles(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)

	require.Equal(s.T(), []byte("# user-modified Dockerfile"), sys.files[dockerfilePath+".bkp"], "user Dockerfile should be backed up")
	require.Equal(s.T(), containerimage.Dockerfile, sys.files[dockerfilePath], "Dockerfile should be overwritten with embedded content")
	_, hasBashrcBkp := sys.files[bashrcPath+".bkp"]
	require.False(s.T(), hasBashrcBkp, "identical agent-bashrc should not produce a .bkp")
}

func (s *FSMigrateSuite) TestRefreshContainerFilesBackupWriteError() {
	sys := newFakeSystem()
	dockerfilePath := filepath.Join("/loop", "container", "Dockerfile")
	sys.files[dockerfilePath] = []byte("# user-modified Dockerfile")
	sys.writeErr[dockerfilePath+".bkp"] = errors.New("io error")

	err := refreshContainerFiles(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "backing up Dockerfile")
}

func (s *FSMigrateSuite) TestRefreshContainerFilesReadExistingError() {
	sys := newFakeSystem()
	dockerfilePath := filepath.Join("/loop", "container", "Dockerfile")
	// Pretend the file exists but reading it fails with a non-NotExist error.
	sys.files[dockerfilePath] = []byte("anything")
	sys.readErr[dockerfilePath] = errors.New("permission denied")

	err := refreshContainerFiles(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading existing Dockerfile")
}

func (s *FSMigrateSuite) TestRefreshContainerFilesSetupSkipsWhenPresent() {
	sys := newFakeSystem()
	setupPath := filepath.Join("/loop", "container", "setup.sh")
	// Pre-populate with custom content; Stat will succeed (not missing).
	sys.files[setupPath] = []byte("# user customization")

	err := refreshContainerFiles(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), []byte("# user customization"), sys.files[setupPath], "user setup.sh should be preserved")
}

func (s *FSMigrateSuite) TestRefreshContainerFilesSetupWriteError() {
	sys := newFakeSystem()
	setupPath := filepath.Join("/loop", "container", "setup.sh")
	sys.statMissing[setupPath] = true
	sys.writeErr[setupPath] = errors.New("io error")

	err := refreshContainerFiles(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing setup.sh")
}

// --- Bootstrap entry guard ---

func (s *FSMigrateSuite) TestMigrationsHaveBootstrapEntry() {
	require.GreaterOrEqual(s.T(), len(migrations), 2, "should have bootstrap + at least one real migration")
	require.Equal(s.T(), "bootstrap", migrations[0].Description)
	require.Nil(s.T(), migrations[0].Apply, "bootstrap migration must have nil Apply (never executed)")
}
