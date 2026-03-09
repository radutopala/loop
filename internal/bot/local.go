package bot

import "context"

// LocalBot is a no-op bot for the local (non-platform) mode.
// It implements the orchestrator.Bot interface but performs no
// platform-specific actions because the orchestrator already handles
// storing messages and broadcasting events via the API/WebSocket layer.
type LocalBot struct {
	messageHandler       MessageHandler
	interactionHandler   InteractionHandler
	channelDeleteHandler ChannelDeleteHandler
	channelJoinHandler   ChannelJoinHandler
}

// NewLocalBot creates a new LocalBot.
func NewLocalBot() *LocalBot {
	return &LocalBot{}
}

func (b *LocalBot) Start(_ context.Context) error            { return nil }
func (b *LocalBot) Stop() error                              { return nil }
func (b *LocalBot) RegisterCommands(_ context.Context) error { return nil }
func (b *LocalBot) RemoveCommands(_ context.Context) error   { return nil }
func (b *LocalBot) BotUserID() string                        { return "loop-bot" }

func (b *LocalBot) SendMessage(_ context.Context, _ *OutgoingMessage) error { return nil }
func (b *LocalBot) SendTyping(_ context.Context, _ string) error            { return nil }
func (b *LocalBot) PostMessage(_ context.Context, _, _ string) error        { return nil }

func (b *LocalBot) SendStopButton(_ context.Context, _, _ string) (string, error) { return "", nil }
func (b *LocalBot) RemoveStopButton(_ context.Context, _, _ string) error         { return nil }

func (b *LocalBot) CreateChannel(_ context.Context, _, _ string) (string, error)      { return "", nil }
func (b *LocalBot) CreateThread(_ context.Context, _, _, _, _ string) (string, error) { return "", nil }
func (b *LocalBot) CreateSimpleThread(_ context.Context, _, _, _ string) (string, error) {
	return "", nil
}

func (b *LocalBot) InviteUserToChannel(_ context.Context, _, _ string) error { return nil }
func (b *LocalBot) SetChannelTopic(_ context.Context, _, _ string) error     { return nil }
func (b *LocalBot) DeleteThread(_ context.Context, _ string) error           { return nil }
func (b *LocalBot) RenameThread(_ context.Context, _, _ string) error        { return nil }

func (b *LocalBot) GetOwnerUserID(_ context.Context) (string, error)                { return "", nil }
func (b *LocalBot) GetChannelParentID(_ context.Context, _ string) (string, error)  { return "", nil }
func (b *LocalBot) GetChannelName(_ context.Context, _ string) (string, error)      { return "", nil }
func (b *LocalBot) GetMemberRoles(_ context.Context, _, _ string) ([]string, error) { return nil, nil }

func (b *LocalBot) OnMessage(handler func(ctx context.Context, msg *IncomingMessage)) {
	b.messageHandler = handler
}

func (b *LocalBot) OnInteraction(handler func(ctx context.Context, i *Interaction)) {
	b.interactionHandler = handler
}

func (b *LocalBot) OnChannelDelete(handler func(ctx context.Context, channelID string, isThread bool)) {
	b.channelDeleteHandler = handler
}

func (b *LocalBot) OnChannelJoin(handler func(ctx context.Context, channelID string)) {
	b.channelJoinHandler = handler
}
