package promptedit

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type EditSuite struct{ suite.Suite }

func TestEditSuite(t *testing.T) { suite.Run(t, new(EditSuite)) }

func (s *EditSuite) TestEmptyInstruction() {
	_, err := Prepare(Request{Draft: "d", Instruction: "  \n"})
	require.ErrorIs(s.T(), err, ErrNoInstruction)
}

func (s *EditSuite) TestSystemPrompt() {
	require.NotEmpty(s.T(), SystemPrompt())
	require.Equal(s.T(), systemPrompt, SystemPrompt())
}

func (s *EditSuite) TestPrepareAndApply() {
	tests := []struct {
		name       string
		draft      string
		start, end int
		reply      string
		prompt     string
		want       Result
	}{
		{
			name:   "empty draft",
			reply:  "Write a test.\n",
			prompt: "The draft is empty: write the prompt the instruction describes.\n\n<instruction>\ni\n</instruction>",
			want:   Result{Text: "Write a test.", From: 0, To: 13},
		},
		{
			name:   "whole draft",
			draft:  "fix it",
			reply:  "```\nFix the bug.\n```",
			prompt: "<draft>\nfix it\n</draft>\n\nRewrite the whole draft.\n\n<instruction>\ni\n</instruction>",
			want:   Result{Text: "Fix the bug.", From: 0, To: 12},
		},
		{
			name:   "selection, UTF-16 offsets",
			draft:  "😀 one two",
			start:  3,
			end:    6,
			reply:  "1",
			prompt: "<draft>\n😀 one two\n</draft>\n\n<selection>\none\n</selection>\n\nRewrite only the selection; the rest of the draft stays as it is.\n\n<instruction>\ni\n</instruction>",
			want:   Result{Text: "😀 1 two", From: 3, To: 4},
		},
		{
			name:   "reversed out-of-range selection clamps to empty",
			draft:  "abc",
			start:  9,
			end:    -1,
			reply:  "x",
			prompt: "<draft>\nabc\n</draft>\n\nRewrite the whole draft.\n\n<instruction>\ni\n</instruction>",
			want:   Result{Text: "x", From: 0, To: 1},
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			e, err := Prepare(Request{Draft: tt.draft, Start: tt.start, End: tt.end, Instruction: " i "})
			require.NoError(s.T(), err)
			require.Equal(s.T(), tt.prompt, e.Prompt)
			require.Equal(s.T(), tt.want, e.Apply(tt.reply))
		})
	}
}

func (s *EditSuite) TestCleanReply() {
	tests := []struct{ in, want string }{
		{" plain ", "plain"},
		{"```md\nbody\n```", "body"},
		{"```one line```", "```one line```"},
		{"``````", "``````"},
		{"```", "```"},
		{"```a\nb", "```a\nb"},
		{"text ```x```", "text ```x```"},
	}
	for _, tt := range tests {
		require.Equal(s.T(), tt.want, cleanReply(tt.in), tt.in)
	}
}
