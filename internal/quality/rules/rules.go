package rules

import (
	"fmt"
	"sort"

	"github.com/radutopala/loop/internal/quality/graph"
	"github.com/radutopala/loop/internal/quality/metrics"
)

// Severity reports a rule's pass/fail status. The CLI exits 0 regardless
// of severity (rule status is data, not behaviour, mirroring gofmt's
// philosophy). Callers gate CI with their own parsers (jq, etc.).
type Severity string

const (
	// SevPass means the rule's invariant holds against the current scan.
	SevPass Severity = "pass"
	// SevFail means the invariant is violated; Citations point to the
	// offending files/lines for the panel and rules MCP tool to render.
	SevFail Severity = "fail"
)

// Citation is one file/line span the rule wants the user to see.
type Citation struct {
	Path      string
	StartLine int
	EndLine   int
	Note      string
}

// Result is one rule's outcome from one scan.
type Result struct {
	Name      string
	Severity  Severity
	Message   string
	Citations []Citation
}

// RuleConfig carries the per-rule overrides resolved from the project
// config (quality.rules.<name>.{threshold,enabled}). Threshold is the
// generic knob — rules that have no numeric threshold ignore it.
type RuleConfig struct {
	Enabled   bool
	Threshold float64
}

// Config is the full set of per-rule overrides. Missing entries fall
// back to the rule's hard-coded default.
type Config struct {
	Rules map[string]RuleConfig
}

// SignalFloorDefault is the lower bound on quality_signal that the
// signal_floor rule treats as healthy. Tunable via
// quality.rules.signal_floor.threshold.
const SignalFloorDefault = 5000.0

// ParseFailMaxDefault is the maximum fraction of parse-failed files the
// parse_fail rule tolerates. 0.01 = 1%. Tunable via
// quality.rules.parse_fail.threshold.
const ParseFailMaxDefault = 0.01

// ComplexityCeilingDefault is the maximum number of functions allowed to
// breach the complexity soft thresholds before the complexity_ceiling
// rule fails. Tunable via quality.rules.complexity_ceiling.threshold.
const ComplexityCeilingDefault = 10.0

// ComplexityScoreFloorDefault is the lower bound on the complexity
// metric score that the complexity_score_floor rule treats as healthy.
// Tunable via quality.rules.complexity_score_floor.threshold.
const ComplexityScoreFloorDefault = 0.5

// DuplicationCeilingDefault is the maximum tolerated fraction of
// duplicated_LOC over total_LOC the duplication_ceiling rule allows.
// 0.10 = 10%. Tunable via quality.rules.duplication_ceiling.threshold.
const DuplicationCeilingDefault = 0.10

// Built-in rule names. Exported so callers can build Config maps without
// stringly-typing the keys.
const (
	NoImportCycles       = "no_import_cycles"
	SignalFloor          = "signal_floor"
	ParseFail            = "parse_fail"
	ComplexityCeiling    = "complexity_ceiling"
	ComplexityScoreFloor = "complexity_score_floor"
	DuplicationCeiling   = "duplication_ceiling"
)

// Run evaluates every built-in rule against (g, sig) using cfg as the
// override source. Results come back in a stable order (rule-name asc)
// so panel renders and JSON outputs are deterministic.
func Run(cfg Config, g *graph.Graph, sig metrics.Signal) []Result {
	results := []Result{
		evalNoImportCycles(cfg, sig),
		evalSignalFloor(cfg, sig),
		evalParseFail(cfg, g),
		evalComplexityCeiling(cfg, sig),
		evalComplexityScoreFloor(cfg, sig),
		evalDuplicationCeiling(cfg, sig),
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Name < results[j].Name })
	return results
}

func ruleEnabled(cfg Config, name string) bool {
	if rc, ok := cfg.Rules[name]; ok {
		return rc.Enabled
	}
	return true
}

func ruleThreshold(cfg Config, name string, fallback float64) float64 {
	if rc, ok := cfg.Rules[name]; ok && rc.Threshold > 0 {
		return rc.Threshold
	}
	return fallback
}

