package workflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type TemplateSuite struct {
	suite.Suite
}

func TestTemplateSuite(t *testing.T) {
	suite.Run(t, new(TemplateSuite))
}

// runCtx is a run whose check node left structured data.
func (s *TemplateSuite) runCtx() *RunContext {
	return &RunContext{
		Inputs:      map[string]string{"s": "Hello World", "j": `{"a":2}`},
		NodeOutputs: map[string]string{"prev": "out"},
		NodeData: map[string]any{
			"check": map[string]any{
				"score":    int64(7),
				"ratio":    0.5,
				"name":     " Ana ",
				"tags":     []any{"a", "b"},
				"ok":       true,
				"none":     nil,
				"bad key;": "dropped in bash",
			},
		},
	}
}

func (s *TemplateSuite) TestRenderTemplate() {
	tests := []struct {
		name    string
		tmpl    string
		want    string
		wantErr string
	}{
		{name: "contains", tmpl: `{{contains .Inputs.s "World"}}`, want: "true"},
		{name: "contains nothing", tmpl: `{{contains .NodeData.check.none ""}}`, want: "true"},
		{name: "contains a number", tmpl: `{{contains .NodeData.check.score 7}}`, want: "true"},
		{name: "hasPrefix", tmpl: `{{hasPrefix .Inputs.s "Hello"}}`, want: "true"},
		{name: "hasSuffix", tmpl: `{{hasSuffix .Inputs.s "Hello"}}`, want: "false"},
		{name: "trim lower upper", tmpl: `{{trim .NodeData.check.name}}{{lower "X"}}{{upper "y"}}`, want: "AnaxY"},
		{name: "data field", tmpl: `{{.NodeData.check.score}} {{.NodeData.check.ratio}}`, want: "7 0.5"},
		{name: "compare an int", tmpl: `{{gt .NodeData.check.score 5}}`, want: "true"},
		{name: "missing data key", tmpl: `{{.NodeData.check.nope}}`, want: "<no value>"},
		{name: "fromJSON object", tmpl: `{{(fromJSON .Inputs.j).a}}`, want: "2"},
		{name: "fromJSON whole number", tmpl: `{{gt (fromJSON "3") 2}}`, want: "true"},
		{name: "fromJSON float", tmpl: `{{fromJSON "1.5"}} {{fromJSON "1e3"}}`, want: "1.5 1000"},
		{name: "fromJSON invalid", tmpl: `{{fromJSON "x"}}`, wantErr: "fromJSON: invalid character"},
		{name: "fromJSON two values", tmpl: `{{fromJSON "1 2"}}`, wantErr: "fromJSON: more than one JSON value"},
		{name: "toJSON", tmpl: `{{toJSON .NodeData.check.tags}}`, want: `["a","b"]`},
		{name: "toJSON fails", tmpl: `{{toJSON (fromJSON "1e400")}}`, wantErr: "toJSON: json: unsupported value"},
		{name: "empty", tmpl: "", want: ""},
		{name: "bad template", tmpl: "{{", wantErr: "unclosed action"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			got, err := renderTemplate(tt.tmpl, s.runCtx())
			if tt.wantErr != "" {
				require.ErrorContains(s.T(), err, tt.wantErr)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), tt.want, got)
		})
	}
}

