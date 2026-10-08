package bot

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/events"
)

type ParkCardSuite struct {
	suite.Suite
}

func TestParkCardSuite(t *testing.T) {
	suite.Run(t, new(ParkCardSuite))
}

func (s *ParkCardSuite) TestFormatAskCard() {
	tests := []struct {
		name string
		data events.AskUserQuestionEventData
		bold string
		want string
	}{
		{
			name: "single question with header and descriptions",
			data: events.AskUserQuestionEventData{Questions: []events.AskUserQuestion{{
				Question: "Which one?", Header: "Pick",
				Options: []events.AskUserOption{{Label: "A", Description: "first"}, {Label: "B"}, {Label: "Red", Description: "red"}},
			}}},
			bold: "**",
			want: "**Pick:** Which one?\n1. A - first\n2. B\n3. Red\n\n" + askReplyHint,
		},
		{
			name: "slack bold marker",
			data: events.AskUserQuestionEventData{Questions: []events.AskUserQuestion{{
				Question: "Which one?", Header: "Pick", Options: []events.AskUserOption{{Label: "A"}},
			}}},
			bold: "*",
			want: "*Pick:* Which one?\n1. A\n\n" + askReplyHint,
		},
		{
			name: "several questions, one multi-select",
			data: events.AskUserQuestionEventData{Questions: []events.AskUserQuestion{
				{Question: "Features?", MultiSelect: true, Options: []events.AskUserOption{{Label: "X"}}},
				{Question: "Theme?", Options: []events.AskUserOption{{Label: "Dark"}}},
			}},
			bold: "**",
			want: "Features? (pick one or more)\n1. X\n\nTheme?\n1. Dark\n\n" + askReplyHintMulti,
		},
		{
			name: "single multi-select question",
			data: events.AskUserQuestionEventData{Questions: []events.AskUserQuestion{{
				Question: "Features?", MultiSelect: true, Options: []events.AskUserOption{{Label: "X"}, {Label: "Y"}},
			}}},
			bold: "**",
			want: "Features? (pick one or more)\n1. X\n2. Y\n\n" + askReplyHintMultiSelect,
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, FormatAskCard(tc.data, tc.bold))
		})
	}
}

func (s *ParkCardSuite) TestFormatPlanCard() {
	got := FormatPlanCard(events.ExitPlanModeEventData{Plan: "# Plan\nStep 1"}, "*")
	require.Equal(s.T(), "*Plan ready for review*\n\n# Plan\nStep 1\n\n"+planReplyHint, got)
}

func (s *ParkCardSuite) TestAskCardButtons() {
	skip := CardButton{Label: "Skip", Choice: AskReplySkip}
	many := make([]events.AskUserOption, maxCardButtons)
	for i := range many {
		many[i] = events.AskUserOption{Label: "O" + strconv.Itoa(i)}
	}
	long := strings.Repeat("é", maxCardButtonLabel+5)
	tests := []struct {
		name string
		data events.AskUserQuestionEventData
		want []CardButton
	}{
		{
			name: "single question gets a button per option",
			data: events.AskUserQuestionEventData{Questions: []events.AskUserQuestion{{
				Question: "Which?", Options: []events.AskUserOption{{Label: "A"}, {Label: long}},
			}}},
			want: []CardButton{
				{Label: "A", Choice: "1"},
				{Label: strings.Repeat("é", maxCardButtonLabel-1) + "…", Choice: "2"},
				skip,
			},
		},
		{
			name: "multi-select question",
			data: events.AskUserQuestionEventData{Questions: []events.AskUserQuestion{{
				Question: "Which?", MultiSelect: true, Options: []events.AskUserOption{{Label: "A"}},
			}}},
			want: []CardButton{skip},
		},
		{
			name: "several questions",
			data: events.AskUserQuestionEventData{Questions: []events.AskUserQuestion{
				{Question: "Q1", Options: []events.AskUserOption{{Label: "A"}}},
				{Question: "Q2", Options: []events.AskUserOption{{Label: "B"}}},
			}},
			want: []CardButton{skip},
		},
		{
			name: "too many options",
			data: events.AskUserQuestionEventData{Questions: []events.AskUserQuestion{{Question: "Which?", Options: many}}},
			want: []CardButton{skip},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, AskCardButtons(tc.data))
		})
	}
}

func (s *ParkCardSuite) TestPlanCardButtons() {
	require.Equal(s.T(), []CardButton{
		{Label: "Approve", Choice: PlanReplyApprove, Style: CardButtonPrimary},
		{Label: "Reject", Choice: PlanReplyReject, Style: CardButtonDanger},
	}, PlanCardButtons())
}

func (s *ParkCardSuite) TestCardActionIDRoundTrip() {
	id := CardActionID("C1:1700000000.000100", "toolu_1", "2")
	require.Equal(s.T(), "card:C1:1700000000.000100:toolu_1:2", id)
	ch, card, choice, ok := ParseCardActionID(id)
	require.True(s.T(), ok)
	require.Equal(s.T(), "C1:1700000000.000100", ch)
	require.Equal(s.T(), "toolu_1", card)
	require.Equal(s.T(), "2", choice)
}

func (s *ParkCardSuite) TestParseCardActionIDInvalid() {
	for _, id := range []string{
		"gate:r1:once",
		"card:",
		"card:toolu_1",
		"card:ch1:toolu_1:",
		"card::toolu_1:1",
		"card:ch1::1",
	} {
		s.Run(id, func() {
			_, _, _, ok := ParseCardActionID(id)
			require.False(s.T(), ok)
		})
	}
}

func (s *ParkCardSuite) TestCardClosedText() {
	require.Equal(s.T(), "› Approved — <@U1>", CardClosedText("Approved", "U1"))
}
