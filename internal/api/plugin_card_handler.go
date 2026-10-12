package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"

	"github.com/radutopala/loop/internal/chatcomponents"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/types"
)

type pluginCardRequest struct {
	Card  string          `json:"card"`
	Title string          `json:"title"`
	Data  json.RawMessage `json:"data"`
}

type pluginCardResponse struct {
	ID int64 `json:"id"`
}

// handlePostPluginCard posts one of the calling plugin's cards to its
// channel's notification drawer, which draws it with the card's page from the
// plugin's bundle and the data it was posted with.
func (s *Server) handlePostPluginCard(w http.ResponseWriter, r *http.Request) {
	p, ch, ok := s.pluginCaller(w, r, plugin.CapUICard)
	if !ok || !requireConfigured(w, s.notifications, "notifications not configured") {
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
	if ok, wait := s.notifyLimit.allow(p.PluginID); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
		http.Error(w, "too many notifications; try again later", http.StatusTooManyRequests)
		return
	}
	n := &db.Notification{
		PluginID:  p.PluginID,
		Kind:      db.NotificationCard,
		ChannelID: ch.ChannelID,
		Title:     chatcomponents.Title(req.Title),
		Card:      req.Card,
		Data:      string(data),
	}
	if err := s.notify(r.Context(), n); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeHTTPJSON(w, http.StatusCreated, pluginCardResponse{ID: n.ID}, s.logger)
}
