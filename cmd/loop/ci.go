package main

import (
	"context"
	"log/slog"

	"github.com/radutopala/loop/internal/api"
	"github.com/radutopala/loop/internal/ciwatch"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/forge"
	"github.com/radutopala/loop/internal/orchestrator"
)

// startCIWatcher watches the threads' PR/MR CI: failures on an agent's own
// commits go back to it, and armed merges happen once checks pass. A store
// without CI state leaves it off.
func startCIWatcher(ctx context.Context, srv *api.Server, store db.Store, hub *api.EventsHub, msgs api.IncomingMessageHandler, forges map[string]forge.Forge, logger *slog.Logger) {
	cs, ok := store.(ciwatch.Store)
	if !ok {
		return
	}
	w := ciwatch.New(ciDeps(srv, store, cs, hub, msgs, forges, logger))
	srv.SetCIWatcher(w)
	hub.Observe(srv.ObserveCI)
	go w.Run(ctx)
}

// ciDeps are the CI watcher's dependencies: the server resolves threads'
// PR/MRs and checkouts, fixes go to the agent as messages from
// ciwatch.AuthorID and notices are system messages in the thread.
func ciDeps(srv *api.Server, store db.Store, cs ciwatch.Store, hub *api.EventsHub, msgs api.IncomingMessageHandler, forges map[string]forge.Forge, logger *slog.Logger) ciwatch.Deps {
	return ciwatch.Deps{
		Resolve: srv.CITarget,
		Head:    srv.ChannelHead,
		Forges:  forges,
		Store:   cs,
		Enqueue: func(ctx context.Context, channelID, content string) {
			msgs.HandleIncomingMessage(ctx, channelID, ciwatch.AuthorID, content, "")
		},
		Notice: func(ctx context.Context, channelID, content string) {
			orchestrator.StoreSystemNotice(ctx, store, hub, channelID, content)
		},
		Broadcast: hub.BroadcastChannelCI,
		Logger:    logger,
	}
}
