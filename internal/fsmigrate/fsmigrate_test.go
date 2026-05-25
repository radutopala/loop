package fsmigrate

import (
	"context"
	"database/sql"
	"encoding/json"
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

// --- seedBuiltinCodeReviewShortcut tests ---

func (s *FSMigrateSuite) TestSeedBuiltinCodeReviewShortcutNoConfigFile() {
	sys := newFakeSystem()

	err := seedBuiltinCodeReviewShortcut(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, json.MarshalIndent)
	require.NoError(s.T(), err)
	require.Empty(s.T(), sys.files, "no file should be written when config is absent")
}

func (s *FSMigrateSuite) TestSeedBuiltinCodeReviewShortcutReadError() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte("{}")
	sys.readErr[configPath] = errors.New("permission denied")

	err := seedBuiltinCodeReviewShortcut(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, json.MarshalIndent)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading")
}

func (s *FSMigrateSuite) TestSeedBuiltinCodeReviewShortcutInvalidHJSON() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte("{not even close")

	err := seedBuiltinCodeReviewShortcut(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, json.MarshalIndent)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "standardizing")
}

func (s *FSMigrateSuite) TestSeedBuiltinCodeReviewShortcutInvalidJSON() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	// Valid HJSON that standardizes to a non-object — passes Standardize but
	// fails json.Unmarshal into map[string]any.
	sys.files[configPath] = []byte("[]")

	err := seedBuiltinCodeReviewShortcut(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, json.MarshalIndent)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parsing")
}

func (s *FSMigrateSuite) TestSeedBuiltinCodeReviewShortcutAlreadyPresent() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`{"prompt_shortcuts":[{"name":"builtin code review","prompt":"/code-review"}]}`)

	err := seedBuiltinCodeReviewShortcut(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, json.MarshalIndent)
	require.NoError(s.T(), err)
	require.Equal(s.T(),
		[]byte(`{"prompt_shortcuts":[{"name":"builtin code review","prompt":"/code-review"}]}`),
		sys.files[configPath],
		"file must not be rewritten when entry already exists",
	)
}

func (s *FSMigrateSuite) TestSeedBuiltinCodeReviewShortcutEmptyConfig() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`{}`)

	err := seedBuiltinCodeReviewShortcut(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, json.MarshalIndent)
	require.NoError(s.T(), err)

	var cfg map[string]any
	require.NoError(s.T(), json.Unmarshal(sys.files[configPath], &cfg))
	shortcuts := cfg["prompt_shortcuts"].([]any)
	require.Len(s.T(), shortcuts, 1)
	sc := shortcuts[0].(map[string]any)
	require.Equal(s.T(), "builtin code review", sc["name"])
	require.Equal(s.T(), "/code-review", sc["prompt"])
	require.Contains(s.T(), sc["description"], "/code-review")
}

func (s *FSMigrateSuite) TestSeedBuiltinCodeReviewShortcutAppendsToExisting() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`{"prompt_shortcuts":[{"name":"review","prompt_path":"review-code.md"}]}`)

	err := seedBuiltinCodeReviewShortcut(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, json.MarshalIndent)
	require.NoError(s.T(), err)

	var cfg map[string]any
	require.NoError(s.T(), json.Unmarshal(sys.files[configPath], &cfg))
	shortcuts := cfg["prompt_shortcuts"].([]any)
	require.Len(s.T(), shortcuts, 2)
	require.Equal(s.T(), "review", shortcuts[0].(map[string]any)["name"])
	require.Equal(s.T(), "builtin code review", shortcuts[1].(map[string]any)["name"])
}

func (s *FSMigrateSuite) TestSeedBuiltinCodeReviewShortcutSkipsNonMapEntries() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	// Defensive: a bogus non-object entry must not crash or block the seed.
	sys.files[configPath] = []byte(`{"prompt_shortcuts":["bogus",{"name":"other"}]}`)

	err := seedBuiltinCodeReviewShortcut(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, json.MarshalIndent)
	require.NoError(s.T(), err)

	var cfg map[string]any
	require.NoError(s.T(), json.Unmarshal(sys.files[configPath], &cfg))
	shortcuts := cfg["prompt_shortcuts"].([]any)
	require.Len(s.T(), shortcuts, 3)
	require.Equal(s.T(), "builtin code review", shortcuts[2].(map[string]any)["name"])
}

