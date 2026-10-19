package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/radutopala/loop/internal/review"
)

// handleReviewAssessThread serves POST
// /api/channels/{id}/review/comments/{cid}/assess: the verification step's
// call on a GitHub thread a person started, by the id of its first comment.
// The thread is addressed, or open with an impact, which the PR outcome
// counts. Answers the assessment recorded.
func (s *reviewService) handleReviewAssessThread(w http.ResponseWriter, r *http.Request) {
	if s.sessions == nil {
		http.Error(w, "review service not configured", http.StatusNotImplemented)
		return
	}
	var body review.ThreadAssessment
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !review.KnownThreadStatus(body.Status) {
		http.Error(w, "status must be addressed or open", http.StatusBadRequest)
		return
	}
	if body.Status == review.ThreadOpen && !review.KnownImpact(body.Impact) {
		http.Error(w, "an open thread needs impact blocking or nice_to_have", http.StatusBadRequest)
		return
	}
	if body.Status == review.ThreadAddressed && body.Impact != "" {
		http.Error(w, "impact goes with an open thread only", http.StatusBadRequest)
		return
	}
	body.Reason = strings.TrimSpace(body.Reason)
	channelID := r.PathValue("id")
	commentID := r.PathValue("cid")
	c, sess := s.sessions.FindComment(channelID, commentID)
	if sess == nil {
		http.Error(w, "no review session for channel", http.StatusNotFound)
		return
	}
	if c == nil {
		http.Error(w, "comment not found", http.StatusNotFound)
		return
	}
	if !s.sessions.AssessThread(channelID, commentID, body) {
		http.Error(w, "only the first comment of a GitHub thread a person started can be assessed; give an agent comment a verdict", http.StatusConflict)
		return
	}
	writeHTTPJSON(w, http.StatusOK, body, s.deps.logger)
}

// reviewApproveRequest is the body of POST /review/approve: the review's
// text, and force to approve when the outcome isn't approve or
// approve_with_nits.
type reviewApproveRequest struct {
	Body  string `json:"body"`
	Force bool   `json:"force"`
}

// handleReviewApprove serves POST /api/channels/{id}/review/approve, the
// panel's Approve on GitHub: it approves the PR at the commit the session
// is on, as the configured gh user. Owner only: approving someone's PR is
// the user's click, never an agent's. It answers 409 when the outcome isn't
// approve or approve_with_nits and force is off, and when the PR's head
// moved past the session's, since the approval would cover commits nobody
// reviewed; GitHub's refusals (an own PR, a merged one) come back as 502
// with GitHub's message.
func (s *reviewService) handleReviewApprove(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.client, "review service not configured") {
		return
	}
	if s.sessions == nil {
		http.Error(w, "review service not configured", http.StatusNotImplemented)
		return
	}
	var req reviewApproveRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	channelID := r.PathValue("id")
	sess := s.sessions.Get(channelID)
	if sess == nil {
		http.Error(w, "no review session for channel", http.StatusNotFound)
		return
	}
	if sess.PR == nil || sess.HeadSHA == "" {
		http.Error(w, "session not ready (no PR or head SHA)", http.StatusConflict)
		return
	}
	outcome := review.DeriveOutcome(sess)
	if !req.Force && outcome.State != review.OutcomeApprove && outcome.State != review.OutcomeApproveWithNits {
		http.Error(w, "the outcome is "+outcome.Summary()+"; pass force to approve anyway", http.StatusConflict)
		return
	}
	channelDirPath, parentDirPath := s.reviewRunDirs(r.Context(), channelID)
	if !s.requireReviewEnabled(w, channelDirPath, parentDirPath) {
		return
	}
	ghUser := s.deps.configs.ghUser(channelDirPath, parentDirPath)
	head, err := s.client.FetchPRHeadSHA(r.Context(), channelDirPath, ghUser, sess.PR.Number)
	if err != nil {
		http.Error(w, "reading the PR head: "+err.Error(), http.StatusBadGateway)
		return
	}
	if head != sess.HeadSHA {
		http.Error(w, fmt.Sprintf("the PR head moved to %s since %s was reviewed; Sync and verify again", shortSHA(head), shortSHA(sess.HeadSHA)), http.StatusConflict)
		return
	}
	slug, err := s.client.FetchRepoSlug(r.Context(), channelDirPath, ghUser)
	if err != nil {
		http.Error(w, "reading the repo: "+err.Error(), http.StatusBadGateway)
		return
	}
	if err := s.client.ApprovePR(r.Context(), channelDirPath, ghUser, *slug, sess.PR.Number, sess.HeadSHA, strings.TrimSpace(req.Body)); err != nil {
		http.Error(w, "approving on GitHub: "+err.Error(), http.StatusBadGateway)
		return
	}
	s.sessions.MarkApproved(channelID, sess.HeadSHA)
	if hub := s.deps.eventsHub; hub != nil {
		hub.BroadcastReviewApproved(channelID, sess.PR.Number, sess.HeadSHA)
	}
	writeHTTPJSON(w, http.StatusOK, map[string]string{"approved_sha": sess.HeadSHA}, s.deps.logger)
}

// shortSHA is sha cut to the 7 characters GitHub shows.
func shortSHA(sha string) string {
	return sha[:min(len(sha), 7)]
}
