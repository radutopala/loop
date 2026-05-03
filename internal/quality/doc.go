// Package quality is a clean-room Go implementation of an architectural
// quality sensor. Design inspired by sentrux (Rust, MIT,
// https://github.com/sentrux/sentrux); algorithms are independent
// re-implementations.
//
// The engine reduces a workspace to one continuous quality signal in the
// 0–10000 band, computed as the geometric mean of five root-cause graph
// metrics: modularity (Newman's Q), acyclicity (Tarjan SCC), depth (Lakos
// levelization), equality (Gini coefficient over per-function complexity),
// and redundancy (dead and duplicate code).
//
// The parser is github.com/odvcencio/gotreesitter (pure-Go tree-sitter,
// MIT), with active grammars pinned in grammars.go. Sub-packages cover the
// graph builder, metrics, snapshot persistence, rules engine, agentgate
// write hub, and the engine entry point that orchestrates a scan.
package quality