func (s *FSMigrateSuite) TestSeedBuiltinCodeReviewShortcutWriteError() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`{}`)
	sys.writeErr[configPath] = errors.New("io error")

	err := seedBuiltinCodeReviewShortcut(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, json.MarshalIndent)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing")
}

func (s *FSMigrateSuite) TestSeedBuiltinCodeReviewShortcutMarshalError() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`{}`)

	failingMarshal := func(_ any, _, _ string) ([]byte, error) { return nil, errors.New("marshal fail") }
	err := seedBuiltinCodeReviewShortcut(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, failingMarshal)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "serializing")
}

// --- Bootstrap entry guard ---

func (s *FSMigrateSuite) TestMigrationsHaveBootstrapEntry() {
	require.GreaterOrEqual(s.T(), len(migrations), 2, "should have bootstrap + at least one real migration")
	require.Equal(s.T(), "bootstrap", migrations[0].Description)
	require.Nil(s.T(), migrations[0].Apply, "bootstrap migration must have nil Apply (never executed)")
}

// --- seedReviewLoopWorkflows tests ---

func (s *FSMigrateSuite) TestSeedReviewLoopWorkflowsNoConfigFile() {
	sys := newFakeSystem()

	err := seedReviewLoopWorkflows(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, json.MarshalIndent)
	require.NoError(s.T(), err)
	require.Empty(s.T(), sys.files, "no file should be written when config is absent")
}

func (s *FSMigrateSuite) TestSeedReviewLoopWorkflowsReadError() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte("{}")
	sys.readErr[configPath] = errors.New("permission denied")

	err := seedReviewLoopWorkflows(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, json.MarshalIndent)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading")
}

func (s *FSMigrateSuite) TestSeedReviewLoopWorkflowsInvalidHJSON() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte("{not even close")

	err := seedReviewLoopWorkflows(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, json.MarshalIndent)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "standardizing")
}

func (s *FSMigrateSuite) TestSeedReviewLoopWorkflowsInvalidJSON() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte("[]")

	err := seedReviewLoopWorkflows(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, json.MarshalIndent)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parsing")
}

func (s *FSMigrateSuite) TestSeedReviewLoopWorkflowsSeedsBoth() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`{}`)

	err := seedReviewLoopWorkflows(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, json.MarshalIndent)
	require.NoError(s.T(), err)

	var cfg map[string]any
	require.NoError(s.T(), json.Unmarshal(sys.files[configPath], &cfg))
	workflows, ok := cfg["workflows"].([]any)
	require.True(s.T(), ok)
	require.Len(s.T(), workflows, 2)
	names := []string{
		workflows[0].(map[string]any)["name"].(string),
		workflows[1].(map[string]any)["name"].(string),
	}
	require.Contains(s.T(), names, "review-loop")
	require.Contains(s.T(), names, "review-fix-loop")
}

func (s *FSMigrateSuite) TestSeedReviewLoopWorkflowsBothAlreadyPresentIsNoop() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	original := []byte(`{"workflows":[{"name":"review-loop"},{"name":"review-fix-loop"}]}`)
	sys.files[configPath] = append([]byte(nil), original...)

	err := seedReviewLoopWorkflows(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, json.MarshalIndent)
	require.NoError(s.T(), err)
	require.Equal(s.T(), original, sys.files[configPath], "file must not be rewritten when both entries already exist")
}

func (s *FSMigrateSuite) TestSeedReviewLoopWorkflowsAppendsMissingOnly() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	// review-loop already present; only review-fix-loop should be appended.
	sys.files[configPath] = []byte(`{"workflows":[{"name":"review-loop"}]}`)

	err := seedReviewLoopWorkflows(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, json.MarshalIndent)
	require.NoError(s.T(), err)

	var cfg map[string]any
	require.NoError(s.T(), json.Unmarshal(sys.files[configPath], &cfg))
	workflows := cfg["workflows"].([]any)
	require.Len(s.T(), workflows, 2)
	require.Equal(s.T(), "review-loop", workflows[0].(map[string]any)["name"])
	require.Equal(s.T(), "review-fix-loop", workflows[1].(map[string]any)["name"])
}

