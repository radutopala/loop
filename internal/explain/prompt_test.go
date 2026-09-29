package explain

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/types"
)

type PromptSuite struct {
	suite.Suite
}

func TestPromptSuite(t *testing.T) {
	suite.Run(t, new(PromptSuite))
}

// TestSectionsDocumented makes sure the prompt asks for every part of an
// explanation the engineer relies on.
func (s *PromptSuite) TestSectionsDocumented() {
	for _, section := range []string{"Summary", "What changed", "Commands and results", "Decisions", "Risks and gaps", "How to review", "Follow-ups"} {
		require.Contains(s.T(), basePrompt, "\n### "+section+"\n", section)
	}
}

func (s *PromptSuite) TestSystemPrompt() {
	tests := []struct {
		name  string
		extra string
		want  string
	}{
		{"no extra", "  ", basePrompt},
		{"extra", "  Keep it short.  ", basePrompt + "\n## Additional instructions\n\nKeep it short.\n"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, SystemPrompt(tc.extra))
		})
	}
}

func (s *PromptSuite) TestTriggerMessage() {
	long := strings.Repeat("é", maxQuoted+10)
	tests := []struct {
		name   string
		prompt string
		reply  string
		want   string
	}{
		{
			name:   "prompt and reply",
			prompt: " fix the tests\nthen lint ",
			reply:  "Done.\nAll green.",
			want:   "Explain the turn in \"api\" that ended with the reply below.\n\nIts prompt was:\n\n> fix the tests\n> then lint\n\nIts final reply was:\n\n> Done.\n> All green.",
		},
		{
			name:  "no prompt",
			reply: "Done.",
			want:  "Explain the turn in \"api\" that ended with the reply below.\n\nIts final reply was:\n\n> Done.",
		},
		{
			name:  "long reply cut",
			reply: long,
			want:  "Explain the turn in \"api\" that ended with the reply below.\n\nIts final reply was:\n\n> " + strings.Repeat("é", maxQuoted) + " …",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, TriggerMessage("api", tc.prompt, tc.reply))
		})
	}
}

func (s *PromptSuite) TestIsTrigger() {
	tests := []struct {
		name   string
		prompt string
		want   bool
	}{
		{"bare", TriggerMessage("api", "fix it", "done"), true},
		{"behind the author prefix", "loop: " + TriggerMessage("a: b\nc", "", "done"), true},
		{"behind the worktree hint", dirHint + "/wt. Always use absolute paths.\n\nloop: " + TriggerMessage("wt", "", "done"), true},
		{"name with a colon, no prefix", TriggerMessage("a: b", "", "done"), true},
		{"a user prompt", "radu: explain the tests", false},
		{"a user prompt in a worktree", dirHint + "/wt.\n\nradu: fix it", false},
		{"quoting the lead only", "radu: Explain the turn in \"api\" please", false},
		{"empty", "", false},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, IsTrigger(tc.prompt))
		})
	}
}

func (s *PromptSuite) TestUnavailable() {
	tests := []struct {
		name string
		ch   *db.Channel
		want string
	}{
		{"desktop channel", &db.Channel{Platform: types.PlatformLocal}, ""},
		{"desktop thread", &db.Channel{Platform: types.PlatformLocal, ParentID: "ch-1"}, ""},
		{"slack", &db.Channel{Platform: types.PlatformSlack}, "explain runs only in desktop app channels"},
		{"task thread", &db.Channel{Platform: types.PlatformLocal, TaskID: 3}, "explain doesn't run in task threads"},
		{"learn thread", &db.Channel{Platform: types.PlatformLocal, Kind: db.ChannelKindLearn}, "explain doesn't run in learn or explain threads"},
		{"explain thread", &db.Channel{Platform: types.PlatformLocal, Kind: db.ChannelKindExplain}, "explain doesn't run in learn or explain threads"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, Unavailable(tc.ch))
		})
	}
}
