package rules

import (
	"testing"

	"github.com/radutopala/loop/internal/quality/graph"
	"github.com/radutopala/loop/internal/quality/metrics"
	"github.com/radutopala/loop/internal/quality/parser"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type RulesSuite struct {
	suite.Suite
}

func TestRulesSuite(t *testing.T) {
	suite.Run(t, new(RulesSuite))
}

// --- helpers ---

func acyclicGraph() *graph.Graph {
	return graph.Build([]*parser.FileFacts{
		{Path: "a.go", Language: "go", LOC: 10, Imports: []parser.Import{{Path: "b.go"}}},
		{Path: "b.go", Language: "go", LOC: 10},
	})
}

func cyclicGraph() *graph.Graph {
	return graph.Build([]*parser.FileFacts{
		{Path: "a.go", Language: "go", LOC: 10, Imports: []parser.Import{{Path: "b.go"}}},
		{Path: "b.go", Language: "go", LOC: 10, Imports: []parser.Import{{Path: "a.go"}}},
	})
}

func parseFailedGraph(failed, ok int) *graph.Graph {
	facts := make([]*parser.FileFacts, 0, failed+ok)
	for i := range failed {
		facts = append(facts, &parser.FileFacts{Path: pathOf("bad", i), ParseFailed: true})
	}
	for i := range ok {
		facts = append(facts, &parser.FileFacts{Path: pathOf("ok", i), Language: "go", LOC: 1})
	}
	return graph.Build(facts)
}

func pathOf(prefix string, i int) string {
	return prefix + "_" + itoa(i) + ".go"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// --- Run + DefaultConfig ---

func (s *RulesSuite) TestRunHealthyGraphAllPass() {
	g := acyclicGraph()
	// Hand-built signal so we exercise all-pass without depending on the
	// metrics package's zero-floor semantics on tiny graphs.
	sig := metrics.Signal{Value: 6000}
	results := Run(DefaultConfig(), g, sig)
	require.Len(s.T(), results, 3)
	require.False(s.T(), AnyFailed(results))
	// Sorted by name asc.
	require.Equal(s.T(), NoImportCycles, results[0].Name)
	require.Equal(s.T(), ParseFail, results[1].Name)
	require.Equal(s.T(), SignalFloor, results[2].Name)
}

func (s *RulesSuite) TestAnyFailed() {
	require.False(s.T(), AnyFailed([]Result{{Severity: SevPass}}))
	require.True(s.T(), AnyFailed([]Result{{Severity: SevPass}, {Severity: SevFail}}))
}

func (s *RulesSuite) TestDefaultConfig() {
	cfg := DefaultConfig()
	require.True(s.T(), cfg.Rules[NoImportCycles].Enabled)
	require.True(s.T(), cfg.Rules[SignalFloor].Enabled)
	require.Equal(s.T(), SignalFloorDefault, cfg.Rules[SignalFloor].Threshold)
	require.Equal(s.T(), ParseFailMaxDefault, cfg.Rules[ParseFail].Threshold)
}

// --- no_import_cycles ---

func (s *RulesSuite) TestNoImportCyclesPasses() {
	g := acyclicGraph()
	sig := metrics.Compute(g)
	results := Run(DefaultConfig(), g, sig)
	r := findResult(results, NoImportCycles)
	require.Equal(s.T(), SevPass, r.Severity)
	require.Empty(s.T(), r.Citations)
	require.Equal(s.T(), "no import cycles detected", r.Message)
}

func (s *RulesSuite) TestNoImportCyclesFailsWithCitations() {
	g := cyclicGraph()
	sig := metrics.Compute(g)
	results := Run(DefaultConfig(), g, sig)
	r := findResult(results, NoImportCycles)
	require.Equal(s.T(), SevFail, r.Severity)
	require.Contains(s.T(), r.Message, "import cycle")
	// Both files cited.
	paths := citationPaths(r.Citations)
	require.Contains(s.T(), paths, "a.go")
	require.Contains(s.T(), paths, "b.go")
}

func (s *RulesSuite) TestNoImportCyclesDisabled() {
	cfg := DefaultConfig()
	cfg.Rules[NoImportCycles] = RuleConfig{Enabled: false}
	g := cyclicGraph()
	sig := metrics.Compute(g)
	results := Run(cfg, g, sig)
	r := findResult(results, NoImportCycles)
	require.Equal(s.T(), SevPass, r.Severity)
	require.Equal(s.T(), "rule disabled", r.Message)
}

func (s *RulesSuite) TestNoImportCyclesMissingMetricPasses() {
	// Hand-built signal with no cycles metric → vacuous pass.
	sig := metrics.Signal{Metrics: []metrics.Result{{Name: "other"}}}
	results := Run(DefaultConfig(), &graph.Graph{}, sig)
	r := findResult(results, NoImportCycles)
	require.Equal(s.T(), SevPass, r.Severity)
}

func (s *RulesSuite) TestNoImportCyclesWrongDetailTypePassesVacuously() {
	// The cycles metric is present but its Detail is the wrong type —
	// rules engine must not panic; treat as "no detail → no cycles".
	sig := metrics.Signal{Metrics: []metrics.Result{{Name: metrics.CyclesName, Detail: "not a CyclesDetail"}}}
	results := Run(DefaultConfig(), &graph.Graph{}, sig)
	r := findResult(results, NoImportCycles)
	require.Equal(s.T(), SevPass, r.Severity)
}

// --- signal_floor ---

func (s *RulesSuite) TestSignalFloorPassesAtThreshold() {
	sig := metrics.Signal{Value: 5000}
	results := Run(DefaultConfig(), &graph.Graph{}, sig)
	r := findResult(results, SignalFloor)
	require.Equal(s.T(), SevPass, r.Severity)
}

func (s *RulesSuite) TestSignalFloorFailsBelowThreshold() {
	sig := metrics.Signal{Value: 4999}
	results := Run(DefaultConfig(), &graph.Graph{}, sig)
	r := findResult(results, SignalFloor)
	require.Equal(s.T(), SevFail, r.Severity)
	require.Contains(s.T(), r.Message, "below floor")
}

func (s *RulesSuite) TestSignalFloorRespectsCustomThreshold() {
	cfg := DefaultConfig()
	cfg.Rules[SignalFloor] = RuleConfig{Enabled: true, Threshold: 7000}
	sig := metrics.Signal{Value: 6500}
	results := Run(cfg, &graph.Graph{}, sig)
	r := findResult(results, SignalFloor)
	require.Equal(s.T(), SevFail, r.Severity)
	require.Contains(s.T(), r.Message, "7000")
}

func (s *RulesSuite) TestSignalFloorDisabled() {
	cfg := DefaultConfig()
	cfg.Rules[SignalFloor] = RuleConfig{Enabled: false}
	sig := metrics.Signal{Value: 0}
	results := Run(cfg, &graph.Graph{}, sig)
	r := findResult(results, SignalFloor)
	require.Equal(s.T(), SevPass, r.Severity)
	require.Equal(s.T(), "rule disabled", r.Message)
}

// --- parse_fail ---

func (s *RulesSuite) TestParseFailPassesWithinTolerance() {
	g := parseFailedGraph(0, 100)
	results := Run(DefaultConfig(), g, metrics.Signal{})
	r := findResult(results, ParseFail)
	require.Equal(s.T(), SevPass, r.Severity)
}

func (s *RulesSuite) TestParseFailFailsAboveTolerance() {
	// Default 1%; 5/100 = 5% → fail.
	g := parseFailedGraph(5, 95)
	results := Run(DefaultConfig(), g, metrics.Signal{})
	r := findResult(results, ParseFail)
	require.Equal(s.T(), SevFail, r.Severity)
	require.Len(s.T(), r.Citations, 5)
}

func (s *RulesSuite) TestParseFailExactlyAtBoundaryPasses() {
	// 1/100 = 1.0% — equal to threshold 0.01, NOT greater than. Pass.
	g := parseFailedGraph(1, 99)
	results := Run(DefaultConfig(), g, metrics.Signal{})
	r := findResult(results, ParseFail)
	require.Equal(s.T(), SevPass, r.Severity)
}

func (s *RulesSuite) TestParseFailEmptyGraphPasses() {
	g := &graph.Graph{}
	results := Run(DefaultConfig(), g, metrics.Signal{})
	r := findResult(results, ParseFail)
	require.Equal(s.T(), SevPass, r.Severity)
	require.Equal(s.T(), "no files scanned", r.Message)
}

func (s *RulesSuite) TestParseFailDisabled() {
	cfg := DefaultConfig()
	cfg.Rules[ParseFail] = RuleConfig{Enabled: false}
	g := parseFailedGraph(50, 50)
	results := Run(cfg, g, metrics.Signal{})
	r := findResult(results, ParseFail)
	require.Equal(s.T(), SevPass, r.Severity)
	require.Equal(s.T(), "rule disabled", r.Message)
}

func (s *RulesSuite) TestParseFailRespectsCustomThreshold() {
	cfg := DefaultConfig()
	cfg.Rules[ParseFail] = RuleConfig{Enabled: true, Threshold: 0.10} // 10%
	g := parseFailedGraph(5, 95)                                      // 5%
	results := Run(cfg, g, metrics.Signal{})
	r := findResult(results, ParseFail)
	require.Equal(s.T(), SevPass, r.Severity)
}

// --- empty Config falls back to enabled defaults ---

func (s *RulesSuite) TestRuleEnabledFallback() {
	cfg := Config{Rules: map[string]RuleConfig{}}
	g := acyclicGraph()
	sig := metrics.Compute(g)
	results := Run(cfg, g, sig)
	for _, r := range results {
		require.NotEqual(s.T(), "rule disabled", r.Message, "%s should be enabled by default", r.Name)
	}
	// Spot-check that fallback wraps a rule with no entry — drop signal_floor
	// from the map entirely and confirm it still runs (won't pass on a tiny
	// graph but also won't be marked disabled).
	cfg2 := Config{Rules: map[string]RuleConfig{NoImportCycles: {Enabled: true}}}
	results2 := Run(cfg2, g, sig)
	r := findResult(results2, SignalFloor)
	require.NotEqual(s.T(), "rule disabled", r.Message)
}

func (s *RulesSuite) TestRuleThresholdFallbackOnZeroOverride() {
	cfg := DefaultConfig()
	// Threshold=0 (zero value) → fall back to default. Disabled flag
	// would have to be set to opt out.
	cfg.Rules[SignalFloor] = RuleConfig{Enabled: true, Threshold: 0}
	sig := metrics.Signal{Value: 4999}
	results := Run(cfg, &graph.Graph{}, sig)
	r := findResult(results, SignalFloor)
	require.Equal(s.T(), SevFail, r.Severity)
	require.Contains(s.T(), r.Message, "5000")
}

// --- helpers ---

func findResult(rs []Result, name string) Result {
	for _, r := range rs {
		if r.Name == name {
			return r
		}
	}
	return Result{}
}

func citationPaths(cs []Citation) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Path)
	}
	return out
}
