package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
)

// reviewBodyNodeID is the well-known child ID inside a loop body whose
// stdout is parsed as `loop review run` JSON into runCtx.Review. The seeded
// review-loop and review-fix-loop workflows pin the bash node to this ID
// so the parser knows which child's output to interpret.
const reviewBodyNodeID = "review"

// reviewParsedWorkflows enumerates the workflow names whose body child
// `reviewBodyNodeID` is treated as the review CLI envelope. Scoped so a
// user-authored workflow that happens to name a bash child "review" doesn't
// have its stdout silently consumed and reshaped into runCtx.Review.
var reviewParsedWorkflows = map[string]struct{}{
	"review-loop":     {},
	"review-fix-loop": {},
}

// maxLoopIterations is an absolute server-side ceiling on a loop node's
// iteration count. The FE caps the input at 10; this guards against scripted
// callers that bypass the FE and pass a much larger value through the HTTP
// surface or a user-authored workflow with a runaway max_iterations.
const maxLoopIterations = 50

func (e *defaultEngine) executeLoopNode(ctx context.Context, run *db.WorkflowRun, node *config.NodeDef, runCtx *RunContext, mu *sync.Mutex) (string, error) {
	maxIter := node.MaxIterations
	if maxIter <= 0 {
		// Fall back to the `max_iterations` workflow input when the node
		// itself doesn't pin a value. The seeded review/review-fix loops
		// rely on this so the FE's max-iter input can drive the cap
		// without rewriting the workflow definition per request.
		mu.Lock()
		raw := runCtx.Inputs["max_iterations"]
		mu.Unlock()
		if raw != "" {
			if v, perr := strconv.Atoi(raw); perr == nil && v > 0 {
				maxIter = v
			}
		}
	}
	if maxIter <= 0 {
		maxIter = 10 // default
	}
	// Server-side absolute ceiling. The FE caps the input at 10, but the
	// runtime input is a free-form string from the HTTP body and a misuse
	// (or scripted caller) could pass an arbitrarily large value that pins
	// the executor goroutine forever. Anything above this is almost
	// certainly a typo — generously above the FE's 10 so the legitimate
	// path is unaffected.
	if maxIter > maxLoopIterations {
		maxIter = maxLoopIterations
	}

	hasBody := len(node.Body) > 0

	// Iteration must reset to 0 on EVERY exit (success, error, or ctx
	// cancellation) so any downstream non-loop node templating {{.Iteration}}
	// doesn't see the last-attempted index from a failed loop. Review must
	// also reset on exit so downstream nodes don't template stale
	// findings from the loop's last iteration. Defer instead of resetting
	// only on the success path.
	defer func() {
		mu.Lock()
		runCtx.Iteration = 0
		runCtx.Review = ReviewState{}
		mu.Unlock()
	}()

	var lastOutput string
	for i := 0; i < maxIter; i++ {
		if ctx.Err() != nil {
			return lastOutput, ctx.Err()
		}

		mu.Lock()
		runCtx.Iteration = i
		mu.Unlock()

		if !hasBody {
			// Backward compat: self-prompt each iteration.
			res, err := e.executePromptNode(ctx, run, node, runCtx, mu)
			if err != nil {
				return res.output, err
			}
			lastOutput = res.output
		} else {
			output, err := e.executeLoopBody(ctx, run, node, runCtx, mu, i)
			if err != nil {
				return output, err
			}
			lastOutput = output
		}

		// Evaluate stop condition.
		if node.Condition != "" {
			mu.Lock()
			result, tmplErr := renderTemplate(node.Condition, runCtx)
			mu.Unlock()
			if tmplErr != nil {
				e.logger.Warn("workflow: loop condition failed", "node_id", node.ID, "error", tmplErr)
				continue
			}
			if result == "true" {
				break
			}
		}
	}

	return lastOutput, nil
}

