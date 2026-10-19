package review

import (
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"time"
)

// The PR outcome. Once a review is verified, its findings and the PR's
// GitHub threads say whether the PR can be approved: DeriveOutcome reads it
// off the session, it is never stored.

// The outcome states.
const (
	OutcomeApprove         = "approve"
	OutcomeApproveWithNits = "approve_with_nits"
	OutcomeRequestChanges  = "request_changes"
	OutcomeUnknown         = "unknown"
)

// The statuses the verification step gives a GitHub thread a person started.
const (
	ThreadAddressed = "addressed"
	ThreadOpen      = "open"
)

// ThreadAssessment is the verification step's call on a GitHub thread a
// person started: addressed (a later commit or a reply settles it), or still
// open with an impact. A person is never overruled, so there is no false
// positive.
type ThreadAssessment struct {
	Status string `json:"status"`
	Impact string `json:"impact,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// Outcome is whether the PR can be approved, and the comments that decide
// it: Blocking ones request changes, Nits are left for later, and Pending
// ones aren't judged yet. GitHub threads are listed by their first
// comment's id. Reason says why the state is unknown.
type Outcome struct {
	State       string   `json:"state"`
	Blocking    []string `json:"blocking"`
	Nits        []string `json:"nits"`
	Pending     []string `json:"pending"`
	Reason      string   `json:"reason,omitempty"`
	ReviewedSHA string   `json:"reviewed_sha,omitempty"`
}

// DeriveOutcome returns sess's outcome:
//   - request_changes when an agent finding is real and blocking, unless it
//     was pushed and its thread resolved, or an open GitHub thread a person
//     started was assessed blocking;
//   - unknown otherwise when the head isn't the commit last reviewed, or a
//     finding or thread isn't judged yet;
//   - approve_with_nits when only nice-to-haves remain;
//   - approve when nothing does.
func DeriveOutcome(sess *Session) Outcome {
	o := Outcome{Blocking: []string{}, Nits: []string{}, Pending: []string{}, ReviewedSHA: sess.ReviewedSHA}
	for _, c := range sess.Comments {
		if c == nil || c.Resolved {
			continue
		}
		var impact string
		switch {
		case deletable(c):
			if Dismissed(c.Verdict) {
				continue
			}
			if c.Verdict == VerdictReal {
				impact = c.Impact
			}
		case c.ReplyTo == 0 && c.GitHubID > 0:
			a, ok := sess.Threads[c.GitHubID]
			if ok && a.Status == ThreadAddressed {
				continue
			}
			if ok && a.Status == ThreadOpen {
				impact = a.Impact
			}
		default:
			continue
		}
		switch impact {
		case ImpactBlocking:
			o.Blocking = append(o.Blocking, c.ID)
		case ImpactNiceToHave:
			o.Nits = append(o.Nits, c.ID)
		default:
			o.Pending = append(o.Pending, c.ID)
		}
	}
	switch {
	case len(o.Blocking) > 0:
		o.State = OutcomeRequestChanges
	case sess.ReviewedSHA == "" || sess.ReviewedSHA != sess.HeadSHA:
		o.State, o.Reason = OutcomeUnknown, "the head commit isn't reviewed yet"
	case len(o.Pending) > 0:
		o.State, o.Reason = OutcomeUnknown, fmt.Sprintf("%d comment(s) not judged yet", len(o.Pending))
	case len(o.Nits) > 0:
		o.State = OutcomeApproveWithNits
	default:
		o.State = OutcomeApprove
	}
	return o
}

// Summary is o in one line, for the verification step's message and
// get_review_comments.
func (o Outcome) Summary() string {
	switch o.State {
	case OutcomeRequestChanges:
		return fmt.Sprintf("request changes: %d blocking (%s)", len(o.Blocking), strings.Join(o.Blocking, ", "))
	case OutcomeApproveWithNits:
		return fmt.Sprintf("approve with nits: %d nice to have (%s)", len(o.Nits), strings.Join(o.Nits, ", "))
	case OutcomeApprove:
		return "approve: nothing left open"
	default:
		return "unknown: " + o.Reason
	}
}

// KnownThreadStatus reports whether v is one of the Thread constants.
func KnownThreadStatus(v string) bool {
	return v == ThreadAddressed || v == ThreadOpen
}

// encodeThreads returns threads as JSON, "" when there are none. Map keys
// are sorted, so equal maps encode the same.
func encodeThreads(threads map[int64]ThreadAssessment) string {
	if len(threads) == 0 {
		return ""
	}
	out, _ := json.Marshal(threads)
	return string(out)
}

// decodeThreads reverses encodeThreads; what doesn't decode is dropped.
func decodeThreads(s string) map[int64]ThreadAssessment {
	if s == "" {
		return nil
	}
	var threads map[int64]ThreadAssessment
	if json.Unmarshal([]byte(s), &threads) != nil {
		return nil
	}
	return threads
}

// AssessThread records a on the GitHub thread whose first comment has
// commentID: a thread a person started, since an agent comment's thread is
// judged through its verdict. a.Impact is kept only while the thread is
// open. Returns false when there is no such thread.
func (s *Store) AssessThread(channelID, commentID string, a ThreadAssessment) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[channelID]
	if !ok {
		return false
	}
	for _, c := range sess.Comments {
		if c.ID != commentID {
			continue
		}
		if deletable(c) || c.ReplyTo != 0 || c.GitHubID <= 0 {
			return false
		}
		if a.Status != ThreadOpen {
			a.Impact = ""
		}
		threads := maps.Clone(sess.Threads)
		if threads == nil {
			threads = map[int64]ThreadAssessment{}
		}
		threads[c.GitHubID] = a
		sess.Threads = threads
		sess.UpdatedAt = time.Now()
		s.save(sess)
		return true
	}
	return false
}

// StartVerify readies the session for a verification step: the thread
// assessments of the last one go, so a thread the new one skips reads as
// not judged rather than keeping a call made on older code. Returns false
// if no session exists for channelID.
func (s *Store) StartVerify(channelID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[channelID]
	if !ok {
		return false
	}
	sess.Threads = nil
	sess.UpdatedAt = time.Now()
	s.save(sess)
	return true
}

// MarkReviewed records that a review run or verification step finished on
// the session's head commit. Returns false if no session exists.
func (s *Store) MarkReviewed(channelID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[channelID]
	if !ok {
		return false
	}
	sess.ReviewedSHA = sess.HeadSHA
	sess.UpdatedAt = time.Now()
	s.save(sess)
	return true
}

// MarkApproved records that the PR was approved at sha. Returns false if
// no session exists.
func (s *Store) MarkApproved(channelID, sha string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[channelID]
	if !ok {
		return false
	}
	sess.ApprovedSHA = sha
	sess.UpdatedAt = time.Now()
	s.save(sess)
	return true
}
