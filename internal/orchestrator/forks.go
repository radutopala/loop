package orchestrator

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/osutil"
)

// sessionFiles deletes a Claude Code session's files: its transcript
// <id>.jsonl and the <id>/ directory beside it, under the project directory
// Claude Code keeps for a working directory.
type sessionFiles struct {
	userHomeDir func() (string, error)
	removeAll   func(string) error
}

// remove deletes session sessionID's files for working directory workDir.
// Files already gone aren't an error.
func (f sessionFiles) remove(workDir, sessionID string) error {
	home, err := f.userHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".claude", "projects", osutil.EncodeClaudeProjectPath(workDir))
	base := filepath.Base(sessionID)
	return errors.Join(
		f.removeAll(filepath.Join(dir, base+".jsonl")),
		f.removeAll(filepath.Join(dir, base)),
	)
}

// dropFork deletes forked session sessionID of hidden thread h, once no run
// in h can still use it: callers hold h's drain lock with no run in flight.
// It keeps a session that isn't h's own fork: any session of a channel that
// isn't hidden, one another channel points to, and the parent session a
// thread still waiting to fork (ForkPending) holds. A failure is only
// logged; the files are just left behind.
func (o *Orchestrator) dropFork(ctx context.Context, h *db.Channel, sessionID string) {
	if sessionID == "" || h.DirPath == "" || !db.IsHiddenKind(h.Kind) {
		return
	}
	if h.ForkPending && sessionID == h.SessionID {
		return
	}
	inUse, err := o.store.SessionInUse(ctx, sessionID, h.ChannelID)
	if err != nil {
		o.logger.Warn("forks: checking the session", "error", err, "channel_id", h.ChannelID, "session_id", sessionID)
		return
	}
	if inUse {
		o.logger.Debug("forks: kept", "reason", "in use", "channel_id", h.ChannelID, "session_id", sessionID)
		return
	}
	if err := o.sessionFiles.remove(h.DirPath, sessionID); err != nil {
		o.logger.Warn("forks: deleting the session", "error", err, "channel_id", h.ChannelID, "session_id", sessionID)
		return
	}
	o.logger.Debug("forks: deleted", "channel_id", h.ChannelID, "session_id", sessionID)
}

// afterHiddenRun deletes the forks a run in hidden thread h (as loaded before
// the run) no longer needs. req is the run's request and ran the session it
// ended in, "" when unknown; stored is whether ran was stored as h's session.
// An explain run's fork is never used again. A learn thread keeps its latest
// fork for the user's replies: the one it replaces goes once a new one is
// stored, and a fork that was never stored goes at once. Everything goes
// when h was deleted during the run. Only a fork the run made counts: a run
// that resumed without forking ends in the session it started from.
func (o *Orchestrator) afterHiddenRun(ctx context.Context, h *db.Channel, req *agent.AgentRequest, ran string, stored bool) {
	if h == nil || req == nil || !db.IsHiddenKind(h.Kind) {
		return
	}
	if ran == req.SessionID {
		ran = ""
	}
	if fresh, err := o.store.GetChannel(ctx, h.ChannelID); err == nil && fresh == nil {
		// Deleted meanwhile: StopHiddenThread drops the fork it had.
		o.dropFork(ctx, h, ran)
		return
	}
	switch {
	case h.Kind == db.ChannelKindExplain || !stored:
		o.dropFork(ctx, h, ran)
	case ran != "" && h.SessionID != ran:
		o.dropFork(ctx, h, h.SessionID)
	}
}

// StopHiddenThread cancels hidden thread h's run, for when h is deleted with
// its channel, and then deletes h's fork. Its queued triggers are rows
// deleted with it. The fork goes on h's drain, once the cancelled run has
// wound down.
func (o *Orchestrator) StopHiddenThread(h *db.Channel) {
	o.CancelActiveRun(h.ChannelID)
	o.drainSpawn(func() {
		lock := channelLock(&o.channelLocks, h.ChannelID)
		lock.Lock()
		defer lock.Unlock()
		o.dropFork(context.Background(), h, h.SessionID)
	})
}
