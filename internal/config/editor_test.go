package config

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type EditorConfigSuite struct {
	suite.Suite
}

func TestEditorConfigSuite(t *testing.T) {
	suite.Run(t, new(EditorConfigSuite))
}

func (s *EditorConfigSuite) TestLoad() {
	gofmt := Formatter{Glob: "*.go", Command: "gofmt"}
	tests := []struct {
		name string
		json string
		want EditorConfig
	}{
		{"unset", ``, DefaultEditorConfig()},
		{"empty block", `, "editor": {}`, DefaultEditorConfig()},
		{"every field", `, "editor": {"follow_active_file": false, "format_on_save": true, "formatters": [{"glob": "*.go", "command": "gofmt"}]}`,
			EditorConfig{FollowActiveFileFrom: FromGlobal, FormatOnSave: true, FormatOnSaveFrom: FromGlobal, Formatters: []Formatter{gofmt}, TabSize: DefaultTabSize, InsertSpaces: true, PreviewTabs: true}},
		{"indent and save cleanups", `, "editor": {"tab_size": 2, "insert_spaces": false, "trim_trailing_whitespace": true, "insert_final_newline": true, "search_exclude": ["*.min.js"]}`,
			EditorConfig{FollowActiveFile: true, FollowActiveFileFrom: FromDefault, FormatOnSaveFrom: FromDefault, TabSize: 2, TrimTrailingWhitespace: true, InsertFinalNewline: true, SearchExclude: []string{"*.min.js"}, PreviewTabs: true}},
		{"tab behaviour", `, "editor": {"auto_save_on_blur": true, "preview_tabs": false}`,
			EditorConfig{FollowActiveFile: true, FollowActiveFileFrom: FromDefault, FormatOnSaveFrom: FromDefault, TabSize: DefaultTabSize, InsertSpaces: true, AutoSaveOnBlur: true}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			l := &Loader{
				userHomeDir: func() (string, error) { return "/home/testuser", nil },
				readFile: func(string) ([]byte, error) {
					return []byte(`{"platforms": ["discord"], "discord_token": "t", "discord_app_id": "a"` + tc.json + `}`), nil
				},
			}
			cfg, err := l.load()
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.want, cfg.Editor)
		})
	}
}

func (s *EditorConfigSuite) TestOverlay() {
	on := true
	g := Formatter{Glob: "*.go", Command: "g"}
	p := Formatter{Glob: "*.go", Command: "p"}
	base := EditorConfig{FollowActiveFile: true, FollowActiveFileFrom: FromGlobal, FormatOnSaveFrom: FromDefault, Formatters: []Formatter{g}}

	require.Equal(s.T(), base, base.overlay(nil, FromProject))
	got := base.overlay(&jsonEditorConfig{FormatOnSave: &on, Formatters: []Formatter{p}}, FromProject)
	require.Equal(s.T(), EditorConfig{
		FollowActiveFile: true, FollowActiveFileFrom: FromGlobal,
		FormatOnSave: true, FormatOnSaveFrom: FromProject,
		Formatters: []Formatter{p, g},
	}, got)
	require.Equal(s.T(), []Formatter{g}, base.Formatters, "the layer below keeps its formatters")
}

func (s *EditorConfigSuite) TestOverlayTabSize() {
	tests := []struct {
		name string
		set  int
		want int
	}{
		{"in range", 8, 8},
		{"lowest", 1, 1},
		{"highest", 16, 16},
		{"zero is ignored", 0, DefaultTabSize},
		{"too wide is ignored", 17, DefaultTabSize},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, DefaultEditorConfig().overlay(&jsonEditorConfig{TabSize: &tc.set}, FromProject).TabSize)
		})
	}
}

func (s *EditorConfigSuite) TestOverlaySearchExclude() {
	base := EditorConfig{SearchExclude: []string{"dist", "*.min.js"}}
	got := base.overlay(&jsonEditorConfig{SearchExclude: []string{"gen/**", "dist"}}, FromProject)
	require.Equal(s.T(), []string{"gen/**", "dist", "*.min.js"}, got.SearchExclude)
	require.Equal(s.T(), []string{"dist", "*.min.js"}, base.SearchExclude, "the layer below keeps its globs")
}

