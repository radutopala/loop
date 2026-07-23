// stream.go holds the stream-JSON parsing helpers for Claude's output format.
package container

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// claudeResponse represents a stream-json event from claude --output-format stream-json.
// The final event has Type "result" and contains the response.
type claudeResponse struct {
	Type       string `json:"type"`
	Result     string `json:"result"`
	SessionID  string `json:"session_id"`
	IsError    bool   `json:"is_error"`
	DurationMs int    `json:"duration_ms"`
	NumTurns   int    `json:"num_turns"`
	StopReason string `json:"stop_reason"`
	Model      string `json:"-"` // set by scanStreamJSON from assistant events
}

// assistantContentBlock is a single content block within an assistant message.
// A block is one of: "text" (Text), "thinking" (Thinking), or "tool_use"
// (ID + Name + Input). Other fields are zero on a given block.
type assistantContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`     // text blocks
	Thinking string          `json:"thinking"` // thinking blocks
	ID       string          `json:"id"`       // tool_use id
	Name     string          `json:"name"`     // tool_use name
	Input    json.RawMessage `json:"input"`    // tool_use input
}

// assistantMessage represents an "assistant" event from Claude's stream-json output.
// Each assistant turn contains a message with content blocks.
type assistantMessage struct {
	Type    string `json:"type"`
	Message struct {
		Model   string                  `json:"model"`
		Content []assistantContentBlock `json:"content"`
	} `json:"message"`
}

// systemEvent represents a "system" event from Claude's stream-json output.
type systemEvent struct {
	Type            string `json:"type"`
	Subtype         string `json:"subtype"`
	Description     string `json:"description"`
	Status          string `json:"status"`
	EstimatedTokens int    `json:"estimated_tokens"`
	Summary         string `json:"summary"`
	// api_retry fields: the CLI is backing off on an upstream API error
	// (e.g. 529 overloaded) before retrying the request itself.
	Attempt      int    `json:"attempt"`
	MaxRetries   int    `json:"max_retries"`
	RetryDelayMs int    `json:"retry_delay_ms"`
	Error        string `json:"error"`
	ErrorStatus  int    `json:"error_status"`
}

// extractText joins all text content blocks from an assistant message.
func (m *assistantMessage) extractText() string {
	var texts []string
	for _, c := range m.Message.Content {
		if c.Type == "text" && c.Text != "" {
			texts = append(texts, c.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// extractThinking joins all thinking content blocks from an assistant message.
func (m *assistantMessage) extractThinking() string {
	var parts []string
	for _, c := range m.Message.Content {
		if c.Type == "thinking" && c.Thinking != "" {
			parts = append(parts, c.Thinking)
		}
	}
	return strings.Join(parts, "\n")
}

// ToolUse represents a tool invocation extracted from an assistant message.
type ToolUse struct {
	ID    string // per-block tool_use id, pairs with the matching tool_result
	Name  string
	Input string // short summary of the input
}

// extractToolUses returns tool_use content blocks from an assistant message.
func (m *assistantMessage) extractToolUses() []ToolUse {
	var tools []ToolUse
	for _, c := range m.Message.Content {
		if c.Type == "tool_use" && c.Name != "" {
			summary := summarizeToolInput(c.Name, c.Input)
			tools = append(tools, ToolUse{ID: c.ID, Name: c.Name, Input: summary})
		}
	}
	return tools
}

// toolInputSummaryMax caps the tool-input summary sent to the chat. It is a
// safety net against pathological inputs, not a display limit — the FE collapses
// long summaries to a one-line preview and expands them on click, so the full
// command must survive to the client (the old 120-char cap hid most of a
// multi-line Bash command even when expanded).
const toolInputSummaryMax = 2000

// summarizeToolInput extracts a short description from tool input JSON.
func summarizeToolInput(name string, raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	switch name {
	case "Bash":
		if cmd, ok := m["command"].(string); ok {
			if len(cmd) > toolInputSummaryMax {
				cmd = cmd[:toolInputSummaryMax] + "..."
			}
			return cmd
		}
	case "Read":
		if fp, ok := m["file_path"].(string); ok {
			return fp
		}
	case "Edit":
		if fp, ok := m["file_path"].(string); ok {
			return fp
		}
	case "Write":
		if fp, ok := m["file_path"].(string); ok {
			return fp
		}
	case "Glob":
		if p, ok := m["pattern"].(string); ok {
			return p
		}
	case "Grep":
		if p, ok := m["pattern"].(string); ok {
			return p
		}
	case "Agent":
		if desc, ok := m["description"].(string); ok {
			return desc
		}
	case "AskUserQuestion", "ExitPlanMode", "TodoWrite", "TaskCreate", "TaskUpdate":
		return string(raw)
	}
	// For other tools, try common keys.
	for _, key := range []string{"description", "query", "prompt", "path", "url"} {
		if v, ok := m[key].(string); ok {
			if len(v) > toolInputSummaryMax {
				v = v[:toolInputSummaryMax] + "..."
			}
			return v
		}
	}
	return ""
}

// streamCallbacks holds optional callbacks for scanStreamJSON.
type streamCallbacks struct {
	onTurn       func(string)
	onToolUse    func(toolUseID, name, input string)
	onActivity   func(activity, detail string)
	onThinking   func(text string)
	onToolResult func(toolUseID, output string, isError bool)
}

// userEventMaxBytes caps the size of "user" stream-json lines we will fully
// read. Above this we drain the line (without buffering it) — this protects
// against multi-MB screenshot tool_results. Below it, we parse the line so
// non-image tool_result blocks (Read/Bash/Grep output) reach the chat.
const userEventMaxBytes = 256 * 1024

// toolResultMaxInline caps the live tool_result output we forward over SSE
// and persist via OnToolResult. Anything above is truncated at this boundary;
// /timeline re-applies the same cap defensively when serving the row.
const toolResultMaxInline = 8 * 1024

// scannerBufInit is the initial reader buffer capacity.
const scannerBufInit = 64 * 1024

// userMessage represents a "user" event from Claude's stream-json output, used
// only to surface tool_result blocks live. The Message.Content is polymorphic:
// each block's Content is either a plain string OR an array of {type, text|image}
// blocks; both shapes are handled by parseToolResultContent.
type userMessage struct {
	Type    string `json:"type"`
	Message struct {
		Content []struct {
			Type      string          `json:"type"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
			IsError   bool            `json:"is_error"`
		} `json:"content"`
	} `json:"message"`
}