func evalNoImportCycles(cfg Config, sig metrics.Signal) Result {
	r := Result{Name: NoImportCycles, Severity: SevPass, Message: "no import cycles detected"}
	if !ruleEnabled(cfg, NoImportCycles) {
		r.Message = "rule disabled"
		return r
	}
	detail, ok := metricDetail[metrics.CyclesDetail](sig, metrics.CyclesName)
	if !ok || len(detail.Cycles) == 0 {
		return r
	}
	r.Severity = SevFail
	r.Message = fmt.Sprintf("%d import cycle(s) detected; largest contains %d files", len(detail.Cycles), detail.LargestCycleSize)
	for _, scc := range detail.Cycles {
		for _, p := range scc {
			r.Citations = append(r.Citations, Citation{Path: p, Note: "in cycle"})
		}
	}
	return r
}

func evalSignalFloor(cfg Config, sig metrics.Signal) Result {
	threshold := ruleThreshold(cfg, SignalFloor, SignalFloorDefault)
	r := Result{Name: SignalFloor, Severity: SevPass, Message: fmt.Sprintf("quality_signal=%d ≥ %.0f", sig.Value, threshold)}
	if !ruleEnabled(cfg, SignalFloor) {
		r.Message = "rule disabled"
		return r
	}
	if float64(sig.Value) < threshold {
		r.Severity = SevFail
		r.Message = fmt.Sprintf("quality_signal=%d below floor %.0f", sig.Value, threshold)
	}
	return r
}

func evalParseFail(cfg Config, g *graph.Graph) Result {
	threshold := ruleThreshold(cfg, ParseFail, ParseFailMaxDefault)
	r := Result{Name: ParseFail, Severity: SevPass}
	if !ruleEnabled(cfg, ParseFail) {
		r.Message = "rule disabled"
		return r
	}
	total := len(g.Nodes)
	if total == 0 {
		r.Message = "no files scanned"
		return r
	}
	frac := float64(g.ParseFailed) / float64(total)
	r.Message = fmt.Sprintf("%d/%d files failed to parse (%.2f%%, max %.2f%%)", g.ParseFailed, total, frac*100, threshold*100)
	if frac > threshold {
		r.Severity = SevFail
		for _, n := range g.Nodes {
			if n.ParseFailed {
				r.Citations = append(r.Citations, Citation{Path: n.Path, Note: "parse failed"})
			}
		}
	}
	return r
}

func evalComplexityCeiling(cfg Config, sig metrics.Signal) Result {
	threshold := ruleThreshold(cfg, ComplexityCeiling, ComplexityCeilingDefault)
	r := Result{Name: ComplexityCeiling, Severity: SevPass}
	if !ruleEnabled(cfg, ComplexityCeiling) {
		r.Message = "rule disabled"
		return r
	}
	detail, ok := metricDetail[metrics.ComplexityDetail](sig, metrics.ComplexityName)
	if !ok {
		r.Message = "complexity metric absent"
		return r
	}
	r.Message = fmt.Sprintf("%d/%d functions over complexity thresholds (max %.0f)",
		detail.OverThreshold, detail.TotalFunctions, threshold)
	if float64(detail.OverThreshold) > threshold {
		r.Severity = SevFail
		for _, f := range detail.Functions {
			if f.Score >= 1.0 {
				continue
			}
			r.Citations = append(r.Citations, Citation{
				Path:      f.Path,
				StartLine: f.StartLine,
				Note:      fmt.Sprintf("%s — score %.2f", f.Name, f.Score),
			})
		}
	}
	return r
}

