package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// editorChannel points ch-1 at a fresh temp dir and returns it.
func (s *ServerSuite) editorChannel() string {
	dir := s.T().TempDir()
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", DirPath: dir}, nil)
	return dir
}

// editorGlobal sets the global config's editor section.
func (s *ServerSuite) editorGlobal(ec config.EditorConfig) {
	s.srv.configs.load = func() (*config.Config, error) { return &config.Config{Editor: ec}, nil }
}

func (s *ServerSuite) TestEditorConfig() {
	tests := []struct {
		name  string
		setup func()
		want  editorConfigResponse
	}{
		{"defaults", func() { s.editorGlobal(config.DefaultEditorConfig()) },
			editorConfigResponse{FollowActiveFile: true, FollowActiveFileFrom: "default", FormatOnSaveFrom: "default", TabSize: 4, InsertSpaces: true, PreviewTabs: true}},
		{"global", func() {
			s.editorGlobal(config.EditorConfig{FollowActiveFileFrom: "global", FormatOnSave: true, FormatOnSaveFrom: "global", Formatters: []config.Formatter{{Glob: "*", Command: "x"}}, TabSize: 2, AutoSaveOnBlur: true})
		}, editorConfigResponse{FollowActiveFileFrom: "global", FormatOnSave: true, FormatOnSaveFrom: "global", Formatters: 1, TabSize: 2, AutoSaveOnBlur: true}},
		{"global load fails", func() { s.srv.configs.load = func() (*config.Config, error) { return nil, os.ErrNotExist } },
			editorConfigResponse{FollowActiveFile: true, FollowActiveFileFrom: "default", FormatOnSaveFrom: "default", TabSize: 4, InsertSpaces: true, PreviewTabs: true}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.editorChannel()
			tc.setup()
			rec := s.testRequest("GET", "/api/channels/ch-1/editor-config", "")
			require.Equal(s.T(), http.StatusOK, rec.Code)
			var got editorConfigResponse
			require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &got))
			require.Equal(s.T(), tc.want, got)
		})
	}
}

func (s *ServerSuite) TestEditorConfig_ProjectLayer() {
	dir := s.editorChannel()
	require.NoError(s.T(), os.MkdirAll(filepath.Join(dir, ".loop"), 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, ".loop", "config.json"), []byte(`{"editor": {"follow_active_file": false}}`), 0o644))
	s.editorGlobal(config.DefaultEditorConfig())

	rec := s.testRequest("GET", "/api/channels/ch-1/editor-config", "")
	var got editorConfigResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &got))
	require.False(s.T(), got.FollowActiveFile)
	require.Equal(s.T(), "project", got.FollowActiveFileFrom)
}

func (s *ServerSuite) TestEditorConfig_UnknownChannel() {
	s.store.On("GetChannel", mock.Anything, "nope").Return(nil, nil)
	rec := s.testRequest("GET", "/api/channels/nope/editor-config", "")
	var got editorConfigResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(s.T(), "default", got.FollowActiveFileFrom)
}