// parseToolResultContent extracts the textual body of a tool_result content
// field. The field is polymorphic: a plain string OR an array of {type, text}
// or {type, image} blocks. Image blocks are dropped; text blocks are joined.
func parseToolResultContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var sb strings.Builder
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}

// truncateInline trims s to maxBytes and reports whether it was truncated.
func truncateInline(s string, maxBytes int) (string, bool) {
	if len(s) <= maxBytes {
		return s, false
	}
	return s[:maxBytes], true
}

// readLineOrSkip reads a line from the buffered reader. For "user" events
// (tool results), it caps the buffered bytes at userEventMaxBytes — over the
// cap, the rest of the line is drained without buffering and the function
// returns nil (typically a screenshot). Under the cap, the full line is
// returned so callers can dispatch tool_result blocks live. Non-user lines
// are returned in full.
func readLineOrSkip(br *bufio.Reader) ([]byte, error) {
	// Peek at the first bytes to detect the event type without reading
	// the full line. Tool results (screenshots) can be several MB.
	peek, peekErr := br.Peek(30)
	if len(peek) == 0 && peekErr != nil {
		return nil, peekErr // EOF or real error
	}
	isUser := strings.Contains(string(peek), `"type":"user"`)

	var (
		buf  []byte
		over bool
	)
	for {
		chunk, err := br.ReadSlice('\n')
		if isUser && !over && len(buf)+len(chunk) > userEventMaxBytes {
			// Over cap — stop buffering and start draining.
			over = true
			buf = nil
		}
		if !over {
			buf = append(buf, chunk...)
		}
		switch {
		case err == nil:
			if over {
				return nil, nil
			}
			return bytes.TrimSpace(buf), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if over {
				return nil, nil
			}
			return bytes.TrimSpace(buf), nil
		default:
			return nil, err
		}
	}
}

