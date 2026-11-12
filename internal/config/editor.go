package config

import (
	"bytes"
	"path"
	"slices"
	"strings"
)

// Bounds of EditorConfig.TabSize; a layer setting one outside them is
// ignored.
const (
	DefaultTabSize = 4
	maxTabSize     = 16
)

// DefaultEditorConfig is the editor config no layer has set.
func DefaultEditorConfig() EditorConfig {
	return EditorConfig{
		FollowActiveFile:     true,
		FollowActiveFileFrom: FromDefault,
		FormatOnSaveFrom:     FromDefault,
		TabSize:              DefaultTabSize,
		InsertSpaces:         true,
		PreviewTabs:          true,
	}
}

// editorConfigOf returns the global editor config jc sets.
func editorConfigOf(jc *jsonEditorConfig) EditorConfig {
	return DefaultEditorConfig().overlay(jc, FromGlobal)
}

// overlay returns c with the fields jc sets on top, recording from as
// their layer. jc's formatters go before c's, so the inner layer's match
// first.
func (c EditorConfig) overlay(jc *jsonEditorConfig, from string) EditorConfig {
	if jc == nil {
		return c
	}
	if jc.FollowActiveFile != nil {
		c.FollowActiveFile, c.FollowActiveFileFrom = *jc.FollowActiveFile, from
	}
	if jc.FormatOnSave != nil {
		c.FormatOnSave, c.FormatOnSaveFrom = *jc.FormatOnSave, from
	}
	if len(jc.Formatters) > 0 {
		c.Formatters = append(append([]Formatter(nil), jc.Formatters...), c.Formatters...)
	}
	if jc.TabSize != nil && *jc.TabSize >= 1 && *jc.TabSize <= maxTabSize {
		c.TabSize = *jc.TabSize
	}
	if jc.InsertSpaces != nil {
		c.InsertSpaces = *jc.InsertSpaces
	}
	if jc.TrimTrailingWhitespace != nil {
		c.TrimTrailingWhitespace = *jc.TrimTrailingWhitespace
	}
	if jc.InsertFinalNewline != nil {
		c.InsertFinalNewline = *jc.InsertFinalNewline
	}
	if jc.AutoSaveOnBlur != nil {
		c.AutoSaveOnBlur = *jc.AutoSaveOnBlur
	}
	if jc.PreviewTabs != nil {
		c.PreviewTabs = *jc.PreviewTabs
	}
	if len(jc.SearchExclude) > 0 {
		merged := append([]string(nil), jc.SearchExclude...)
		for _, g := range c.SearchExclude {
			if !slices.Contains(merged, g) {
				merged = append(merged, g)
			}
		}
		c.SearchExclude = merged
	}
	return c
}

// Excluded reports whether a search skips the file or directory at the
// root-relative path rel: one of SearchExclude matches it.
func (c EditorConfig) Excluded(rel string) bool {
	for _, g := range c.SearchExclude {
		if g != "" && globMatches(g, rel) {
			return true
		}
	}
	return false
}

// ExcludeGlobs is SearchExclude with its braces expanded, for a tool that
// doesn't read them.
func (c EditorConfig) ExcludeGlobs() []string {
	var out []string
	for _, g := range c.SearchExclude {
		if g != "" {
			out = append(out, expandBraces(g)...)
		}
	}
	return out
}

// Tidy applies the save-time cleanups to content: trailing whitespace
// stripped from each line, and a final newline, as the config asks.
func (c EditorConfig) Tidy(content []byte) []byte {
	if c.TrimTrailingWhitespace {
		var out bytes.Buffer
		out.Grow(len(content))
		for i, line := range bytes.Split(content, []byte("\n")) {
			if i > 0 {
				out.WriteByte('\n')
			}
			cr := bytes.HasSuffix(line, []byte("\r"))
			out.Write(bytes.TrimRight(bytes.TrimSuffix(line, []byte("\r")), " \t"))
			if cr {
				out.WriteByte('\r')
			}
		}
		content = out.Bytes()
	}
	if c.InsertFinalNewline && len(content) > 0 && content[len(content)-1] != '\n' {
		content = append(content[:len(content):len(content)], '\n')
	}
	return content
}

// FormatterFor returns the first formatter whose glob matches the file at
// the root-relative path rel, and false when none does.
func (c EditorConfig) FormatterFor(rel string) (Formatter, bool) {
	for _, f := range c.Formatters {
		if f.Command != "" && f.matches(rel) {
			return f, true
		}
	}
	return Formatter{}, false
}

// matches reports whether f's glob matches rel.
func (f Formatter) matches(rel string) bool {
	return globMatches(f.Glob, rel)
}

// globMatches reports whether glob matches rel: its base name, or the whole
// path when the glob holds a "/".
func globMatches(glob, rel string) bool {
	name := path.Base(rel)
	if strings.Contains(glob, "/") {
		name = rel
	}
	for _, g := range expandBraces(glob) {
		if ok, _ := path.Match(g, name); ok {
			return true
		}
	}
	return false
}

// expandBraces expands the first {a,b} group of glob, recursively, into the
// globs it stands for. A glob without a closed group is itself.
func expandBraces(glob string) []string {
	open := strings.IndexByte(glob, '{')
	if open < 0 {
		return []string{glob}
	}
	end := strings.IndexByte(glob[open:], '}')
	if end < 0 {
		return []string{glob}
	}
	end += open
	var out []string
	for alt := range strings.SplitSeq(glob[open+1:end], ",") {
		out = append(out, expandBraces(glob[:open]+alt+glob[end+1:])...)
	}
	return out
}
