package main

import (
	"context"
	"log/slog"

	"github.com/radutopala/loop/internal/api"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/forge"
	"github.com/radutopala/loop/internal/orchestrator"
	"github.com/radutopala/loop/internal/prwatch"
)

// startPRWatcher watches the threads' PR/MR CI: failures on an agent's own
// commits go back to it, and armed merges happen once checks pass. A store
// without CI state leaves it off.
func startPRWatcher(ctx context.Context, srv *api.Server, store db.Store, hub *api.EventsHub, msgs api.IncomingMessageHandler, forges map[string]forge.Forge, logger *slog.Logger) {
	cs, ok := store.(prwatch.Store)
	if !ok {
		return
	}
	w := prwatch.New(prDeps(srv, store, cs, hub, msgs, forges, logger))
	srv.SetPRWatcher(w)
	hub.Observe(srv.ObservePR)
	go w.Run(ctx)
}

// prDeps are the PR watcher's dependencies: the server resolves threads'
// PR/MRs and checkouts, fixes go to the agent as messages from
// prwatch.AuthorID and notices are system messages in the thread.
func prDeps(srv *api.Server, store db.Store, cs prwatch.Store, hub *api.EventsHub, msgs api.IncomingMessageHandler, forges map[string]forge.Forge, logger *slog.Logger) prwatch.Deps {
	return prwatch.Deps{
		Resolve: srv.PRTarget,
		Head:    srv.ChannelHead,
		Forges:  forges,
		Store:   cs,
		Enqueue: func(ctx context.Context, channelID, content string) {
			msgs.HandleIncomingMessage(ctx, channelID, prwatch.AuthorID, content, "")
		},
		Notice: func(ctx context.Context, channelID, content string) {
			orchestrator.StoreSystemNotice(ctx, store, hub, channelID, content)
		},
		Broadcast: hub.BroadcastChannelPR,
		Logger:    logger,
	}
}
