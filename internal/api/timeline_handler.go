package api

import (
	"net/http"

	"github.com/radutopala/loop/internal/db"
)

// timelineInlineCap caps the bytes returned for any single thinking or
// tool_result block. Live writes already truncate at this size, but we cap
// again on read so legacy rows or oversize stores don't bloat the response.
const timelineInlineCap = 8 * 1024

// timelineItem is the discriminated union returned by /timeline. Its Kind
// determines which sibling fields are populated; clients switch on Kind.
type timelineItem struct {
	Kind      string           `json:"kind"`
	Position  int64            `json:"position"`
	ID        int64            `json:"id"`
	Data      *messageResponse `json:"data,omitempty"`        // kind == "message"
	Text      string           `json:"text,omitempty"`        // thinking, tool_result
	Truncated bool             `json:"truncated,omitempty"`   // thinking, tool_result
	ToolUseID string           `json:"tool_use_id,omitempty"` // tool_use, tool_result
	ToolName  string           `json:"tool_name,omitempty"`   // tool_use
	ToolInput string           `json:"tool_input,omitempty"`  // tool_use
	IsError   bool             `json:"is_error,omitempty"`    // tool_result
}

type timelineCursor struct {
	Position int64 `json:"position"`
	ID       int64 `json:"id"`
}

type timelineResponse struct {
	Items      []timelineItem  `json:"items"`
	NextCursor *timelineCursor `json:"next_cursor"`
}

func (s *Server) handleTimeline(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "timeline not configured") {
		return
	}

	channelID := r.PathValue("id")

	limit, ok := parseQueryInt(w, r, "limit", defaultMessageLimit, maxMessageLimit)
	if !ok {
		return
	}
	cursorPosition, ok := parseQueryInt64(w, r, "cursor_position")
	if !ok {
		return
	}
	cursorID, ok := parseQueryInt64(w, r, "cursor_id")
	if !ok {
		return
	}

	rows, err := s.store.GetTimeline(r.Context(), channelID, cursorPosition, cursorID, limit+1)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	items := make([]timelineItem, 0, len(rows))
	for _, m := range rows {
		items = append(items, buildTimelineItem(m))
	}

	resp := timelineResponse{Items: items}
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		resp.NextCursor = &timelineCursor{Position: last.ChainPosition, ID: last.ID}
	}

	writeHTTPJSON(w, http.StatusOK, resp, s.logger)
}

// buildTimelineItem maps a single message row to its on-the-wire shape.
// Content for agent-event rows lives inline on the row (Content, ToolName,
// IsError) — written by the docker-stream callbacks at run time.
func buildTimelineItem(m *db.Message) timelineItem {
	base := timelineItem{Position: m.ChainPosition, ID: m.ID}
	switch m.Kind {
	case db.MessageKindMessage, "":
		resp := toMessageResponse(m)
		base.Kind = "message"
		base.Data = &resp
		return base
	case db.MessageKindThinking:
		base.Kind = "thinking"
		base.Text, base.Truncated = capInline(m.Content)
		return base
	case db.MessageKindToolUse:
		base.Kind = "tool_use"
		base.ToolUseID = m.ToolUseID
		base.ToolName = m.ToolName
		base.ToolInput, base.Truncated = capInline(m.Content)
		return base
	case db.MessageKindToolResult:
		base.Kind = "tool_result"
		base.ToolUseID = m.ToolUseID
		base.IsError = m.IsError
		base.Text, base.Truncated = capInline(m.Content)
		return base
	default:
		base.Kind = string(m.Kind)
		return base
	}
}

func capInline(s string) (string, bool) {
	if len(s) <= timelineInlineCap {
		return s, false
	}
	return s[:timelineInlineCap], true
}
