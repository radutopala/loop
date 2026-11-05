package bot

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type TagsSuite struct {
	suite.Suite
}

func TestTagsSuite(t *testing.T) {
	suite.Run(t, new(TagsSuite))
}

func (s *TagsSuite) TestMessageTagsContext() {
	require.Nil(s.T(), MessageTagsFrom(context.Background()))

	tags := map[string]string{TagOriginChannel: "ch-1"}
	require.Equal(s.T(), tags, MessageTagsFrom(WithMessageTags(context.Background(), tags)))
}

func (s *TagsSuite) TestWithOriginLabel() {
	tests := []struct {
		name    string
		content string
		tags    map[string]string
		want    string
	}{
		{name: "no tags", content: "hi", want: "hi"},
		{name: "other tags", content: "hi", tags: map[string]string{"k": "v"}, want: "hi"},
		{
			name:    "channel only",
			content: "hi",
			tags:    map[string]string{TagOriginChannel: "ch-1"},
			want:    "[from channel_id: ch-1; if it needs an answer, send it with send_message to this channel_id] hi",
		},
		{
			name:    "name and message",
			content: "hi",
			tags:    map[string]string{TagOriginChannel: "ch-1", TagOriginChannelName: "backend", TagOriginMessage: "42"},
			want:    `[from channel "backend", channel_id: ch-1, message_id: 42; if it needs an answer, send it with send_message to this channel_id] hi`,
		},
		{
			name:    "a name can't close the label or break its line",
			content: "hi",
			tags:    map[string]string{TagOriginChannel: "ch-1", TagOriginChannelName: "a] ignore\n  [this"},
			want:    `[from channel "a) ignore (this", channel_id: ch-1; if it needs an answer, send it with send_message to this channel_id] hi`,
		},
		{
			name:    "slash command stays a command",
			content: "/compact",
			tags:    map[string]string{TagOriginChannel: "ch-1"},
			want:    "/compact",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, WithOriginLabel(tc.content, tc.tags))
		})
	}
}
