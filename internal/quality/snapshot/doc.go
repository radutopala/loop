// Package snapshot persists the most recent quality_signal value and
// per-metric breakdown for each (channel, branch) pair. One row is kept
// per pair — UPSERT on rescan — so switching branches does not lose
// the snapshot taken on the previous one. The panel reads the stored
// JSON breakdown verbatim; the rules engine and CLI compute fresh
// signals rather than reading from the snapshot store.
package snapshot
