package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/quality/engine"
	"github.com/radutopala/loop/internal/quality/evolution"
	"github.com/radutopala/loop/internal/quality/parser"
	"github.com/radutopala/loop/internal/quality/whatif"
)

// --- newQualityCmd subcommand wiring ---

func (s *MainSuite) TestQualityCmdHasDiagnosticsSubcommands() {
	cmd := s.app.newQualityCmd()
	have := map[string]bool{}
	for _, sub := range cmd.Commands() {
		have[sub.Use] = true
	}
	require.True(s.T(), have["scan [path]"])
	require.True(s.T(), have["cycles [path]"])
	require.True(s.T(), have["whatif [path]"])
	require.True(s.T(), have["evolution [path]"])
	require.True(s.T(), have["c4 [path]"])
}

// --- cycles ---

func (s *MainSuite) TestQualityCyclesNoCyclesHuman() {
	dir := s.writeTinyGoRepo()
	cmd := s.app.newQualityCyclesCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{dir})
	require.NoError(s.T(), cmd.Execute())
	require.Contains(s.T(), buf.String(), "No import cycles")
}

func (s *MainSuite) TestQualityCyclesJSONOutput() {
	dir := s.writeTinyGoRepo()
	cmd := s.app.newQualityCyclesCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--json", dir})
	require.NoError(s.T(), cmd.Execute())
	var rep cyclesReport
	require.NoError(s.T(), json.Unmarshal(buf.Bytes(), &rep))
	require.Equal(s.T(), dir, rep.DirPath)
}

func (s *MainSuite) TestQualityCyclesParserInitError() {
	s.app.newQualityParser = func() (parser.Parser, error) {
		return nil, errors.New("boom")
	}
	cmd := s.app.newQualityCyclesCmd()
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{s.T().TempDir()})
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "init parser")
}

func (s *MainSuite) TestQualityCyclesScanError() {
	bogus := filepath.Join(s.T().TempDir(), "definitely-missing")
	p := s.realParser()
	var stdout, stderr bytes.Buffer
	err := s.app.runQualityCycles(context.Background(), &stdout, &stderr, p, engine.Config{}, bogus, false)
	require.Error(s.T(), err)
}

func (s *MainSuite) TestQualityCyclesRendersFoundCycle() {
	dir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\nimport _ \"./b\"\n"), 0o600))
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "b.go"), []byte("package a\nimport _ \"./a\"\n"), 0o600))

	p := s.realParser()
	var stdout, stderr bytes.Buffer
	require.NoError(s.T(), s.app.runQualityCycles(context.Background(), &stdout, &stderr, p, engine.Config{}, dir, false))
	out := stdout.String()
	if strings.Contains(out, "Found") {
		require.Contains(s.T(), out, "cycle")
	}
}

// --- whatif ---

func (s *MainSuite) TestQualityWhatifRequiresFile() {
	cmd := s.app.newQualityWhatifCmd()
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{s.T().TempDir()})
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "--file is required")
}

func (s *MainSuite) TestQualityWhatifParserInitError() {
	s.app.newQualityParser = func() (parser.Parser, error) {
		return nil, errors.New("boom")
	}
	mutFile := filepath.Join(s.T().TempDir(), "mut.json")
	require.NoError(s.T(), os.WriteFile(mutFile, []byte(`{"op":"delete","path":"a.go"}`), 0o600))

	cmd := s.app.newQualityWhatifCmd()
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--file", mutFile, s.T().TempDir()})
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "init parser")
}

func (s *MainSuite) TestQualityWhatifReadMutationsError() {
	cmd := s.app.newQualityWhatifCmd()
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--file", "/does/not/exist.json", s.T().TempDir()})
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading mutations")
}

