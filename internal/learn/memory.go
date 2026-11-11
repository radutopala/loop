package learn

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Limits on a memory proposal.
const (
	maxMemoryDescriptionLen = 200
	maxMemoryBodyLen        = 4000
)

// MemoryIndex is the file in Claude Code's auto-memory dir that lists every
// memory, one pointer per line. Claude Code loads it into each session.
const MemoryIndex = "MEMORY.md"

// MemoryTypes are the memory types Claude Code's auto-memory knows.
var MemoryTypes = []string{"user", "feedback", "project", "reference"}

// memoryName is a memory's slug, which is also its file name.
var memoryName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Memory is a memory proposal's payload: one fact for Claude Code's
// auto-memory, in the shape Claude Code writes its own.
type Memory struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Body        string `json:"body"`
}

func (m *Memory) check() error {
	if !memoryName.MatchString(m.Name) {
		return fmt.Errorf("name %q must be a kebab-case slug: lowercase letters, digits and dashes, at most 64", m.Name)
	}
	// On a case-insensitive filesystem, memory.md is the index.
	if strings.EqualFold(m.File(), MemoryIndex) {
		return fmt.Errorf("name %q is taken by the memory index", m.Name)
	}
	if err := checkText("description", m.Description, maxMemoryDescriptionLen, true); err != nil {
		return err
	}
	if strings.ContainsAny(strings.TrimSpace(m.Description), "\r\n") {
		return fmt.Errorf("description must be one line")
	}
	if !slices.Contains(MemoryTypes, m.Type) {
		return fmt.Errorf("type %q must be one of %s", m.Type, strings.Join(MemoryTypes, ", "))
	}
	return checkText("body", m.Body, maxMemoryBodyLen, true)
}

// File is the memory's file name in the auto-memory dir.
func (m *Memory) File() string {
	return m.Name + ".md"
}

// Content is the memory file: the frontmatter Claude Code reads, then the
// body.
func (m *Memory) Content() string {
	return fmt.Sprintf("---\nname: %s\ndescription: %s\nmetadata:\n  type: %s\n---\n\n%s\n",
		yamlScalar(m.Name), yamlScalar(strings.TrimSpace(m.Description)), m.Type, strings.TrimSpace(m.Body))
}

// IndexLine is the pointer to the memory that MEMORY.md gets.
func (m *Memory) IndexLine() string {
	return fmt.Sprintf("- [%s](%s) — %s", m.Name, m.File(), strings.TrimSpace(m.Description))
}

// Indexed reports whether index, a MEMORY.md, already points at the memory.
func (m *Memory) Indexed(index string) bool {
	return strings.Contains(index, "]("+m.File()+")")
}

// yamlScalar returns s as a YAML scalar: plain when YAML reads it back as
// the same string, else double-quoted (a JSON string is a valid one). So
// true, null, 123 or a trailing comment come back quoted.
func yamlScalar(s string) string {
	var v any
	if err := yaml.Unmarshal([]byte("v: "+s), &v); err == nil {
		if m, ok := v.(map[string]any); ok && len(m) == 1 && m["v"] == s {
			return s
		}
	}
	q, _ := json.Marshal(s) // a string always marshals
	return string(q)
}