// scanStreamJSON scans newline-delimited JSON events from Claude's stream-json output.
// It dispatches "assistant" text to onTurn, tool_use blocks to onToolUse,
// model/system events to onActivity, and returns the final "result" event.
func scanStreamJSON(r io.Reader, cb streamCallbacks) (*claudeResponse, error) {
	br := bufio.NewReaderSize(r, scannerBufInit)
	var result *claudeResponse
	var lastModel string
	for {
		// Peek at the first bytes to detect the event type without reading
		// the entire line. Tool results (screenshots) can be several MB —
		// we only need to fully read "assistant", "system", and "result" events.
		line, err := readLineOrSkip(br)
		if err != nil {
			if err == io.EOF {
				break
			}
			return result, fmt.Errorf("reading container output: %w", err)
		}
		if len(line) == 0 {
			continue
		}

		var typeCheck struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &typeCheck); err != nil {
			continue // skip non-JSON lines (e.g. ANSI noise)
		}

		switch typeCheck.Type {
		case "assistant":
			var msg assistantMessage
			if err := json.Unmarshal(line, &msg); err != nil {
				continue
			}
			if msg.Message.Model != "" && msg.Message.Model != lastModel {
				lastModel = msg.Message.Model
				if cb.onActivity != nil {
					cb.onActivity("model", lastModel)
				}
			}
			if cb.onTurn != nil {
				if text := msg.extractText(); text != "" {
					cb.onTurn(text)
				}
			}
			if cb.onThinking != nil {
				if text := msg.extractThinking(); text != "" {
					cb.onThinking(text)
				}
			}
			if cb.onToolUse != nil {
				for _, tu := range msg.extractToolUses() {
					cb.onToolUse(tu.ID, tu.Name, tu.Input)
				}
			}
		case "user":
			if cb.onToolResult == nil {
				continue
			}
			var um userMessage
			if err := json.Unmarshal(line, &um); err != nil {
				continue
			}
			for _, blk := range um.Message.Content {
				if blk.Type != "tool_result" {
					continue
				}
				body := parseToolResultContent(blk.Content)
				out, _ := truncateInline(body, toolResultMaxInline)
				cb.onToolResult(blk.ToolUseID, out, blk.IsError)
			}
		case "system":
			if cb.onActivity != nil {
				var evt systemEvent
				if err := json.Unmarshal(line, &evt); err != nil {
					continue
				}
				switch evt.Subtype {
				case "task_started":
					cb.onActivity("subagent_started", evt.Description)
				case "task_progress":
					cb.onActivity("subagent_progress", evt.Description)
				case "task_notification":
					// A background task finished (or was stopped); summary
					// carries its one-line outcome. Surface it so the user
					// sees background completions instead of them vanishing.
					if evt.Summary != "" {
						cb.onActivity("task_notification", evt.Summary)
					}
				case "api_retry":
					// The CLI is retrying an upstream API error with backoff —
					// surface it so long silent stalls (529 storms back off
					// for minutes) are visible instead of looking like a hang.
					if evt.Attempt > 0 {
						cb.onActivity("api_retry", fmt.Sprintf("%s (%d) — retry %d/%d in %.1fs",
							evt.Error, evt.ErrorStatus, evt.Attempt, evt.MaxRetries, float64(evt.RetryDelayMs)/1000))
					}
				case "status":
					cb.onActivity(evt.Status, evt.Description)
				case "thinking_tokens":
					// Opus emits running thinking-token estimates while it
					// reasons (the thinking text itself is redacted). Surface
					// them as a live "thinking" activity so the chat shows a
					// progress indicator, mirroring sonnet's thinking display.
					cb.onActivity("thinking", fmt.Sprintf("%d", evt.EstimatedTokens))
				}
			}
		case "tool_progress":
			// Periodic heartbeat the CLI emits while a long-running tool
			// executes (e.g. a multi-minute Bash call): tool name + elapsed
			// seconds. Surfaced as activity so the chat UI can show "Bash —
			// running 90s" instead of a silent spinner.
			if cb.onActivity != nil {
				var evt struct {
					ToolName string  `json:"tool_name"`
					Elapsed  float64 `json:"elapsed_time_seconds"`
				}
				if err := json.Unmarshal(line, &evt); err != nil || evt.ToolName == "" {
					continue
				}
				cb.onActivity("tool_progress", fmt.Sprintf("%s — running %ds", evt.ToolName, int(evt.Elapsed)))
			}
		case "result":
			var evt claudeResponse
			if err := json.Unmarshal(line, &evt); err != nil {
				continue
			}
			result = &evt
		}
	}
	if result == nil {
		return nil, fmt.Errorf("parsing claude response: no result event found")
	}
	if lastModel != "" {
		result.Model = lastModel
	}
	return result, nil
}
