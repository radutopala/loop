package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/config/hjsonedit"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/learn"
	"github.com/radutopala/loop/internal/types"
	"github.com/radutopala/loop/internal/unidiff"
)

// maxLearnProposals limits the proposals one propose_learnings call files,
// maxLearnWithdrawals those it withdraws.
const (
	maxLearnProposals   = 5
	maxLearnWithdrawals = 20
)

type learnProposalInput struct {
	Kind      string          `json:"kind"`
	Title     string          `json:"title"`
	Rationale string          `json:"rationale"`
	Payload   json.RawMessage `json:"payload"`
	// Replaces withdraws this open proposal, which the new one supersedes.
	Replaces int64 `json:"replaces"`
}

type learnWithdrawInput struct {
	ID     int64  `json:"id"`
	Reason string `json:"reason"`
}

type createLearnProposalsRequest struct {
	Proposals []learnProposalInput `json:"proposals"`
	Withdraw  []learnWithdrawInput `json:"withdraw"`
}

type learnProposalsResponse struct {
	Proposals []*db.LearnProposal `json:"proposals"`
	// Withdrawn are the proposals a propose call withdrew.
	Withdrawn []*db.LearnProposal `json:"withdrawn,omitempty"`
}

// handleCreateLearnProposals files a learn pass's proposals and withdraws
// the open ones it found stale. The id is the hidden learn thread; the
// proposals belong to the channel it learns from. Either every item is valid
// and it all goes through, or nothing does and the error says which item to
// fix.
func (s *Server) handleCreateLearnProposals(w http.ResponseWriter, r *http.Request) {
	var req createLearnProposalsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Proposals) == 0 && len(req.Withdraw) == 0 {
		http.Error(w, "proposals or withdraw is required", http.StatusBadRequest)
		return
	}
	if len(req.Proposals) > maxLearnProposals {
		http.Error(w, fmt.Sprintf("at most %d proposals per call", maxLearnProposals), http.StatusBadRequest)
		return
	}
	if len(req.Withdraw) > maxLearnWithdrawals {
		http.Error(w, fmt.Sprintf("at most %d withdrawals per call", maxLearnWithdrawals), http.StatusBadRequest)
		return
	}
	withdraw, err := learnWithdrawals(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}
	l, err := s.store.GetChannel(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if l == nil || l.Kind != db.ChannelKindLearn {
		http.Error(w, "not a learn thread: propose_learnings only works in a learn pass", http.StatusNotFound)
		return
	}
	proposals := make([]*db.LearnProposal, 0, len(req.Proposals))
	for i, in := range req.Proposals {
		p, err := newLearnProposal(l, in)
		if err != nil {
			http.Error(w, fmt.Sprintf("proposal %d: %v", i+1, err), http.StatusBadRequest)
			return
		}
		proposals = append(proposals, p)
	}
	// The proposals belong to the turn the running (else the last done)
	// pass reviews.
	pass, err := s.store.LatestLearnPass(r.Context(), l.ChannelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if pass != nil {
		for _, p := range proposals {
			p.MessageID = pass.MessageID
		}
	}
	withdrawn, err := s.store.FileLearnProposals(r.Context(), l.ParentID, proposals, withdraw)
	var werr *db.LearnWithdrawError
	if errors.As(err, &werr) {
		http.Error(w, werr.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if s.eventsHub != nil {
		s.eventsHub.BroadcastLearnProposals(l.ParentID, proposals, withdrawn)
	}
	writeHTTPJSON(w, http.StatusCreated, learnProposalsResponse{Proposals: proposals, Withdrawn: withdrawn}, s.logger)
}

// learnWithdrawals checks a propose call's withdrawals and gathers them with
// the proposals its new ones replace. A proposal may be withdrawn once per
// call.
func learnWithdrawals(req createLearnProposalsRequest) ([]db.LearnWithdrawal, error) {
	var out []db.LearnWithdrawal
	seen := make(map[int64]bool)
	add := func(id int64, reason string) error {
		if seen[id] {
			return fmt.Errorf("proposal %d is withdrawn twice", id)
		}
		seen[id] = true
		out = append(out, db.LearnWithdrawal{ID: id, Reason: reason})
		return nil
	}
	for i, in := range req.Withdraw {
		reason, err := learn.ValidateReason(in.Reason)
		if err == nil {
			err = add(in.ID, reason)
		}
		if err != nil {
			return nil, fmt.Errorf("withdraw %d: %w", i+1, err)
		}
	}
	for i, in := range req.Proposals {
		if in.Replaces == 0 {
			continue
		}
		if err := add(in.Replaces, learn.ReplacedReason); err != nil {
			return nil, fmt.Errorf("proposal %d: replaces: %w", i+1, err)
		}
	}
	return out, nil
}

// newLearnProposal checks one proposed item and builds its row for the
// channel learn thread l learns from.
func newLearnProposal(l *db.Channel, in learnProposalInput) (*db.LearnProposal, error) {
	title, rationale, payload, err := learn.Validate(in.Kind, in.Title, in.Rationale, in.Payload)
	if err != nil {
		return nil, err
	}
	return &db.LearnProposal{
		ChannelID:      l.ParentID,
		LearnChannelID: l.ChannelID,
		Kind:           in.Kind,
		Title:          title,
		Rationale:      rationale,
		Payload:        string(payload),
	}, nil
}

// handleListLearnProposals returns a channel's proposals, newest first.
func (s *Server) handleListLearnProposals(w http.ResponseWriter, r *http.Request) {
	ch := s.visibleChannelFor(w, r)
	if ch == nil {
		return
	}
	proposals, err := s.store.ListLearnProposals(r.Context(), ch.ChannelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if proposals == nil {
		proposals = []*db.LearnProposal{}
	}
	writeHTTPJSON(w, http.StatusOK, learnProposalsResponse{Proposals: proposals}, s.logger)
}

type learnPassesResponse struct {
	Passes []*db.LearnPass `json:"passes"`
}

// handleListLearnPasses returns a channel's learn passes, newest first.
func (s *Server) handleListLearnPasses(w http.ResponseWriter, r *http.Request) {
	ch := s.visibleChannelFor(w, r)
	if ch == nil {
		return
	}
	passes, err := s.store.ListLearnPasses(r.Context(), ch.ChannelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeHTTPJSON(w, http.StatusOK, learnPassesResponse{Passes: append([]*db.LearnPass{}, passes...)}, s.logger)
}

// LearnTurner starts a learn pass over one chat turn on demand: it returns
// the turn's pass already queued or running, or queues a new one (see
// orchestrator.LearnTurn).
type LearnTurner interface {
	LearnTurn(ctx context.Context, ch *db.Channel, messageID string) (*db.LearnPass, error)
}

// SetLearnTurner configures what POST /api/channels/{id}/learn/passes
// starts learn passes with.
func (s *Server) SetLearnTurner(l LearnTurner) {
	s.learnTurner = l
}

type learnTurnRequest struct {
	MessageID string `json:"message_id"`
}

// handleLearnTurn starts a learn pass over the turn that ended with
// message_id: it returns the turn's pass already queued or running, or
// queues a new one.
func (s *Server) handleLearnTurn(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.learnTurner, "learn not configured") {
		return
	}
	var req learnTurnRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.MessageID == "" {
		http.Error(w, "message_id is required", http.StatusBadRequest)
		return
	}
	ch := s.visibleChannelFor(w, r)
	if ch == nil {
		return
	}
	p, err := s.learnTurner.LearnTurn(r.Context(), ch, req.MessageID)
	switch {
	case errors.Is(err, learn.ErrUnavailable), errors.Is(err, learn.ErrNotATurn):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case errors.Is(err, learn.ErrNoSession):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case errors.Is(err, db.ErrParentGone):
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeHTTPJSON(w, http.StatusOK, p, s.logger)
}

// claimLearnProposal loads the proposal at the path's id and moves it to
// applying, failing the request when it's missing or already settled.
func (s *Server) claimLearnProposal(ctx context.Context, w http.ResponseWriter, r *http.Request) *db.LearnProposal {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return nil
	}
	id, ok := parsePathInt64(w, r, "id")
	if !ok {
		return nil
	}
	p, err := s.store.GetLearnProposal(ctx, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return nil
	}
	if p == nil {
		http.Error(w, "proposal not found", http.StatusNotFound)
		return nil
	}
	ok, err = s.store.ClaimLearnProposal(ctx, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return nil
	}
	if !ok {
		// Read it again: a learn pass may have withdrawn it since the lookup.
		if cur, err := s.store.GetLearnProposal(ctx, id); err == nil && cur != nil {
			p = cur
		}
		http.Error(w, "proposal is already "+p.Status, http.StatusConflict)
		return nil
	}
	return p
}

// settleLearnProposal records a claimed proposal's outcome, tells every
// window and returns it.
func (s *Server) settleLearnProposal(ctx context.Context, w http.ResponseWriter, p *db.LearnProposal, status, errText string) {
	if err := s.store.SetLearnProposalStatus(ctx, p.ID, status, errText); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	p.Status, p.Error = status, errText
	if s.eventsHub != nil {
		s.eventsHub.BroadcastLearnProposalUpdated(p)
	}
	writeHTTPJSON(w, http.StatusOK, p, s.logger)
}

// handleApplyLearnProposal applies a pending (or previously failed)
// proposal. A failure is recorded on the proposal, not returned as an HTTP
// error, so it can be shown beside it and retried. Once claimed, the
// proposal is applied and settled even if the client goes away: a
// cancelled apply would leave it applying until it goes stale and can be
// claimed again, applying it twice.
func (s *Server) handleApplyLearnProposal(w http.ResponseWriter, r *http.Request) {
	ctx := context.WithoutCancel(r.Context())
	p := s.claimLearnProposal(ctx, w, r)
	if p == nil {
		return
	}
	status, errText := db.LearnApplied, ""
	if err := s.applyLearnProposal(ctx, p); err != nil {
		status, errText = db.LearnFailed, err.Error()
	}
	s.settleLearnProposal(ctx, w, p, status, errText)
}

// handleDismissLearnProposal drops a pending or failed proposal, settling
// it like handleApplyLearnProposal even if the client goes away.
func (s *Server) handleDismissLearnProposal(w http.ResponseWriter, r *http.Request) {
	ctx := context.WithoutCancel(r.Context())
	p := s.claimLearnProposal(ctx, w, r)
	if p == nil {
		return
	}
	s.settleLearnProposal(ctx, w, p, db.LearnDismissed, "")
}

// learnProposalTarget decodes p's payload and loads the channel it changes.
func (s *Server) learnProposalTarget(ctx context.Context, p *db.LearnProposal) (any, *db.Channel, error) {
	v, err := learn.Decode(p.Kind, json.RawMessage(p.Payload))
	if err != nil {
		return nil, nil, err
	}
	ch, err := s.store.GetChannel(ctx, p.ChannelID)
	if err != nil {
		return nil, nil, err
	}
	if ch == nil {
		return nil, nil, errors.New("channel not found")
	}
	return v, ch, nil
}

// applyLearnProposal makes the change p describes. Config kinds are
// appended to the project's .loop/config.json (the root checkout's, for
// worktree threads), keeping the user's comments.
func (s *Server) applyLearnProposal(ctx context.Context, p *db.LearnProposal) error {
	v, ch, err := s.learnProposalTarget(ctx, p)
	if err != nil {
		return err
	}
	switch v := v.(type) {
	case *learn.Rename:
		name := strings.TrimSpace(v.Name)
		if err := s.store.UpdateChannelName(ctx, ch.ChannelID, name); err != nil {
			return err
		}
		if s.eventsHub != nil {
			s.eventsHub.BroadcastChannelUpdated(events.ChannelUpdatedData{ChannelID: ch.ChannelID, Name: name})
		}
		return nil
	case *learn.Description:
		description := strings.TrimSpace(v.Description)
		if err := s.store.UpdateChannelDescription(ctx, ch.ChannelID, description); err != nil {
			return err
		}
		if s.eventsHub != nil {
			s.eventsHub.BroadcastChannelUpdated(events.ChannelUpdatedData{ChannelID: ch.ChannelID, Description: &description})
		}
		return nil
	case *learn.TicketURL:
		ticketURL := strings.TrimSpace(v.TicketURL)
		if err := s.store.UpdateChannelTicketURL(ctx, ch.ChannelID, ticketURL); err != nil {
			return err
		}
		if s.eventsHub != nil {
			s.eventsHub.BroadcastChannelUpdated(events.ChannelUpdatedData{ChannelID: ch.ChannelID, TicketURL: &ticketURL})
		}
		return nil
	case *learn.ScheduledTask:
		task := &db.ScheduledTask{
			ChannelID:     s.resolveTaskChannelID(ctx, ch.ChannelID),
			Schedule:      v.Schedule,
			Type:          db.TaskType(v.Type),
			Prompt:        v.Prompt,
			BashScript:    v.BashScript,
			AutoDeleteSec: v.AutoDeleteSec,
			Enabled:       true,
		}
		id, err := s.scheduler.AddTask(ctx, task)
		if err != nil {
			return err
		}
		if s.eventsHub != nil {
			s.eventsHub.BroadcastTaskCreated(events.TaskEventData{TaskID: id, ChannelID: task.ChannelID})
		}
		return nil
	}
	_, _, _, err = s.editLearnConfig(ctx, ch, v, true)
	return err
}

// isLearnConfigKind reports whether v, a decoded proposal payload, is one
// editLearnConfig appends to the project config.
func isLearnConfigKind(v any) bool {
	switch v.(type) {
	case *learn.PromptShortcut, *learn.BashShortcut, *learn.GateRule, *learn.Mount:
		return true
	}
	return false
}

// editLearnConfig appends a config-kind proposal to the project config at
// configPath, returning the file before (nil when missing) and after. With
// write false it only works out the edit, for the preview: apply and
// preview make the same checks and the same change. before and after are
// both nil when the change is already there. The file stays locked from
// the duplicate check to the write, so proposals applied at once (Apply
// all) neither drop each other's entries nor both add the same one.
func (s *Server) editLearnConfig(ctx context.Context, ch *db.Channel, v any, write bool) (configPath string, before, after []byte, err error) {
	dir, err := s.resolveProjectConfigDirPath(ctx, ch.ChannelID)
	if err != nil {
		return "", nil, nil, err
	}
	configPath = filepath.Join(dir, ".loop", "config.json")
	defer s.configLocks.lock(configPath)()
	merged := s.configs.merged(ch.DirPath, s.workspace.resolveParentDirPath(ctx, ch.ChannelID))
	if merged == nil {
		return configPath, nil, nil, errors.New("loading config failed")
	}
	var (
		path []string
		item any
	)
	switch v := v.(type) {
	case *learn.PromptShortcut:
		if slices.ContainsFunc(merged.PromptShortcuts, func(sc config.PromptShortcut) bool { return sc.Name == v.Name }) {
			return configPath, nil, nil, fmt.Errorf("a prompt shortcut named %q already exists", v.Name)
		}
		path, item = []string{"prompt_shortcuts"}, v
	case *learn.BashShortcut:
		if slices.ContainsFunc(merged.BashShortcuts, func(sc config.BashShortcut) bool { return sc.Name == v.Name }) {
			return configPath, nil, nil, fmt.Errorf("a bash shortcut named %q already exists", v.Name)
		}
		path, item = []string{"bash_shortcuts"}, v
	case *learn.GateRule:
		key, rule, _ := v.ConfigRule() // Decode already checked it
		if hasGateRule(merged.Gates.Agentgate, rule) {
			return configPath, nil, nil, nil // already there: applying it again changes nothing
		}
		path, item = []string{"gates", "agentgate", key}, rule
	case *learn.Mount:
		// Merged project mounts have their relative host paths resolved
		// against the project dir, so compare the proposal resolved the same
		// way against the dir it's written to.
		resolved, _ := config.ResolveMount(v.Mount, dir) // Decode already checked it
		if slices.Contains(merged.Mounts, resolved) {
			return configPath, nil, nil, fmt.Errorf("mount %q already exists", v.Mount)
		}
		path, item = []string{"mounts"}, v.Mount
	}
	edit := hjsonedit.Appended
	if write {
		edit = hjsonedit.AppendData
	}
	before, after, err = edit(s.sys, configPath, path, item)
	if err != nil {
		return configPath, nil, nil, err
	}
	if write && s.projectTrust != nil {
		// The owner reviewed the proposal and applied it.
		s.keepProjectTrust(dir, before, after)
	}
	return configPath, before, after, nil
}

// learnPreviewResponse is the edit applying a proposal would make: the
// config file it changes and a unified diff of the change ("" when it's
// already there). Error is why applying it would fail. All are empty for
// kinds that edit no file.
type learnPreviewResponse struct {
	Path  string `json:"path,omitempty"`
	Diff  string `json:"diff,omitempty"`
	Error string `json:"error,omitempty"`
}

// handlePreviewLearnProposal shows what applying a proposal would change in
// the project config, without changing it: the same edit apply makes.
func (s *Server) handlePreviewLearnProposal(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}
	id, ok := parsePathInt64(w, r, "id")
	if !ok {
		return
	}
	p, err := s.store.GetLearnProposal(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if p == nil {
		http.Error(w, "proposal not found", http.StatusNotFound)
		return
	}
	var resp learnPreviewResponse
	v, ch, err := s.learnProposalTarget(r.Context(), p)
	switch {
	case err != nil:
		resp.Error = err.Error()
	case isLearnConfigKind(v):
		path, before, after, err := s.editLearnConfig(r.Context(), ch, v, false)
		resp.Path = path
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Diff = learnConfigDiff(path, before, after)
		}
	}
	writeHTTPJSON(w, http.StatusOK, resp, s.logger)
}

// learnConfigDiff renders a config edit as a unified diff, from /dev/null
// for a file it creates. An edit that changes nothing has none.
func learnConfigDiff(path string, before, after []byte) string {
	if after == nil {
		return ""
	}
	from := path
	if before == nil {
		from = "/dev/null"
	}
	return unidiff.Diff(from, path, string(before), string(after))
}

// hasGateRule reports whether gate already has rule (a *types.PathRule,
// *types.CommandRule or *types.FileRule, as learn.GateRule.ConfigRule
// returns). Rules are compared by their JSON, so a list left out and an
// empty one match.
func hasGateRule(gate config.AgentgateConfig, rule any) bool {
	switch r := rule.(type) {
	case *types.PathRule:
		return containsJSON(gate.PathRules, r)
	case *types.CommandRule:
		return containsJSON(gate.CommandRules, r)
	default:
		return containsJSON(gate.FileRules, rule)
	}
}

// containsJSON reports whether any of items marshals to the same JSON as v.
func containsJSON[T any](items []T, v any) bool {
	want, _ := json.Marshal(v)
	return slices.ContainsFunc(items, func(item T) bool {
		got, _ := json.Marshal(item)
		return bytes.Equal(got, want)
	})
}