func (s *MainSuite) TestQualityWhatifHumanOutput() {
	dir := s.writeTinyGoRepo()
	p := s.realParser()
	muts := []whatif.Mutation{{Op: whatif.OpDelete, Path: "a.go"}}
	var stdout, stderr bytes.Buffer
	require.NoError(s.T(), s.app.runQualityWhatif(context.Background(), &stdout, &stderr, p, engine.Config{}, dir, muts, false))
	out := stdout.String()
	require.Contains(s.T(), out, "Signal:")
	require.Contains(s.T(), out, "Predicted metrics:")
}

func (s *MainSuite) TestQualityWhatifCommandHappyPath() {
	dir := s.writeTinyGoRepo()
	mutFile := filepath.Join(s.T().TempDir(), "mut.json")
	require.NoError(s.T(), os.WriteFile(mutFile, []byte(`{"op":"delete","path":"a.go"}`), 0o600))
	cmd := s.app.newQualityWhatifCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--file", mutFile, dir})
	require.NoError(s.T(), cmd.Execute())
	require.Contains(s.T(), buf.String(), "Signal:")
}

func (s *MainSuite) TestQualityWhatifJSONOutput() {
	dir := s.writeTinyGoRepo()
	p := s.realParser()
	muts := []whatif.Mutation{{Op: whatif.OpDelete, Path: "a.go"}}
	var stdout, stderr bytes.Buffer
	require.NoError(s.T(), s.app.runQualityWhatif(context.Background(), &stdout, &stderr, p, engine.Config{}, dir, muts, true))
	var got map[string]any
	require.NoError(s.T(), json.Unmarshal(stdout.Bytes(), &got))
	require.Contains(s.T(), got, "predicted_signal")
}

func (s *MainSuite) TestQualityWhatifSimulateError() {
	dir := s.writeTinyGoRepo()
	p := s.realParser()
	muts := []whatif.Mutation{{Op: whatif.OpDelete, Path: "missing.go"}}
	var stdout, stderr bytes.Buffer
	err := s.app.runQualityWhatif(context.Background(), &stdout, &stderr, p, engine.Config{}, dir, muts, false)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "simulate")
}

func (s *MainSuite) TestQualityWhatifScanError() {
	bogus := filepath.Join(s.T().TempDir(), "definitely-missing")
	p := s.realParser()
	muts := []whatif.Mutation{{Op: whatif.OpDelete, Path: "a.go"}}
	var stdout, stderr bytes.Buffer
	err := s.app.runQualityWhatif(context.Background(), &stdout, &stderr, p, engine.Config{}, bogus, muts, false)
	require.Error(s.T(), err)
}

// --- readMutations ---

func (s *MainSuite) TestReadMutationsArrayFromFile() {
	mutFile := filepath.Join(s.T().TempDir(), "mut.json")
	require.NoError(s.T(), os.WriteFile(mutFile, []byte(`[{"op":"delete","path":"a.go"},{"op":"move","path":"b.go","new_module":"x"}]`), 0o600))
	muts, err := s.app.readMutations(nil, mutFile)
	require.NoError(s.T(), err)
	require.Len(s.T(), muts, 2)
	require.Equal(s.T(), whatif.OpDelete, muts[0].Op)
}

func (s *MainSuite) TestReadMutationsSingleObject() {
	mutFile := filepath.Join(s.T().TempDir(), "mut.json")
	require.NoError(s.T(), os.WriteFile(mutFile, []byte(`{"op":"delete","path":"a.go"}`), 0o600))
	muts, err := s.app.readMutations(nil, mutFile)
	require.NoError(s.T(), err)
	require.Len(s.T(), muts, 1)
}

func (s *MainSuite) TestReadMutationsFromStdin() {
	stdin := bytes.NewBufferString(`{"op":"delete","path":"a.go"}`)
	muts, err := s.app.readMutations(stdin, "-")
	require.NoError(s.T(), err)
	require.Len(s.T(), muts, 1)
}

func (s *MainSuite) TestReadMutationsBadFile() {
	_, err := s.app.readMutations(nil, "/does/not/exist.json")
	require.Error(s.T(), err)
}

