package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"text/template"

	"github.com/radutopala/loop/internal/config"
)

// templateFuncs are the helpers every workflow template may call, on top
// of text/template's builtins. A bash script's template gets bashFuncs,
// which have the same names.
var templateFuncs = template.FuncMap{
	"contains":  func(s, substr any) bool { return strings.Contains(text(s), text(substr)) },
	"hasPrefix": func(s, prefix any) bool { return strings.HasPrefix(text(s), text(prefix)) },
	"hasSuffix": func(s, suffix any) bool { return strings.HasSuffix(text(s), text(suffix)) },
	"trim":      strings.TrimSpace,
	"lower":     strings.ToLower,
	"upper":     strings.ToUpper,
	"fromJSON":  fromJSON,
	"toJSON":    toJSON,
}

// text is v as a predicate helper reads it: a string, or a bash script's
// quoted value, as its raw text; nothing as ""; anything else as fmt
// prints it.
func text(v any) string {
	if v == nil {
		return ""
	}
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.String {
		return rv.String()
	}
	return fmt.Sprint(v)
}

// fromJSON decodes s, so a template can read fields of JSON text.
func fromJSON(s string) (any, error) {
	v, err := decodeJSON([]byte(s))
	if err != nil {
		return nil, fmt.Errorf("fromJSON: %w", err)
	}
	return v, nil
}

// decodeJSON decodes b with whole numbers as int64 and the rest as
// float64, so a template compares {{gt .NodeData.check.score 5}} without
// a type error.
func decodeJSON(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("more than one JSON value")
	}
	return numbers(v), nil
}

// numbers replaces the json.Numbers in v, in place.
func numbers(v any) any {
	switch v := v.(type) {
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i
		}
		// A number the decoder accepted that int64 doesn't hold.
		f, _ := v.Float64()
		return f
	case map[string]any:
		for k, x := range v {
			v[k] = numbers(x)
		}
	case []any:
		for i, x := range v {
			v[i] = numbers(x)
		}
	}
	return v
}

// toJSON encodes v as JSON text.
func toJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("toJSON: %w", err)
	}
	return string(b), nil
}

// parseTemplate parses tmplStr with the workflow helpers.
func parseTemplate(tmplStr string) (*template.Template, error) {
	return template.New("").Funcs(templateFuncs).Parse(tmplStr)
}

// renderTemplate renders a Go text/template string with the given RunContext.
func renderTemplate(tmplStr string, data *RunContext) (string, error) {
	if tmplStr == "" {
		return "", nil
	}
	tmpl, err := parseTemplate(tmplStr)
	if err != nil {
		return "", err
	}
	return execute(tmpl, data)
}

// execute renders tmpl against data.
func execute(tmpl *template.Template, data *RunContext) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// validateNodeTemplates parses every template field of n, so a typo or an
// unknown function fails when the workflow is saved or started, not
// halfway through a run. A prompt_path file is read at run time, so it
// isn't checked here.
func validateNodeTemplates(n *config.NodeDef) error {
	fields := []struct{ name, tmpl string }{
		{"when", n.When},
		{"prompt", n.Prompt},
		{"system_prompt", n.SystemPrompt},
		{"script", n.Script},
		{"condition", n.Condition},
		{"message", n.Message},
	}
	for _, f := range fields {
		if _, err := parseTemplate(f.tmpl); err != nil {
			return fmt.Errorf("node %q: %s: %w", n.ID, f.name, err)
		}
	}
	in, err := pluginInput(n.Input)
	if err != nil {
		// validatePluginNode reports a bad input.
		return nil
	}
	if err := validateInputTemplates(in); err != nil {
		return fmt.Errorf("node %q: input: %w", n.ID, err)
	}
	return nil
}

// validateInputTemplates parses every string in v, a decoded JSON value.
func validateInputTemplates(v any) error {
	switch v := v.(type) {
	case string:
		_, err := parseTemplate(v)
		return err
	case map[string]any:
		for _, x := range v {
			if err := validateInputTemplates(x); err != nil {
				return err
			}
		}
	case []any:
		for _, x := range v {
			if err := validateInputTemplates(x); err != nil {
				return err
			}
		}
	}
	return nil
}
