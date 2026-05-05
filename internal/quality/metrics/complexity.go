package metrics

import (
	"sort"

	"github.com/radutopala/loop/internal/quality/graph"
)

// ComplexityName is the canonical key for the function-complexity metric.
const ComplexityName = "complexity"

// ComplexityConfig carries the soft thresholds the per-function score
// curve uses. Each dimension scores 1.0 at or below T, decaying linearly
// to 0 at 2·T (a doubled threshold gets the worst score).
//
// Defaults match the published soft norms: cyclomatic 10 (McCabe),
// cognitive 15 (Sonar), nesting 4 (Sonar), params 5 (Clean Code), LOC 60.
type ComplexityConfig struct {
	CyclomaticT int
	CognitiveT  int
	NestingT    int
	ParamsT     int
	LOCT        int
}

// DefaultComplexityConfig returns the soft thresholds used when no
// project-level overrides exist.
func DefaultComplexityConfig() ComplexityConfig {
	return ComplexityConfig{
		CyclomaticT: 10,
		CognitiveT:  15,
		NestingT:    4,
		ParamsT:     5,
		LOCT:        60,
	}
}

// ComplexityDetail is the panel-facing payload: top offenders for the
// diagnostics list plus a per-dimension histogram for the trend chart.
type ComplexityDetail struct {
	// Functions is the capped offender list, sorted by Score asc (worst
	// first), tie-broken by (Path, StartLine).
	Functions []FuncComplexity

	// TotalFunctions is every function definition seen with Body != nil.
	TotalFunctions int

	// OverThreshold is the count exceeding any soft threshold (per-dim
	// score below 1.0). Same shape as RedundancyDetail.DeadCount.
	OverThreshold int

	// Histogram counts functions per (dimension, bucket). Buckets are
	// "ok" (≤ T), "warn" (T..2T), "crit" (>2T). Stable for trend charts.
	Histogram map[string]map[string]int
}

// FuncComplexity is one function's per-dimension snapshot.
type FuncComplexity struct {
	Path       string
	Name       string
	StartLine  int
	Cyclomatic int
	Cognitive  int
	MaxNesting int
	ParamCount int
	LOC        int
	Score      float64
}

const complexityHotspotCap = 100

// ComputeComplexity reduces per-function body data to a single score.
// Per-function score is the minimum of five per-dimension scores
// (cyclomatic, cognitive, nesting, params, LOC), each on a 1.0-down-to-0
// curve seeded at the soft threshold. The metric score is the LOC-weighted
// mean across all functions with body data — large complex functions pull
// the score down harder than small ones.
//
// Functions without Body data (parse-failed languages, trivial bodies)
// are skipped. Empty graphs and graphs with no body data return Score 1.0.
func ComputeComplexity(g *graph.Graph, cfg ComplexityConfig) Result {
	if g == nil || len(g.Nodes) == 0 {
		return Result{Name: ComplexityName, Raw: 0, Score: 1.0, Detail: ComplexityDetail{Histogram: emptyHistogram()}}
	}

	all := make([]FuncComplexity, 0)
	histogram := emptyHistogram()
	for _, n := range g.Nodes {
		for _, f := range n.Functions {
			if f.Body == nil {
				continue
			}
			b := f.Body
			fc := FuncComplexity{
				Path:       n.Path,
				Name:       f.Name,
				StartLine:  f.StartLine,
				Cyclomatic: b.DecisionPoints,
				Cognitive:  b.CognitiveLoad,
				MaxNesting: b.MaxNesting,
				ParamCount: b.ParamCount,
				LOC:        b.LOC,
			}
			fc.Score = perFunctionScore(fc, cfg)
			all = append(all, fc)
			recordHistogram(histogram, fc, cfg)
		}
	}
	if len(all) == 0 {
		return Result{Name: ComplexityName, Raw: 0, Score: 1.0, Detail: ComplexityDetail{Histogram: histogram}}
	}

	// LOC-weighted mean of per-function scores.
	var weighted, totalLOC float64
	overThreshold := 0
	for _, fc := range all {
		w := float64(fc.LOC)
		if w <= 0 {
			w = 1
		}
		weighted += fc.Score * w
		totalLOC += w
		if fc.Score < 1.0 {
			overThreshold++
		}
	}
	score := weighted / totalLOC

	sort.Slice(all, func(i, j int) bool {
		if all[i].Score != all[j].Score {
			return all[i].Score < all[j].Score
		}
		if all[i].Path != all[j].Path {
			return all[i].Path < all[j].Path
		}
		return all[i].StartLine < all[j].StartLine
	})
	listed := all
	if len(listed) > complexityHotspotCap {
		listed = listed[:complexityHotspotCap]
	}

	return Result{
		Name:  ComplexityName,
		Raw:   float64(overThreshold),
		Score: clamp01(score),
		Detail: ComplexityDetail{
			Functions:      listed,
			TotalFunctions: len(all),
			OverThreshold:  overThreshold,
			Histogram:      histogram,
		},
	}
}

// perFunctionScore is the worst per-dimension score: a single bad axis
// dominates so the panel surface flags the right finding.
func perFunctionScore(fc FuncComplexity, cfg ComplexityConfig) float64 {
	scores := []float64{
		dimScore(fc.Cyclomatic, cfg.CyclomaticT),
		dimScore(fc.Cognitive, cfg.CognitiveT),
		dimScore(fc.MaxNesting, cfg.NestingT),
		dimScore(fc.ParamCount, cfg.ParamsT),
		dimScore(fc.LOC, cfg.LOCT),
	}
	min := 1.0
	for _, s := range scores {
		if s < min {
			min = s
		}
	}
	return min
}

// dimScore maps a raw value against threshold T to a [0, 1] score.
// Values at or below T score 1.0; values at 2·T score 0; the curve is
// linear in between. T <= 0 disables the dimension (always 1.0).
func dimScore(raw, t int) float64 {
	if t <= 0 {
		return 1.0
	}
	if raw <= t {
		return 1.0
	}
	if raw >= 2*t {
		return 0.0
	}
	return 1.0 - float64(raw-t)/float64(t)
}

func emptyHistogram() map[string]map[string]int {
	dims := []string{"cyclomatic", "cognitive", "nesting", "params", "loc"}
	out := make(map[string]map[string]int, len(dims))
	for _, d := range dims {
		out[d] = map[string]int{"ok": 0, "warn": 0, "crit": 0}
	}
	return out
}

func recordHistogram(h map[string]map[string]int, fc FuncComplexity, cfg ComplexityConfig) {
	bumpBucket(h["cyclomatic"], fc.Cyclomatic, cfg.CyclomaticT)
	bumpBucket(h["cognitive"], fc.Cognitive, cfg.CognitiveT)
	bumpBucket(h["nesting"], fc.MaxNesting, cfg.NestingT)
	bumpBucket(h["params"], fc.ParamCount, cfg.ParamsT)
	bumpBucket(h["loc"], fc.LOC, cfg.LOCT)
}

func bumpBucket(m map[string]int, raw, t int) {
	switch {
	case t <= 0 || raw <= t:
		m["ok"]++
	case raw < 2*t:
		m["warn"]++
	default:
		m["crit"]++
	}
}
