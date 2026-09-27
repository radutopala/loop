package learn

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/types"
)

type PromptSuite struct {
	suite.Suite
}

func TestPromptSuite(t *testing.T) {
	suite.Run(t, new(PromptSuite))
}

// TestEveryKindDocumented makes sure each proposal kind has its own section
// in the prompt, so adding a kind without teaching the agent fails here.
func (s *PromptSuite) TestEveryKindDocumented() {
	require.Len(s.T(), Kinds, 8)
	for _, kind := range Kinds {
		require.Contains(s.T(), basePrompt, "\n### "+kind+"\n", kind)
	}
}

func (s *PromptSuite) TestSystemPrompt() {
	cfg := &config.Config{
		PromptShortcuts: []config.PromptShortcut{{Name: "fix-tests", Prompt: "make test"}},
		BashShortcuts:   []config.BashShortcut{{Name: "lint", Command: "make lint"}},
		TaskTemplates:   []config.TaskTemplate{{Name: "nightly", Schedule: "0 2 * * *", Type: "cron"}},
		Mounts:          []string{"~/.ssh:~/.ssh:ro"},
		Learn:           config.LearnConfig{Prompt: "  Prefer bash shortcuts.  "},
	}
	cfg.Gates.Agentgate.CommandRules = []types.CommandRule{{Commands: []string{"git"}, Decision: types.DecisionApprove}}
	cfg.Gates.Agentgate.FileRules = []types.FileRule{{Paths: []string{"**/.env"}, Decision: types.DecisionDeny}}

	tests := []struct {
		name     string
		state    State
		contains []string
		excludes []string
	}{
		{
			name: "full state",
			state: State{
				ChannelName: "wt-login",
				Description: "login bug",
				TicketURL:   "https://tracker.example.com/T-1",
				Worktree:    true,
				ProjectDir:  "/project",
				Config:      cfg,
				Tasks: []*db.ScheduledTask{
					{Type: db.TaskTypeCron, Schedule: "0 9 * * *", Prompt: "check deps", Enabled: true},
				},
			},
			contains: []string{
				`- Channel: "wt-login" (a worktree thread)`,
				`- Description: "login bug"`,
				"- Ticket URL: https://tracker.example.com/T-1\n",
				"- Project config: `/project/.loop/config.json`",
				`"name": "fix-tests"`,
				`"command": "make lint"`,
				`"prompt": "check deps"`,
				`"name": "nightly"`,
				`"commands": [`,
				`"**/.env"`,
				`"~/.ssh:~/.ssh:ro"`,
				"### Agentgate path rules\n\nnone\n",
				"## Additional instructions\n\nPrefer bash shortcuts.\n",
			},
		},
		{
			name:  "empty state",
			state: State{ChannelName: "general"},
			contains: []string{
				`- Channel: "general"` + "\n",
				"- Description: none",
				"- Ticket URL: none",
				"### Prompt shortcuts\n\nnone\n",
				"### Mounts\n\nnone\n",
			},
			excludes: []string{"(a worktree thread)", "- Project config", "## Additional instructions"},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			got := SystemPrompt(tc.state)
			require.True(s.T(), strings.HasPrefix(got, basePrompt))
			for _, want := range tc.contains {
				require.Contains(s.T(), got, want)
			}
			for _, not := range tc.excludes {
				require.NotContains(s.T(), got, not)
			}
		})
	}
}

func (s *PromptSuite) TestTriggerMessage() {
	tests := []struct {
		name       string
		lastPrompt string
		want       string
	}{
		{
			name:       "with prompt",
			lastPrompt: " fix the tests\nthen lint ",
			want:       "The run in \"api\" just finished. Review it and propose what Loop should learn from it.\n\nIts last prompt was:\n\n> fix the tests\n> then lint",
		},
		{
			name: "without prompt",
			want: "The run in \"api\" just finished. Review it and propose what Loop should learn from it.",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, TriggerMessage("api", tc.lastPrompt))
		})
	}
}
