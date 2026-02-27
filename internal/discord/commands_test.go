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

	expected := map[string]discordgo.ApplicationCommandOptionType{
		"schedule":    discordgo.ApplicationCommandOptionSubCommand,
		"tasks":       discordgo.ApplicationCommandOptionSubCommand,
		"task":        discordgo.ApplicationCommandOptionSubCommand,
		"cancel":      discordgo.ApplicationCommandOptionSubCommand,
		"toggle":      discordgo.ApplicationCommandOptionSubCommand,
		"edit":        discordgo.ApplicationCommandOptionSubCommand,
		"status":      discordgo.ApplicationCommandOptionSubCommand,
		"stop":        discordgo.ApplicationCommandOptionSubCommand,
		"readme":      discordgo.ApplicationCommandOptionSubCommand,
		"template":    discordgo.ApplicationCommandOptionSubCommandGroup,
		"allow_user":  discordgo.ApplicationCommandOptionSubCommand,
		"allow_role":  discordgo.ApplicationCommandOptionSubCommand,
		"deny_user":   discordgo.ApplicationCommandOptionSubCommand,
		"deny_role":   discordgo.ApplicationCommandOptionSubCommand,
		"iamtheowner": discordgo.ApplicationCommandOptionSubCommand,
	}

	require.Len(s.T(), root.Options, len(expected))

	for _, opt := range root.Options {
		expectedType, exists := expected[opt.Name]
		require.True(s.T(), exists, "unexpected option: %s", opt.Name)
		require.Equal(s.T(), expectedType, opt.Type, "option %s has wrong type", opt.Name)
		require.NotEmpty(s.T(), opt.Description, "option %s should have description", opt.Name)
	}
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
	for _, name := range []string{"tasks", "status", "stop", "readme"} {
		sub := findSubcommand(cmds[0], name)
		require.NotNil(s.T(), sub, "subcommand %s should exist", name)
		require.Empty(s.T(), sub.Options, "subcommand %s should have no options", name)
	}
}

func (s *CommandsSuite) TestTemplateSubcommandGroup() {
	cmds := Commands()
	root := cmds[0]

	var templateGroup *discordgo.ApplicationCommandOption
	for _, opt := range root.Options {
		if opt.Name == "template" {
			templateGroup = opt
			break
		}
	}
	require.NotNil(s.T(), templateGroup)
	require.Equal(s.T(), discordgo.ApplicationCommandOptionSubCommandGroup, templateGroup.Type)
	require.Len(s.T(), templateGroup.Options, 2)

	subNames := map[string]*discordgo.ApplicationCommandOption{}
	for _, sub := range templateGroup.Options {
		subNames[sub.Name] = sub
		require.Equal(s.T(), discordgo.ApplicationCommandOptionSubCommand, sub.Type)
	}

	require.Contains(s.T(), subNames, "add")
	require.Contains(s.T(), subNames, "list")

	// "add" should have a required "name" option
	require.Len(s.T(), subNames["add"].Options, 1)
	require.Equal(s.T(), "name", subNames["add"].Options[0].Name)
	require.Equal(s.T(), discordgo.ApplicationCommandOptionString, subNames["add"].Options[0].Type)
	require.True(s.T(), subNames["add"].Options[0].Required)

	// "list" should have no options
	require.Empty(s.T(), subNames["list"].Options)
}

func (s *CommandsSuite) TestIamtheownerSubcommand() {
	cmds := Commands()
	sub := findSubcommand(cmds[0], "iamtheowner")
	require.NotNil(s.T(), sub)
	require.Empty(s.T(), sub.Options)
}

func findSubcommand(root *discordgo.ApplicationCommand, name string) *discordgo.ApplicationCommandOption {
	for _, opt := range root.Options {
		if opt.Name == name {
			return opt
		}
	}
	return nil
}
