package chatcomponents

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/config"
)

type ChatComponentsSuite struct {
	suite.Suite
}

func TestChatComponentsSuite(t *testing.T) {
	suite.Run(t, new(ChatComponentsSuite))
}

// files is a readFile over an in-memory map.
func files(m map[string]string) func(string) ([]byte, error) {
	return func(p string) ([]byte, error) {
		if s, ok := m[p]; ok {
			return []byte(s), nil
		}
		return nil, os.ErrNotExist
	}
}

func (s *ChatComponentsSuite) TestResolveBuiltins() {
	got := Resolve(nil, nil, files(nil))
	require.Len(s.T(), got, 2)
	require.Equal(s.T(), "math", got[0].Name)
	require.Contains(s.T(), got[0].Shell, "{{content}}")
	require.Contains(s.T(), got[0].CSS, ".paper")
	require.Contains(s.T(), got[0].Guide, "data-step")
	require.Equal(s.T(), "canvas", got[1].Name)
	require.Contains(s.T(), got[1].Shell, `<canvas id="c">`)
}

func (s *ChatComponentsSuite) TestResolveConfigEntries() {
	builtinMath := Resolve(nil, nil, files(nil))[0]
	tests := []struct {
		name    string
		entries []config.ChatComponent
		files   map[string]string
		check   func(ts []Template)
	}{
		{
			name:    "a new template reads its files from the first dir that has them",
			entries: []config.ChatComponent{{Name: "reaction", Description: "Reactions"}},
			files: map[string]string{
				"/proj/.loop/components/reaction/style.css":  "project css",
				"/home/.loop/components/reaction/style.css":  "global css",
				"/home/.loop/components/reaction/shell.html": "<main>{{content}}</main>",
			},
			check: func(ts []Template) {
				require.Len(s.T(), ts, 3)
				require.Equal(s.T(), Template{Name: "reaction", Description: "Reactions", CSS: "project css", Shell: "<main>{{content}}</main>"}, ts[2])
			},
		},
		{
			name:    "an entry named like a built-in replaces it in place and inherits missing files",
			entries: []config.ChatComponent{{Name: "math", Path: "paper"}},
			files:   map[string]string{"/home/.loop/components/paper/style.css": "my paper"},
			check: func(ts []Template) {
				require.Len(s.T(), ts, 2)
				require.Equal(s.T(), "math", ts[0].Name)
				require.Equal(s.T(), "my paper", ts[0].CSS)
				require.Equal(s.T(), builtinMath.Shell, ts[0].Shell)
				require.Equal(s.T(), builtinMath.Description, ts[0].Description)
			},
		},
		{
			name:    "a later entry with the same name replaces an earlier one",
			entries: []config.ChatComponent{{Name: "chart", Description: "first"}, {Name: "chart", Description: "second"}},
			check: func(ts []Template) {
				require.Len(s.T(), ts, 3)
				require.Equal(s.T(), "second", ts[2].Description)
			},
		},
		{
			name:    "names that can't sit in a fence info string are skipped",
			entries: []config.ChatComponent{{Name: "two words"}, {Name: ""}, {Name: "Upper"}},
			check: func(ts []Template) {
				require.Len(s.T(), ts, 2)
			},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			tc.check(Resolve(tc.entries, []string{"/proj/.loop", "/home/.loop"}, files(tc.files)))
		})
	}
}

func (s *ChatComponentsSuite) TestFind() {
	ts := []Template{{Name: "math"}, {Name: "canvas"}}
	got, ok := Find(ts, "canvas")
	require.True(s.T(), ok)
	require.Equal(s.T(), "canvas", got.Name)
	_, ok = Find(ts, "nope")
	require.False(s.T(), ok)
}

func (s *ChatComponentsSuite) TestTitle() {
	tests := []struct {
		name, in, want string
	}{
		{name: "trimmed to one line", in: "  Fracții\n algebrice\t pas cu pas ", want: "Fracții algebrice pas cu pas"},
		{name: "empty", in: " \n", want: ""},
		{name: "entities decoded", in: "|(−5x + 10)/2| &gt; −5 &amp;&amp; x &lt; 1", want: "|(−5x + 10)/2| > −5 && x < 1"},
		{name: "capped by runes", in: strings.Repeat("ă", 130), want: strings.Repeat("ă", 120)},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, Title(tc.in))
		})
	}
}

func (s *ChatComponentsSuite) TestCompose() {
	tests := []struct {
		name     string
		tmpl     Template
		content  Content
		contains []string
		excludes []string
	}{
		{
			name:    "content fills the slot, styles and scripts in order",
			tmpl:    Template{Shell: `<div class="paper">{{content}}</div>`, CSS: ".paper{}"},
			content: Content{Title: "a < b", HTML: "<p>hi</p>", CSS: "p{}", JS: "console.log(1)"},
			contains: []string{
				"<title>a &lt; b</title>",
				"<style>\n.paper{}\n</style>\n<style>\np{}\n</style>",
				`<div class="paper"><p>hi</p></div>`,
				"loop-component-height",
				"<script type=\"module\">\nconsole.log(1)\n</script>",
			},
		},
		{
			name:     "no slot puts the content after the shell",
			tmpl:     Template{Shell: "<canvas></canvas>"},
			content:  Content{HTML: "<p>legend</p>"},
			contains: []string{"<canvas></canvas>\n<p>legend</p>"},
			excludes: []string{`type="module"`},
		},
		{
			name:     "closing tags in css and js can't end their element",
			content:  Content{CSS: "a{}</style><b>", JS: `s = "</script><i>"; t = "</SCRIPT>"`},
			contains: []string{`a{}<\/style><b>`, `s = "<\/script><i>"; t = "<\/SCRIPT>"`},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			doc := Compose(tc.tmpl, tc.content)
			require.True(s.T(), strings.HasPrefix(doc, "<!doctype html>"))
			for _, want := range tc.contains {
				require.Contains(s.T(), doc, want)
			}
			for _, not := range tc.excludes {
				require.NotContains(s.T(), doc, not)
			}
		})
	}
}

func (s *ChatComponentsSuite) TestFence() {
	tests := []struct {
		name, template, title, doc, want string
	}{
		{name: "plain", template: "math", title: "Fracții", doc: "<p>x</p>\n", want: "```loop-component math Fracții\n<p>x</p>\n```"},
		{name: "no title", template: "canvas", doc: "<p>x</p>", want: "```loop-component canvas\n<p>x</p>\n```"},
		{name: "a backtick run in the document lengthens the fence", template: "math", doc: "a\n````js\nb", want: "`````loop-component math\na\n````js\nb\n`````"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, Fence(tc.template, tc.title, tc.doc))
		})
	}
}

func (s *ChatComponentsSuite) TestGuide() {
	g := Guide([]Template{{Name: "math", Description: "Paper", Guide: "Use .frac\n"}, {Name: "bare", Description: "No guide"}})
	require.Contains(s.T(), g, `action "show"`)
	require.Contains(s.T(), g, "\n## math\nPaper\n\nUse .frac\n")
	require.Contains(s.T(), g, "\n## bare\nNo guide\n")
	require.NotContains(s.T(), g, "No guide\n\n\n")
}
