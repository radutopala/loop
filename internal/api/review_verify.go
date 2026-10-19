package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/radutopala/loop/internal/review"
)

// verifyStep is a review run's choice of the verification step: whether it
// follows the run, and the instructions it runs on ("" -> the default).
type verifyStep struct {
	on     bool
	prompt string
}

// handleReviewVerify serves POST /api/channels/{id}/review/verify (`loop
// review:verify`): it runs the verification step over the channel's review
// session on its own, as a review run does once it is over. Like a run, it
// answers 202 at once and the step goes on in the background; the session
// shows status=verifying until it is done. When the config turns the step
// off, it answers 202 {"status":"disabled"} and does nothing. It takes the
// review-run slot, so it can't overlap a run or a dedup pass.
func (s *reviewService) handleReviewVerify(w http.ResponseWriter, r *http.Request) {
	if s.sessions == nil || s.runner == nil {
		http.Error(w, "review service not configured", http.StatusNotImplemented)
		return
	}
	channelID := r.PathValue("id")
	sess := s.sessions.Get(channelID)
	if sess == nil {
		http.Error(w, "no review session for channel", http.StatusNotFound)
		return
	}
	if sess.WorktreePath == "" {
		http.Error(w, "session has no worktree", http.StatusConflict)
		return
	}
	runCtx, cancelRun := context.WithCancel(context.Background())
	if !s.registerReviewRun(channelID, cancelRun) {
		cancelRun()
		http.Error(w, "a review run is in progress", http.StatusConflict)
		return
	}
	if sess.Status != review.StatusReady {
		s.unregisterReviewRun(channelID)
		http.Error(w, "session not ready (status="+string(sess.Status)+")", http.StatusConflict)
		return
	}
	channelDirPath, parentDirPath := s.reviewRunDirs(r.Context(), channelID)
	if !s.requireReviewEnabled(w, channelDirPath, parentDirPath) {
		s.unregisterReviewRun(channelID)
		return
	}
	step := s.deps.configs.reviewVerify(channelDirPath, parentDirPath)
	if !step.on {
		s.unregisterReviewRun(channelID)
		writeHTTPJSON(w, http.StatusAccepted, map[string]string{"status": "disabled"}, s.deps.logger)
		return
	}
	s.sessions.UpdateStatus(channelID, review.StatusVerifying, "")
	s.broadcastReviewStatus(channelID, review.StatusVerifying, "")
	go func() {
		defer s.unregisterReviewRun(channelID)
		s.verifyAfterRun(runCtx, channelID, parentDirPath, step.prompt)
	}()
	writeHTTPJSON(w, http.StatusAccepted, map[string]string{"status": "started"}, s.deps.logger)
}

// verifyAfterRun runs the verification step and settles the session: ready
// once it is done, error when the verification agent failed. A cancelled
// step (Stop, session delete, shutdown) settles as a stopped run does. The
// caller owns the run slot.
func (s *reviewService) verifyAfterRun(runCtx context.Context, channelID, parentDirPath, instructions string) {
	err := s.runVerifyStep(runCtx, channelID, parentDirPath, instructions)
	if runCtx.Err() != nil {
		s.settleStopped(channelID)
		return
	}
	if err != nil {
		s.sessions.UpdateStatus(channelID, review.StatusError, err.Error())
		s.broadcastReviewStatus(channelID, review.StatusError, err.Error())
		return
	}
	s.sessions.UpdateStatus(channelID, review.StatusReady, "")
	s.broadcastReviewStatus(channelID, review.StatusReady, "")
}

// runVerifyStep checks every agent comment of the session and assesses the
// PR's unresolved GitHub threads. It runs the dedup pass over the whole
// session first (as `loop review:dedup` does, without refreshing it, so the
// checkout stays at the reviewed commit); a failed pass is logged and the
// step goes on. Then a read-only agent, allowed only to record verdicts and
// thread assessments on top of reading, judges each comment against the
// code, and its reply is posted to the channel with the PR outcome. A
// session with nothing to check is reviewed as it is. The status reads
// verifying while it runs; the caller settles it.
func (s *reviewService) runVerifyStep(ctx context.Context, channelID, parentDirPath, instructions string) error {
	sess := s.sessions.Get(channelID)
	s.sessions.StartVerify(channelID)
	if sess == nil || !review.NeedsVerify(sess.Comments) {
		s.sessions.MarkReviewed(channelID)
		return nil
	}
	s.sessions.UpdateStatus(channelID, review.StatusVerifying, "")
	s.broadcastReviewStatus(channelID, review.StatusVerifying, "")

	dedup := s.verifyDedup(ctx, channelID, parentDirPath, sess)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// The pass may have deleted comments, or the session itself.
	if sess = s.sessions.Get(channelID); sess == nil || !review.NeedsVerify(sess.Comments) {
		s.sessions.MarkReviewed(channelID)
		return nil
	}
	vc := review.VerifyContext{HeadSHA: sess.HeadSHA, Checkout: sess.WorktreePath, Dedup: dedup}
	if sess.PR != nil {
		vc.PRNumber, vc.PRURL = sess.PR.Number, sess.PR.URL
	}
	verifyCtx, cancel := withTimeout(ctx, s.verifyTimeout)
	defer cancel()
	states := s.findingStates(channelID, true)
	resp, err := s.runner.Run(verifyCtx, review.RunRequest{
		ChannelID:     channelID,
		DirPath:       sess.WorktreePath,
		ParentDirPath: parentDirPath,
		Prompt:        review.BuildVerifyPrompt(instructions, vc),
		ReadOnly:      true,
		AllowTools:    review.VerifyTools,
		Model:         sess.Model,
		Effort:        sess.Effort,
	})
	if resp != nil {
		s.attachTranscript(channelID, sess.WorktreePath, resp.SessionID, states)
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || verifyCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("verification timed out after %s", s.verifyTimeout)
		}
		return fmt.Errorf("verification run: %w", err)
	}
	s.sessions.MarkReviewed(channelID)
	summary := strings.TrimSpace(resp.Response)
	if summary == "" || s.messages == nil {
		return nil
	}
	if sess = s.sessions.Get(channelID); sess != nil {
		summary += "\n\nOutcome: " + review.DeriveOutcome(sess).Summary()
	}
	if err := s.messages.PostMessage(ctx, channelID, summary); err != nil {
		s.deps.logger.Warn("review: posting the verification summary failed", "channel_id", channelID, "error", err)
	}
	return nil
}

// verifyDedup runs the verification step's dedup pass and describes its
// outcome for the verification agent: the pass's result as JSON, or why it
// failed. Comments it deletes are recorded as the session's Superseded.
func (s *reviewService) verifyDedup(ctx context.Context, channelID, parentDirPath string, sess *review.Session) string {
	cands := review.DedupCandidates(sess.Comments)
	if len(cands) == 0 {
		return "skipped: no agent comment is anchored to a file"
	}
	dedupCtx, cancel := withTimeout(ctx, s.dedupTimeout)
	defer cancel()
	res, err := s.runDedupPass(dedupCtx, channelID, parentDirPath, sess, cands, nil)
	if err != nil {
		s.deps.logger.Warn("review: dedup pass before the verification failed", "channel_id", channelID, "error", err)
		return "failed: " + err.Error()
	}
	superseded := map[string]string{}
	for _, cl := range res.Clusters {
		for _, id := range cl.Removed {
			superseded[id] = cl.Kept
		}
	}
	if len(superseded) > 0 {
		s.sessions.SetSuperseded(channelID, superseded)
	}
	out, _ := json.Marshal(res)
	return string(out)
}