func (s *ServerSuite) TestWriteFile_FormatOnSave() {
	formatters := []config.Formatter{{Glob: "*.go", Command: "fmt {file}"}}
	tests := []struct {
		name        string
		query       string
		formatOn    bool
		path        string
		format      func(ctx context.Context, dir, command string, stdin []byte) ([]byte, error)
		timeout     time.Duration
		wantDisk    string
		wantContent *string
		wantErr     string
	}{
		{name: "without format=1 the content is written as sent", query: "", formatOn: true, path: "a.go", wantDisk: "raw"},
		{name: "format on save off", query: "&format=1", path: "a.go", wantDisk: "raw"},
		{name: "no formatter matches", query: "&format=1", formatOn: true, path: "a.txt", wantDisk: "raw"},
		{name: "formatted", query: "&format=1", formatOn: true, path: "a.go",
			format: func(_ context.Context, _, command string, stdin []byte) ([]byte, error) {
				return []byte(strings.ToUpper(string(stdin)) + " " + command), nil
			},
			wantDisk: "RAW fmt '<dir>/a.go'", wantContent: new("RAW fmt '<dir>/a.go'")},
		{name: "unchanged by the formatter", query: "&format=1", formatOn: true, path: "a.go",
			format:   func(_ context.Context, _, _ string, stdin []byte) ([]byte, error) { return stdin, nil },
			wantDisk: "raw"},
		{name: "formatter fails", query: "&format=1", formatOn: true, path: "a.go",
			format: func(context.Context, string, string, []byte) ([]byte, error) {
				return nil, errors.New(strings.Repeat("e", 3000))
			},
			wantDisk: "raw", wantErr: "fmt {file} failed: " + strings.Repeat("e", maxFormatError) + "…"},
		{name: "formatter prints nothing", query: "&format=1", formatOn: true, path: "a.go",
			format:   func(context.Context, string, string, []byte) ([]byte, error) { return nil, nil },
			wantDisk: "raw", wantErr: "fmt {file} printed nothing"},
		{name: "formatter times out", query: "&format=1", formatOn: true, path: "a.go", timeout: time.Millisecond,
			format: func(ctx context.Context, _, _ string, _ []byte) ([]byte, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
			wantDisk: "raw", wantErr: "fmt {file} timed out after 1ms"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			dir := s.editorChannel()
			dir, err := filepath.EvalSymlinks(dir)
			require.NoError(s.T(), err)
			s.editorGlobal(config.EditorConfig{FormatOnSave: tc.formatOn, Formatters: formatters})
			s.srv.editorCmds = editorCommands{format: tc.format, formatTimeout: tc.timeout}

			rec := s.testRequest("PUT", "/api/channels/ch-1/file?path="+tc.path+tc.query, "raw")
			require.Equal(s.T(), http.StatusOK, rec.Code)
			var resp writeFileResponse
			require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
			data, err := os.ReadFile(filepath.Join(dir, tc.path))
			require.NoError(s.T(), err)
			require.Equal(s.T(), strings.ReplaceAll(tc.wantDisk, "<dir>", dir), string(data))
			if tc.wantContent != nil {
				require.NotNil(s.T(), resp.Content)
				require.Equal(s.T(), strings.ReplaceAll(*tc.wantContent, "<dir>", dir), *resp.Content)
			} else {
				require.Nil(s.T(), resp.Content)
			}
			require.Equal(s.T(), tc.wantErr, resp.FormatError)
		})
	}
}

func (s *ServerSuite) TestWriteFile_SaveCleanups() {
	tests := []struct {
		name        string
		ec          config.EditorConfig
		format      func(ctx context.Context, dir, command string, stdin []byte) ([]byte, error)
		wantDisk    string
		wantContent bool
		wantErr     string
	}{
		{name: "off", wantDisk: "a  \nb"},
		{name: "trim and final newline", ec: config.EditorConfig{TrimTrailingWhitespace: true, InsertFinalNewline: true},
			wantDisk: "a\nb\n", wantContent: true},
		{name: "the formatter gets the cleaned-up content",
			ec: config.EditorConfig{TrimTrailingWhitespace: true, FormatOnSave: true, Formatters: []config.Formatter{{Glob: "*", Command: "f"}}},
			format: func(_ context.Context, _, _ string, stdin []byte) ([]byte, error) {
				return []byte(strings.ToUpper(string(stdin))), nil
			},
			wantDisk: "A\nB", wantContent: true},
		{name: "a failed format still saves the cleanups",
			ec: config.EditorConfig{InsertFinalNewline: true, FormatOnSave: true, Formatters: []config.Formatter{{Glob: "*", Command: "f"}}},
			format: func(context.Context, string, string, []byte) ([]byte, error) {
				return nil, errors.New("boom")
			},
			wantDisk: "a  \nb\n", wantContent: true, wantErr: "f failed: boom"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			dir := s.editorChannel()
			s.editorGlobal(tc.ec)
			s.srv.editorCmds = editorCommands{format: tc.format}

			rec := s.testRequest("PUT", "/api/channels/ch-1/file?path=a.txt&format=1", "a  \nb")
			require.Equal(s.T(), http.StatusOK, rec.Code)
			var resp writeFileResponse
			require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
			data, err := os.ReadFile(filepath.Join(dir, "a.txt"))
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.wantDisk, string(data))
			if tc.wantContent {
				require.NotNil(s.T(), resp.Content)
				require.Equal(s.T(), tc.wantDisk, *resp.Content)
			} else {
				require.Nil(s.T(), resp.Content)
			}
			require.Equal(s.T(), tc.wantErr, resp.FormatError)
		})
	}
}

