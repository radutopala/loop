package api

import (
	"net/http"
	"strings"
	"sync"

	"github.com/radutopala/loop/internal/events"
)

// promptSuggestions keeps each channel's latest prompt suggestion, so a chat
// opened after it arrived still offers it: the event only reaches the chat
// that's open when it's sent. It's kept until the channel's next run starts,
// and lost on a restart, being just a guess at the next prompt.
type promptSuggestions struct {
	mu        sync.Mutex
	byChannel map[string]string
}

func newPromptSuggestions() *promptSuggestions {
	return &promptSuggestions{byChannel: make(map[string]string)}
}

// observe follows the hub's events: a suggestion sets its channel's, a run
// starting in the channel drops it, and so does the channel's deletion. A
// thread's run status, told to its parent too, leaves the parent's alone.
func (p *promptSuggestions) observe(evt Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch evt.Type {
	case EventPromptSuggestion:
		d, _ := evt.Data.(events.PromptSuggestionEventData)
		if text := strings.TrimSpace(d.Suggestion); text != "" {
			p.byChannel[evt.ChannelID] = text
		}
	case EventAgentStatus:
		d, _ := evt.Data.(events.AgentStatusEventData)
		if d.Status == "running" && (d.ThreadID == "" || d.ThreadID == evt.ChannelID) {
			delete(p.byChannel, evt.ChannelID)
		}
	case EventChannelDeleted:
		delete(p.byChannel, evt.ChannelID)
	}
}

// get returns channelID's suggestion, or "" when it has none.
func (p *promptSuggestions) get(channelID string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.byChannel[channelID]
}

type promptSuggestionResponse struct {
	Suggestion string `json:"suggestion"`
}

// handlePromptSuggestion returns the channel's latest prompt suggestion,
// empty when it has none.
func (s *Server) handlePromptSuggestion(w http.ResponseWriter, r *http.Request) {
	writeHTTPJSON(w, http.StatusOK, promptSuggestionResponse{Suggestion: s.suggestions.get(r.PathValue("id"))}, s.logger)
}
