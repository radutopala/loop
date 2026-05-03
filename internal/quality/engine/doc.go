// Package engine is the synchronous entry point that orchestrates a full
// quality scan: enumerate files → parse each → build the graph → compute
// metrics → persist the snapshot. It is the bridge between the per-channel
// trigger surfaces (MCP scan tool, "Scan now" button, CLI subcommand) and
// the lower-level packages they all use.
//
// The engine coalesces concurrent scans per channel: if a scan is already
// running for a given channelID, a second trigger returns immediately with
// InProgress=true rather than queueing or canceling the in-flight call.
package engine
