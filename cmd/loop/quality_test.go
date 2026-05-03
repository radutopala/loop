package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/osutil"
	"github.com/radutopala/loop/internal/quality/engine"
	"github.com/radutopala/loop/internal/quality/metrics"
	"github.com/radutopala/loop/internal/quality/parser"
	"github.com/radutopala/loop/internal/quality/rules"
	"github.com/radutopala/loop/internal/quality/snapshot"
)

func (s *MainSuite) TestNewQualityCmd() {
	cmd := s.app.newQualityCmd()
	require.Equal(s.T(), "quality", cmd.Use)
	require.True(s.T(), cmd.HasSubCommands())

	var found bool
	for _, sub := range cmd.Commands() {
		if sub.Use == "scan [path]" {
			found = true
			require.NotNil(s.T(), sub.RunE)
		}
	}
	require.True(s.T(), found, "quality should have scan subcommand")
}

func (s *MainSuite) TestQualityCmdRegisteredOnRoot() {
	root := s.app.newRootCmd()
	var found bool
	for _, sub := range root.Commands() {
		if sub.Use == "quality" {
			found = true
		}
	}
	require.True(s.T(), found)
}

func (s *MainSuite) TestQualityScanCmdExecuteHumanPath() {
	dir := s.writeTinyGoRepo()
	cmd := s.app.newQualityScanCmd()

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{dir})
	require.NoError(s.T(), cmd.Execute())
	require.Contains(s.T(), buf.String(), "quality_signal:")
}

func (s *MainSuite) TestQualityScanCmdExecuteParserInitError() {
	s.app.newQualityParser = func() (parser.Parser, error) {
		return nil, errors.New("boom")
	}
	cmd := s.app.newQualityScanCmd()
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{s.T().TempDir()})
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "init parser")
}

func (s *MainSuite) TestQualityScanHumanOutput() {
	dir := s.writeTinyGoRepo()
	p := s.realParser()

	var stdout, stderr bytes.Buffer
	require.NoError(s.T(), s.app.runQualityScan(context.Background(), &stdout, &stderr, p, engine.Config{}, dir, false))

	out := stdout.String()
	require.Contains(s.T(), out, "quality_signal:")
	require.Contains(s.T(), out, "metrics:")
	require.Contains(s.T(), out, "rules:")
	require.Contains(s.T(), out, "no_import_cycles")
	require.Empty(s.T(), stderr.String())
}

func (s *MainSuite) TestQualityScanJSONOutput() {
	dir := s.writeTinyGoRepo()
	p := s.realParser()

	var stdout, stderr bytes.Buffer
	require.NoError(s.T(), s.app.runQualityScan(context.Background(), &stdout, &stderr, p, engine.Config{}, dir, true))

	var rep scanReport
	require.NoError(s.T(), json.Unmarshal(stdout.Bytes(), &rep))
	require.Equal(s.T(), dir, rep.DirPath)
	require.GreaterOrEqual(s.T(), rep.FileCount, 1)
	require.NotEmpty(s.T(), rep.Metrics)
	require.NotEmpty(s.T(), rep.Rules.Passed)
}

func (s *MainSuite) TestQualityScanDefaultsToCwd() {
	dir := s.writeTinyGoRepo()
	prev, err := os.Getwd()
	require.NoError(s.T(), err)
	require.NoError(s.T(), os.Chdir(dir))
	s.T().Cleanup(func() { _ = os.Chdir(prev) })

	p := s.realParser()
	var stdout, stderr bytes.Buffer
	require.NoError(s.T(), s.app.runQualityScan(context.Background(), &stdout, &stderr, p, engine.Config{}, "", true))

	var rep scanReport
	require.NoError(s.T(), json.Unmarshal(stdout.Bytes(), &rep))
	require.GreaterOrEqual(s.T(), rep.FileCount, 1)
}

func (s *MainSuite) TestQualityScanGetwdError() {
	s.app.sys = errSystem{base: osutil.RealSystem{}, getwdErr: errors.New("boom")}
	p := s.realParser()
	var stdout, stderr bytes.Buffer
	err := s.app.runQualityScan(context.Background(), &stdout, &stderr, p, engine.Config{}, "", false)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "resolving working directory")
}

func (s *MainSuite) TestQualityScanScanError() {
	bogus := filepath.Join(s.T().TempDir(), "definitely-missing")
	p := s.realParser()
	var stdout, stderr bytes.Buffer
	err := s.app.runQualityScan(context.Background(), &stdout, &stderr, p, engine.Config{}, bogus, false)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "scan:")
}

func (s *MainSuite) TestQualityScanRepoTooLarge() {
	// MaxFiles=1 with 2 files trips the cap before any parsing happens.
	dir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o600))
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "b.go"), []byte("package a\n"), 0o600))

	p := s.realParser()
	var stdout, stderr bytes.Buffer
	err := s.app.runQualityScan(context.Background(), &stdout, &stderr, p, engine.Config{MaxFiles: 1}, dir, false)
	require.Error(s.T(), err)
	require.Contains(s.T(), stderr.String(), "repo too large to scan")
}

