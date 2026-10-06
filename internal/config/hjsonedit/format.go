package hjsonedit

import (
	"strings"

	"github.com/tailscale/hujson"
)

// formatUnit is the indentation step Format lays a file out with.
const formatUnit = "  "

// Format re-indents an HJSON config with two spaces per level and keeps its
// comments, blank lines between members, key order and trailing commas. An
// object or array written over several lines, and the top-level one, gets one
// member per line; one written on a single line stays on one, spaced as
// `{"a": 1, "b": [1, 2]}`.
func Format(data []byte) ([]byte, error) {
	v, err := hujson.Parse(data)
	if err != nil {
		return nil, err
	}
	v.BeforeExtra = formatExtra(v.BeforeExtra, extraLayout{start: true})
	formatValue(&v, "", true)
	v.AfterExtra = formatExtra(v.AfterExtra, extraLayout{pad: "\n", blank: true})
	return v.Pack(), nil
}

// formatValue lays out what's inside v, a value whose line is indented by
// indent. top forces one member per line.
func formatValue(v *hujson.Value, indent string, top bool) {
	switch c := v.Value.(type) {
	case *hujson.Object:
		heads := make([]*hujson.Value, len(c.Members))
		for i := range c.Members {
			heads[i] = &c.Members[i].Name
		}
		lines := onePerLine(heads, c.AfterExtra, top)
		for i := range c.Members {
			m := &c.Members[i]
			line := formatHead(&m.Name, i, indent, lines)
			m.Name.AfterExtra = formatExtra(m.Name.AfterExtra, inline(line, ""))
			m.Value.BeforeExtra = formatExtra(m.Value.BeforeExtra, inline(line, " "))
			formatValue(&m.Value, line, false)
			m.Value.AfterExtra = formatExtra(m.Value.AfterExtra, inline(line, ""))
		}
		c.AfterExtra = formatClosing(c.AfterExtra, indent, lines)
	case *hujson.Array:
		heads := make([]*hujson.Value, len(c.Elements))
		for i := range c.Elements {
			heads[i] = &c.Elements[i]
		}
		lines := onePerLine(heads, c.AfterExtra, top)
		for i := range c.Elements {
			e := &c.Elements[i]
			line := formatHead(e, i, indent, lines)
			formatValue(e, line, false)
			e.AfterExtra = formatExtra(e.AfterExtra, inline(line, ""))
		}
		c.AfterExtra = formatClosing(c.AfterExtra, indent, lines)
	}
}

// onePerLine reports whether a container gets one member per line: it has
// members or comments, and it's top or written over several lines. heads are
// the values its members start with (their names, in an object).
func onePerLine(heads []*hujson.Value, closing hujson.Extra, top bool) bool {
	if len(heads) == 0 && !hasComment(closing) {
		return false
	}
	if top || strings.Contains(string(closing), "\n") {
		return true
	}
	for _, h := range heads {
		if strings.Contains(string(h.BeforeExtra), "\n") {
			return true
		}
	}
	return false
}

// formatHead lays out the extra before the i-th member of a container whose
// line is indented by indent, and returns the indentation of the member's line.
func formatHead(h *hujson.Value, i int, indent string, lines bool) string {
	if lines {
		child := indent + formatUnit
		h.BeforeExtra = formatExtra(h.BeforeExtra, extraLayout{indent: child, pad: "\n" + child, blank: true, gap: true})
		return child
	}
	pad := " "
	if i == 0 {
		pad = ""
	}
	h.BeforeExtra = formatExtra(h.BeforeExtra, inline(indent, pad))
	return indent
}

// formatClosing lays out the extra before a container's closing bracket.
func formatClosing(e hujson.Extra, indent string, lines bool) hujson.Extra {
	if lines {
		return formatExtra(e, extraLayout{indent: indent + formatUnit, pad: "\n" + indent, after: indent, blank: true})
	}
	return formatExtra(e, inline(indent, ""))
}

// extraLayout says how formatExtra lays out the whitespace and comments
// between two tokens.
type extraLayout struct {
	indent string // indentation of a comment on its own line
	pad    string // what's left between the tokens, after any comments
	after  string // indentation of the next token, when a comment ends its line
	start  bool   // the extra starts the file
	blank  bool   // keep one blank line before a comment where the extra had any
	gap    bool   // keep one blank line before pad where the extra had any
}

// inline is the layout of an extra within a line indented by indent.
func inline(indent, pad string) extraLayout {
	return extraLayout{indent: indent, pad: pad, after: indent}
}

// formatExtra lays out e, a run of whitespace and comments, as l says: a
// comment that started a line starts one at l.indent, one that followed a
// token stays on that token's line, and l.pad comes last. The result is nil
// only if e is, since an object's or array's trailing comma depends on that.
func formatExtra(e hujson.Extra, l extraLayout) hujson.Extra {
	var b strings.Builder
	s := string(e)
	newlines, comments, lineComment := 0, 0, false
	for i := 0; i < len(s); {
		switch s[i] {
		case '\n':
			newlines++
			i++
			continue
		case ' ', '\t', '\r':
			i++
			continue
		}
		end := commentEnd(s, i)
		switch {
		case l.start && comments == 0:
		case newlines > 0:
			b.WriteString(breakLine(newlines, l.blank) + l.indent)
		default:
			b.WriteString(" ")
		}
		b.WriteString(s[i:end])
		lineComment = strings.HasPrefix(s[i:], "//")
		comments++
		newlines = 0
		i = end
	}
	switch {
	case strings.HasPrefix(l.pad, "\n"):
		b.WriteString(breakLine(newlines, l.gap) + l.pad[1:])
	case comments > 0 && (lineComment || newlines > 0):
		b.WriteString("\n" + l.after)
	default:
		b.WriteString(l.pad)
	}
	if e == nil && b.Len() == 0 {
		return nil
	}
	return hujson.Extra(b.String())
}

// breakLine is the line break for a run of whitespace with n newlines: a
// blank line where it had any and keep is set, else a plain newline.
func breakLine(n int, keep bool) string {
	if n > 1 && keep {
		return "\n\n"
	}
	return "\n"
}

// commentEnd returns the end of the comment starting at s[i]: the newline
// after a // comment (which hujson requires), or just past a block comment's
// */.
func commentEnd(s string, i int) int {
	if strings.HasPrefix(s[i:], "//") {
		return i + strings.IndexByte(s[i:], '\n')
	}
	return i + strings.Index(s[i:], "*/") + len("*/")
}

// hasComment reports whether e holds a comment.
func hasComment(e hujson.Extra) bool {
	return strings.Contains(string(e), "/")
}