// executeLoopBody runs the body children of a loop node in declaration order
// for a single iteration. Each child is persisted as its own node_runs row
// keyed by (run_id, child.ID, iteration). After a bash child whose ID is
// reviewBodyNodeID finishes, its stdout is parsed into runCtx.Review.
func (e *defaultEngine) executeLoopBody(ctx context.Context, run *db.WorkflowRun, loopNode *config.NodeDef, runCtx *RunContext, mu *sync.Mutex, iteration int) (string, error) {
	var lastOutput string
	for _, child := range loopNode.Body {
		if ctx.Err() != nil {
			return lastOutput, ctx.Err()
		}

		// Evaluate child-level when.
		mu.Lock()
		shouldRun := e.evaluateWhen(child, runCtx)
		mu.Unlock()

		now := time.Now().UTC()
		if !shouldRun {
			e.persistBodyChildSkip(ctx, run, child, iteration, now)
			continue
		}

		res, attempts, execErr := e.runBodyChild(ctx, run, child, runCtx, mu, iteration, now)

		status := db.NodeRunStatusSuccess
		if execErr != nil {
			status = db.NodeRunStatusFailed
		}

		e.applyReviewOutcome(run, child, runCtx, mu, res.output, execErr)
		e.persistBodyChildEnd(run, child, iteration, res, status, execErr, attempts, now)

		if execErr != nil {
			return res.output, execErr
		}
		lastOutput = res.output
	}
	return lastOutput, nil
}

// persistBodyChildSkip records a skipped (child, iteration) row and
// broadcasts the terminal skip so the run graph shows the gated child.
func (e *defaultEngine) persistBodyChildSkip(ctx context.Context, run *db.WorkflowRun, child *config.NodeDef, iteration int, now time.Time) {
	nr := &db.NodeRun{
		RunID:      run.ID,
		NodeID:     child.ID,
		Iteration:  iteration,
		Status:     db.NodeRunStatusSkipped,
		Attempt:    1,
		StartedAt:  &now,
		FinishedAt: &now,
	}
	if err := e.store.UpsertNodeRun(ctx, nr); err != nil {
		e.logger.Error("workflow: failed to persist body skip", "node_id", child.ID, "iteration", iteration, "error", err)
	}
	if e.broadcaster != nil {
		e.broadcaster.BroadcastWorkflowNodeCompleted(events.WorkflowNodeEventData{
			RunID:     run.ID,
			NodeID:    child.ID,
			Status:    string(db.NodeRunStatusSkipped),
			Iteration: iteration,
		})
	}
}

