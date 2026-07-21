package main

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/agentgate"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/orchestrator"
	"github.com/radutopala/loop/internal/quality/rules"
	"github.com/radutopala/loop/internal/types"
)

// ServeSuite covers the wireGatePolicy helper on *app. With agentgate stage-2
// the host side no longer opens a listener or runs a watchdog — policy is
// compiled once at startup, stored on the runner, and serialised into each
// container's policyDir at spawn time. These tests pin that contract: enable
// branch stores the policy + policyDir on the runner; compile-error branch
// logs and leaves the runner untouched; disable branch is a no-op.
type ServeSuite struct {
	suite.Suite
	app *app
}

func TestServeSuite(t *testing.T) {
	suite.Run(t, new(ServeSuite))
}

func (s *ServeSuite) SetupTest() {
	s.app = newApp()
}

func (s *ServeSuite) newRunner() *container.DockerRunner {
	dc := new(mockDockerClient)
	return container.NewDockerRunner(dc, &config.Config{}, func() (*config.Config, error) {
		return &config.Config{}, nil
	})
}

// captureLogger returns a slog.Logger whose output is captured in buf. Used
// to assert that wireGatePolicy routes compile errors through to the logger.
func captureLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(h), &buf
}

func gateCfg(enabled bool) *config.Config {
	return &config.Config{
		Gates: config.GatesConfig{
			Agentgate: config.AgentgateConfig{
				Enabled:         enabled,
				DefaultDecision: types.DecisionAllow,
			},
		},
	}
}

// --- containerApprovalAdapter ---

func (s *ServeSuite) TestContainerApprovalAdapterHitReturnsManager() {
	r := agentgate.NewMultiManagerResolver()
	m := agentgate.NewManager(&orchestrator.GateBotRouter{Bot: nil}, types.RateLimits{})
	r.AddWithToken("cid-1", "tok-1", m, "ch-1")

	adapter := containerApprovalAdapter{r: r}
	cid, mgr, channelID, ok := adapter.ByToken("tok-1")
	require.True(s.T(), ok)
	require.Equal(s.T(), "cid-1", cid)
	require.Equal(s.T(), "ch-1", channelID)
	require.NotNil(s.T(), mgr)
}

func (s *ServeSuite) TestContainerApprovalAdapterMissReturnsNilManager() {
	r := agentgate.NewMultiManagerResolver()
	adapter := containerApprovalAdapter{r: r}
	cid, mgr, channelID, ok := adapter.ByToken("missing")
	require.False(s.T(), ok)
	require.Empty(s.T(), cid)
	require.Nil(s.T(), mgr)
	require.Empty(s.T(), channelID)
}

// --- wireGatePolicy: disabled branch ---

func (s *ServeSuite) TestWireGatePolicyDisabledIsNoOp() {
	cfg := gateCfg(false)
	logger, buf := captureLogger()
	runner := s.newRunner()

	s.app.wireGatePolicy(cfg, runner, "/tmp/loop-test/run", logger)

	require.Empty(s.T(), runner.PolicyDir(),
		"gate disabled → wireGatePolicy must not call SetGatePolicy so policyDir stays empty")
	require.Empty(s.T(), buf.String(), "gate disabled → no log output")
}

// --- wireGatePolicy: compile-error branch ---

func (s *ServeSuite) TestWireGatePolicyCompileErrorLogsAndSkips() {
	cfg := gateCfg(true)
	// Empty Pattern is rejected by CompilePolicy → "path_rules[0]: pattern is required".
	cfg.Gates.Agentgate.PathRules = []types.PathRule{{Pattern: "", Decision: types.DecisionDeny}}

	logger, buf := captureLogger()
	runner := s.newRunner()

	s.app.wireGatePolicy(cfg, runner, "/tmp/loop-test/run", logger)

	require.Empty(s.T(), runner.PolicyDir(),
		"compile error must route through to skip so runner stays on the gate-disabled path")
	require.Contains(s.T(), buf.String(), "gate policy compile failed")
	require.Contains(s.T(), buf.String(), "pattern is required")
}

