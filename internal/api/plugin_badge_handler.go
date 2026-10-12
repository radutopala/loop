package api

import (
	"context"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin"
)

// A plugin badge's limits, in characters.
const (
	pluginBadgeTextMax  = 16
	pluginBadgeTitleMax = 200
)

// PluginBadgeStore keeps the badges plugins show on sidebar rows.
type PluginBadgeStore interface {
	SetPluginBadge(ctx context.Context, b *db.PluginBadge) error
	DeletePluginBadge(ctx context.Context, pluginID, channelID string) error
	ListPluginBadges(ctx context.Context) ([]*db.PluginBadge, error)
}

// SetPluginBadges keeps plugin badges in store and serves the badge routes.
func (s *Server) SetPluginBadges(store PluginBadgeStore) {
	s.badges = store
}

// pluginBadge is a badge on a channel's row: the plugin's label and its
// tooltip.
type pluginBadge struct {
	PluginID string `json:"plugin_id"`
	Text     string `json:"text"`
	Title    string `json:"title,omitempty"`
}

type pluginBadgeRequest struct {
	Text  string `json:"text"`
	Title string `json:"title"`
}

// pluginBadgeCaller is pluginCaller for the badge routes, which also need
// the badge store.
func (s *Server) pluginBadgeCaller(w http.ResponseWriter, r *http.Request) (string, *db.Channel, bool) {
	p, ch, ok := s.pluginCaller(w, r, plugin.CapUIBadge)
	if !ok {
		return "", nil, false
	}
	if !requireConfigured(w, s.badges, "plugin badges not configured") {
		return "", nil, false
	}
	return p.PluginID, ch, true
}

// handlePutPluginBadge sets the calling plugin's badge on its channel's
// sidebar row.
func (s *Server) handlePutPluginBadge(w http.ResponseWriter, r *http.Request) {
	pluginID, ch, ok := s.pluginBadgeCaller(w, r)
	if !ok {
		return
	}
	var req pluginBadgeRequest
	r.Body = http.MaxBytesReader(w, r.Body, pluginEventMax)
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if msg := badgeRefusal(req); msg != "" {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}
	b := &db.PluginBadge{PluginID: pluginID, ChannelID: ch.ChannelID, Text: req.Text, Title: req.Title}
	if err := s.badges.SetPluginBadge(r.Context(), b); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.broadcastPluginBadge(ch.ChannelID, pluginBadge{PluginID: pluginID, Text: req.Text, Title: req.Title})
	w.WriteHeader(http.StatusNoContent)
}

// handleDeletePluginBadge clears the calling plugin's badge.
func (s *Server) handleDeletePluginBadge(w http.ResponseWriter, r *http.Request) {
	pluginID, ch, ok := s.pluginBadgeCaller(w, r)
	if !ok {
		return
	}
	if err := s.badges.DeletePluginBadge(r.Context(), pluginID, ch.ChannelID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.broadcastPluginBadge(ch.ChannelID, pluginBadge{PluginID: pluginID})
	w.WriteHeader(http.StatusNoContent)
}

// badgeRefusal returns what's wrong with a badge, or "" when it's fine.
func badgeRefusal(req pluginBadgeRequest) string {
	switch {
	case req.Text == "":
		return "text is required"
	case utf8.RuneCountInString(req.Text) > pluginBadgeTextMax:
		return "text is at most 16 characters"
	case strings.ContainsFunc(req.Text, unicode.IsControl):
		return "text is one line, without control characters"
	case utf8.RuneCountInString(req.Title) > pluginBadgeTitleMax:
		return "title is at most 200 characters"
	}
	return ""
}

// broadcastPluginBadge tells every window a channel's badge changed; an
// empty text clears it.
func (s *Server) broadcastPluginBadge(channelID string, b pluginBadge) {
	if s.eventsHub != nil {
		s.eventsHub.Broadcast(Event{Type: "plugin.badge", ChannelID: channelID, Global: true, Data: b})
	}
}

// pluginBadgesByChannel returns the plugin badges by channel, for the
// channel list; none when plugins or badges are off. A failure only loses
// the badges, so it's logged.
func (s *Server) pluginBadgesByChannel(ctx context.Context) map[string][]pluginBadge {
	if s.plugins == nil || s.badges == nil {
		return nil
	}
	badges, err := s.badges.ListPluginBadges(ctx)
	if err != nil {
		s.logger.Warn("plugin badges lookup failed", "error", err)
		return nil
	}
	out := make(map[string][]pluginBadge)
	for _, b := range badges {
		out[b.ChannelID] = append(out[b.ChannelID], pluginBadge{PluginID: b.PluginID, Text: b.Text, Title: b.Title})
	}
	return out
}

// pluginBadgesOf returns the plugin's badges; none when badges are off or
// can't be read, which only leaves other windows showing them until they
// reload.
func (s *Server) pluginBadgesOf(ctx context.Context, pluginID string) []*db.PluginBadge {
	if s.badges == nil {
		return nil
	}
	badges, err := s.badges.ListPluginBadges(ctx)
	if err != nil {
		s.logger.Warn("plugin badges lookup failed", "error", err)
		return nil
	}
	var out []*db.PluginBadge
	for _, b := range badges {
		if b.PluginID == pluginID {
			out = append(out, b)
		}
	}
	return out
}

// prunePluginBadges clears the badges no longer shown: those of a deleted
// channel, and those of a plugin that may no longer show one in its
// channel, being broken, no longer approved for ui.badge or no longer
// enabled by the channel's project. A badge whose state can't be read is
// kept.
func (s *Server) prunePluginBadges(ctx context.Context) {
	if s.plugins == nil || s.badges == nil {
		return
	}
	badges, err := s.badges.ListPluginBadges(ctx)
	if err != nil {
		s.logger.Warn("plugin badges lookup failed", "error", err)
		return
	}
	for _, b := range badges {
		if s.pluginBadgeShown(ctx, b) {
			continue
		}
		if err := s.badges.DeletePluginBadge(ctx, b.PluginID, b.ChannelID); err != nil {
			s.logger.Warn("clearing a plugin badge", "plugin", b.PluginID, "channel_id", b.ChannelID, "error", err)
			continue
		}
		s.broadcastPluginBadge(b.ChannelID, pluginBadge{PluginID: b.PluginID})
	}
}

// pluginBadgeShown reports whether b still shows: its channel exists and
// its plugin may show it there, or that can't be told.
func (s *Server) pluginBadgeShown(ctx context.Context, b *db.PluginBadge) bool {
	ch, err := s.store.GetChannel(ctx, b.ChannelID)
	if err != nil {
		return true
	}
	if ch == nil {
		return false
	}
	shown, err := s.plugins.Shows(ctx, b.PluginID, plugin.CapUIBadge, pluginRuntimeChannel(ch))
	return err != nil || shown
}