// runBodyChild persists/broadcasts the running row for one (child, iteration),
// arms the heartbeat, and executes the child with per-attempt timeout + retry.
// Returns the execution result, the number of attempts actually run (>= 1
// for the persisted Attempt column), and the terminal error.
func (e *defaultEngine) runBodyChild(ctx context.Context, run *db.WorkflowRun, child *config.NodeDef, runCtx *RunContext, mu *sync.Mutex, iteration int, now time.Time) (nodeExecResult, int, error) {
	// Persist running status for this (child, iteration).
	nrStart := &db.NodeRun{
		RunID:     run.ID,
		NodeID:    child.ID,
		Iteration: iteration,
		Status:    db.NodeRunStatusRunning,
		Attempt:   1,
		StartedAt: &now,
	}
	if err := e.store.UpsertNodeRun(ctx, nrStart); err != nil {
		e.logger.Error("workflow: failed to persist body start", "node_id", child.ID, "iteration", iteration, "error", err)
	}
	if e.broadcaster != nil {
		e.broadcaster.BroadcastWorkflowNodeStarted(events.WorkflowNodeEventData{
			RunID:     run.ID,
			NodeID:    child.ID,
			Status:    string(db.NodeRunStatusRunning),
			Iteration: iteration,
		})
	}

	// Body children get the same heartbeat as top-level nodes (see
	// executeNode) so the recovery sweeper can detect a stalled body child
	// after a daemon restart instead of stranding the row at status=running
	// forever.
	stopHeartbeat := e.startHeartbeat(ctx, run.ID, child.ID, iteration)
	defer stopHeartbeat()

	// Parse the child's timeout once. The actual context.WithTimeout is
	// created PER ATTEMPT inside execFn so that retries get a fresh
	// deadline — when attempt 1 trips a 30s timeout, attempt 2 would
	// otherwise inherit an already-cancelled context and immediately
	// fail. The d > 0 guard rejects a user-declared `timeout: "0s"`
	// (which would build an already-expired context and turn every
	// attempt into an instant failure).
	var childTimeout time.Duration
	if child.Timeout != "" {
		if d, perr := time.ParseDuration(child.Timeout); perr == nil && d > 0 {
			childTimeout = d
		}
	}

	var attemptsRun int
	execFn := func() (nodeExecResult, error) {
		attemptsRun++
		attemptCtx := ctx
		var attemptCancel context.CancelFunc
		if childTimeout > 0 {
			attemptCtx, attemptCancel = context.WithTimeout(ctx, childTimeout)
			defer attemptCancel()
		}
		switch child.Type {
		case config.NodeTypePrompt:
			return e.executePromptNode(attemptCtx, run, child, runCtx, mu)
		case config.NodeTypeBash:
			return e.executeBashNode(attemptCtx, run, child, runCtx, mu)
		default:
			// validateWorkflowDef rejects this at StartRun, but
			// executeDAGFromCheckpoint resumes from the DB-pinned definition
			// without re-validating — a stored workflow with an unsupported
			// body-child type (manual DB edit, pre-validator definition)
			// would otherwise persist as Success with empty output. Make
			// the miss observable instead.
			return nodeExecResult{}, fmt.Errorf("unsupported body child type: %s", child.Type)
		}
	}
	// Honor the child's retry: block the same as a top-level node would.
	// Without this, the seeded fix prompt's retry config (if added) would
	// be silently ignored, and a transient agent hiccup would tank the
	// whole loop on iteration 1. Pass the parent `ctx` (not a per-attempt
	// timeout-bound ctx) so the retry backoff sleeps against the outer
	// run context and the per-attempt timeout doesn't cancel the retry
	// loop itself.
	res, execErr := e.executeWithRetry(ctx, run, child, iteration, execFn)
	return res, max(attemptsRun, 1), execErr
}

