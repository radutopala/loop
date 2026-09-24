package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/chatcomponents"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/randutil"
	"github.com/radutopala/loop/internal/types"
)

// maxComponentBytes caps a component's request body: html, css and js
// together. The composed document is stored in the chat message.
const maxComponentBytes = 512 << 10

// handleListComponents lists the chat component templates available to a
// channel; ?format=guide returns them as the guide text the MCP tool gives
// an agent.
func (s *Server) handleListComponents(w http.ResponseWriter, r *http.Request) {
	cfg, _, loopDirs, readFile, ok := s.resolveShortcutContext(w, r)
	if !ok {
		return
	}
	templates := chatcomponents.Resolve(cfg.ChatComponents, loopDirs, readFile)
	if r.URL.Query().Get("format") == "guide" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(chatcomponents.Guide(templates)))
		return
	}
	writeHTTPJSON(w, http.StatusOK, templates, s.logger)
}

type showComponentRequest struct {
	Template string `json:"template"`
	Title    string `json:"title"`
	HTML     string `json:"html"`
	CSS      string `json:"css"`
	JS       string `json:"js"`
}

type showComponentResponse struct {
	MsgID string `json:"msg_id"`
}

// handleShowComponent fills a template with the agent's content and posts it
// to the channel's chat as an inert bot message, grouped under the run in
// progress. Only the desktop app renders components, so other platforms get
// an error telling the agent to answer in text.
func (s *Server) handleShowComponent(w http.ResponseWriter, r *http.Request) {
	channelID := r.URL.Query().Get("channel_id")
	if channelID == "" {
		http.Error(w, "channel_id is required", http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxComponentBytes)
	var req showComponentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
			http.Error(w, fmt.Sprintf("component too large: html, css and js together must stay under %d KB", maxComponentBytes>>10), http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.HTML) == "" && strings.TrimSpace(req.JS) == "" {
		http.Error(w, "html or js is required", http.StatusBadRequest)
		return
	}
	if !requireConfigured(w, s.store, "store not configured") {
		return
	}
	ctx := r.Context()
	ch, err := s.store.GetChannel(ctx, channelID)
	if err != nil {
		http.Error(w, "failed to look up channel", http.StatusInternalServerError)
		return
	}
	if ch == nil {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}
	if ch.Platform != types.PlatformLocal {
		http.Error(w, "chat components render in the Loop desktop app only; answer in text instead", http.StatusBadRequest)
		return
	}
	cfg, _, loopDirs, readFile, ok := s.resolveShortcutContext(w, r)
	if !ok {
		return
	}
	templates := chatcomponents.Resolve(cfg.ChatComponents, loopDirs, readFile)
	tmpl, found := chatcomponents.Find(templates, req.Template)
	if !found {
		names := make([]string, len(templates))
		for i, t := range templates {
			names[i] = t.Name
		}
		http.Error(w, fmt.Sprintf("unknown template %q; available: %s", req.Template, strings.Join(names, ", ")), http.StatusBadRequest)
		return
	}
	title := chatcomponents.Title(req.Title)
	doc := chatcomponents.Compose(tmpl, chatcomponents.Content{Title: title, HTML: req.HTML, CSS: req.CSS, JS: req.JS})
	content := chatcomponents.Fence(tmpl.Name, title, doc)

	triggerMsgID, err := s.store.RunningMessageID(ctx, channelID)
	if err != nil {
		http.Error(w, "failed to look up the running turn", http.StatusInternalServerError)
		return
	}
	msg := &db.Message{
		ChatID:       ch.ID,
		ChannelID:    channelID,
		MsgID:        "component-" + randutil.HexID(16),
		AuthorName:   "agent",
		Content:      content,
		IsBot:        true,
		IsProcessed:  true,
		TriggerMsgID: triggerMsgID,
		CreatedAt:    time.Now().UTC(),
	}
	if err := s.store.InsertMessage(ctx, msg); err != nil {
		http.Error(w, "failed to store component", http.StatusInternalServerError)
		return
	}
	if s.eventsHub != nil {
		s.eventsHub.BroadcastMessageCreated(channelID, events.MessageEventData{
			MsgID:        msg.MsgID,
			AuthorName:   msg.AuthorName,
			Content:      msg.Content,
			IsBot:        true,
			IsProcessed:  true,
			TriggerMsgID: triggerMsgID,
		})
	}
	writeHTTPJSON(w, http.StatusCreated, showComponentResponse{MsgID: msg.MsgID}, s.logger)
}