func (s *ServerSuite) TestExecFormat() {
	dir := s.T().TempDir()
	out, err := execFormat(context.Background(), dir, "tr a-z A-Z; pwd >&2", []byte("abc"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ABC", string(out))

	_, err = execFormat(context.Background(), dir, "echo bad >&2; exit 3", nil)
	require.EqualError(s.T(), err, "exit status 3: bad")

	_, err = execFormat(context.Background(), dir, "exit 4", nil)
	require.EqualError(s.T(), err, "exit status 4")
}

func (s *ServerSuite) TestExecStream() {
	var got string
	err := execStream(context.Background(), s.T().TempDir(), "sh", []string{"-c", "echo hi; exit 1"}, func(r io.Reader) error {
		b, err := io.ReadAll(r)
		got = string(b)
		return err
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "hi\n", got)

	// read returning early kills a command that's still printing.
	stop := errors.New("stop")
	err = execStream(context.Background(), s.T().TempDir(), "sh", []string{"-c", "yes"}, func(io.Reader) error { return stop })
	require.ErrorIs(s.T(), err, stop)

	err = execStream(context.Background(), s.T().TempDir(), "/no/such/binary", nil, func(io.Reader) error { return nil })
	require.Error(s.T(), err)
}

func (s *ServerSuite) TestEditorCommandsDefaults() {
	var c editorCommands
	require.NotNil(s.T(), c.formatFn())
	require.NotNil(s.T(), c.lookPathFn())
	require.NotNil(s.T(), c.streamFn())
	require.Equal(s.T(), formatTimeout, c.formatTimeoutOr())
	require.Equal(s.T(), `'it'\''s'`, shellQuote("it's"))
}

// grep runs a content search over ch-1 and decodes it.
func (s *ServerSuite) grep(query string) (int, grepResponse) {
	rec := s.testRequest("GET", "/api/channels/ch-1/files/grep?"+query, "")
	var resp grepResponse
	if rec.Code == http.StatusOK {
		require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	}
	return rec.Code, resp
}

// builtinGrep makes the search run without rg.
func (s *ServerSuite) builtinGrep() {
	s.srv.editorCmds.lookPath = func(string) (string, error) { return "", errors.New("no rg") }
}

func (s *ServerSuite) TestGrep_Builtin() {
	dir := s.editorChannel()
	s.builtinGrep()
	files := map[string]string{
		"a.go":               "package a\n\tfunc Hello() {}\n// héllo HELLO\n",
		"sub/b.txt":          "nothing\nhello there\r\n",
		"bin.dat":            "hello\x00binary",
		".hidden":            "hello",
		".git/config":        "hello",
		"node_modules/x.js":  "hello",
		"ignored/c.txt":      "hello",
		"ignored.log":        "hello",
		"skip/.dotdir/d.txt": "hello",
		".gitignore":         "ignored/\n*.log\n",
	}
	for rel, content := range files {
		require.NoError(s.T(), os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755))
		require.NoError(s.T(), os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644))
	}
	require.NoError(s.T(), os.Symlink(filepath.Join(dir, "a.go"), filepath.Join(dir, "link.go")))

	code, resp := s.grep("q=hello")
	require.Equal(s.T(), http.StatusOK, code)
	require.Equal(s.T(), "builtin", resp.Engine)
	require.False(s.T(), resp.Truncated)
	require.Equal(s.T(), []grepFile{
		{RootIndex: 0, RelPath: "a.go", Matches: []grepMatch{{Line: 2, Column: 7, Length: 5, Text: "\tfunc Hello() {}"}, {Line: 3, Column: 10, Length: 5, Text: "// héllo HELLO"}}},
		{RootIndex: 0, RelPath: "sub/b.txt", Matches: []grepMatch{{Line: 2, Column: 1, Length: 5, Text: "hello there"}}},
	}, resp.Files)

	_, resp = s.grep("q=Hello&case=1")
	require.Len(s.T(), resp.Files, 1)
	require.Len(s.T(), resp.Files[0].Matches, 1)

	_, resp = s.grep("q=h.llo%20there&regex=1")
	require.Equal(s.T(), "sub/b.txt", resp.Files[0].RelPath)

	_, resp = s.grep("q=h.llo")
	require.Empty(s.T(), resp.Files, "a literal query doesn't match as a regex")
}

func (s *ServerSuite) TestGrep_BuiltinSearchExclude() {
	dir := s.editorChannel()
	s.builtinGrep()
	s.editorGlobal(config.EditorConfig{SearchExclude: []string{"*.min.js", "fixtures"}})
	for _, rel := range []string{"a.js", "a.min.js", "fixtures/b.js", "pkg/fixtures/c.js"} {
		require.NoError(s.T(), os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755))
		require.NoError(s.T(), os.WriteFile(filepath.Join(dir, rel), []byte("needle"), 0o644))
	}

	_, resp := s.grep("q=needle")
	require.Len(s.T(), resp.Files, 1)
	require.Equal(s.T(), "a.js", resp.Files[0].RelPath)
}