func (s *FSMigrateSuite) TestSeedReviewLoopWorkflowsSkipsNonMapEntries() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`{"workflows":["bogus",{"name":"other"}]}`)

	err := seedReviewLoopWorkflows(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, json.MarshalIndent)
	require.NoError(s.T(), err)

	var cfg map[string]any
	require.NoError(s.T(), json.Unmarshal(sys.files[configPath], &cfg))
	workflows := cfg["workflows"].([]any)
	require.Len(s.T(), workflows, 4, "should append both seeded workflows past the bogus + other entries")
}

func (s *FSMigrateSuite) TestSeedReviewLoopWorkflowsWriteError() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`{}`)
	sys.writeErr[configPath] = errors.New("io error")

	err := seedReviewLoopWorkflows(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, json.MarshalIndent)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing")
}

func (s *FSMigrateSuite) TestSeedReviewLoopWorkflowsMarshalError() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`{}`)

	failingMarshal := func(_ any, _, _ string) ([]byte, error) { return nil, errors.New("marshal fail") }
	err := seedReviewLoopWorkflows(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, failingMarshal)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "serializing")
}

// --- builtin loop def shape guards ---

func (s *FSMigrateSuite) TestBuiltinReviewLoopDefShape() {
	def := builtinReviewLoopDef()
	require.Equal(s.T(), "review-loop", def["name"])
	require.Contains(s.T(), def["description"], "review")
	nodes := def["nodes"].([]any)
	require.Len(s.T(), nodes, 1)
	loop := nodes[0].(map[string]any)
	require.Equal(s.T(), "loop", loop["type"])
	body := loop["body"].([]any)
	require.Len(s.T(), body, 1)
	review := body[0].(map[string]any)
	require.Equal(s.T(), "review", review["id"])
	require.Equal(s.T(), "bash", review["type"])
}

func (s *FSMigrateSuite) TestBuiltinReviewFixLoopDefShape() {
	def := builtinReviewFixLoopDef()
	require.Equal(s.T(), "review-fix-loop", def["name"])
	nodes := def["nodes"].([]any)
	loop := nodes[0].(map[string]any)
	body := loop["body"].([]any)
	require.Len(s.T(), body, 3)
	ids := []string{
		body[0].(map[string]any)["id"].(string),
		body[1].(map[string]any)["id"].(string),
		body[2].(map[string]any)["id"].(string),
	}
	require.Equal(s.T(), []string{"review", "fix", "verify"}, ids)
	// fix + verify must be gated on .Review.NoComments to skip when clean.
	require.Equal(s.T(), "{{ not .Review.NoComments }}", body[1].(map[string]any)["when"])
	require.Equal(s.T(), "{{ not .Review.NoComments }}", body[2].(map[string]any)["when"])
	// verify script must run an explicit commit so leftover changes survive
	// the loop even if the fix prompt forgets to commit.
	require.Equal(s.T(), reviewFixVerifyScript, body[2].(map[string]any)["script"])
}

// --- patchReviewFixVerifyScript tests ---

// reviewFixLoopWithVerifyScript builds the on-disk shape patchReviewFixVerifyScript
// walks: a workflows array containing one `review-fix-loop` whose single loop
// node has a body with a `verify` bash child set to the given script.
func reviewFixLoopWithVerifyScript(script string) string {
	cfg := map[string]any{
		"workflows": []any{
			map[string]any{
				"name": "review-fix-loop",
				"nodes": []any{
					map[string]any{
						"type": "loop",
						"body": []any{
							map[string]any{"id": "review", "type": "bash"},
							map[string]any{"id": "fix", "type": "prompt"},
							map[string]any{"id": "verify", "type": "bash", "script": script},
						},
					},
				},
			},
		},
	}
	out, _ := json.Marshal(cfg)
	return string(out)
}

func (s *FSMigrateSuite) TestPatchReviewFixVerifyScriptNoConfigFile() {
	sys := newFakeSystem()
	err := patchReviewFixVerifyScript(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, json.MarshalIndent)
	require.NoError(s.T(), err)
}

func (s *FSMigrateSuite) TestPatchReviewFixVerifyScriptReadError() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`{}`)
	sys.readErr[configPath] = errors.New("io error")
	err := patchReviewFixVerifyScript(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, json.MarshalIndent)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading")
}

func (s *FSMigrateSuite) TestPatchReviewFixVerifyScriptInvalidHJSON() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`{not json`)
	err := patchReviewFixVerifyScript(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, json.MarshalIndent)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "standardizing")
}

