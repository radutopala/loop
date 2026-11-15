package mcpbrowser

import (
	"strings"

	"github.com/radutopala/loop/internal/browser"
)

// matchRefs keeps the refs whose role, name, description and value hold every
// word of the query, ignoring case, in the page's order.
func matchRefs(refs []browser.ElementRef, query string) []browser.ElementRef {
	words := strings.Fields(strings.ToLower(query))
	var matches []browser.ElementRef
	for _, ref := range refs {
		text := strings.ToLower(strings.Join([]string{ref.Role, ref.Name, ref.Description, ref.Value}, " "))
		if !containsAll(text, words) {
			continue
		}
		matches = append(matches, ref)
	}
	return matches
}

func containsAll(text string, words []string) bool {
	for _, w := range words {
		if !strings.Contains(text, w) {
			return false
		}
	}
	return true
}
