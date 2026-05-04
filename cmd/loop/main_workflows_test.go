package main

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/quality/rules"
)

func (s *MainSuite) TestWorkflowsFromConfigReloadSuccess() {
	cfg := &config.Config{
		Workflows: []config.WorkflowDef{{Name: "initial"}},
	}
	reload := func() (*config.Config, error) {
		return &config.Config{
			Workflows: []config.WorkflowDef{{Name: "reloaded"}},
		}, nil
	}

	fn := workflowsFromConfig(cfg, reload)
	wfs := fn("", "")
	require.Len(s.T(), wfs, 1)
	require.Equal(s.T(), "reloaded", wfs[0].Name)
}

func (s *MainSuite) TestWorkflowsFromConfigReloadError() {
	cfg := &config.Config{
		Workflows: []config.WorkflowDef{{Name: "fallback"}},
	}
	reload := func() (*config.Config, error) {
		return nil, errors.New("config error")
	}

	fn := workflowsFromConfig(cfg, reload)
	wfs := fn("", "")
	require.Len(s.T(), wfs, 1)
	require.Equal(s.T(), "fallback", wfs[0].Name)
}

func (s *MainSuite) TestWorkflowsFromConfigWithDirPath() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(loopDir, "config.json"), []byte(`{
		"workflows": [
			{"name": "project-wf", "description": "project workflow", "nodes": []}
		]
	}`), 0o644))

	cfg := &config.Config{
		Workflows: []config.WorkflowDef{{Name: "global-wf", Description: "global workflow"}},
	}
	reload := func() (*config.Config, error) {
		return cfg, nil
	}

	fn := workflowsFromConfig(cfg, reload)

	wfs := fn("", "")
	require.Len(s.T(), wfs, 1)
	require.Equal(s.T(), "global-wf", wfs[0].Name)

	wfs = fn(tmpDir, "")
	require.Len(s.T(), wfs, 2)
	names := map[string]bool{}
	for _, wf := range wfs {
		names[wf.Name] = true
	}
	require.True(s.T(), names["global-wf"])
	require.True(s.T(), names["project-wf"])
}

func (s *MainSuite) TestWorkflowsFromConfigDirPathOverridesGlobal() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(loopDir, "config.json"), []byte(`{
		"workflows": [
			{"name": "validate", "description": "project validate", "nodes": [{"id":"lint","type":"bash","script":"make lint"}]}
		]
	}`), 0o644))

	cfg := &config.Config{
		Workflows: []config.WorkflowDef{{Name: "validate", Description: "global validate"}},
	}
	reload := func() (*config.Config, error) {
		return cfg, nil
	}

	fn := workflowsFromConfig(cfg, reload)
	wfs := fn(tmpDir, "")
	require.Len(s.T(), wfs, 1)
	require.Equal(s.T(), "validate", wfs[0].Name)
	require.Equal(s.T(), "project validate", wfs[0].Description)
}

func (s *MainSuite) TestWorkflowsFromConfigWorktreeThreeLayerMerge() {
	parentDir := s.T().TempDir()
	parentLoopDir := filepath.Join(parentDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(parentLoopDir, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(parentLoopDir, "config.json"), []byte(`{
		"workflows": [
			{"name": "parent-wf", "description": "parent workflow", "nodes": []},
			{"name": "shared-wf", "description": "parent shared", "nodes": []}
		]
	}`), 0o644))

	worktreeDir := s.T().TempDir()
	wtLoopDir := filepath.Join(worktreeDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(wtLoopDir, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(wtLoopDir, "config.json"), []byte(`{
		"workflows": [
			{"name": "worktree-wf", "description": "worktree only", "nodes": []},
			{"name": "shared-wf", "description": "worktree override", "nodes": []}
		]
	}`), 0o644))

	cfg := &config.Config{
		Workflows: []config.WorkflowDef{{Name: "global-wf", Description: "global workflow"}},
	}
	reload := func() (*config.Config, error) {
		return cfg, nil
	}

	fn := workflowsFromConfig(cfg, reload)

	wfs := fn(worktreeDir, parentDir)
	names := map[string]string{}
	for _, wf := range wfs {
		names[wf.Name] = wf.Description
	}
	require.Len(s.T(), wfs, 4)
	require.Equal(s.T(), "global workflow", names["global-wf"])
	require.Equal(s.T(), "parent workflow", names["parent-wf"])
	require.Equal(s.T(), "worktree only", names["worktree-wf"])
	require.Equal(s.T(), "worktree override", names["shared-wf"]) // worktree wins
}

func (s *MainSuite) TestQualityConfigLoaderUsesReloadedGlobal() {
	cfg := &config.Config{
		Quality: config.QualityConfig{MaxFiles: 1, ExcludePaths: []string{"seed/"}},
	}
	reload := func() (*config.Config, error) {
		return &config.Config{
			Quality: config.QualityConfig{MaxFiles: 99, ExcludePaths: []string{"reloaded/"}},
		}, nil
	}

	loader := qualityConfigLoader(cfg, reload)
	got, err := loader("", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), 99, got.MaxFiles)
	require.Equal(s.T(), []string{"reloaded/"}, got.ExcludePaths)
}

func (s *MainSuite) TestQualityConfigLoaderFallsBackToInitialOnReloadError() {
	cfg := &config.Config{
		Quality: config.QualityConfig{MaxFiles: 7, ExcludePaths: []string{"fallback/"}},
	}
	reload := func() (*config.Config, error) { return nil, errors.New("disk gone") }

	loader := qualityConfigLoader(cfg, reload)
	got, err := loader("", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), 7, got.MaxFiles)
	require.Equal(s.T(), []string{"fallback/"}, got.ExcludePaths)
}