func (s *FSMigrateSuite) TestPatchReviewFixVerifyScriptInvalidJSON() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`["not an object"]`)
	err := patchReviewFixVerifyScript(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, json.MarshalIndent)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parsing")
}

func (s *FSMigrateSuite) TestPatchReviewFixVerifyScriptUpdatesOldScript() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(reviewFixLoopWithVerifyScript(reviewFixVerifyScriptOld))

	err := patchReviewFixVerifyScript(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, json.MarshalIndent)
	require.NoError(s.T(), err)

	var cfg map[string]any
	require.NoError(s.T(), json.Unmarshal(sys.files[configPath], &cfg))
	body := cfg["workflows"].([]any)[0].(map[string]any)["nodes"].([]any)[0].(map[string]any)["body"].([]any)
	verify := body[2].(map[string]any)
	require.Equal(s.T(), reviewFixVerifyScript, verify["script"])
}

func (s *FSMigrateSuite) TestPatchReviewFixVerifyScriptUpdatesBuggyAddAllScript() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(reviewFixLoopWithVerifyScript(reviewFixVerifyScriptBuggyAddAll))

	err := patchReviewFixVerifyScript(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, json.MarshalIndent)
	require.NoError(s.T(), err)

	var cfg map[string]any
	require.NoError(s.T(), json.Unmarshal(sys.files[configPath], &cfg))
	body := cfg["workflows"].([]any)[0].(map[string]any)["nodes"].([]any)[0].(map[string]any)["body"].([]any)
	verify := body[2].(map[string]any)
	require.Equal(s.T(), reviewFixVerifyScript, verify["script"])
}

func (s *FSMigrateSuite) TestPatchReviewFixVerifyScriptLeavesCustomizedScriptAlone() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	original := []byte(reviewFixLoopWithVerifyScript("my custom script"))
	sys.files[configPath] = append([]byte(nil), original...)

	err := patchReviewFixVerifyScript(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, json.MarshalIndent)
	require.NoError(s.T(), err)
	require.Equal(s.T(), original, sys.files[configPath], "customized verify script must not be rewritten")
}

func (s *FSMigrateSuite) TestPatchReviewFixVerifyScriptNoMatchingWorkflowIsNoop() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	original := []byte(`{"workflows":[{"name":"some-other-workflow"}]}`)
	sys.files[configPath] = append([]byte(nil), original...)

	err := patchReviewFixVerifyScript(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, json.MarshalIndent)
	require.NoError(s.T(), err)
	require.Equal(s.T(), original, sys.files[configPath])
}

func (s *FSMigrateSuite) TestPatchReviewFixVerifyScriptSkipsNonMapEntries() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	// Workflows list contains bogus + a malformed review-fix-loop whose
	// nodes[0] is not a map. Walker must not panic and must not patch.
	sys.files[configPath] = []byte(`{"workflows":["bogus",{"name":"review-fix-loop","nodes":["not-a-map",{"type":"loop","body":["not-a-map",{"id":"verify","type":"bash","script":"` + reviewFixVerifyScriptOld + `"}]}]}]}`)

	err := patchReviewFixVerifyScript(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, json.MarshalIndent)
	require.NoError(s.T(), err)

	var cfg map[string]any
	require.NoError(s.T(), json.Unmarshal(sys.files[configPath], &cfg))
	// the nested loop's body's verify child was reachable — should be patched.
	body := cfg["workflows"].([]any)[1].(map[string]any)["nodes"].([]any)[1].(map[string]any)["body"].([]any)
	require.Equal(s.T(), reviewFixVerifyScript, body[1].(map[string]any)["script"])
}

func (s *FSMigrateSuite) TestPatchReviewFixVerifyScriptWriteError() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(reviewFixLoopWithVerifyScript(reviewFixVerifyScriptOld))
	sys.writeErr[configPath] = errors.New("io error")

	err := patchReviewFixVerifyScript(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, json.MarshalIndent)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing")
}

func (s *FSMigrateSuite) TestPatchReviewFixVerifyScriptMarshalError() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(reviewFixLoopWithVerifyScript(reviewFixVerifyScriptOld))

	failingMarshal := func(_ any, _, _ string) ([]byte, error) { return nil, errors.New("marshal fail") }
	err := patchReviewFixVerifyScript(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}, failingMarshal)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "serializing")
}
