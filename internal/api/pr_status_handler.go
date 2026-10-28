package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/forge"
	"github.com/radutopala/loop/internal/githubapi"
	"github.com/radutopala/loop/internal/gitlabapi"
	"github.com/radutopala/loop/internal/gitutil"
	"github.com/radutopala/loop/internal/prwatch"
)

const prNotConfiguredMsg = "PR watching not configured"

// PRWatcher watches each thread's PR/MR CI, sends failures on the agent's
// own commits back to it and merges what the owner armed. Satisfied by
// *prwatch.Watcher.
type PRWatcher interface {
	Watch(channelID string)
	Status(ctx context.Context, channelID string) *events.PREventData
	Flags(ctx context.Context) (map[string]events.PRFlags, error)
	SetAutoFix(ctx context.Context, channelID string, on *bool) error
	FixNow(ctx context.Context, channelID string) error
	Rerun(ctx context.Context, channelID string) error
	Log(ctx context.Context, channelID, checkID string) (string, error)
	Arm(ctx context.Context, channelID, method string) error
	CancelMerge(ctx context.Context, channelID string) error
	MergeNow(ctx context.Context, channelID, method string) error
	AgentStatus(channelID string, d events.AgentStatusEventData)
	ChannelUpdated(d events.ChannelUpdatedData)
}

// SetPRWatcher wires the PR watcher behind the channel PR status endpoints.
func (s *Server) SetPRWatcher(w PRWatcher) {
	s.prWatch = w
}

// ObservePR hands the PR watcher the events it follows: agent runs and
// checkout changes. Registered with EventsHub.Observe; it doesn't block.
func (s *Server) ObservePR(evt Event) {
	if s.prWatch == nil {
		return
	}
	switch d := evt.Data.(type) {
	case events.AgentStatusEventData:
		s.prWatch.AgentStatus(evt.ChannelID, d)
	case events.ChannelUpdatedData:
		s.prWatch.ChannelUpdated(d)
	}
}

// PRTarget resolves the channel's PR/MR for the PR watcher, with the
// channel's ci config: nil when its branch has none.
func (s *Server) PRTarget(ctx context.Context, channelID string) (*prwatch.Target, error) {
	ch, err := s.store.GetChannel(ctx, channelID)
	if err != nil || ch == nil {
		return nil, err
	}
	dir := channelDir(ch, s.loopDir)
	branch := gitBranch(ctx, dir)
	if branch == "" {
		return nil, nil
	}
	parent := s.workspace.resolveParentDirPath(ctx, channelID)
	resp, ok := s.prLookup.get(dir, branch)
	if !ok {
		pr, err := s.lookupBranchPR(ctx, dir, parent, branch)
		if errors.Is(err, githubapi.ErrGhNotInstalled) || errors.Is(err, gitlabapi.ErrGlabNotInstalled) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		resp = prResponse{Present: pr != nil, PR: pr}
		s.prLookup.put(dir, branch, resp)
	}
	if resp.PR == nil {
		return nil, nil
	}
	t := &prwatch.Target{
		Kind: forge.KindPR,
		Ref:  forge.Ref{Dir: dir, Account: s.configs.ghUser(dir, parent), Number: resp.PR.Number},
		URL:  resp.PR.URL,
		PR:   config.DefaultPRConfig(),
	}
	if resp.PR.Kind == forge.KindMR {
		t.Kind = forge.KindMR
		t.Ref.Account = s.expandHomePath(s.configs.glabConfigDir(dir, parent))
	}
	if cfg := s.configs.merged(dir, parent); cfg != nil {
		t.PR = cfg.PR
	}
	return t, nil
}