func (s *MainSuite) TestBuildScanReportSplitsRules() {
	res := engine.ScanResult{
		Signal:    metrics.Signal{Value: 9000, Metrics: []metrics.Result{{Name: "modularity", Score: 1, Raw: 0}}},
		FileCount: 5,
	}
	rs := []rules.Result{
		{Name: "alpha", Severity: rules.SevPass, Message: "ok"},
		{Name: "beta", Severity: rules.SevFail, Message: "bad", Citations: []rules.Citation{{Path: "x.go", Note: "boom"}}},
	}
	rep := buildScanReport("/tmp", res, rs)
	require.Len(s.T(), rep.Rules.Passed, 1)
	require.Len(s.T(), rep.Rules.Failed, 1)
	require.Equal(s.T(), "x.go", rep.Rules.Failed[0].Citations[0].Path)
}

func (s *MainSuite) TestWriteHumanReportRendersFailures() {
	res := engine.ScanResult{
		Signal:    metrics.Signal{Value: 9000, Metrics: []metrics.Result{{Name: "modularity", Score: 1, Raw: 0}}},
		FileCount: 5,
	}
	rs := []rules.Result{
		{Name: "alpha", Severity: rules.SevPass, Message: "ok"},
		{Name: "beta", Severity: rules.SevFail, Message: "bad", Citations: []rules.Citation{{Path: "x.go", Note: "boom"}}},
	}
	rep := buildScanReport("/tmp", res, rs)

	var buf bytes.Buffer
	require.NoError(s.T(), writeHumanReport(&buf, rep))
	out := buf.String()
	require.Contains(s.T(), out, "✓ alpha")
	require.Contains(s.T(), out, "✗ beta")
	require.Contains(s.T(), out, "x.go (boom)")
}

func (s *MainSuite) TestNoopStoreInterface() {
	// noopStore exists to satisfy snapshot.Store; only Save is exercised
	// by the engine. The other methods are still part of the interface
	// contract — assert their no-op behaviour explicitly.
	var st snapshot.Store = noopStore{}
	require.NoError(s.T(), st.Save(context.Background(), "c", "main", metrics.Signal{}, time.Time{}))
	got, err := st.Get(context.Background(), "c", "main")
	require.Nil(s.T(), got)
	require.ErrorIs(s.T(), err, snapshot.ErrNotFound)
	got2, err := st.GetLatest(context.Background(), "c")
	require.Nil(s.T(), got2)
	require.ErrorIs(s.T(), err, snapshot.ErrNotFound)
	require.NoError(s.T(), st.DeleteForChannel(context.Background(), "c"))
}

func (s *MainSuite) TestDefaultNewQualityParser() {
	p, err := defaultNewQualityParser()
	require.NoError(s.T(), err)
	require.NotNil(s.T(), p)
}

// realParser is constructed once per suite via the production hook —
// each suite test reuses it so we don't pay the gotreesitter init cost
// 10 times.
func (s *MainSuite) realParser() parser.Parser {
	p, err := defaultNewQualityParser()
	require.NoError(s.T(), err)
	return p
}

func (s *MainSuite) writeTinyGoRepo() string {
	dir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\nfunc Foo() {}\n"), 0o600))
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "b.go"), []byte("package a\nfunc Bar() {}\n"), 0o600))
	return dir
}

// errSystem is a thin wrapper that lets a single call return a forced
// error while delegating the rest to a real system. Avoids hand-rolling
// every appSystem method.
type errSystem struct {
	base        appSystem
	getwdErr    error
	getwdReturn string
}

func (e errSystem) UserHomeDir() (string, error)           { return e.base.UserHomeDir() }
func (e errSystem) Stat(n string) (os.FileInfo, error)     { return e.base.Stat(n) }
func (e errSystem) MkdirAll(p string, m os.FileMode) error { return e.base.MkdirAll(p, m) }
func (e errSystem) WriteFile(n string, d []byte, m os.FileMode) error {
	return e.base.WriteFile(n, d, m)
}
func (e errSystem) Getwd() (string, error) {
	if e.getwdErr != nil {
		return "", e.getwdErr
	}
	if e.getwdReturn != "" {
		return e.getwdReturn, nil
	}
	return e.base.Getwd()
}
func (e errSystem) ReadFile(n string) ([]byte, error)        { return e.base.ReadFile(n) }
func (e errSystem) Remove(n string) error                    { return e.base.Remove(n) }
func (e errSystem) Executable() (string, error)              { return e.base.Executable() }
func (e errSystem) EvalSymlinks(p string) (string, error)    { return e.base.EvalSymlinks(p) }
func (e errSystem) Chmod(n string, m os.FileMode) error      { return e.base.Chmod(n, m) }
func (e errSystem) Rename(o, n string) error                 { return e.base.Rename(o, n) }
func (e errSystem) CreateTemp(d, p string) (*os.File, error) { return e.base.CreateTemp(d, p) }
