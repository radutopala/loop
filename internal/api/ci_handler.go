package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/radutopala/loop/internal/ciwatch"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/forge"
	"github.com/radutopala/loop/internal/githubapi"
	"github.com/radutopala/loop/internal/gitlabapi"
	"github.com/radutopala/loop/internal/gitutil"
)

const ciNotConfiguredMsg = "CI watching not configured"

// CIWatcher watches each thread's PR/MR CI, sends failures on the agent's
// own commits back to it and merges what the owner armed. Satisfied by
// *ciwatch.Watcher.
type CIWatcher interface {
	Watch(channelID string)
	Status(ctx context.Context, channelID string) *events.CIEventData
	Attention(ctx context.Context) (map[string]string, error)
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

// SetCIWatcher wires the CI watcher behind the channel CI endpoints.
func (s *Server) SetCIWatcher(w CIWatcher) {
	s.ci = w
}

// ObserveCI hands the CI watcher the events it follows: agent runs and
// checkout changes. Registered with EventsHub.Observe; it doesn't block.
func (s *Server) ObserveCI(evt Event) {
	if s.ci == nil {
		return
	}
	switch d := evt.Data.(type) {
	case events.AgentStatusEventData:
		s.ci.AgentStatus(evt.ChannelID, d)
	case events.ChannelUpdatedData:
		s.ci.ChannelUpdated(d)
	}
}

// CITarget resolves the channel's PR/MR for the CI watcher, with the
// channel's ci config: nil when its branch has none.
func (s *Server) CITarget(ctx context.Context, channelID string) (*ciwatch.Target, error) {
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
	t := &ciwatch.Target{
		Kind: forge.KindPR,
		Ref:  forge.Ref{Dir: dir, Account: s.configs.ghUser(dir, parent), Number: resp.PR.Number},
		URL:  resp.PR.URL,
		CI:   config.DefaultCIConfig(),
	}
	if resp.PR.Kind == forge.KindMR {
		t.Kind = forge.KindMR
		t.Ref.Account = s.expandHomePath(s.configs.glabConfigDir(dir, parent))
	}
	if cfg := s.configs.merged(dir, parent); cfg != nil {
		t.CI = cfg.CI
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

// ciResponse is GET /api/channels/{id}/ci: the CI of the channel's PR/MR,
// absent when its branch has none.
type ciResponse struct {
	Present bool                `json:"present"`
	CI      *events.CIEventData `json:"ci,omitempty"`
}

// ciLogResponse is GET /api/channels/{id}/ci/log: the tail of a failed
// check's log.
type ciLogResponse struct {
	Log string `json:"log"`
}

type ciAutoFixRequest struct {
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

// handleChannelCI serves GET /api/channels/{id}/ci.
func (s *Server) handleChannelCI(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.ci, ciNotConfiguredMsg) {
		return
	}
	d := s.ci.Status(r.Context(), r.PathValue("id"))
	writeHTTPJSON(w, http.StatusOK, ciResponse{Present: d != nil, CI: d}, s.logger)
}

// handleChannelCIFix serves POST /api/channels/{id}/ci/fix: the current
// failure goes to the thread's agent now.
func (s *Server) handleChannelCIFix(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.ci, ciNotConfiguredMsg) {
		return
	}
	s.ciResult(w, s.ci.FixNow(r.Context(), r.PathValue("id")))
}

// handleChannelCIRerun serves POST /api/channels/{id}/ci/rerun: the
// PR/MR's failed checks run again.
func (s *Server) handleChannelCIRerun(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.ci, ciNotConfiguredMsg) {
		return
	}
	s.ciResult(w, s.ci.Rerun(r.Context(), r.PathValue("id")))
}

// handleChannelCILog serves GET /api/channels/{id}/ci/log?check=<id>.
func (s *Server) handleChannelCILog(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.ci, ciNotConfiguredMsg) {
		return
	}
	log, err := s.ci.Log(r.Context(), r.PathValue("id"), r.URL.Query().Get("check"))
	if err != nil {
		s.ciResult(w, err)
		return
	}
	writeHTTPJSON(w, http.StatusOK, ciLogResponse{Log: log}, s.logger)
}

// handleChannelCIAutoFix serves PUT /api/channels/{id}/ci/auto-fix.
func (s *Server) handleChannelCIAutoFix(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.ci, ciNotConfiguredMsg) {
		return
	}
	var req ciAutoFixRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	s.ciResult(w, s.ci.SetAutoFix(r.Context(), r.PathValue("id"), req.AutoFix))
}

// handlePRMerge serves POST /api/channels/{id}/pr/merge: merges the PR/MR
// now, or arms "merge when ready".
func (s *Server) handlePRMerge(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.ci, ciNotConfiguredMsg) {
		return
	}
	var req prMergeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.WhenReady {
		s.ciResult(w, s.ci.Arm(r.Context(), r.PathValue("id"), req.Method))
		return
	}
	s.ciResult(w, s.ci.MergeNow(r.Context(), r.PathValue("id"), req.Method))
}

// handlePRMergeCancel serves DELETE /api/channels/{id}/pr/merge: disarms
// "merge when ready".
func (s *Server) handlePRMergeCancel(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.ci, ciNotConfiguredMsg) {
		return
	}
	s.ciResult(w, s.ci.CancelMerge(r.Context(), r.PathValue("id")))
}

// ciResult answers a CI action: 204 when done, 404 without a PR/MR or the
// failed check, 409 without a failure to fix, else 502 with the forge's
// error.
func (s *Server) ciResult(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, ciwatch.ErrNoPR), errors.Is(err, ciwatch.ErrNoCheck):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ciwatch.ErrNoFailure):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		http.Error(w, err.Error(), http.StatusBadGateway)
	}
}

// ciAttention returns each thread's CI attention for the channel list; nil
// without a watcher or when it can't be read.
func (s *Server) ciAttention(ctx context.Context) map[string]string {
	if s.ci == nil {
		return nil
	}
	att, err := s.ci.Attention(ctx)
	if err != nil {
		s.logger.Warn("ci attention lookup failed", "error", err)
	}
	return att
}