func (s *ServerSuite) TestGrep_Requests() {
	s.editorChannel()
	s.builtinGrep()

	code, resp := s.grep("q=a")
	require.Equal(s.T(), http.StatusOK, code)
	require.Equal(s.T(), []grepFile{}, resp.Files, "too short a query searches nothing")

	code, _ = s.grep("q=(ab&regex=1")
	require.Equal(s.T(), http.StatusBadRequest, code)

	s.store.On("GetChannel", mock.Anything, "ch-2").Return(nil, errors.New("db down"))
	rec := s.testRequest("GET", "/api/channels/ch-2/files/grep?q=ab", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)

	rec = httptest.NewRecorder()
	(&Server{}).handleGrepFiles(rec, httptest.NewRequest("GET", "/api/channels/x/files/grep?q=ab", nil))
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestGrep_EmptyExtraRootIsSkipped() {
	dir := s.editorChannel()
	extra := s.T().TempDir()
	for _, d := range []string{dir, extra} {
		require.NoError(s.T(), os.WriteFile(filepath.Join(d, "a.txt"), []byte("abc"), 0o644))
	}
	s.srv.configs.load = func() (*config.Config, error) { return &config.Config{ExtraDirs: []string{"", extra}}, nil }
	s.builtinGrep()
	_, resp := s.grep("q=abc")
	require.Equal(s.T(), []grepFile{
		{RootIndex: 0, RelPath: "a.txt", Matches: []grepMatch{{Line: 1, Column: 1, Length: 3, Text: "abc"}}},
		{RootIndex: 2, RelPath: "a.txt", Matches: []grepMatch{{Line: 1, Column: 1, Length: 3, Text: "abc"}}},
	}, resp.Files)
}

func (s *ServerSuite) TestGrep_BuiltinWalkErrors() {
	s.editorChannel()
	s.builtinGrep()
	call := s.sys.Override("WalkDir", mock.Anything, mock.Anything).Return(errors.New("walk failed"))
	call.Run(func(args mock.Arguments) {
		root := args.String(0)
		fn := args.Get(1).(fs.WalkDirFunc)
		bad := errors.New("unreadable")
		require.Equal(s.T(), fs.SkipDir, fn(filepath.Join(root, "d"), fakeDirEntry{name: "d", isDir: true}, bad))
		require.NoError(s.T(), fn(filepath.Join(root, "f"), fakeDirEntry{name: "f"}, bad))
		require.NoError(s.T(), fn(filepath.Join(root, "f"), nil, bad))
		// No info, and info but no content.
		require.NoError(s.T(), fn(filepath.Join(root, "f"), fakeDirEntry{name: "f"}, nil))
		require.NoError(s.T(), fn(filepath.Join(root, "g"), infoDirEntry{name: "g"}, nil))
	})
	s.sys.Override("ReadFile", mock.Anything).Return(nil, os.ErrNotExist)
	s.srv.sys = &realOpenSys{s.sys}

	code, resp := s.grep("q=abc")
	require.Equal(s.T(), http.StatusOK, code)
	require.Empty(s.T(), resp.Files)
}

func (s *ServerSuite) TestGrep_Caps() {
	dir := s.editorChannel()
	s.builtinGrep()
	many := strings.Repeat("hit\n", grepFileMatches+5)
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "many.txt"), []byte(many), 0o644))
	long := "a" + strings.Repeat("é", grepMaxLine) + "hit"
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "long.txt"), []byte(long), 0o644))

	_, resp := s.grep("q=hit")
	require.True(s.T(), resp.Truncated)
	require.Equal(s.T(), "long.txt", resp.Files[0].RelPath)
	require.Equal(s.T(), "a"+strings.Repeat("é", grepMaxLine/2-1)+"…", resp.Files[0].Matches[0].Text, "cut on a character boundary")
	require.Equal(s.T(), grepMaxLine+2, resp.Files[0].Matches[0].Column)
	require.Len(s.T(), resp.Files[1].Matches, grepFileMatches)
}