func (s *MainSuite) TestQualityConfigLoaderMergesProjectConfig() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(loopDir, "config.json"), []byte(`{
		"quality": {"max_files": 5000, "exclude_paths": [".vite/", "dist-electron/"]}
	}`), 0o644))

	cfg := &config.Config{
		Quality: config.QualityConfig{MaxFiles: 10, ExcludePaths: []string{"global/"}},
	}
	reload := func() (*config.Config, error) { return cfg, nil }

	loader := qualityConfigLoader(cfg, reload)
	got, err := loader(tmpDir, "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), 5000, got.MaxFiles)
	require.Equal(s.T(), []string{".vite/", "dist-electron/"}, got.ExcludePaths)
}

func (s *MainSuite) TestQualityConfigLoaderMergesWorktreeProjectConfig() {
	parentDir := s.T().TempDir()
	parentLoopDir := filepath.Join(parentDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(parentLoopDir, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(parentLoopDir, "config.json"), []byte(`{
		"quality": {"max_files": 1000, "exclude_paths": ["parent/"]}
	}`), 0o644))

	worktreeDir := s.T().TempDir()
	wtLoopDir := filepath.Join(worktreeDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(wtLoopDir, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(wtLoopDir, "config.json"), []byte(`{
		"quality": {"exclude_paths": ["worktree/"]}
	}`), 0o644))

	cfg := &config.Config{
		Quality: config.QualityConfig{MaxFiles: 10, ExcludePaths: []string{"global/"}},
	}
	reload := func() (*config.Config, error) { return cfg, nil }

	loader := qualityConfigLoader(cfg, reload)
	got, err := loader(worktreeDir, parentDir)
	require.NoError(s.T(), err)
	// Worktree exclude_paths wins; parent's MaxFiles survives because the
	// worktree config doesn't set it.
	require.Equal(s.T(), 1000, got.MaxFiles)
	require.Equal(s.T(), []string{"worktree/"}, got.ExcludePaths)
}

func (s *MainSuite) TestQualityConfigLoaderProjectConfigReadError() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(loopDir, "config.json"), []byte(`{not valid json`), 0o644))

	cfg := &config.Config{}
	reload := func() (*config.Config, error) { return cfg, nil }

	loader := qualityConfigLoader(cfg, reload)
	_, err := loader(tmpDir, "")
	require.Error(s.T(), err)
}

func (s *MainSuite) TestQualityConfigLoaderWorktreeReadError() {
	parentDir := s.T().TempDir()
	parentLoopDir := filepath.Join(parentDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(parentLoopDir, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(parentLoopDir, "config.json"), []byte(`{not valid`), 0o644))

	cfg := &config.Config{}
	reload := func() (*config.Config, error) { return cfg, nil }

	loader := qualityConfigLoader(cfg, reload)
	_, err := loader(s.T().TempDir(), parentDir)
	require.Error(s.T(), err)
}

func (s *MainSuite) TestQualityRulesLoaderNoOverridesReturnsNil() {
	cfg := &config.Config{}
	reload := func() (*config.Config, error) { return cfg, nil }

	loader := qualityRulesLoader(cfg, reload)
	require.Nil(s.T(), loader("", ""))
}

func (s *MainSuite) TestQualityRulesLoaderMergesProjectOverrides() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(loopDir, "config.json"), []byte(`{
		"quality": {"rules": {"signal_floor": {"enabled": true, "threshold": 6500}}}
	}`), 0o644))

	cfg := &config.Config{}
	reload := func() (*config.Config, error) { return cfg, nil }

	loader := qualityRulesLoader(cfg, reload)
	got := loader(tmpDir, "")
	require.NotNil(s.T(), got)
	require.InDelta(s.T(), 6500.0, got.Rules[rules.SignalFloor].Threshold, 0.001)
}

func (s *MainSuite) TestQualityRulesLoaderFallsBackToInitialOnProjectError() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(loopDir, "config.json"), []byte(`{not valid`), 0o644))

	cfg := &config.Config{
		Quality: config.QualityConfig{Rules: map[string]config.QualityRuleConfig{
			rules.SignalFloor: {Enabled: true, Threshold: 7000},
		}},
	}
	reload := func() (*config.Config, error) { return cfg, nil }

	loader := qualityRulesLoader(cfg, reload)
	got := loader(tmpDir, "")
	// Project config errored — fall back to initial cfg's overrides.
	require.NotNil(s.T(), got)
	require.InDelta(s.T(), 7000.0, got.Rules[rules.SignalFloor].Threshold, 0.001)
}

func (s *MainSuite) TestCloseLogFileOK() {
	f, err := os.CreateTemp(s.T().TempDir(), "closeok-*")
	require.NoError(s.T(), err)
	require.NoError(s.T(), closeLogFile(f, "mcp", nil))
}

func (s *MainSuite) TestCloseLogFilePreservesExistingErr() {
	f, err := os.CreateTemp(s.T().TempDir(), "preserve-*")
	require.NoError(s.T(), err)
	prior := errors.New("prior failure")
	got := closeLogFile(f, "mcp", prior)
	require.Same(s.T(), prior, got)
}

func (s *MainSuite) TestCloseLogFileCloseError() {
	f, err := os.CreateTemp(s.T().TempDir(), "closeerr-*")
	require.NoError(s.T(), err)
	require.NoError(s.T(), f.Close()) // pre-close so the deferred Close returns an error
	got := closeLogFile(f, "mcp", nil)
	require.Error(s.T(), got)
	require.Contains(s.T(), got.Error(), "closing mcp log")
}
