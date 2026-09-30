package agentgate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"syscall"
	"time"

	"github.com/radutopala/loop/internal/types"
)

// RenameReview shows the content of a rename onto a path an approve rule
// covers, the way the git guard does for git config. Editors and Claude
// Code's Edit tool write a temp file next to the target and rename it over
// the target, so the rename is where the new content appears: the card
// carries a diff of the current file against it, and on allow the gate
// installs the exact bytes it showed, as the agent's uid and gid. A rename
// it can't show (a directory or symlink, a big or binary file, an exchange
// or whiteout) gets the rule's usual card, which shows the path only.
type RenameReview struct {
	FS         GuardFS
	Approver   Approver
	Auditor    Auditor
	PeerSource PeerSourceLookup
	Now        func() time.Time
}

// Rename asks about r with the diff and performs it on allow. rule is the
// approve rule that matched r.Dst. handled=false means the content can't be
// shown and nothing was asked; the caller goes on with the file rules.
// The rename also removes r.Src, so the card names it; the gate removes it
// only while it still holds the approved bytes.
func (v *RenameReview) Rename(ctx context.Context, r GuardRename, rule MatchResult) (resp TrapResponse, handled bool) {
	if v == nil || v.Approver == nil || r.Flags&(renameExchange|renameWhiteout) != 0 {
		return TrapResponse{}, false
	}
	uid, gid, err := r.Tracee.Creds()
	if err != nil {
		return TrapResponse{}, false
	}
	target := OpWrite + " " + r.Dst
	snap, err := v.FS.Snapshot(r.Src, r.Dst, uid, gid)
	switch {
	case errors.Is(err, ErrGuardNotRegular), errors.Is(err, ErrGuardTooLarge):
		return TrapResponse{}, false
	case err != nil:
		return v.fail(r, rule, target, err), true
	}
	if !reviewableText(snap.Data) || !reviewableText(snap.Old) {
		return TrapResponse{}, false
	}

	sum := sha256.Sum256(snap.Data)
	out := v.Approver.Request(ctx, r.ChannelID, ApprovalRequest{
		Kind:     "file",
		Target:   target,
		Source:   sourceForPID(r.PID, v.PeerSource),
		Message:  rule.Message,
		CacheKey: "file:review:" + r.Dst + ":" + hex.EncodeToString(sum[:]),
		Details:  map[string]string{"diff": gitGuardDiff(snap), "source": r.Src},
		OnPrompt: func() {
			v.write(AuditEntry{Ts: v.now(), Channel: r.ChannelID, PID: r.PID, Kind: "file", Target: target, RuleID: rule.RuleID, Event: "request"})
		},
	})
	v.audit(r, rule, target, string(out.Decision), out.Actor, nil)
	if out.Decision != types.DecisionAllow {
		return denyResp(r.TrapID, syscall.EPERM), true
	}
	if err := v.FS.Install(snap, r.Flags&renameNoReplace != 0); err != nil {
		return v.fail(r, rule, target, err), true
	}
	return performedResp(r.TrapID), true
}

// fail maps a filesystem error to the errno the tracee would have seen,
// or EPERM.
func (v *RenameReview) fail(r GuardRename, rule MatchResult, target string, err error) TrapResponse {
	v.audit(r, rule, target, string(types.DecisionDeny), "", map[string]string{"error": err.Error()})
	var errno syscall.Errno
	if errors.As(err, &errno) && errno != 0 {
		return denyResp(r.TrapID, errno)
	}
	return denyResp(r.TrapID, syscall.EPERM)
}

func (v *RenameReview) audit(r GuardRename, rule MatchResult, target, decision, actor string, extra map[string]string) {
	v.write(AuditEntry{
		Ts:          v.now(),
		Channel:     r.ChannelID,
		PID:         r.PID,
		Kind:        "file",
		Target:      target,
		RuleID:      rule.RuleID,
		Decision:    decision,
		PromptedWho: actor,
		Extra:       extra,
	})
}

func (v *RenameReview) write(e AuditEntry) {
	if v.Auditor != nil {
		v.Auditor.Write(e)
	}
}

func (v *RenameReview) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}