func (s *ServerSuite) TestGrep_BuiltinStopsWhenFull() {
	dir := s.editorChannel()
	s.builtinGrep()
	for i := range grepMaxFiles + 1 {
		require.NoError(s.T(), os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%03d.txt", i)), []byte("hit"), 0o644))
	}
	_, resp := s.grep("q=hit")
	require.True(s.T(), resp.Truncated)
	require.Len(s.T(), resp.Files, grepMaxFiles)
}

func (s *ServerSuite) TestGrepCollector() {
	c := &grepCollector{}
	for i := range grepMaxFiles {
		require.NoError(s.T(), c.add(0, fmt.Sprint(i), grepMatch{Line: 1}))
	}
	require.ErrorIs(s.T(), c.add(0, "one more", grepMatch{}), errGrepFull)
	require.True(s.T(), c.truncated)

	c = &grepCollector{matches: grepMaxMatches}
	require.ErrorIs(s.T(), c.add(0, "a", grepMatch{}), errGrepFull)

	require.Equal(s.T(), 1, column("abc", -1))
	require.Equal(s.T(), 4, column("abc", 10))
}

// rgOutput is rg --json output for the ripgrep tests.
const rgOutput = `{"type":"begin","data":{"path":{"text":"./b.go"}}}
not json
{"type":"match","data":{"path":{"text":"./b.go"},"lines":{"text":"x := héllo\n"},"line_number":3,"submatches":[{"start":8,"end":11}]}}
{"type":"match","data":{"path":{"text":"./a.go"},"lines":{"text":"hello\n"},"line_number":1,"submatches":[]}}
{"type":"end","data":{}}
`

func (s *ServerSuite) TestGrep_Ripgrep() {
	dir := s.editorChannel()
	var gotDir, gotName string
	var gotArgs []string
	s.srv.editorCmds = editorCommands{
		lookPath: func(name string) (string, error) { return "/bin/" + name, nil },
		stream: func(_ context.Context, d, name string, args []string, read func(io.Reader) error) error {
			gotDir, gotName, gotArgs = d, name, args
			return read(strings.NewReader(rgOutput))
		},
	}

	code, resp := s.grep("q=h.llo&regex=1&case=1")
	require.Equal(s.T(), http.StatusOK, code)
	require.Equal(s.T(), "ripgrep", resp.Engine)
	require.Equal(s.T(), dir, gotDir)
	require.Equal(s.T(), "/bin/rg", gotName)
	require.Contains(s.T(), gotArgs, "--case-sensitive")
	require.NotContains(s.T(), gotArgs, "--fixed-strings")
	require.Contains(s.T(), gotArgs, "!node_modules/")
	require.Equal(s.T(), []string{"--regexp", "h.llo", "--", "."}, gotArgs[len(gotArgs)-4:])
	require.Equal(s.T(), []grepFile{
		{RootIndex: 0, RelPath: "a.go", Matches: []grepMatch{{Line: 1, Column: 1, Text: "hello"}}},
		{RootIndex: 0, RelPath: "b.go", Matches: []grepMatch{{Line: 3, Column: 8, Length: 3, Text: "x := héllo"}}},
	}, resp.Files)

	_, _ = s.grep("q=hello")
	require.Contains(s.T(), gotArgs, "--fixed-strings")
	require.Contains(s.T(), gotArgs, "--ignore-case")
	require.NotContains(s.T(), gotArgs, "!*.min.js")

	s.editorGlobal(config.EditorConfig{SearchExclude: []string{"*.min.{js,css}"}})
	_, _ = s.grep("q=hello")
	require.Contains(s.T(), gotArgs, "!*.min.js")
	require.Contains(s.T(), gotArgs, "!*.min.css")
}

func (s *ServerSuite) TestGrep_RipgrepStops() {
	s.editorChannel()
	s.srv.configs.load = func() (*config.Config, error) { return &config.Config{ExtraDirs: []string{s.T().TempDir()}}, nil }
	roots := 0
	s.srv.editorCmds = editorCommands{
		lookPath: func(name string) (string, error) { return name, nil },
		stream: func(_ context.Context, _, _ string, _ []string, read func(io.Reader) error) error {
			roots++
			var b strings.Builder
			for i := range grepMaxFiles + 1 {
				fmt.Fprintf(&b, `{"type":"match","data":{"path":{"text":"f%d"},"lines":{"text":"x"},"line_number":1}}`+"\n", i)
			}
			return read(strings.NewReader(b.String()))
		},
	}
	_, resp := s.grep("q=xx")
	require.True(s.T(), resp.Truncated)
	require.Len(s.T(), resp.Files, grepMaxFiles)
	require.Equal(s.T(), 1, roots, "a full search doesn't search the next root")

	// A failing search is logged, and the next root still searched.
	roots = 0
	s.srv.editorCmds.stream = func(_ context.Context, _, _ string, _ []string, read func(io.Reader) error) error {
		roots++
		return read(iotest{})
	}
	code, resp := s.grep("q=xx")
	require.Equal(s.T(), http.StatusOK, code)
	require.Empty(s.T(), resp.Files)
	require.Equal(s.T(), 2, roots)
}

// iotest is a reader that fails.
type iotest struct{}

func (iotest) Read([]byte) (int, error) { return 0, errors.New("read failed") }

// infoDirEntry is a regular file entry whose Info succeeds.
type infoDirEntry struct{ name string }

func (e infoDirEntry) Name() string               { return e.name }
func (e infoDirEntry) IsDir() bool                { return false }
func (e infoDirEntry) Type() fs.FileMode          { return 0 }
func (e infoDirEntry) Info() (fs.FileInfo, error) { return fakeFileInfo{name: e.name}, nil }
