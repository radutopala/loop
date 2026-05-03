// Package rules evaluates the built-in structural-quality rules a scan
// must satisfy. The CLI surfaces failures via JSON output; the panel
// renders them as red rule cards with file/line citations.
//
// Three built-ins ship at this milestone:
//
//   - no_import_cycles: fails if any SCC contains more than one file.
//   - signal_floor:     fails if quality_signal < threshold (default 5000).
//   - parse_fail:       fails if parse-failed files / scanned files
//     exceeds a fraction (default 0.01 = 1%).
//
// Per-rule overrides map to project-config keys quality.rules.<name>.*.
// The rule list itself is implicit — at this milestone there is no
// project-defined rule format. Future PRs may add a custom-rule path.
package rules
