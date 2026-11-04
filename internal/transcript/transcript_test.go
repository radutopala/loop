package transcript

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/osutil"
)

type TranscriptSuite struct {
	suite.Suite
}

func TestTranscriptSuite(t *testing.T) {
	suite.Run(t, new(TranscriptSuite))
}

// session is a transcript with two turns: a prompt answered by two replies,
// then a second prompt answered by one.
var session = []string{
	`{"type":"queue-operation"}`,
	`{"type":"user","uuid":"u-1","parentUuid":null,"message":{"role":"user","content":"first prompt"}}`,
	`{"type":"assistant","uuid":"a-1","parentUuid":"u-1","message":{"content":[{"type":"text","text":"looking"}]}}`,
	`{"type":"user","uuid":"r-1","parentUuid":"a-1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`,
	`{"type":"assistant","uuid":"a-2","parentUuid":"r-1","message":{"content":[{"type":"text","text":"done"}]}}`,
	`{"type":"user","uuid":"u-2","parentUuid":"a-2","message":{"role":"user","content":"second prompt"}}`,
	`{"type":"attachment","uuid":"x-1","parentUuid":"u-2"}`,
	`{"type":"assistant","uuid":"a-3","parentUuid":"x-1","message":{"content":[{"type":"text","text":"again"}]}}`,
}

func parse(lines ...string) []Entry {
	entries, _ := Parse([]byte(strings.Join(lines, "\n")))
	return entries
}

// seekFailer fails the seek numbered failAt (from 1).
type seekFailer struct {
	io.ReadSeeker
	failAt, seeks int
}

func (f *seekFailer) Seek(offset int64, whence int) (int64, error) {
	f.seeks++
	if f.seeks == f.failAt {
		return 0, errors.New("seek failed")
	}
	return f.ReadSeeker.Seek(offset, whence)
}

// shortReader's file shrinks after its size was taken.
type shortReader struct{ io.ReadSeeker }

func (shortReader) Read([]byte) (int, error) { return 0, io.EOF }

func (s *TranscriptSuite) TestReadTail() {
	path := filepath.Join(s.T().TempDir(), "t.jsonl")
	require.NoError(s.T(), os.WriteFile(path, []byte("0123456789"), 0o600))
	tests := []struct {
		name      string
		n         int64
		want      string
		wantWhole bool
	}{
		{name: "the end", n: 4, want: "6789"},
		{name: "exactly all", n: 10, want: "0123456789", wantWhole: true},
		{name: "more than all", n: 11, want: "0123456789", wantWhole: true},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			got, whole, err := ReadTail(path, tc.n)
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.want, string(got))
			require.Equal(s.T(), tc.wantWhole, whole)
		})
	}
}

func (s *TranscriptSuite) TestReadTailErrors() {
	file := strings.NewReader("0123456789")
	_, _, err := ReadTail(filepath.Join(s.T().TempDir(), "missing.jsonl"), 4)
	require.ErrorIs(s.T(), err, os.ErrNotExist)
	for _, failAt := range []int{1, 2} {
		_, _, err := readTail(&seekFailer{ReadSeeker: file, failAt: failAt}, 4)
		require.ErrorContains(s.T(), err, "seek failed")
	}
	_, _, err = readTail(shortReader{file}, 4)
	require.ErrorIs(s.T(), err, io.EOF)
}

func (s *TranscriptSuite) TestParse() {
	entries, lines := Parse([]byte("\n" + session[1] + "\nnot json\n" + session[2] + "\n"))
	require.Equal(s.T(), 5, lines)
	require.Len(s.T(), entries, 2)
	require.Equal(s.T(), []int{1, 3}, []int{entries[0].Line, entries[1].Line})
	require.Equal(s.T(), []string{"u-1", "a-1"}, []string{entries[0].UUID, entries[1].UUID})
	require.Equal(s.T(), "u-1", entries[1].ParentUUID)
}

func (s *TranscriptSuite) TestPrompt() {
	entries := parse(session...)
	tests := []struct {
		name   string
		entry  Entry
		want   string
		wantOK bool
	}{
		{name: "prompt", entry: entries[1], want: "first prompt", wantOK: true},
		{name: "tool result", entry: entries[3]},
		{name: "assistant", entry: entries[2]},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			got, ok := tc.entry.Prompt()
			require.Equal(s.T(), tc.wantOK, ok)
			require.Equal(s.T(), tc.want, got)
		})
	}
}

func (s *TranscriptSuite) TestPath() {
	tests := []struct {
		name      string
		sessionID string
		want      string
		wantErr   bool
	}{
		{name: "session", sessionID: "sess-1", want: filepath.Join("/home", ".claude", "projects", osutil.EncodeClaudeProjectPath("/proj"), "sess-1.jsonl")},
		{name: "base name only", sessionID: "../../sess-1", want: filepath.Join("/home", ".claude", "projects", osutil.EncodeClaudeProjectPath("/proj"), "sess-1.jsonl")},
		{name: "parent dir", sessionID: "..", wantErr: true},
		{name: "empty", sessionID: "", wantErr: true},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			got, err := Path("/home", "/proj", tc.sessionID)
			if tc.wantErr {
				require.Error(s.T(), err)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.want, got)
		})
	}
}

func (s *TranscriptSuite) TestPromptOf() {
	tests := []struct {
		name    string
		entries []Entry
		uuid    string
		want    string
		wantOK  bool
	}{
		{name: "later prompt", entries: parse(session...), uuid: "a-3", want: "u-2", wantOK: true},
		{name: "past a tool result", entries: parse(session...), uuid: "a-2", want: "u-1", wantOK: true},
		{name: "the prompt itself", entries: parse(session...), uuid: "u-2", want: "u-2", wantOK: true},
		{name: "unknown entry", entries: parse(session...), uuid: "a-9"},
		{name: "chain breaks", entries: parse(`{"type":"assistant","uuid":"a-1","parentUuid":"gone"}`), uuid: "a-1"},
		{
			name: "loop",
			entries: parse(
				`{"type":"assistant","uuid":"a-1","parentUuid":"a-2"}`,
				`{"type":"assistant","uuid":"a-2","parentUuid":"a-1"}`,
			),
			uuid: "a-1",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			got, ok := PromptOf(tc.entries, tc.uuid)
			require.Equal(s.T(), tc.wantOK, ok)
			require.Equal(s.T(), tc.want, got.UUID)
		})
	}
}
