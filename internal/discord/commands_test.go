package discord

import (
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type CommandsSuite struct {
	suite.Suite
}

func TestCommandsSuite(t *testing.T) {
	suite.Run(t, new(CommandsSuite))
}

func (s *CommandsSuite) TestCommandsReturnsNonEmpty() {
	cmds := Commands()
	require.NotEmpty(s.T(), cmds)
}

func (s *CommandsSuite) TestRootCommandName() {
	cmds := Commands()
	require.Equal(s.T(), "loop", cmds[0].Name)
	require.NotEmpty(s.T(), cmds[0].Description)
}

func (s *CommandsSuite) TestSubcommands() {
	cmds := Commands()
	root := cmds[0]

	expected := map[string]bool{
		"ask":      true,
		"schedule": true,
		"tasks":    true,
		"cancel":   true,
		"toggle":   true,
		"edit":     true,
		"status":   true,
	}

	require.Len(s.T(), root.Options, len(expected))

	for _, opt := range root.Options {
		require.Equal(s.T(), discordgo.ApplicationCommandOptionSubCommand, opt.Type, "option %s should be subcommand", opt.Name)
		require.True(s.T(), expected[opt.Name], "unexpected subcommand: %s", opt.Name)
		require.NotEmpty(s.T(), opt.Description, "subcommand %s should have description", opt.Name)
	}
}

func (s *CommandsSuite) TestAskSubcommand() {
	cmds := Commands()
	ask := findSubcommand(cmds[0], "ask")
	require.NotNil(s.T(), ask)
	require.Len(s.T(), ask.Options, 1)
	require.Equal(s.T(), "prompt", ask.Options[0].Name)
	require.Equal(s.T(), discordgo.ApplicationCommandOptionString, ask.Options[0].Type)
	require.True(s.T(), ask.Options[0].Required)
}

func (s *CommandsSuite) TestScheduleSubcommand() {
	cmds := Commands()
	sched := findSubcommand(cmds[0], "schedule")
	require.NotNil(s.T(), sched)
	require.Len(s.T(), sched.Options, 3)

	optNames := map[string]*discordgo.ApplicationCommandOption{}
	for _, o := range sched.Options {
		optNames[o.Name] = o
	}

	require.Contains(s.T(), optNames, "schedule")
	require.Equal(s.T(), discordgo.ApplicationCommandOptionString, optNames["schedule"].Type)
	require.True(s.T(), optNames["schedule"].Required)

	require.Contains(s.T(), optNames, "prompt")
	require.Equal(s.T(), discordgo.ApplicationCommandOptionString, optNames["prompt"].Type)
	require.True(s.T(), optNames["prompt"].Required)

	require.Contains(s.T(), optNames, "type")
	require.Equal(s.T(), discordgo.ApplicationCommandOptionString, optNames["type"].Type)
	require.True(s.T(), optNames["type"].Required)
	require.Len(s.T(), optNames["type"].Choices, 3)

	choiceNames := make([]string, 0, 3)
	for _, c := range optNames["type"].Choices {
		choiceNames = append(choiceNames, c.Name)
	}
	require.ElementsMatch(s.T(), []string{"cron", "interval", "once"}, choiceNames)
}

func (s *CommandsSuite) TestCancelSubcommand() {
	cmds := Commands()
	cancel := findSubcommand(cmds[0], "cancel")
	require.NotNil(s.T(), cancel)
	require.Len(s.T(), cancel.Options, 1)
	require.Equal(s.T(), "task_id", cancel.Options[0].Name)
	require.Equal(s.T(), discordgo.ApplicationCommandOptionInteger, cancel.Options[0].Type)
	require.True(s.T(), cancel.Options[0].Required)
}

func (s *CommandsSuite) TestToggleSubcommand() {
	cmds := Commands()
	toggle := findSubcommand(cmds[0], "toggle")
	require.NotNil(s.T(), toggle)
	require.Len(s.T(), toggle.Options, 1)
	require.Equal(s.T(), "task_id", toggle.Options[0].Name)
	require.Equal(s.T(), discordgo.ApplicationCommandOptionInteger, toggle.Options[0].Type)
	require.True(s.T(), toggle.Options[0].Required)
}

func (s *CommandsSuite) TestEditSubcommand() {
	cmds := Commands()
	edit := findSubcommand(cmds[0], "edit")
	require.NotNil(s.T(), edit)
	require.Len(s.T(), edit.Options, 4)

	optNames := map[string]*discordgo.ApplicationCommandOption{}
	for _, o := range edit.Options {
		optNames[o.Name] = o
	}

	require.Contains(s.T(), optNames, "task_id")
	require.Equal(s.T(), discordgo.ApplicationCommandOptionInteger, optNames["task_id"].Type)
	require.True(s.T(), optNames["task_id"].Required)

	require.Contains(s.T(), optNames, "schedule")
	require.Equal(s.T(), discordgo.ApplicationCommandOptionString, optNames["schedule"].Type)
	require.False(s.T(), optNames["schedule"].Required)

	require.Contains(s.T(), optNames, "type")
	require.Equal(s.T(), discordgo.ApplicationCommandOptionString, optNames["type"].Type)
	require.False(s.T(), optNames["type"].Required)
	require.Len(s.T(), optNames["type"].Choices, 3)

	require.Contains(s.T(), optNames, "prompt")
	require.Equal(s.T(), discordgo.ApplicationCommandOptionString, optNames["prompt"].Type)
	require.False(s.T(), optNames["prompt"].Required)
}

func (s *CommandsSuite) TestTasksStatusHaveNoOptions() {
	cmds := Commands()
	for _, name := range []string{"tasks", "status"} {
		sub := findSubcommand(cmds[0], name)
		require.NotNil(s.T(), sub, "subcommand %s should exist", name)
		require.Empty(s.T(), sub.Options, "subcommand %s should have no options", name)
	}
}

func findSubcommand(root *discordgo.ApplicationCommand, name string) *discordgo.ApplicationCommandOption {
	for _, opt := range root.Options {
		if opt.Name == name {
			return opt
		}
	}
	return nil
}
