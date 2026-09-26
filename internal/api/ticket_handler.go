package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/radutopala/loop/internal/events"
)

// ── Channel Ticket URL ──

// maxTicketURLLen caps a ticket URL, in bytes.
const maxTicketURLLen = 2048

type setTicketURLRequest struct {
	TicketURL string `json:"ticket_url"`
}

type setTicketURLResponse struct {
	ChannelID string `json:"channel_id"`
	TicketURL string `json:"ticket_url"`
}

// normalizeTicketURL trims a ticket URL and checks it's an absolute http(s)
// link; any tracker's (Jira, GitHub, Linear, …) will do. Empty is allowed and
// clears it.
func normalizeTicketURL(raw string) (string, error) {
	ticketURL := strings.TrimSpace(raw)
	if ticketURL == "" {
		return "", nil
	}
	if len(ticketURL) > maxTicketURLLen {
		return "", fmt.Errorf("ticket_url is longer than %d characters", maxTicketURLLen)
	}
	u, err := url.Parse(ticketURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("ticket_url must be an absolute http(s) URL")
	}
	return ticketURL, nil
}

// handleSetChannelTicketURL links a channel or thread to its ticket. An empty
// ticket_url clears it.
func (s *Server) handleSetChannelTicketURL(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel store not configured") {
		return
	}

	channelID := r.PathValue("id")

	var req setTicketURLRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ticketURL, err := normalizeTicketURL(req.TicketURL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ch, err := s.store.GetChannel(r.Context(), channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ch == nil {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}

	if err := s.store.UpdateChannelTicketURL(r.Context(), channelID, ticketURL); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if s.eventsHub != nil {
		s.eventsHub.BroadcastChannelUpdated(events.ChannelUpdatedData{
			ChannelID: channelID,
			TicketURL: &ticketURL,
		})
	}

	writeHTTPJSON(w, http.StatusOK, setTicketURLResponse{
		ChannelID: channelID,
		TicketURL: ticketURL,
	}, s.logger)
}
