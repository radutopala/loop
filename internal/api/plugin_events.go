package api

import (
	"context"
	"slices"
	"time"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/plugin/proxy"
)

// pluginEventTimeout bounds sending one event to the plugins that subscribe
// to it, starting their containers included.
const pluginEventTimeout = 2 * time.Minute

// ForwardToPlugins sends evt, in the background, to the plugins that
// subscribe to its type in its channel. An event in a thread goes to the
// parent channel's plugins with the thread's id; one in a hidden thread,
// such as a plugin's own agent run, goes nowhere.
func (s *Server) ForwardToPlugins(evt Event) {
	if s.plugins == nil || evt.ChannelID == "" || !slices.Contains(plugin.Events, evt.Type) {
		return
	}
	s.deliverAsync(func() {
		ctx, cancel := context.WithTimeout(context.Background(), pluginEventTimeout)
		defer cancel()
		s.forwardToPlugins(ctx, evt)
	})
}

func (s *Server) forwardToPlugins(ctx context.Context, evt Event) {
	ch, err := s.store.GetChannel(ctx, evt.ChannelID)
	if err != nil || ch == nil {
		s.logger.Warn("plugin event: reading the channel", "channel_id", evt.ChannelID, "type", evt.Type, "error", err)
		return
	}
	out := proxy.Event{Type: evt.Type, ChannelID: ch.ChannelID, Data: evt.Data, Timestamp: evt.Timestamp}
	if ch.ParentID != "" {
		if db.IsHiddenKind(ch.Kind) {
			return
		}
		thread := ch
		if ch, err = s.store.GetChannel(ctx, thread.ParentID); err != nil || ch == nil {
			s.logger.Warn("plugin event: reading the parent channel", "channel_id", thread.ParentID, "type", evt.Type, "error", err)
			return
		}
		out.ChannelID, out.ThreadID = ch.ChannelID, thread.ChannelID
	}
	if err := s.plugins.Deliver(ctx, pluginRuntimeChannel(ch), out); err != nil {
		s.logger.Warn("plugin event: delivering", "channel_id", out.ChannelID, "type", evt.Type, "error", err)
	}
}
