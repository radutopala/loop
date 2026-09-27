package api

import (
	"net/http"

	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/types"
)

// ── Channel Ticket URL ──

type setTicketURLRequest struct {
	TicketURL string `json:"ticket_url"`
}

type setTicketURLResponse struct {
	ChannelID string `json:"channel_id"`
	TicketURL string `json:"ticket_url"`
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
	ticketURL, err := types.NormalizeTicketURL(req.TicketURL)
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