// ChannelHead returns the commit the channel's checkout is on, or "".
func (s *Server) ChannelHead(ctx context.Context, channelID string) string {
	ch, err := s.store.GetChannel(ctx, channelID)
	if err != nil || ch == nil {
		return ""
	}
	dir := channelDir(ch, s.loopDir)
	if dir == "" {
		return ""
	}
	out, err := gitutil.Command(ctx, dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// prStatusResponse is GET /api/channels/{id}/pr/status: the status of the channel's
// PR/MR, absent when its branch has none.
type prStatusResponse struct {
	Present bool                `json:"present"`
	PR      *events.PREventData `json:"pr,omitempty"`
}

// prLogResponse is GET /api/channels/{id}/pr/log: the tail of a failed
// check's log.
type prLogResponse struct {
	Log string `json:"log"`
}

type prAutoFixRequest struct {
	// AutoFix overrides the config's ci.auto_fix for the thread; null goes
	// back to the config's.
	AutoFix *bool `json:"auto_fix"`
}

type prMergeRequest struct {
	// WhenReady arms "merge when ready" rather than merging now.
	WhenReady bool `json:"when_ready"`
	// Method is squash, merge or rebase; empty picks ci.merge_method, else
	// the first the repo allows.
	Method string `json:"method,omitempty"`
}

// handleChannelPRStatus serves GET /api/channels/{id}/pr/status.
func (s *Server) handleChannelPRStatus(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.prWatch, prNotConfiguredMsg) {
		return
	}
	d := s.prWatch.Status(r.Context(), r.PathValue("id"))
	writeHTTPJSON(w, http.StatusOK, prStatusResponse{Present: d != nil, PR: d}, s.logger)
}

// handleChannelPRFix serves POST /api/channels/{id}/pr/fix: the current
// failure goes to the thread's agent now.
func (s *Server) handleChannelPRFix(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.prWatch, prNotConfiguredMsg) {
		return
	}
	s.prResult(w, s.prWatch.FixNow(r.Context(), r.PathValue("id")))
}

// handleChannelPRRerun serves POST /api/channels/{id}/pr/rerun: the
// PR/MR's failed checks run again.
func (s *Server) handleChannelPRRerun(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.prWatch, prNotConfiguredMsg) {
		return
	}
	s.prResult(w, s.prWatch.Rerun(r.Context(), r.PathValue("id")))
}

// handleChannelPRLog serves GET /api/channels/{id}/pr/log?check=<id>.
func (s *Server) handleChannelPRLog(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.prWatch, prNotConfiguredMsg) {
		return
	}
	log, err := s.prWatch.Log(r.Context(), r.PathValue("id"), r.URL.Query().Get("check"))
	if err != nil {
		s.prResult(w, err)
		return
	}
	writeHTTPJSON(w, http.StatusOK, prLogResponse{Log: log}, s.logger)
}

// handleChannelPRAutoFix serves PUT /api/channels/{id}/pr/auto-fix.
func (s *Server) handleChannelPRAutoFix(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.prWatch, prNotConfiguredMsg) {
		return
	}
	var req prAutoFixRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	s.prResult(w, s.prWatch.SetAutoFix(r.Context(), r.PathValue("id"), req.AutoFix))
}

// handlePRMerge serves POST /api/channels/{id}/pr/merge: merges the PR/MR
// now, or arms "merge when ready".
func (s *Server) handlePRMerge(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.prWatch, prNotConfiguredMsg) {
		return
	}
	var req prMergeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.WhenReady {
		s.prResult(w, s.prWatch.Arm(r.Context(), r.PathValue("id"), req.Method))
		return
	}
	s.prResult(w, s.prWatch.MergeNow(r.Context(), r.PathValue("id"), req.Method))
}

// handlePRMergeCancel serves DELETE /api/channels/{id}/pr/merge: disarms
// "merge when ready".
func (s *Server) handlePRMergeCancel(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.prWatch, prNotConfiguredMsg) {
		return
	}
	s.prResult(w, s.prWatch.CancelMerge(r.Context(), r.PathValue("id")))
}

// prResult answers a CI action: 204 when done, 404 without a PR/MR or the
// failed check, 409 without a failure to fix, else 502 with the forge's
// error.
func (s *Server) prResult(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, prwatch.ErrNoPR), errors.Is(err, prwatch.ErrNoCheck):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, prwatch.ErrNoFailure):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		http.Error(w, err.Error(), http.StatusBadGateway)
	}
}

// prFlags returns what each thread's pills show of its PR/MR for the channel
// list; nil without a watcher or when it can't be read.
func (s *Server) prFlags(ctx context.Context) map[string]events.PRFlags {
	if s.prWatch == nil {
		return nil
	}
	fl, err := s.prWatch.Flags(ctx)
	if err != nil {
		s.logger.Warn("ci flags lookup failed", "error", err)
	}
	return fl
}
