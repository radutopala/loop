package slack

import (
	goslack "github.com/slack-go/slack"
)

// SlackSession abstracts the slack.Client methods used by the bot,
// enabling test mocking.
type SlackSession interface {
	PostMessage(channelID string, options ...goslack.MsgOption) (string, string, error)
	DeleteMessage(channel, messageTimestamp string) (string, string, error)
	AuthTest() (*goslack.AuthTestResponse, error)
	CreateConversation(params goslack.CreateConversationParams) (*goslack.Channel, error)
	AddReaction(name string, item goslack.ItemRef) error
	RemoveReaction(name string, item goslack.ItemRef) error
	GetConversationReplies(params *goslack.GetConversationRepliesParameters) ([]goslack.Message, bool, string, error)
	InviteUsersToConversation(channelID string, users ...string) (*goslack.Channel, error)
	GetConversations(params *goslack.GetConversationsParameters) ([]goslack.Channel, string, error)
	GetUsers(options ...goslack.GetUsersOption) ([]goslack.User, error)
	SetTopicOfConversation(channelID, topic string) (*goslack.Channel, error)
	SetUserPresence(presence string) error
	GetConversationInfo(input *goslack.GetConversationInfoInput) (*goslack.Channel, error)
}
