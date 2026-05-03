// Package metrics reduces a *graph.Graph to the five structural numbers
// the engine combines into a single quality_signal: modularity (Newman's
// Q), cycle pressure (Tarjan SCC), import-DAG depth (Lakos), file-size
// equality (Gini), and redundancy (dead code + duplicates). Each metric
// is independently testable; the per-metric Score normalises the raw
// number to a 0.0-1.0 scale so the aggregator can take a geometric mean
// without per-metric weighting hacks.
package metrics
