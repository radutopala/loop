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
)

// maxLearnProposals limits one propose_learnings call.
const maxLearnProposals = 5

type learnProposalInput struct {
	Kind      string          `json:"kind"`
	Title     string          `json:"title"`
	Rationale string          `json:"rationale"`
	Payload   json.RawMessage `json:"payload"`
}

type createLearnProposalsRequest struct {
	Proposals []learnProposalInput `json:"proposals"`
}

type learnProposalsResponse struct {
	Proposals []*db.LearnProposal `json:"proposals"`
}

// handleCreateLearnProposals files a learn pass's proposals. The id is the
// hidden learn thread; the proposals belong to the channel it learns from.
// Either every proposal is valid and they're all stored, or none are and the
// error says which one to fix.
func (s *Server) handleCreateLearnProposals(w http.ResponseWriter, r *http.Request) {
	var req createLearnProposalsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Proposals) == 0 {
		http.Error(w, "proposals is required", http.StatusBadRequest)
		return
	}
	if len(req.Proposals) > maxLearnProposals {
		http.Error(w, fmt.Sprintf("at most %d proposals per call", maxLearnProposals), http.StatusBadRequest)
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
	if err := s.store.InsertLearnProposals(r.Context(), proposals); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if s.eventsHub != nil {
		s.eventsHub.BroadcastLearnProposals(l.ParentID, proposals)
	}
	writeHTTPJSON(w, http.StatusCreated, learnProposalsResponse{Proposals: proposals}, s.logger)
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
	ch := s.learnChannelFor(w, r)
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

// applyLearnProposal makes the change p describes. Config kinds are
// appended to the project's .loop/config.json (the root checkout's, for
// worktree threads), keeping the user's comments.
func (s *Server) applyLearnProposal(ctx context.Context, p *db.LearnProposal) error {
	v, err := learn.Decode(p.Kind, json.RawMessage(p.Payload))
	if err != nil {
		return err
	}
	ch, err := s.store.GetChannel(ctx, p.ChannelID)
	if err != nil {
		return err
	}
	if ch == nil {
		return errors.New("channel not found")
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
	return s.applyLearnConfig(ctx, ch, v)
}

// applyLearnConfig appends a config-kind proposal to the project config.
// The file stays locked from the duplicate check to the write, so proposals
// applied at once (Apply all) neither drop each other's entries nor both
// add the same one.
func (s *Server) applyLearnConfig(ctx context.Context, ch *db.Channel, v any) error {
	dir, err := s.resolveProjectConfigDirPath(ctx, ch.ChannelID)
	if err != nil {
		return err
	}
	configPath := filepath.Join(dir, ".loop", "config.json")
	defer s.configLocks.lock(configPath)()
	merged, global := s.configs.mergedWithGlobal(ch.DirPath, s.workspace.resolveParentDirPath(ctx, ch.ChannelID))
	if merged == nil {
		return errors.New("loading config failed")
	}
	var (
		path []string
		item any
		seed []any
	)
	switch v := v.(type) {
	case *learn.PromptShortcut:
		if slices.ContainsFunc(merged.PromptShortcuts, func(sc config.PromptShortcut) bool { return sc.Name == v.Name }) {
			return fmt.Errorf("a prompt shortcut named %q already exists", v.Name)
		}
		path, item = []string{"prompt_shortcuts"}, v
	case *learn.BashShortcut:
		if slices.ContainsFunc(merged.BashShortcuts, func(sc config.BashShortcut) bool { return sc.Name == v.Name }) {
			return fmt.Errorf("a bash shortcut named %q already exists", v.Name)
		}
		path, item = []string{"bash_shortcuts"}, v
	case *learn.GateRule:
		key, rule, _ := v.ConfigRule() // Decode already checked it
		if hasGateRule(merged.Gates.Agentgate, rule) {
			return nil // already there: applying it again changes nothing
		}
		path, item = []string{"gates", "agentgate", key}, rule
	case *learn.Mount:
		// Merged project mounts have their relative host paths resolved
		// against the project dir, so compare the proposal resolved the same
		// way against the dir it's written to.
		resolved, _ := config.ResolveMount(v.Mount, dir) // Decode already checked it
		if slices.Contains(merged.Mounts, resolved) {
			return fmt.Errorf("mount %q already exists", v.Mount)
		}
		// Project mounts replace the global ones, so a project's first mount
		// starts from the global list or the rest would silently go.
		for _, m := range global.Mounts {
			seed = append(seed, m)
		}
		path, item = []string{"mounts"}, v.Mount
	}
	return hjsonedit.Append(s.sys, configPath, path, item, seed)
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
