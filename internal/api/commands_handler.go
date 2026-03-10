package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/radutopala/loop/internal/bot"
)

// InteractionHandler processes a parsed slash command interaction.
type InteractionHandler interface {
	HandleInteraction(ctx context.Context, inter *bot.Interaction)
}

type commandRequest struct {
	ChannelID string `json:"channel_id"`
	AuthorID  string `json:"author_id"`
	Command   string `json:"command"`
}

func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	if s.interactionHandler == nil {
		http.Error(w, "commands not configured", http.StatusServiceUnavailable)
		return
	}

	var req commandRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.ChannelID == "" {
		http.Error(w, "channel_id is required", http.StatusBadRequest)
		return
	}
	if req.Command == "" {
		http.Error(w, "command is required", http.StatusBadRequest)
		return
	}

	authorID := req.AuthorID
	if authorID == "" {
		authorID = "api-user"
	}

	inter := parseCommand(req.Command, req.ChannelID, authorID)
	if inter == nil {
		http.Error(w, "unknown command", http.StatusBadRequest)
		return
	}

	go s.interactionHandler.HandleInteraction(context.Background(), inter)
	w.WriteHeader(http.StatusNoContent)
}

// parseCommand parses a command string into a bot.Interaction.
// The command string format: "subcommand [positional_arg] [key=value ...]"
func parseCommand(command, channelID, authorID string) *bot.Interaction {
	parts := tokenize(command)
	if len(parts) == 0 {
		return nil
	}

	cmdName := parts[0]
	args := parts[1:]

	inter := &bot.Interaction{
		ChannelID:   channelID,
		CommandName: cmdName,
		Options:     make(map[string]string),
		AuthorID:    authorID,
	}

	switch cmdName {
	case "tasks", "status", "readme", "template-list", "iamtheowner":
		// No arguments needed.
	case "task", "cancel", "toggle":
		// Single positional: task_id.
		if len(args) > 0 {
			inter.Options["task_id"] = args[0]
		}
	case "stop":
		// Optional positional: channel_id.
		if len(args) > 0 {
			inter.Options["channel_id"] = args[0]
		}
	case "template-add":
		// Single positional: name.
		if len(args) > 0 {
			inter.Options["name"] = args[0]
		}
	case "schedule":
		// All key=value: type, schedule, prompt.
		parseKeyValues(args, inter.Options)
	case "edit":
		// First positional is task_id, rest are key=value.
		if len(args) > 0 {
			inter.Options["task_id"] = args[0]
			parseKeyValues(args[1:], inter.Options)
		}
	case "allow_user":
		// Positionals: target_id, optional role.
		if len(args) > 0 {
			inter.Options["target_id"] = args[0]
		}
		if len(args) > 1 {
			inter.Options["role"] = args[1]
		}
	case "deny_user":
		// Positional: target_id.
		if len(args) > 0 {
			inter.Options["target_id"] = args[0]
		}
	default:
		return nil
	}

	return inter
}

// parseKeyValues parses key=value pairs from args into opts.
func parseKeyValues(args []string, opts map[string]string) {
	for _, arg := range args {
		if k, v, ok := strings.Cut(arg, "="); ok {
			opts[k] = v
		}
	}
}

// tokenize splits a command string into tokens, respecting quoted strings.
func tokenize(s string) []string {
	var tokens []string
	var current strings.Builder
	inQuote := false
	quoteChar := byte(0)

	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case inQuote:
			if ch == quoteChar {
				inQuote = false
			} else {
				current.WriteByte(ch)
			}
		case ch == '"' || ch == '\'':
			inQuote = true
			quoteChar = ch
		case ch == ' ' || ch == '\t':
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(ch)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}