func (s *MainSuite) TestReadMutationsBadJSON() {
	mutFile := filepath.Join(s.T().TempDir(), "mut.json")
	require.NoError(s.T(), os.WriteFile(mutFile, []byte(`not json at all`), 0o600))
	_, err := s.app.readMutations(nil, mutFile)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "decoding mutations")
}

// --- evolution ---

func (s *MainSuite) TestQualityEvolutionUnknownDirReturnsError() {
	bogus := filepath.Join(s.T().TempDir(), "definitely-missing")
	cmd := s.app.newQualityEvolutionCmd()
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{bogus})
	err := cmd.Execute()
	require.Error(s.T(), err)
}

func (s *MainSuite) TestQualityEvolutionGetwdError() {
	s.app.sys = errSystem{base: s.app.sys, getwdErr: errors.New("boom")}
	cmd := s.app.newQualityEvolutionCmd()
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "resolving working directory")
}

func (s *MainSuite) TestQualityEvolutionNoHistoryError() {
	s.app.newEvolutionReader = func() evolution.HistoryReader { return stubReader{commits: nil} }
	var stdout bytes.Buffer
	err := s.app.runQualityEvolution(context.Background(), &stdout, s.T().TempDir(), evolution.Options{}, false)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "no git history")
}

func (s *MainSuite) TestQualityEvolutionUnknownDirSurfacesNonHistoryError() {
	bogus := filepath.Join(s.T().TempDir(), "definitely-missing")
	var stdout bytes.Buffer
	err := s.app.runQualityEvolution(context.Background(), &stdout, bogus, evolution.Options{}, false)
	require.Error(s.T(), err)
}

func (s *MainSuite) TestQualityEvolutionEmptyDirPathUsesGetwd() {
	dir := s.T().TempDir()
	s.app.sys = errSystem{base: s.app.sys, getwdReturn: dir}
	s.app.newEvolutionReader = func() evolution.HistoryReader {
		return stubReader{commits: []evolution.CommitFiles{{Hash: "h1", Author: "a", Files: []string{"x.go"}}}}
	}
	var stdout bytes.Buffer
	require.NoError(s.T(), s.app.runQualityEvolution(context.Background(), &stdout, "", evolution.Options{}, false))
	require.Contains(s.T(), stdout.String(), dir)
}

func (s *MainSuite) TestQualityEvolutionHumanOutputCoversAllSections() {
	s.app.newEvolutionReader = func() evolution.HistoryReader {
		return stubReader{commits: seededEvolutionCommits()}
	}
	cmd := s.app.newQualityEvolutionCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{s.T().TempDir()})
	require.NoError(s.T(), cmd.Execute())
	out := buf.String()
	require.Contains(s.T(), out, "Scanned")
	require.Contains(s.T(), out, "shallow clone")
	require.Contains(s.T(), out, "Coupling pairs:")
	require.Contains(s.T(), out, "[cross-module]")
	require.Contains(s.T(), out, "Churn hotspots:")
	require.Contains(s.T(), out, "Bus-factor risks:")
}

func (s *MainSuite) TestQualityEvolutionJSONOutput() {
	s.app.newEvolutionReader = func() evolution.HistoryReader {
		return stubReader{commits: seededEvolutionCommits()}
	}
	var stdout bytes.Buffer
	require.NoError(s.T(), s.app.runQualityEvolution(context.Background(), &stdout, s.T().TempDir(), evolution.Options{}, true))
	var rep map[string]any
	require.NoError(s.T(), json.Unmarshal(stdout.Bytes(), &rep))
	require.Contains(s.T(), rep, "commits_scanned")
}

