package learn

import (
	"fmt"
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
				Proposals: []*db.LearnProposal{
					{Kind: db.LearnKindBashShortcut, Title: "Add a vitest shortcut", Payload: `{"name":"vitest"}`, Status: db.LearnPending},
					{Kind: db.LearnKindDescription, Title: "Describe the thread", Payload: "not json", Status: db.LearnFailed},
					{Kind: db.LearnKindRename, Title: "Rename to wt-auth", Payload: `{"name":"wt-auth"}`, Status: db.LearnDismissed},
					{Kind: db.LearnKindMount, Title: "Mount the cache", Payload: `{"mount":"~/.cache"}`, Status: db.LearnApplied},
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
				"### Proposals waiting for the user\n\n```json",
				`"title": "Add a vitest shortcut"`,
				`"name": "vitest"`,
				`"payload": "not json"`,
				"### Proposals the user dismissed\n\n```json",
				`"title": "Rename to wt-auth"`,
			},
			excludes: []string{"Mount the cache"},
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
				"### Proposals waiting for the user\n\nnone\n",
				"### Proposals the user dismissed\n\nnone\n",
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

func (s *PromptSuite) TestDismissedCapped() {
	var proposals []*db.LearnProposal
	for i := range maxDismissed + 5 {
		proposals = append(proposals, &db.LearnProposal{Kind: db.LearnKindRename, Title: fmt.Sprintf("dismissed-%02d", i), Payload: "{}", Status: db.LearnDismissed})
	}
	waiting, dismissed := proposalSummaries(proposals)
	require.Empty(s.T(), waiting)
	require.Len(s.T(), dismissed, maxDismissed)
	require.Equal(s.T(), "dismissed-00", dismissed[0].Title)
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

func (s *PromptSuite) TestIsTrigger() {
	tests := []struct {
		name   string
		prompt string
		want   bool
	}{
		{"bare", TriggerMessage("api", "fix it"), true},
		{"behind the author prefix", "loop: " + TriggerMessage("a: b\nc", ""), true},
		{"name with a colon, no prefix", TriggerMessage("a: b", ""), true},
		{"a user prompt", "radu: fix the tests", false},
		{"quoting the lead only", "radu: The run in \"api\" was slow", false},
		{"empty", "", false},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, IsTrigger(tc.prompt))
		})
	}
}
