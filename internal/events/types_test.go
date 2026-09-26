package events

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAskUserQuestionUnmarshalMatchesToolSchema pins the JSON tags to Claude
// Code's AskUserQuestion tool-input schema: the orchestrator unmarshals the raw
// tool_use input straight into AskUserQuestionEventData, so a tag drift (e.g.
// multiSelect -> multi_select, or a missing option preview) silently drops the
// field — which previously made checkbox mode and option previews never render.
func TestAskUserQuestionUnmarshalMatchesToolSchema(t *testing.T) {
	cases := []struct {
		name  string
		input string
		check func(t *testing.T, d AskUserQuestionEventData)
	}{
		{
			name: "multiSelect and option preview populate",
			input: `{"questions":[
				{"question":"Which features?","header":"Features","multiSelect":true,
				 "options":[{"label":"Auth","description":"auth flow","preview":"POST /login"},{"label":"Search"}]},
				{"question":"Theme?","header":"Theme",
				 "options":[{"label":"Dark"}]}
			]}`,
			check: func(t *testing.T, d AskUserQuestionEventData) {
				require.Len(t, d.Questions, 2)
				require.True(t, d.Questions[0].MultiSelect, "multiSelect must parse into MultiSelect")
				require.False(t, d.Questions[1].MultiSelect, "absent multiSelect defaults to false")
				require.Equal(t, "auth flow", d.Questions[0].Options[0].Description)
				require.Equal(t, "POST /login", d.Questions[0].Options[0].Preview, "option preview must parse")
				require.Empty(t, d.Questions[0].Options[1].Preview)
			},
		},
		{
			name:  "single-select without options",
			input: `{"questions":[{"question":"Proceed?","header":"Confirm"}]}`,
			check: func(t *testing.T, d AskUserQuestionEventData) {
				require.Len(t, d.Questions, 1)
				require.False(t, d.Questions[0].MultiSelect)
				require.Empty(t, d.Questions[0].Options)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var d AskUserQuestionEventData
			require.NoError(t, json.Unmarshal([]byte(tc.input), &d))
			tc.check(t, d)
		})
	}
}

// A description or ticket change must reach the sidebar even when it clears
// it, and git updates must carry neither.
func TestChannelUpdatedDescriptionJSON(t *testing.T) {
	empty, set, ticket := "", "fixes login", "https://x.com/T-1"
	cases := []struct {
		name string
		data ChannelUpdatedData
		want string
		has  bool
	}{
		{name: "set", data: ChannelUpdatedData{ChannelID: "t1", Description: &set}, want: `"description":"fixes login"`, has: true},
		{name: "cleared", data: ChannelUpdatedData{ChannelID: "t1", Description: &empty}, want: `"description":""`, has: true},
		{name: "git update", data: ChannelUpdatedData{ChannelID: "t1", Branch: "main"}, want: `"description"`, has: false},
		{name: "ticket set", data: ChannelUpdatedData{ChannelID: "t1", TicketURL: &ticket}, want: `"ticket_url":"https://x.com/T-1"`, has: true},
		{name: "ticket cleared", data: ChannelUpdatedData{ChannelID: "t1", TicketURL: &empty}, want: `"ticket_url":""`, has: true},
		{name: "git update has no ticket", data: ChannelUpdatedData{ChannelID: "t1", Branch: "main"}, want: `"ticket_url"`, has: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.data)
			require.NoError(t, err)
			if tc.has {
				require.Contains(t, string(b), tc.want)
			} else {
				require.NotContains(t, string(b), tc.want)
			}
		})
	}
}
