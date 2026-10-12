package workflow

import (
	"encoding/json"
	"maps"
	"regexp"
	"strings"
	"text/template"
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
	return funcs
}()

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
