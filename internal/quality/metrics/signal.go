package metrics

import (
	"math"

	"github.com/radutopala/loop/internal/quality/graph"
)

// SignalName is the panel-facing key for the aggregated quality signal.
const SignalName = "quality_signal"

// SignalScale projects the [0, 1] geometric mean onto a 0–10000 band,
// matching the sentrux convention so the panel and rules engine can
// share thresholds.
const SignalScale = 10000

// Signal is the aggregated structural-health number plus its per-metric
// breakdown. The panel renders Value as the headline number and Metrics
// as the row of small metric cards.
type Signal struct {
	// Value is the rounded geometric mean × SignalScale; integer, 0–10000.
	Value int

	// GeoMean is the unrounded geometric mean of Metric scores; 0.0–1.0.
	GeoMean float64

	// Metrics is the per-metric results in their canonical order
	// (modularity, cycles, depth, equality, redundancy, complexity).
	// Same slice the caller passed to Aggregate, kept here so consumers
	// don't have to thread results through both APIs.
	Metrics []Result

	// Tiles is the per-file deficit projection rendered as the panel's
	// treemap. Populated by Compute (which runs AttributeFiles); empty
	// when Aggregate is called directly without the graph context.
	Tiles []FileTile
}

// Aggregate folds per-metric results into a single Signal via the
// geometric mean of their Score fields. Empty input is treated as
// vacuously healthy (Signal at SignalScale).
//
// The geometric mean is the right shape here because each metric is
// independently "healthy or not" on a [0, 1] scale — a single bad
// metric pulls the signal down hard, while the arithmetic mean would
// let one perfect metric mask another's failure. If any metric scores
// 0, the signal goes to 0 — that's the correct answer; the panel
// should make that visible rather than smoothing it over.
func Aggregate(results []Result) Signal {
	if len(results) == 0 {
		return Signal{Value: SignalScale, GeoMean: 1.0}
	}
	prod := 1.0
	for _, r := range results {
		prod *= r.Score
	}
	geo := math.Pow(prod, 1.0/float64(len(results)))
	return Signal{
		Value:   int(math.Round(geo * float64(SignalScale))),
		GeoMean: geo,
		Metrics: results,
	}
}

// Config carries the threshold knobs ComputeWith propagates into the
// per-metric routines that take config (currently complexity and
// clones). Pass DefaultConfig() when no project-level overrides exist.
type Config struct {
	Complexity ComplexityConfig
	Clones     ClonesConfig
}

// DefaultConfig returns the production defaults, equivalent to calling
// each metric's individual default constructor.
func DefaultConfig() Config {
	return Config{
		Complexity: DefaultComplexityConfig(),
		Clones:     DefaultClonesConfig(),
	}
}

// Compute is the convenience entry point: runs all metrics on g with
// default thresholds and returns the aggregated Signal. Useful for the
// snapshot package, the MCP scan tool, and the CLI.
func Compute(g *graph.Graph) Signal {
	return ComputeWith(g, DefaultConfig())
}

// ComputeWith runs all metrics with caller-supplied thresholds. Used by
// the engine when project-level config overrides default thresholds.
func ComputeWith(g *graph.Graph, cfg Config) Signal {
	results := []Result{
		Modularity(g),
		Cycles(g),
		Depth(g),
		Equality(g),
		RedundancyWith(g, cfg.Clones),
		ComputeComplexity(g, cfg.Complexity),
	}
	sig := Aggregate(results)
	sig.Tiles = AttributeFiles(g, results)
	return sig
}
