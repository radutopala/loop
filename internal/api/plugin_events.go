package api

import (
	"context"
	"slices"
	"time"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/plugin/proxy"
	"github.com/radutopala/loop/internal/plugin/runtime"
)

// pluginEventTimeout bounds sending one event to the plugins that subscribe
// to it, starting their containers included.
const pluginEventTimeout = 2 * time.Minute

// ForwardToPlugins sends evt, in the background, to the plugins that
// subscribe to its type in its channel. An event in a thread carries the
// thread's id and goes to the thread's container, the parent channel's or
// both, as each plugin asks; one in a hidden thread, such as a plugin's own
// agent run, goes nowhere.
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
	var thread *runtime.Channel
	if ch.ParentID != "" {
		if db.IsHiddenKind(ch.Kind) {
			return
		}
		t := pluginRuntimeChannel(ch)
		thread = &t
		parentID := ch.ParentID
		if ch, err = s.store.GetChannel(ctx, parentID); err != nil || ch == nil {
			s.logger.Warn("plugin event: reading the parent channel", "channel_id", parentID, "type", evt.Type, "error", err)
			return
		}
		out.ChannelID, out.ThreadID = ch.ChannelID, t.ID
	}
	if err := s.plugins.Deliver(ctx, pluginRuntimeChannel(ch), thread, out); err != nil {
		s.logger.Warn("plugin event: delivering", "channel_id", out.ChannelID, "type", evt.Type, "error", err)
	}
}
