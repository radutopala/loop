package api

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type CommandsSuite struct {
	suite.Suite
}

func TestCommandsSuite(t *testing.T) {
	suite.Run(t, new(CommandsSuite))
}

func (s *CommandsSuite) TestTokenize() {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"simple", "tasks", []string{"tasks"}},
		{"two words", "cancel 5", []string{"cancel", "5"}},
		{"key=value", `schedule type=cron`, []string{"schedule", "type=cron"}},
		{"quoted value", `schedule prompt="hello world"`, []string{"schedule", "prompt=hello world"}},
		{"single quoted", `schedule prompt='hello world'`, []string{"schedule", "prompt=hello world"}},
		{"multiple key=value", `edit 3 schedule="0 9 * * *" prompt="new prompt"`,
			[]string{"edit", "3", "schedule=0 9 * * *", "prompt=new prompt"}},
		{"extra spaces", "  tasks  ", []string{"tasks"}},
		{"empty", "", nil},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			got := tokenize(tc.input)
			require.Equal(s.T(), tc.want, got)
		})
	}
}

func (s *CommandsSuite) TestParseCommand() {
	tests := []struct {
		name    string
		command string
		wantCmd string
		wantOpt map[string]string
		wantNil bool
	}{
		{
			name: "tasks", command: "tasks",
			wantCmd: "tasks", wantOpt: map[string]string{},
		},
		{
			name: "status", command: "status",
			wantCmd: "status", wantOpt: map[string]string{},
		},
		{
			name: "readme", command: "readme",
			wantCmd: "readme", wantOpt: map[string]string{},
		},
		{
			name: "template-list", command: "template-list",
			wantCmd: "template-list", wantOpt: map[string]string{},
		},
		{
			name: "iamtheowner", command: "iamtheowner",
			wantCmd: "iamtheowner", wantOpt: map[string]string{},
		},
		{
			name: "task with id", command: "task 42",
			wantCmd: "task", wantOpt: map[string]string{"task_id": "42"},
		},
		{
			name: "cancel with id", command: "cancel 7",
			wantCmd: "cancel", wantOpt: map[string]string{"task_id": "7"},
		},
		{
			name: "toggle with id", command: "toggle 3",
			wantCmd: "toggle", wantOpt: map[string]string{"task_id": "3"},
		},
		{
			name: "stop without args", command: "stop",
			wantCmd: "stop", wantOpt: map[string]string{},
		},
		{
			name: "stop with channel", command: "stop ch-5",
			wantCmd: "stop", wantOpt: map[string]string{"channel_id": "ch-5"},
		},
		{
			name: "template-add", command: "template-add my-template",
			wantCmd: "template-add", wantOpt: map[string]string{"name": "my-template"},
		},
		{
			name:    "schedule with options",
			command: `schedule type=cron schedule="0 9 * * *" prompt="check status"`,
			wantCmd: "schedule",
			wantOpt: map[string]string{
				"type":     "cron",
				"schedule": "0 9 * * *",
				"prompt":   "check status",
			},
		},
		{
			name:    "edit with task_id and options",
			command: `edit 5 schedule="*/10 * * * *" prompt="new prompt"`,
			wantCmd: "edit",
			wantOpt: map[string]string{
				"task_id":  "5",
				"schedule": "*/10 * * * *",
				"prompt":   "new prompt",
			},
		},
		{
			name: "allow_user with role", command: "allow_user user-1 owner",
			wantCmd: "allow_user", wantOpt: map[string]string{"target_id": "user-1", "role": "owner"},
		},
		{
			name: "allow_user without role", command: "allow_user user-1",
			wantCmd: "allow_user", wantOpt: map[string]string{"target_id": "user-1"},
		},
		{
			name: "deny_user", command: "deny_user user-1",
			wantCmd: "deny_user", wantOpt: map[string]string{"target_id": "user-1"},
		},
		{
			name: "unknown command", command: "foobar",
			wantNil: true,
		},
		{
			name: "empty", command: "",
			wantNil: true,
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			got := parseCommand(tc.command, "ch-1", "user-1")
			if tc.wantNil {
				require.Nil(s.T(), got)
				return
			}
			require.NotNil(s.T(), got)
			require.Equal(s.T(), tc.wantCmd, got.CommandName)
			require.Equal(s.T(), "ch-1", got.ChannelID)
			require.Equal(s.T(), "user-1", got.AuthorID)
			require.Equal(s.T(), tc.wantOpt, got.Options)
		})
	}
}
