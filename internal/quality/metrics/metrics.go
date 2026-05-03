package metrics

// Result is the per-metric output. Raw is the underlying number (e.g.
// the Q value, the longest-DAG depth, the Gini coefficient); Score is
// the normalised 0.0-1.0 form the geo-mean aggregator consumes — 1.0
// means "this metric is healthy", 0.0 means "this metric is at the
// worst observable level". Detail carries human-readable, per-metric
// payload (cycle members, hotspot files, …) the diagnostics surface
// renders verbatim.
type Result struct {
	Name   string
	Raw    float64
	Score  float64
	Detail any
}