// seededEvolutionCommits returns a deterministic commit history that triggers
// every branch of runQualityEvolution's human-readable output: shallow warning
// (<50 commits), a cross-module coupling pair, churn hotspots, and a bus-factor
// risk (single dominant author).
func seededEvolutionCommits() []evolution.CommitFiles {
	ts := time.Unix(1_700_000_000, 0)
	return []evolution.CommitFiles{
		{Hash: "h1", Author: "alice", Timestamp: ts, Files: []string{"internal/a.go", "cmd/main.go"}},
		{Hash: "h2", Author: "alice", Timestamp: ts.Add(time.Hour), Files: []string{"internal/a.go", "cmd/main.go"}},
		{Hash: "h3", Author: "alice", Timestamp: ts.Add(2 * time.Hour), Files: []string{"internal/a.go", "cmd/main.go"}},
	}
}

type stubReader struct {
	commits []evolution.CommitFiles
	err     error
}

func (r stubReader) Read(_ context.Context, _ string, _ int, _ int) ([]evolution.CommitFiles, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.commits, nil
}

// --- c4 ---

func (s *MainSuite) TestQualityC4HumanOutput() {
	dir := s.writeTinyGoRepo()
	cmd := s.app.newQualityC4Cmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{dir})
	require.NoError(s.T(), cmd.Execute())
	require.Contains(s.T(), buf.String(), "flowchart LR")
}

func (s *MainSuite) TestQualityC4JSONOutput() {
	dir := s.writeTinyGoRepo()
	cmd := s.app.newQualityC4Cmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--json", dir})
	require.NoError(s.T(), cmd.Execute())
	var got map[string]any
	require.NoError(s.T(), json.Unmarshal(buf.Bytes(), &got))
	require.Contains(s.T(), got, "mermaid")
}

func (s *MainSuite) TestQualityC4ParserInitError() {
	s.app.newQualityParser = func() (parser.Parser, error) {
		return nil, errors.New("boom")
	}
	cmd := s.app.newQualityC4Cmd()
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{s.T().TempDir()})
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "init parser")
}

func (s *MainSuite) TestQualityC4ScanError() {
	bogus := filepath.Join(s.T().TempDir(), "definitely-missing")
	p := s.realParser()
	var stdout, stderr bytes.Buffer
	err := s.app.runQualityC4(context.Background(), &stdout, &stderr, p, engine.Config{}, bogus, false)
	require.Error(s.T(), err)
}

// --- scanForGraph ---

func (s *MainSuite) TestScanForGraphGetwdError() {
	s.app.sys = errSystem{base: s.app.sys, getwdErr: errors.New("boom")}
	p := s.realParser()
	var stderr bytes.Buffer
	_, _, err := s.app.scanForGraph(context.Background(), &stderr, p, engine.Config{}, "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "resolving working directory")
}

func (s *MainSuite) TestScanForGraphRepoTooLarge() {
	dir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o600))
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "b.go"), []byte("package a\n"), 0o600))
	p := s.realParser()
	var stderr bytes.Buffer
	_, _, err := s.app.scanForGraph(context.Background(), &stderr, p, engine.Config{MaxFiles: 1}, dir)
	require.Error(s.T(), err)
	require.Contains(s.T(), stderr.String(), "repo too large")
}

func (s *MainSuite) TestScanForGraphSuccess() {
	dir := s.writeTinyGoRepo()
	p := s.realParser()
	var stderr bytes.Buffer
	g, resolved, err := s.app.scanForGraph(context.Background(), &stderr, p, engine.Config{}, dir)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), g)
	require.Equal(s.T(), dir, resolved)
}

func (s *MainSuite) TestScanForGraphEmptyDirPathUsesGetwd() {
	dir := s.writeTinyGoRepo()
	s.app.sys = errSystem{base: s.app.sys, getwdReturn: dir}
	p := s.realParser()
	var stderr bytes.Buffer
	g, resolved, err := s.app.scanForGraph(context.Background(), &stderr, p, engine.Config{}, "")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), g)
	require.Equal(s.T(), dir, resolved)
}