func evalComplexityScoreFloor(cfg Config, sig metrics.Signal) Result {
	threshold := ruleThreshold(cfg, ComplexityScoreFloor, ComplexityScoreFloorDefault)
	r := Result{Name: ComplexityScoreFloor, Severity: SevPass}
	if !ruleEnabled(cfg, ComplexityScoreFloor) {
		r.Message = "rule disabled"
		return r
	}
	res, ok := metricResult(sig, metrics.ComplexityName)
	if !ok {
		r.Message = "complexity metric absent"
		return r
	}
	r.Message = fmt.Sprintf("complexity score %.3f ≥ floor %.3f", res.Score, threshold)
	if res.Score < threshold {
		r.Severity = SevFail
		r.Message = fmt.Sprintf("complexity score %.3f below floor %.3f", res.Score, threshold)
		if detail, ok := res.Detail.(metrics.ComplexityDetail); ok {
			for _, f := range detail.Functions {
				if f.Score >= 1.0 {
					continue
				}
				r.Citations = append(r.Citations, Citation{
					Path:      f.Path,
					StartLine: f.StartLine,
					Note:      fmt.Sprintf("%s — score %.2f", f.Name, f.Score),
				})
			}
		}
	}
	return r
}

func evalDuplicationCeiling(cfg Config, sig metrics.Signal) Result {
	threshold := ruleThreshold(cfg, DuplicationCeiling, DuplicationCeilingDefault)
	r := Result{Name: DuplicationCeiling, Severity: SevPass}
	if !ruleEnabled(cfg, DuplicationCeiling) {
		r.Message = "rule disabled"
		return r
	}
	detail, ok := metricDetail[metrics.RedundancyDetail](sig, metrics.RedundancyName)
	if !ok {
		r.Message = "redundancy metric absent"
		return r
	}
	clones := detail.Clones
	if clones.TotalLOC == 0 {
		r.Message = "no clone-eligible functions"
		return r
	}
	frac := float64(clones.DuplicatedLOC) / float64(clones.TotalLOC)
	r.Message = fmt.Sprintf("duplicated %d/%d LOC (%.2f%%, max %.2f%%)",
		clones.DuplicatedLOC, clones.TotalLOC, frac*100, threshold*100)
	if frac > threshold {
		r.Severity = SevFail
		for _, cl := range clones.Clusters {
			for _, m := range cl.Members {
				r.Citations = append(r.Citations, Citation{
					Path:      m.Path,
					StartLine: m.StartLine,
					EndLine:   m.EndLine,
					Note:      fmt.Sprintf("%s — clone cluster (LOC %d)", m.Name, m.LOC),
				})
			}
		}
	}
	return r
}

// metricResult finds the named metric's Result entry. Returns
// (zero, false) on miss — rules treat that as "metric absent → vacuous
// pass" with an explanatory message.
func metricResult(sig metrics.Signal, name string) (metrics.Result, bool) {
	for _, m := range sig.Metrics {
		if m.Name == name {
			return m, true
		}
	}
	return metrics.Result{}, false
}

// metricDetail finds the named metric's Detail and asserts it to T.
// Returns (zero, false) on miss or type mismatch — rules treat that as
// "metric absent → rule passes vacuously".
func metricDetail[T any](sig metrics.Signal, name string) (T, bool) {
	for _, m := range sig.Metrics {
		if m.Name != name {
			continue
		}
		if d, ok := m.Detail.(T); ok {
			return d, true
		}
		var zero T
		return zero, false
	}
	var zero T
	return zero, false
}

// DefaultConfig returns a Config with every built-in enabled at default
// thresholds. Callers merge in project-config overrides on top.
func DefaultConfig() Config {
	return Config{Rules: map[string]RuleConfig{
		NoImportCycles:       {Enabled: true},
		SignalFloor:          {Enabled: true, Threshold: SignalFloorDefault},
		ParseFail:            {Enabled: true, Threshold: ParseFailMaxDefault},
		ComplexityCeiling:    {Enabled: true, Threshold: ComplexityCeilingDefault},
		ComplexityScoreFloor: {Enabled: true, Threshold: ComplexityScoreFloorDefault},
		DuplicationCeiling:   {Enabled: true, Threshold: DuplicationCeilingDefault},
	}}
}

// AnyFailed is a convenience for "did this scan trip a rule?". Used by
// the rules MCP tool's structured failure summary.
func AnyFailed(results []Result) bool {
	for _, r := range results {
		if r.Severity == SevFail {
			return true
		}
	}
	return false
}
