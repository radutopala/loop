// Package transcript reads Claude Code session transcripts: the
// <session>.jsonl files Claude Code keeps per working directory under
// ~/.claude/projects.
package transcript

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/radutopala/loop/internal/osutil"
)

// Entry is one line of a transcript.
type Entry struct {
	// Line is the entry's line number in the transcript.
	Line       int    `json:"-"`
	Type       string `json:"type"`
	UUID       string `json:"uuid"`
	ParentUUID string `json:"parentUuid"`
	Timestamp  string `json:"timestamp"`
	Message    struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// Prompt returns the text of a user prompt entry; tool results, user
// entries too, carry a content array instead.
func (e Entry) Prompt() (string, bool) {
	var text string
	if e.Type != "user" || json.Unmarshal(e.Message.Content, &text) != nil {
		return "", false
	}
	return text, true
}

// CleanSessionID sanitises a session id to prevent path traversal: only the
// base name is valid (no slashes, no ".." components).
func CleanSessionID(id string) (string, bool) {
	id = filepath.Base(id)
	return id, id != "." && id != ".." && id != ""
}

// Path is the transcript of session sessionID run in projectDir, under home.
func Path(home, projectDir, sessionID string) (string, error) {
	sessionID, ok := CleanSessionID(sessionID)
	if !ok {
		return "", errors.New("invalid session id")
	}
	return filepath.Join(home, ".claude", "projects", osutil.EncodeClaudeProjectPath(projectDir), sessionID+".jsonl"), nil
}

// Parse returns a transcript's entries and its line count; lines that don't
// parse are skipped.
func Parse(data []byte) ([]Entry, int) {
	lines := strings.Split(string(data), "\n")
	var entries []Entry
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry Entry
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		entry.Line = i
		entries = append(entries, entry)
	}
	return entries, len(lines)
}

// PromptOf walks the parent links back from entry uuid to the user prompt
// it answers; a prompt's own uuid gives the prompt. ok is false when the
// chain breaks before reaching a prompt.
func PromptOf(entries []Entry, uuid string) (prompt Entry, ok bool) {
	byUUID := make(map[string]Entry, len(entries))
	for _, e := range entries {
		if e.UUID != "" {
			byUUID[e.UUID] = e
		}
	}
	// Each step moves to a different entry, so a chain longer than the
	// transcript loops.
	for range len(byUUID) {
		e, found := byUUID[uuid]
		if !found {
			return Entry{}, false
		}
		if _, isPrompt := e.Prompt(); isPrompt {
			return e, true
		}
		uuid = e.ParentUUID
	}
	return Entry{}, false
}
