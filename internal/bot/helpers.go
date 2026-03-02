package bot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// osRemove is a package-level variable to allow testing os.Remove.
var osRemove = os.Remove

// RemoveMCPConfig removes the per-channel MCP config file for the given channel.
// It silently ignores os.ErrNotExist (the file may not exist if the agent never ran).
// This is a variable so external packages can override it in tests.
var RemoveMCPConfig = removeMCPConfig

func removeMCPConfig(dirPath, channelID string) error {
	if dirPath == "" {
		return nil
	}
	p := filepath.Join(dirPath, ".loop", "mcp-"+channelID+".json")
	if err := osRemove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing MCP config %s: %w", p, err)
	}
	return nil
}

// CommandPrefix is the text prefix used to trigger bot commands.
const CommandPrefix = "!loop"

// HasCommandPrefix reports whether content starts with the command prefix (case-insensitive).
func HasCommandPrefix(content string) bool {
	lower := strings.ToLower(content)
	return strings.HasPrefix(lower, CommandPrefix)
}

// StripPrefix removes the command prefix from content and trims surrounding whitespace.
func StripPrefix(content string) string {
	if len(content) <= len(CommandPrefix) {
		return ""
	}
	return strings.TrimSpace(content[len(CommandPrefix):])
}

// StripMention removes both <@botUserID> and <@!botUserID> forms from content
// and trims surrounding whitespace.
func StripMention(content, botUserID string) string {
	mention := "<@" + botUserID + ">"
	mentionNick := "<@!" + botUserID + ">"
	content = strings.ReplaceAll(content, mention, "")
	content = strings.ReplaceAll(content, mentionNick, "")
	return strings.TrimSpace(content)
}

// ReplaceTextMention replaces a case-insensitive @username with the given mention string.
func ReplaceTextMention(content, username, mention string) string {
	target := "@" + username
	idx := strings.Index(strings.ToLower(content), strings.ToLower(target))
	if idx == -1 {
		return content
	}
	return content[:idx] + mention + content[idx+len(target):]
}

// FormatThreadMessage builds the initial message for a new thread.
// It always includes a <@botID> mention so that the bot's event handler
// recognises the message as a self-mention trigger and starts a new agent
// run inside the thread.
// Existing bot mentions (both <@botID> and @username forms) are stripped
// from message before prepending a canonical <@botID>.
// If mentionUserID is non-empty the user is appended as a mention.
func FormatThreadMessage(botID, botUsername, mentionUserID, message string) string {
	clean := strings.ReplaceAll(message, "<@"+botID+">", "")
	if botUsername != "" {
		clean = ReplaceTextMention(clean, botUsername, "")
	}
	clean = strings.TrimSpace(clean)
	msg := fmt.Sprintf("<@%s> %s", botID, clean)
	if mentionUserID != "" {
		msg += fmt.Sprintf(" <@%s>", mentionUserID)
	}
	return msg
}

// SplitMessage splits a message into chunks of at most maxLen characters,
// breaking on newlines when possible.
func SplitMessage(content string, maxLen int) []string {
	if len(content) <= maxLen {
		return []string{content}
	}

	var chunks []string
	for len(content) > 0 {
		if len(content) <= maxLen {
			chunks = append(chunks, content)
			break
		}

		cutPoint := FindCutPoint(content, maxLen)
		chunks = append(chunks, content[:cutPoint])
		content = content[cutPoint:]
	}
	return chunks
}

// FindCutPoint returns the best position to split content at, given a maximum length.
// It prefers cutting at a newline, then a space, then does a hard cut.
func FindCutPoint(content string, maxLen int) int {
	// Try to cut at a newline within the limit.
	lastNewline := strings.LastIndex(content[:maxLen], "\n")
	if lastNewline > 0 {
		return lastNewline + 1
	}
	// Try to cut at a space.
	lastSpace := strings.LastIndex(content[:maxLen], " ")
	if lastSpace > 0 {
		return lastSpace + 1
	}
	// Hard cut.
	return maxLen
}
