package slack

import (
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/bot"
)

// --- parseShortcut ---

func (s *BotSuite) TestParseShortcut() {
	tests := []struct {
		name       string
		args       []string
		wantCmd    string
		wantOpts   map[string]string
		wantErrSub string
	}{
		{
			name:     "valid_name",
			args:     []string{"my-shortcut"},
			wantCmd:  "shortcut",
			wantOpts: map[string]string{"name": "my-shortcut"},
		},
		{
			name:     "extra_args_uses_first",
			args:     []string{"deploy-prod", "ignored"},
			wantCmd:  "shortcut",
			wantOpts: map[string]string{"name": "deploy-prod"},
		},
		{
			name:       "no_args",
			args:       []string{},
			wantErrSub: "Usage:",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			inter := &bot.Interaction{
				ChannelID: "C123",
				GuildID:   "T123",
				Options:   make(map[string]string),
			}
			result, errText := parseShortcut(inter, tt.args)
			if tt.wantErrSub != "" {
				require.Contains(s.T(), errText, tt.wantErrSub)
				require.Nil(s.T(), result)
				return
			}
			require.Empty(s.T(), errText)
			require.NotNil(s.T(), result)
			require.Equal(s.T(), tt.wantCmd, result.CommandName)
			for k, v := range tt.wantOpts {
				require.Equal(s.T(), v, result.Options[k], "option %s", k)
			}
		})
	}
}

// --- parseWorkflow ---

func (s *BotSuite) TestParseWorkflow() {
	tests := []struct {
		name       string
		args       []string
		wantCmd    string
		wantOpts   map[string]string
		wantErrSub string
	}{
		{
			name:    "list",
			args:    []string{"list"},
			wantCmd: "workflows",
		},
		{
			name:    "runs",
			args:    []string{"runs"},
			wantCmd: "workflow-runs",
		},
		{
			name:     "run_with_name",
			args:     []string{"run", "my-flow"},
			wantCmd:  "workflow-run",
			wantOpts: map[string]string{"name": "my-flow"},
		},
		{
			name:       "run_no_name",
			args:       []string{"run"},
			wantErrSub: "Usage:",
		},
		{
			name:     "cancel_with_id",
			args:     []string{"cancel", "run-99"},
			wantCmd:  "workflow-cancel",
			wantOpts: map[string]string{"run_id": "run-99"},
		},
		{
			name:       "cancel_no_id",
			args:       []string{"cancel"},
			wantErrSub: "Usage:",
		},
		{
			name:     "delete_with_id",
			args:     []string{"delete", "run-42"},
			wantCmd:  "workflow-delete",
			wantOpts: map[string]string{"run_id": "run-42"},
		},
		{
			name:       "delete_no_id",
			args:       []string{"delete"},
			wantErrSub: "Usage:",
		},
		{
			name:     "retry_with_id",
			args:     []string{"retry", "run-7"},
			wantCmd:  "workflow-retry",
			wantOpts: map[string]string{"run_id": "run-7"},
		},
		{
			name:       "retry_no_id",
			args:       []string{"retry"},
			wantErrSub: "Usage:",
		},
		{
			name:       "no_args",
			args:       []string{},
			wantErrSub: "Usage:",
		},
		{
			name:       "unknown_subcommand",
			args:       []string{"pause"},
			wantErrSub: "Usage:",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			inter := &bot.Interaction{
				ChannelID: "C123",
				GuildID:   "T123",
				Options:   make(map[string]string),
			}
			result, errText := parseWorkflow(inter, tt.args)
			if tt.wantErrSub != "" {
				require.Contains(s.T(), errText, tt.wantErrSub)
				require.Nil(s.T(), result)
				return
			}
			require.Empty(s.T(), errText)
			require.NotNil(s.T(), result)
			require.Equal(s.T(), tt.wantCmd, result.CommandName)
			for k, v := range tt.wantOpts {
				require.Equal(s.T(), v, result.Options[k], "option %s", k)
			}
		})
	}
}