// applyReviewOutcome feeds the review bash child's stdout into
// runCtx.Review for the seeded review workflows. On a failed review child
// the captured output is unreliable — but the stale Comments / IDs from the
// previous iteration MUST NOT be reused by the next iteration's SameAsPrev
// compare. Rotate IDs into PrevIDs (preserving the last-good baseline) and
// clear the rest. ParseFailed gates the fix child's `when:` so the loop
// retries the review rather than fixing stale findings.
func (e *defaultEngine) applyReviewOutcome(run *db.WorkflowRun, child *config.NodeDef, runCtx *RunContext, mu *sync.Mutex, output string, execErr error) {
	if child.Type != config.NodeTypeBash || child.ID != reviewBodyNodeID {
		return
	}
	if _, isSeeded := reviewParsedWorkflows[run.WorkflowName]; !isSeeded {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	if execErr == nil {
		parseReviewOutput(output, runCtx)
		return
	}
	runCtx.Review.PrevIDs = append([]string(nil), runCtx.Review.IDs...)
	runCtx.Review.Comments = nil
	runCtx.Review.CommentsJSON = ""
	runCtx.Review.IDs = nil
	runCtx.Review.NoComments = false
	runCtx.Review.SameAsPrev = false
	runCtx.Review.ParseFailed = true
}

// persistBodyChildEnd writes the terminal (child, iteration) row and
// broadcasts completion with the truncated input/output payloads.
func (e *defaultEngine) persistBodyChildEnd(run *db.WorkflowRun, child *config.NodeDef, iteration int, res nodeExecResult, status db.NodeRunStatus, execErr error, attempt int, startedAt time.Time) {
	finishedAt := time.Now().UTC()
	nrEnd := &db.NodeRun{
		RunID:     run.ID,
		NodeID:    child.ID,
		Iteration: iteration,
		Status:    status,
		Input:     res.input,
		SessionID: res.sessionID,
		Output:    res.output,
		Attempt:   attempt,
		// StartedAt is carried into the UPSERT so that, if the running-row
		// INSERT failed silently in runBodyChild (we only log + swallow),
		// the row inserted here still has a valid started_at — otherwise
		// the column is NOT NULL and the second UPSERT would be rejected,
		// leaving no DB record of the node ever running.
		StartedAt:  &startedAt,
		FinishedAt: &finishedAt,
	}
	if execErr != nil {
		nrEnd.ErrorText = execErr.Error()
	}
	// Detached context — run ctx may already be cancelled, but the
	// terminal node status still needs to be persisted. Matches the
	// pattern in completeNode.
	if err := e.store.UpsertNodeRun(context.Background(), nrEnd); err != nil {
		e.logger.Error("workflow: failed to persist body completion", "node_id", child.ID, "iteration", iteration, "error", err)
	}
	if e.broadcaster != nil {
		e.broadcaster.BroadcastWorkflowNodeCompleted(events.WorkflowNodeEventData{
			RunID:     run.ID,
			NodeID:    child.ID,
			Status:    string(status),
			Input:     truncateOutput(res.input, 1000),
			SessionID: res.sessionID,
			Output:    truncateOutput(res.output, 1000),
			Iteration: iteration,
		})
	}
}

// reviewEnvelope is the JSON shape printed by `loop review run --wait` and
// consumed by parseReviewOutput. Defined as a named type (rather than
// inlined) so extractReviewJSON can validate the shape — specifically the
// Status field — before accepting a candidate line as the envelope.
type reviewEnvelope struct {
	Status     string          `json:"status"`
	NoComments bool            `json:"no_comments"`
	Comments   []ReviewComment `json:"comments"`
}

// parseReviewOutput parses stdout JSON from `loop review run --wait` into
// runCtx.Review. The bash node's captured stdout includes preamble from the
// agent container (e.g. `loop-dockerproxy started ...`) before the CLI's
// JSON line, so the parser scans forwards through the lines and uses the
// first one that parses as the expected envelope. When nothing parses,
// the function clears Review.* (shifting IDs into PrevIDs) but leaves both
// `NoComments` and `SameAsPrev` false so the seeded loops' stop condition
// `{{ or .Review.NoComments .Review.SameAsPrev }}` does NOT trip — an empty
// stdout, a missing JSON envelope, or any other parse miss is a real signal
// (CLI bug, $API_URL misconfig that returned an empty body, future stdout
// pollution after the JSON line) that we want to surface as "keep trying
// until maxIter" rather than silently treating as a clean review. The
// expected shape is:
//
//	{"status": "ready", "no_comments": bool, "comments": [{"id": "...", ...}]}
func parseReviewOutput(stdout string, runCtx *RunContext) {
	var parsed reviewEnvelope
	if !extractReviewJSON(stdout, &parsed) {
		// Treat as a real parse failure, NOT as "no findings". Setting
		// SameAsPrev=true when prev was empty would terminate the loop on
		// the very first iteration with `completed` status, hiding a
		// daemon/CLI bug behind a "review with no findings" UI report.
		//
		// Leave IDs/PrevIDs untouched on parse miss so the next successful
		// iteration's SameAsPrev compares against the last *good* parse —
		// otherwise a transient parse miss between two identical reviews
		// would mask the no-progress signal and burn an extra fix pass.
		// ParseFailed gates the seeded fix child's `when:` so the loop
		// doesn't fire a fix prompt with empty CommentsJSON.
		runCtx.Review.NoComments = false
		runCtx.Review.Comments = nil
		runCtx.Review.CommentsJSON = ""
		runCtx.Review.SameAsPrev = false
		runCtx.Review.ParseFailed = true
		return
	}

	// Status was validated by extractReviewJSON to be "ready" or "error".
	// Only "ready" implies the review actually completed — an "error"
	// envelope means the daemon flipped to failure, which must NOT be
	// reinterpreted as `no_comments=true` (which would terminate the loop
	// with a false-clean verdict via the stop condition
	// `{{ or .Review.NoComments .Review.SameAsPrev }}`). We still rotate
	// IDs into PrevIDs and clear IDs so a subsequent successful retry
	// has the right baseline to compare against (the error iteration
	// produced no findings of its own — treating its absence as the new
	// baseline lets SameAsPrev work correctly across the error).
	if parsed.Status != "ready" {
		runCtx.Review.PrevIDs = append([]string(nil), runCtx.Review.IDs...)
		runCtx.Review.IDs = nil
		runCtx.Review.Comments = nil
		runCtx.Review.CommentsJSON = ""
		runCtx.Review.NoComments = false
		runCtx.Review.SameAsPrev = false
		runCtx.Review.ParseFailed = true
		return
	}

	// Successful parse — rotate PrevIDs now (after we know the envelope is
	// usable). Clear ParseFailed so the fix child's `when:` can fire.
	prev := append([]string(nil), runCtx.Review.IDs...)
	runCtx.Review.PrevIDs = prev
	runCtx.Review.ParseFailed = false

	runCtx.Review.Comments = parsed.Comments
	runCtx.Review.NoComments = parsed.NoComments || len(parsed.Comments) == 0

	if len(parsed.Comments) > 0 {
		raw, _ := json.Marshal(parsed.Comments)
		runCtx.Review.CommentsJSON = string(raw)
	} else {
		runCtx.Review.CommentsJSON = ""
	}

	ids := make([]string, 0, len(parsed.Comments))
	for _, c := range parsed.Comments {
		ids = append(ids, c.ID)
	}
	if len(ids) > 1 {
		slices.Sort(ids)
	}
	runCtx.Review.IDs = ids

	runCtx.Review.SameAsPrev = len(ids) > 0 && slices.Equal(ids, prev)
}

// extractReviewJSON scans stdout forwards through non-empty lines (and as
// a final fallback the entire trimmed stdout) for the first one that parses
// as the review envelope AND carries a recognized Status ("ready" or
// "error"). Returns true on a successful decode, in which case `out`
// carries the parsed payload. The forward walk is intentional: the CLI's
// envelope is the first valid one printed; any JSON-shaped string emitted
// AFTER it (debug `echo`, sidecar ready ping, set-x trace surfacing a
// cached envelope) must NOT displace the real envelope.
//
// Preamble from the agent container (e.g. `loop-dockerproxy started ...`)
// is not JSON and fails the json.Unmarshal call cleanly, so the forward
// scan skips it and lands on the CLI's envelope. The Status check is
// load-bearing: RunBash captures the entire container's logs (not just the
// script's stdout), and an unrelated JSON object on any line would
// otherwise decode silently, default Comments to empty, and flip
// NoComments to true via the `|| len(parsed.Comments) == 0` branch in
// parseReviewOutput — terminating the seeded review-fix loop with a false
// "clean" verdict while the real review surfaced findings.
func extractReviewJSON(stdout string, out *reviewEnvelope) bool {
	tryDecode := func(s string) bool {
		// Use a presence-checking shape with json.RawMessage so we can
		// distinguish "comments key is missing" from "comments: []". A
		// stdout line like `{"status":"ready"}` would otherwise decode
		// silently into reviewEnvelope with Comments=nil, NoComments=false
		// — and the surrounding caller would interpret that as a
		// false-clean review. Requiring the `comments` key to be present
		// rejects unrelated JSON-shaped lines (debug echo, sidecar ping)
		// that happen to carry a recognized status string.
		var probe struct {
			Status   string          `json:"status"`
			Comments json.RawMessage `json:"comments"`
		}
		if err := json.Unmarshal([]byte(s), &probe); err != nil {
			return false
		}
		if probe.Status != "ready" && probe.Status != "error" {
			return false
		}
		if len(probe.Comments) == 0 {
			return false
		}
		var candidate reviewEnvelope
		if err := json.Unmarshal([]byte(s), &candidate); err != nil {
			return false
		}
		*out = candidate
		return true
	}
	for raw := range strings.SplitSeq(stdout, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if tryDecode(line) {
			return true
		}
	}
	if trimmed := strings.TrimSpace(stdout); trimmed != "" {
		return tryDecode(trimmed)
	}
	return false
}
