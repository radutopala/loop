package api

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
)

// EventAttentionChanged tells every window that something waiting on the
// owner came or went: a question, a plan, a gate approval or a PR's state.
// A failed run's notification comes and goes with notifications.changed,
// itself global.
const EventAttentionChanged = "attention.changed"

// attentionEvents are the event types that change what waits on the owner.
var attentionEvents = map[string]bool{
	EventAskUser:               true,
	EventAskResolved:           true,
	EventExitPlan:              true,
	EventPlanResolved:          true,
	EventGateApprovalRequested: true,
	EventGateApprovalResolved:  true,
	EventChannelPR:             true,
}

// Kinds of attention items. Asks, plans and gates hold an agent until the
// owner acts; PRs and failed runs don't.
const (
	attentionAsk  = "ask"
	attentionPlan = "plan"
	attentionGate = "gate"
	attentionPR   = "pr"
	attentionRun  = "run"
)

// attentionItem is one thing waiting on the owner.
type attentionItem struct {
	Kind        string `json:"kind"`
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name,omitempty"`
	// ParentName is the parent channel's name, for a thread.
	ParentName string `json:"parent_name,omitempty"`
	Title      string `json:"title"`
	Detail     string `json:"detail,omitempty"`
	// Ref identifies the item within its kind and channel: a card's tool
	// use id, a gate request's id, a notification's id.
	Ref string `json:"ref,omitempty"`
	// Blocking is whether an agent is held until the owner acts.
	Blocking bool `json:"blocking"`
	// Questions are a question item's, for answering it in the inbox.
	Questions []events.AskUserQuestion `json:"questions,omitempty"`
}

// attentionResponse is what GET /api/attention answers.
type attentionResponse struct {
	Items []attentionItem `json:"items"`
}

// observeAttention tells every window when what waits on the owner may
// have changed. It runs on the broadcasting goroutine, so the broadcast
// runs apart.
func (s *Server) observeAttention(evt Event) {
	if !attentionEvents[evt.Type] {
		return
	}
	s.deliverAsync(func() {
		s.eventsHub.Broadcast(Event{Type: EventAttentionChanged, Global: true})
	})
}

// handleListAttention lists what waits on the owner across every project:
// questions, plans, gate approvals, PRs and unread failed runs, the ones
// holding an agent first. Each goes away with its cause.
func (s *Server) handleListAttention(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	items := []attentionItem{}
	if s.pendingAsks != nil {
		for _, e := range s.pendingAsks.ListAskedChannels() {
			items = append(items, attentionItem{Kind: attentionAsk, ChannelID: e.ChannelID, Title: "Question", Detail: questionText(e.Data), Ref: e.Data.ToolUseID, Blocking: true, Questions: e.Data.Questions})
		}
	}
	if s.pendingPlans != nil {
		for _, e := range s.pendingPlans.ListPlannedChannels() {
			items = append(items, attentionItem{Kind: attentionPlan, ChannelID: e.ChannelID, Title: "Plan to approve", Detail: firstLine(e.Data.Plan), Ref: e.Data.ToolUseID, Blocking: true})
		}
	}
	if s.pendingApprovals != nil {
		for _, a := range s.pendingApprovals.ListPending() {
			items = append(items, attentionItem{Kind: attentionGate, ChannelID: a.ChannelID, Title: a.Message, Detail: strings.TrimSpace(a.Kind + " " + a.Target), Ref: a.ReqID, Blocking: true})
		}
	}
	for ch, f := range s.prFlags(ctx) {
		if detail := prAttentionText(f); detail != "" {
			items = append(items, attentionItem{Kind: attentionPR, ChannelID: ch, Title: "PR needs attention", Detail: detail})
		}
	}
	items = append(items, s.unreadRunFailures(ctx)...)
	s.nameAttention(ctx, items)
	slices.SortStableFunc(items, func(a, b attentionItem) int {
		return cmp.Or(cmp.Compare(rank(a), rank(b)), strings.Compare(a.Kind+"\x00"+a.ChannelID+"\x00"+a.Ref, b.Kind+"\x00"+b.ChannelID+"\x00"+b.Ref))
	})
	writeHTTPJSON(w, http.StatusOK, attentionResponse{Items: items}, s.logger)
}

// rank orders the items holding an agent before the rest.
func rank(it attentionItem) int {
	if it.Blocking {
		return 0
	}
	return 1
}

// nameAttention fills in each item's channel name, and its parent's for a
// thread, leaving them empty for a channel that can't be read.
func (s *Server) nameAttention(ctx context.Context, items []attentionItem) {
	if s.store == nil {
		return
	}
	channels := map[string]*db.Channel{}
	get := func(id string) *db.Channel {
		if c, ok := channels[id]; ok {
			return c
		}
		c, err := s.store.GetChannel(ctx, id)
		if err != nil {
			c = nil
		}
		channels[id] = c
		return c
	}
	for i := range items {
		c := get(items[i].ChannelID)
		if c == nil {
			continue
		}
		items[i].ChannelName = c.Name
		if c.ParentID == "" {
			continue
		}
		if p := get(c.ParentID); p != nil {
			items[i].ParentName = p.Name
		}
	}
}

// unreadRunFailures returns the unread failed-run notifications as items.
func (s *Server) unreadRunFailures(ctx context.Context) []attentionItem {
	if s.notifications == nil {
		return nil
	}
	counts, err := s.notifications.UnreadNotificationCounts(ctx)
	if err != nil {
		s.logger.Warn("attention: unread notification counts failed", "error", err)
		return nil
	}
	var out []attentionItem
	for ch, n := range counts {
		list, err := s.notifications.ListNotifications(ctx, ch, n)
		if err != nil {
			s.logger.Warn("attention: listing notifications failed", "channel_id", ch, "error", err)
			continue
		}
		for _, nt := range list {
			if nt.Read || nt.Kind != db.NotificationRun {
				continue
			}
			out = append(out, attentionItem{Kind: attentionRun, ChannelID: ch, Title: nt.Title, Detail: nt.Body, Ref: strconv.FormatInt(nt.ID, 10)})
		}
	}
	return out
}

// questionText is an ask's questions, one per line, each with its options.
func questionText(d events.AskUserQuestionEventData) string {
	qs := make([]string, 0, len(d.Questions))
	for _, q := range d.Questions {
		line := q.Question
		if len(q.Options) > 0 {
			opts := make([]string, 0, len(q.Options))
			for _, o := range q.Options {
				opts = append(opts, o.Label)
			}
			line += " [" + strings.Join(opts, " | ") + "]"
		}
		qs = append(qs, line)
	}
	return strings.Join(qs, "\n")
}

// firstLine is text's first non-empty line, without a Markdown heading's
// hashes.
func firstLine(text string) string {
	for line := range strings.SplitSeq(text, "\n") {
		if line = strings.TrimSpace(strings.TrimLeft(line, "# ")); line != "" {
			return line
		}
	}
	return ""
}

// prAttentionText says what a PR's flags ask of the owner, or "" for none.
// A CI failure its agent is fixing asks nothing yet.
func prAttentionText(f events.PRFlags) string {
	var parts []string
	switch f.Attention {
	case events.PRAttentionFailed:
		parts = append(parts, "CI failed")
	case events.PRAttentionConflict:
		parts = append(parts, "merge conflict")
	}
	switch {
	case f.UnresolvedThreads == 1:
		parts = append(parts, "1 unresolved review thread")
	case f.UnresolvedThreads > 1:
		parts = append(parts, fmt.Sprintf("%d unresolved review threads", f.UnresolvedThreads))
	}
	return strings.Join(parts, "; ")
}
