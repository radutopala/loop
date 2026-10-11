package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/chatcomponents"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/randutil"
	"github.com/radutopala/loop/internal/types"
)

// pluginCardTag is the info-string tag of a plugin card's fence:
// ```loop-plugin-card <plugin> <card> <title>, its body the card's data as
// one line of JSON.
const pluginCardTag = "loop-plugin-card"

type pluginCardRequest struct {
	Card  string          `json:"card"`
	Title string          `json:"title"`
	Data  json.RawMessage `json:"data"`
}

type pluginCardResponse struct {
	MsgID string `json:"msg_id"`
}

// handlePostPluginCard posts one of the calling plugin's cards to its
// channel's chat as an inert message, which the desktop app draws with the
// card's page from the plugin's bundle and the data it was posted with.
func (s *Server) handlePostPluginCard(w http.ResponseWriter, r *http.Request) {
	p, ch, ok := s.pluginCaller(w, r, plugin.CapUICard)
	if !ok {
		return
	}
	var req pluginCardRequest
	r.Body = http.MaxBytesReader(w, r.Body, pluginEventMax)
	if !decodeJSON(w, r, &req) {
		return
	}
	if ch.Platform != types.PlatformLocal {
		http.Error(w, "cards show in the Loop desktop app only", http.StatusBadRequest)
		return
	}
	_, m, err := s.plugins.UI(r.Context(), p.PluginID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, found := m.Card(req.Card); !found {
		http.Error(w, fmt.Sprintf("the plugin declares no card %q", req.Card), http.StatusBadRequest)
		return
	}
	data := []byte("null")
	if len(req.Data) > 0 {
		var compact bytes.Buffer
		// decodeJSON checked it's JSON.
		_ = json.Compact(&compact, req.Data)
		data = compact.Bytes()
	}
	title := chatcomponents.Title(req.Title)
	msg := &db.Message{
		ChatID:      ch.ID,
		ChannelID:   ch.ChannelID,
		MsgID:       "card-" + randutil.HexID(16),
		AuthorName:  p.PluginID,
		Content:     "```" + strings.TrimSpace(strings.Join([]string{pluginCardTag, p.PluginID, req.Card, title}, " ")) + "\n" + string(data) + "\n```",
		IsBot:       true,
		IsProcessed: true,
		CreatedAt:   time.Now().UTC(),
	}
	if err := s.store.InsertMessage(r.Context(), msg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if s.eventsHub != nil {
		s.eventsHub.BroadcastMessageCreated(ch.ChannelID, events.MessageEventData{
			ID: msg.ID, MsgID: msg.MsgID, AuthorName: msg.AuthorName, Content: msg.Content, IsBot: true, IsProcessed: true,
		})
	}
	writeHTTPJSON(w, http.StatusCreated, pluginCardResponse{MsgID: msg.MsgID}, s.logger)
}
