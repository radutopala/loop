package bot

import (
	"context"
	"strings"
)

// Tags the daemon sets on a message sent from another channel.
const (
	TagOriginChannel     = "origin_channel"
	TagOriginChannelName = "origin_channel_name"
	// TagOriginMessage is the row id of the prompt the sender was on.
	TagOriginMessage = "origin_message"
)

type messageTagsKey struct{}

// WithMessageTags returns ctx carrying the tags to store on an incoming
// message.
func WithMessageTags(ctx context.Context, tags map[string]string) context.Context {
	return context.WithValue(ctx, messageTagsKey{}, tags)
}

// MessageTagsFrom returns the tags ctx carries.
func MessageTagsFrom(ctx context.Context) map[string]string {
	tags, _ := ctx.Value(messageTagsKey{}).(map[string]string)
	return tags
}

// originLabelEscaper keeps a tag value from closing the label early.
var originLabelEscaper = strings.NewReplacer("[", "(", "]", ")")

// WithOriginLabel returns the text an agent reads for a message: content, with
// a label naming the channel it came from and how to answer it. Without that
// last part an agent answers in its own chat, where the sender never sees it.
// An empty message stays empty, and a slash command as it is, or it
// wouldn't run as one.
func WithOriginLabel(content string, tags map[string]string) string {
	from := tags[TagOriginChannel]
	if from == "" || strings.TrimSpace(content) == "" || strings.HasPrefix(content, "/") {
		return content
	}
	label := "channel_id: " + from
	if name := tags[TagOriginChannelName]; name != "" {
		label = `channel "` + name + `", ` + label
	}
	if msg := tags[TagOriginMessage]; msg != "" {
		label += ", message_id: " + msg
	}
	label = originLabelEscaper.Replace(strings.Join(strings.Fields(label), " "))
	return "[from " + label + "; if it needs an answer, send it with send_message to this channel_id] " + content
}
