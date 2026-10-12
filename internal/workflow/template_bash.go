package workflow

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"strings"
	"text/template"
	"text/template/parse"
)

// bashFuncs are templateFuncs for a bash script, whose output must stay
// shell-quoted: a helper that returns text derived from a quoted value
// returns it quoted too.
var bashFuncs = func() template.FuncMap {
	funcs := maps.Clone(templateFuncs)
	funcs["trim"] = bashText(strings.TrimSpace)
	funcs["lower"] = bashText(strings.ToLower)
	funcs["upper"] = bashText(strings.ToUpper)
	funcs["fromJSON"] = func(v any) (any, error) {
		d, err := fromJSON(text(v))
		return bashValue(d), err
	}
	funcs["toJSON"] = func(v any) (bashString, error) {
		s, err := toJSON(v)
		return bashString(s), err
	}
	funcs[bashOut] = func(v any) string {
		if v == nil {
			return "''"
		}
		return fmt.Sprint(v)
	}
	return funcs
}()

// bashOut is the helper parseBashTemplate ends every printed pipeline
// with.
const bashOut = "bashOut"

// parseBashTemplate parses a bash script's template with bashFuncs, and
// ends every pipeline it prints with bashOut, so a value that's missing (a
// field a tool didn't return, an input nobody set) prints as an empty
// quoted string rather than as "<no value>", which the shell reads as a
// redirect.
func parseBashTemplate(tmplStr string) (*template.Template, error) {
	tmpl, err := template.New("").Funcs(bashFuncs).Parse(tmplStr)
	if err != nil {
		return nil, err
	}
	for _, t := range tmpl.Templates() {
		bashWrap(t.Root)
	}
	return tmpl, nil
}

// bashWrap appends bashOut to every printing action under n.
func bashWrap(n parse.Node) {
	switch n := n.(type) {
	case *parse.ListNode:
		if n == nil {
			return
		}
		for _, c := range n.Nodes {
			bashWrap(c)
		}
	case *parse.ActionNode:
		// {{$x := …}} prints nothing.
		if len(n.Pipe.Decl) == 0 {
			n.Pipe.Cmds = append(n.Pipe.Cmds, &parse.CommandNode{
				NodeType: parse.NodeCommand,
				Pos:      n.Pos,
				Args:     []parse.Node{parse.NewIdentifier(bashOut).SetPos(n.Pos)},
			})
		}
	case *parse.IfNode:
		bashWrap(n.List)
		bashWrap(n.ElseList)
	case *parse.RangeNode:
		bashWrap(n.List)
		bashWrap(n.ElseList)
	case *parse.WithNode:
		bashWrap(n.List)
		bashWrap(n.ElseList)
	}
}

// bashText makes f, a string helper, keep a quoted value quoted.
func bashText(f func(string) string) func(any) any {
	return func(v any) any {
		if b, ok := v.(bashString); ok {
			return bashString(f(string(b)))
		}
		return f(text(v))
	}
}

// bashString is a string a bash script's template prints shell-quoted. It
// compares, and encodes as JSON, as its raw value.
type bashString string

func (b bashString) String() string { return shellQuote(string(b)) }

// bashMap is a JSON object a bash script's template prints as its
// shell-quoted JSON.
type bashMap map[string]any

func (m bashMap) String() string { return shellQuoteJSON(m) }

// bashList is a JSON array a bash script's template prints as its
// shell-quoted JSON.
type bashList []any

func (l bashList) String() string { return shellQuoteJSON(l) }

// shellQuoteJSON is v's JSON, shell-quoted. v holds decoded JSON, which
// always encodes.
func shellQuoteJSON(v any) string {
	b, _ := json.Marshal(v)
	return shellQuote(string(b))
}

// bashKeyRe matches the object keys a bash script's template keeps: a
// {{range}} prints keys as they are, so one that isn't a plain word could
// run shell code.
var bashKeyRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// bashValue returns v, decoded JSON, in a form a bash script's template
// prints safely: strings and nothing quoted, objects and arrays as quoted
// JSON, and numbers and booleans as they are.
func bashValue(v any) any {
	switch v := v.(type) {
	case nil:
		return bashString("")
	case string:
		return bashString(v)
	case map[string]any:
		m := make(bashMap, len(v))
		for k, x := range v {
			if bashKeyRe.MatchString(k) {
				m[k] = bashValue(x)
			}
		}
		return m
	case []any:
		l := make(bashList, len(v))
		for i, x := range v {
			l[i] = bashValue(x)
		}
		return l
	}
	return v
}

// bashData returns data with every node's data in bashValue's form.
func bashData(data map[string]any) map[string]any {
	if data == nil {
		return nil
	}
	out := make(map[string]any, len(data))
	for id, d := range data {
		out[id] = bashValue(d)
	}
	return out
}