func (s *TemplateSuite) TestRenderBashScript() {
	tests := []struct {
		name    string
		tmpl    string
		want    string
		wantErr string
	}{
		{name: "string", tmpl: `echo {{.NodeData.check.name}}`, want: `echo ' Ana '`},
		{name: "number and bool", tmpl: `echo {{.NodeData.check.score}} {{.NodeData.check.ratio}} {{.NodeData.check.ok}}`, want: `echo 7 0.5 true`},
		{name: "list", tmpl: `echo {{.NodeData.check.tags}}`, want: `echo '["a","b"]'`},
		{name: "nothing", tmpl: `echo {{.NodeData.check.none}}`, want: `echo ''`},
		{
			name: "object drops keys that aren't words",
			tmpl: `echo {{.NodeData.check}}`,
			want: `echo '{"name":" Ana ","none":"","ok":true,"ratio":0.5,"score":7,"tags":["a","b"]}'`,
		},
		{name: "range keys", tmpl: `{{range $k, $v := .NodeData.check}}{{$k}} {{end}}`, want: "name none ok ratio score tags "},
		{name: "eq compares raw", tmpl: `{{if eq .NodeData.check.name " Ana "}}yes{{end}}`, want: "yes"},
		{name: "contains reads raw", tmpl: `{{contains .NodeData.check.name "An"}}`, want: "true"},
		{name: "trim keeps quoting", tmpl: `echo {{trim .NodeData.check.name}} {{upper .NodeData.check.name}} {{lower "X"}}`, want: `echo 'Ana' ' ANA ' x`},
		{name: "fromJSON quotes", tmpl: `echo {{(fromJSON "{\"a\":\"$(id)\"}").a}}`, want: `echo '$(id)'`},
		{name: "fromJSON fails", tmpl: `{{fromJSON "x"}}`, wantErr: "fromJSON: invalid character"},
		{name: "toJSON quotes", tmpl: `echo {{toJSON .NodeData.check.tags}}`, want: `echo '["a","b"]'`},
		{name: "toJSON fails", tmpl: `{{toJSON (fromJSON "1e400")}}`, wantErr: "toJSON: json: unsupported value"},
		{name: "inputs stay quoted", tmpl: `echo {{.Inputs.s}}`, want: `echo 'Hello World'`},
		{name: "missing data key", tmpl: `echo {{.NodeData.check.nope}} {{.NodeData.nope}}`, want: `echo '' ''`},
		{name: "missing input", tmpl: `echo {{.Inputs.nope}}`, want: `echo ''`},
		{name: "missing key in if", tmpl: `{{if .NodeData.check.ok}}echo {{.NodeData.check.nope}}{{else}}no{{end}}`, want: `echo ''`},
		{name: "missing key in else", tmpl: `{{if .NodeData.check.nope}}yes{{else}}echo {{.NodeData.check.nope}}{{end}}`, want: `echo ''`},
		{name: "missing key in range", tmpl: `{{range .NodeData.check.tags}}{{.}}{{$.NodeData.nope}} {{else}}none{{end}}`, want: `'a''' 'b''' `},
		{name: "missing key in with", tmpl: `{{with .NodeData.check}}echo {{.nope}}{{else}}no{{end}}`, want: `echo ''`},
		{name: "variable", tmpl: `{{$v := .NodeData.check.name}}echo {{$v}}`, want: `echo ' Ana '`},
		{name: "defined template", tmpl: `{{define "x"}}{{.nope}}{{end}}echo {{template "x" .NodeData.check}}`, want: `echo ''`},
		{name: "iteration", tmpl: `echo {{.Iteration}}`, want: `echo 0`},
		{name: "bad template", tmpl: "echo {{", wantErr: "unclosed action"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			got, err := renderBashScript(tt.tmpl, s.runCtx())
			if tt.wantErr != "" {
				require.ErrorContains(s.T(), err, tt.wantErr)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), tt.want, got)
		})
	}
}

// TestRenderBashScriptNoData renders a run without node data or inputs:
// what's missing prints as an empty quoted string.
func (s *TemplateSuite) TestRenderBashScriptNoData() {
	got, err := renderBashScript(`echo {{.NodeData.x}} {{.Inputs.x}}`, &RunContext{})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "echo '' ''", got)
}

// TestRenderBashScriptInjection runs a script whose node data tries to
// break out of its quoting: each value stays one literal argument.
func (s *TemplateSuite) TestRenderBashScriptInjection() {
	dir := s.T().TempDir()
	marker := filepath.Join(dir, "pwned")
	evil := "'; touch " + marker + "; echo '"
	runCtx := &RunContext{NodeData: map[string]any{"check": map[string]any{
		"s":    evil,
		"list": []any{evil},
		"obj":  map[string]any{"k": evil},
	}}}
	script, err := renderBashScript(`printf '%s\n' {{.NodeData.check.s}} {{.NodeData.check.list}} {{.NodeData.check.obj}} {{trim .NodeData.check.s}}`, runCtx)
	require.NoError(s.T(), err)

	out, err := (&LocalBashRunner{SafeDir: dir}).RunBash(context.Background(), script, "", dir, "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), evil+"\n"+`["`+evil+`"]`+"\n"+`{"k":"`+evil+`"}`+"\n"+evil+"\n", out)
	_, err = os.Stat(marker)
	require.ErrorIs(s.T(), err, os.ErrNotExist)
}
