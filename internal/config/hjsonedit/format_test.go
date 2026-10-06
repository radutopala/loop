package hjsonedit

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type FormatSuite struct {
	suite.Suite
}

func TestFormatSuite(t *testing.T) {
	suite.Run(t, new(FormatSuite))
}

func (s *FormatSuite) TestFormat() {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "one-line top level gets one member per line",
			in:   `{"a":1,"b":"x"}`,
			want: "{\n  \"a\": 1,\n  \"b\": \"x\"\n}\n",
		},
		{
			name: "re-indents with two spaces",
			in:   "{\n\t\"a\": {\n\t\t\"b\": [\n\t\t\t1,\n\t\t\t2\n\t\t]\n\t}\n}\n",
			want: "{\n  \"a\": {\n    \"b\": [\n      1,\n      2\n    ]\n  }\n}\n",
		},
		{
			name: "one-line containers stay on one line",
			in:   "{\n\"a\":[1,2 ,{\"x\" :3}],\n\"b\":{ \"c\":true }\n}",
			want: "{\n  \"a\": [1, 2, {\"x\": 3}],\n  \"b\": {\"c\": true}\n}\n",
		},
		{
			name: "multi-line container inside a one-line one",
			in:   "{\"a\": [{\n\"b\": 1}]}",
			want: "{\n  \"a\": [{\n    \"b\": 1\n  }]\n}\n",
		},
		{
			name: "empty containers",
			in:   "{\"a\": { }, \"b\": [\n]}",
			want: "{\n  \"a\": {},\n  \"b\": []\n}\n",
		},
		{
			name: "empty top level",
			in:   "{}",
			want: "{}\n",
		},
		{
			name: "comments keep their lines",
			in:   "// head\n/* block */\n{\n\"a\": 1, // after a\n    // before b\n\"b\": 2 // after b\n}\n// end\n",
			want: "// head\n/* block */\n{\n  \"a\": 1, // after a\n  // before b\n  \"b\": 2 // after b\n}\n// end\n",
		},
		{
			name: "commented-out members at the end of an object",
			in:   "{\n  \"a\": 1\n\n      //\"b\": 2\n}",
			want: "{\n  \"a\": 1\n\n  //\"b\": 2\n}\n",
		},
		{
			name: "comment in an otherwise empty container",
			in:   "{\"a\": [ /* none */ ], \"b\": {\n// none\n}}",
			want: "{\n  \"a\": [ /* none */],\n  \"b\": {\n    // none\n  }\n}\n",
		},
		{
			name: "one blank line kept between members",
			in:   "{\n\"a\": 1,\n\n\n\n\"b\": 2,\n\n// c\n\n\"c\": 3\n\n}",
			want: "{\n  \"a\": 1,\n\n  \"b\": 2,\n\n  // c\n\n  \"c\": 3\n}\n",
		},
		{
			name: "trailing commas kept",
			in:   "{\n\"a\": [1, 2,],\n\"b\": 3,\n}",
			want: "{\n  \"a\": [1, 2,],\n  \"b\": 3,\n}\n",
		},
		{
			name: "comments around a name and a value",
			in:   "{\n\"a\" /* n */ :\n  // v\n  1 /* after */,\n\"b\": 2 // last\n  , \"c\": 3}",
			want: "{\n  \"a\" /* n */:\n  // v\n  1 /* after */,\n  \"b\": 2 // last\n  ,\n  \"c\": 3\n}\n",
		},
		{
			name: "block comment ending a line before a value",
			in:   "{\"a\": /* v */\n1}",
			want: "{\n  \"a\": /* v */\n  1\n}\n",
		},
		{
			name: "line comment after the top level",
			in:   "{} // end\n\n\n",
			want: "{} // end\n",
		},
		{
			name: "leading blank lines and comment on the first line",
			in:   "\n\n  /* a */ // b\n{}",
			want: "/* a */ // b\n{}\n",
		},
		{
			name: "top-level array",
			in:   "[1,\"x\"]",
			want: "[\n  1,\n  \"x\"\n]\n",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			got, err := Format([]byte(tt.in))
			require.NoError(s.T(), err)
			require.Equal(s.T(), tt.want, string(got))
			again, err := Format(got)
			require.NoError(s.T(), err)
			require.Equal(s.T(), string(got), string(again), "formatting is idempotent")
		})
	}
}

func (s *FormatSuite) TestFormatExamplesKeepTheirLayout() {
	for _, path := range []string{"../config.global.example.json", "../config.project.example.json"} {
		data, err := os.ReadFile(path)
		require.NoError(s.T(), err)
		got, err := Format(data)
		require.NoError(s.T(), err)
		// The examples are already laid out this way, bar the odd doubled
		// blank line, which Format collapses.
		require.Equal(s.T(), collapseBlankLines(string(data)), string(got), path)
	}
}

func (s *FormatSuite) TestFormatInvalid() {
	_, err := Format([]byte(`{"a": }`))
	require.Error(s.T(), err)
}

// collapseBlankLines turns each run of blank lines in text into one.
func collapseBlankLines(text string) string {
	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}
	return text
}