// --- wireGatePolicy: happy path ---

// --- buildRulesConfig ---

// TestBuildRulesConfigEmptyReturnsNil: no project overrides → nil so the api
// server falls through to rules.DefaultConfig() at evaluation time.
func (s *ServeSuite) TestBuildRulesConfigEmptyReturnsNil() {
	require.Nil(s.T(), buildRulesConfig(nil))
	require.Nil(s.T(), buildRulesConfig(map[string]config.QualityRuleConfig{}))
}

// TestBuildRulesConfigAppliesOverrides: project overrides flip Enabled and
// raise Threshold while leaving the unmentioned default rule entries
// untouched at their DefaultConfig values.
func (s *ServeSuite) TestBuildRulesConfigAppliesOverrides() {
	cfg := buildRulesConfig(map[string]config.QualityRuleConfig{
		rules.SignalFloor:    {Enabled: true, Threshold: 6500},
		rules.NoImportCycles: {Enabled: false},
	})
	require.NotNil(s.T(), cfg)
	require.True(s.T(), cfg.Rules[rules.SignalFloor].Enabled)
	require.Equal(s.T(), 6500.0, cfg.Rules[rules.SignalFloor].Threshold)
	require.False(s.T(), cfg.Rules[rules.NoImportCycles].Enabled)
	require.True(s.T(), cfg.Rules[rules.ParseFail].Enabled)
	require.Equal(s.T(), rules.ParseFailMaxDefault, cfg.Rules[rules.ParseFail].Threshold)
}

// TestBuildRulesConfigZeroThresholdKeepsDefault: a project override that
// leaves Threshold at zero must NOT clobber the default value — zero is the
// "unset" sentinel, identical to omitting the field in JSON.
func (s *ServeSuite) TestBuildRulesConfigZeroThresholdKeepsDefault() {
	cfg := buildRulesConfig(map[string]config.QualityRuleConfig{
		rules.SignalFloor: {Enabled: true},
	})
	require.NotNil(s.T(), cfg)
	require.Equal(s.T(), rules.SignalFloorDefault, cfg.Rules[rules.SignalFloor].Threshold)
}

// TestBuildRulesConfigUnknownRuleSkipped: an override for a rule the engine
// doesn't ship is silently dropped — typos shouldn't synthesize new rules.
func (s *ServeSuite) TestBuildRulesConfigUnknownRuleSkipped() {
	cfg := buildRulesConfig(map[string]config.QualityRuleConfig{
		"made_up_rule": {Enabled: true, Threshold: 99},
	})
	require.NotNil(s.T(), cfg)
	_, exists := cfg.Rules["made_up_rule"]
	require.False(s.T(), exists)
}

func (s *ServeSuite) TestWireGatePolicyHappyPathPlumbsPolicyDirIntoRunner() {
	cfg := gateCfg(true)

	logger, _ := captureLogger()
	runner := s.newRunner()

	s.app.wireGatePolicy(cfg, runner, "/tmp/loop-test/run", logger)

	require.Equal(s.T(), "/tmp/loop-test/run", runner.PolicyDir(),
		"wireGatePolicy must forward policyDir to runner.SetGatePolicy so the per-container JSON file is written under ~/.loop/run, not /run/loop")
}

func (s *ServeSuite) TestLocalAPIURL() {
	cases := map[string]string{
		":8222":          "http://localhost:8222",
		"0.0.0.0:9000":   "http://localhost:9000",
		"[::]:8222":      "http://localhost:8222",
		"127.0.0.1:8222": "http://127.0.0.1:8222",
		"myhost:8222":    "http://myhost:8222",
		"not-an-addr":    "http://localhost:8222",
		"":               "http://localhost:8222",
	}
	for in, want := range cases {
		require.Equal(s.T(), want, localAPIURL(in), "input %q", in)
	}
}
