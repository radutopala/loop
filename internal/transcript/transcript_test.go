package transcript

import (
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