func (s *EditorConfigSuite) TestExcluded() {
	c := EditorConfig{SearchExclude: []string{"", "*.min.{js,css}", "fixtures", "web/gen"}}
	tests := []struct {
		rel  string
		want bool
	}{
		{"a.min.js", true},
		{"deep/b.min.css", true},
		{"a.js", false},
		{"fixtures", true},
		{"pkg/fixtures", true},
		{"web/gen", true},
		{"other/web/gen", false},
	}
	for _, tc := range tests {
		s.Run(tc.rel, func() {
			require.Equal(s.T(), tc.want, c.Excluded(tc.rel))
		})
	}
	require.Equal(s.T(), []string{"*.min.js", "*.min.css", "fixtures", "web/gen"}, c.ExcludeGlobs())
}

func (s *EditorConfigSuite) TestTidy() {
	tests := []struct {
		name          string
		trim, newline bool
		in, want      string
	}{
		{"off", false, false, "a  \nb\t", "a  \nb\t"},
		{"trim", true, false, "a  \n\tb\t \n", "a\n\tb\n"},
		{"trim keeps CRLF", true, false, "a \r\nb\t\r\n", "a\r\nb\r\n"},
		{"final newline", false, true, "a", "a\n"},
		{"final newline already there", false, true, "a\n", "a\n"},
		{"empty stays empty", true, true, "", ""},
		{"both", true, true, "a \nb ", "a\nb\n"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			in := []byte(tc.in)
			got := EditorConfig{TrimTrailingWhitespace: tc.trim, InsertFinalNewline: tc.newline}.Tidy(in)
			require.Equal(s.T(), tc.want, string(got))
			require.Equal(s.T(), tc.in, string(in), "the input is left as it was")
		})
	}
}

func (s *EditorConfigSuite) TestFormatterFor() {
	c := EditorConfig{Formatters: []Formatter{
		{Glob: "*.md", Command: ""},
		{Glob: "web/*.{ts,tsx}", Command: "web"},
		{Glob: "*.{ts,tsx,mjs}", Command: "ts"},
		{Glob: "*.{go", Command: "broken"},
		{Glob: "Makefile", Command: "make"},
		{Glob: "[", Command: "bad"},
	}}
	tests := []struct {
		rel  string
		want string
	}{
		{"a.md", ""},
		{"web/a.tsx", "web"},
		{"src/web/a.ts", "ts"},
		{"deep/x.mjs", "ts"},
		{"a.{go", "broken"},
		{"a.go", ""},
		{"sub/Makefile", "make"},
	}
	for _, tc := range tests {
		s.Run(tc.rel, func() {
			f, ok := c.FormatterFor(tc.rel)
			require.Equal(s.T(), tc.want != "", ok)
			require.Equal(s.T(), tc.want, f.Command)
		})
	}
}

func (s *EditorConfigSuite) TestTrustedOnlyWhenSet() {
	pc, err := parseProjectConfig([]byte(`{"editor": {"format_on_save": true}}`))
	require.NoError(s.T(), err)
	require.Nil(s.T(), trustedFieldsOf(pc).Editor)

	pc, err = parseProjectConfig([]byte(`{"editor": {"formatters": [{"glob": "*", "command": "x"}]}}`))
	require.NoError(s.T(), err)
	require.Equal(s.T(), &trustedEditor{Formatters: []Formatter{{Glob: "*", Command: "x"}}}, trustedFieldsOf(pc).Editor)

	// Applying an untrusted config drops its formatters but keeps the flags.
	trustedFields{}.apply(pc)
	require.Nil(s.T(), pc.Editor.Formatters)
	pc = &projectConfig{}
	trustedFields{Editor: &trustedEditor{Formatters: []Formatter{{Glob: "*", Command: "x"}}}}.apply(pc)
	require.Len(s.T(), pc.Editor.Formatters, 1)
}
