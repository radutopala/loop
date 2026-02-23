package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/db"
)

// HandleMessage processes an incoming chat message.
func (o *Orchestrator) HandleMessage(ctx context.Context, msg *IncomingMessage) {
	active, err := o.store.IsChannelActive(ctx, msg.ChannelID)
	if err != nil {
		o.logger.Error("checking channel active", "error", err, "channel_id", msg.ChannelID)
		return
	}
	if !active {
		if !o.resolveThread(ctx, msg.ChannelID) {
			if msg.IsDM || msg.IsBotMention || msg.HasPrefix || msg.IsReplyToBot {
				name := o.resolveChannelName(ctx, msg.ChannelID, msg.IsDM)
				if err := o.store.UpsertChannel(ctx, &db.Channel{
					ChannelID: msg.ChannelID,
					GuildID:   msg.GuildID,
					Name:      name,
					Platform:  o.platform,
					Active:    true,
				}); err != nil {
					o.logger.Error("auto-creating channel", "error", err)
					return
				}
				o.logger.Info("auto-created channel", "channel_id", msg.ChannelID, "name", name)
			} else {
				return
			}
		}
	}

	channel, err := o.store.GetChannel(ctx, msg.ChannelID)
	if err != nil || channel == nil {
		o.logger.Error("getting channel", "error", err, "channel_id", msg.ChannelID)
		return
	}

	msgID := msg.MessageID
	if msgID == "" {
		msgID = generateMessageID()
	}

	if err := o.store.InsertMessage(ctx, &db.Message{
		ChatID:     channel.ID,
		ChannelID:  msg.ChannelID,
		MsgID:      msgID,
		AuthorID:   msg.AuthorID,
		AuthorName: msg.AuthorName,
		Content:    msg.Content,
		CreatedAt:  msg.Timestamp,
	}); err != nil {
		o.logger.Error("inserting message", "error", err, "channel_id", msg.ChannelID)
		return
	}

	o.logger.Info("incoming message",
		"channel_id", msg.ChannelID,
		"author", msg.AuthorName,
		"content", msg.Content,
		"triggered", msg.IsBotMention || msg.IsReplyToBot || msg.HasPrefix || msg.IsDM,
	)

	triggered := msg.IsBotMention || msg.IsReplyToBot || msg.HasPrefix || msg.IsDM
	if !triggered {
		return
	}

	// Allow the bot's own self-mentions (e.g. from create_thread MCP tool) to bypass permissions.
	if msg.AuthorID != o.bot.BotUserID() {
		cfgPerms := o.configPermissionsFor(channel.DirPath)
		role := resolveRole(cfgPerms, channel.Permissions, msg.AuthorID, msg.AuthorRoles)
		if role == "" {
			o.logger.Info("message denied by permissions", "channel_id", msg.ChannelID, "author_id", msg.AuthorID)
			return
		}
	}

	o.processTriggeredMessage(ctx, msg)
}

// resolveThread checks if channelID is a thread with an active parent channel.
// If so, it upserts the thread as a channel inheriting from the parent and returns true.
func (o *Orchestrator) resolveThread(ctx context.Context, channelID string) bool {
	parentID, err := o.bot.GetChannelParentID(ctx, channelID)
	if err != nil {
		o.logger.Error("getting channel parent", "error", err, "channel_id", channelID)
		return false
	}
	if parentID == "" {
		return false
	}

	parentActive, err := o.store.IsChannelActive(ctx, parentID)
	if err != nil {
		o.logger.Error("checking parent channel active", "error", err, "parent_id", parentID)
		return false
	}
	if !parentActive {
		return false
	}

	parent, err := o.store.GetChannel(ctx, parentID)
	if err != nil || parent == nil {
		o.logger.Error("getting parent channel", "error", err, "parent_id", parentID)
		return false
	}

	if err := o.store.UpsertChannel(ctx, &db.Channel{
		ChannelID:   channelID,
		GuildID:     parent.GuildID,
		DirPath:     parent.DirPath,
		ParentID:    parentID,
		Platform:    parent.Platform,
		SessionID:   parent.SessionID,
		Permissions: parent.Permissions,
		Active:      true,
	}); err != nil {
		o.logger.Error("upserting thread channel", "error", err, "channel_id", channelID)
		return false
	}

	o.logger.Info("resolved thread to parent channel",
		"thread_id", channelID,
		"parent_id", parentID,
	)
	return true
}

func (o *Orchestrator) processTriggeredMessage(ctx context.Context, msg *IncomingMessage) {
	o.queue.Acquire(msg.ChannelID)
	defer o.queue.Release(msg.ChannelID)

	// Send stop button (non-fatal if it fails)
	stopMsgID, stopErr := o.bot.SendStopButton(ctx, msg.ChannelID, msg.ChannelID)
	if stopErr != nil {
		o.logger.Error("sending stop button", "error", stopErr, "channel_id", msg.ChannelID)
	}
	defer func() {
		o.activeRuns.Delete(msg.ChannelID)
		if stopMsgID != "" {
			if err := o.bot.RemoveStopButton(ctx, msg.ChannelID, stopMsgID); err != nil {
				o.logger.Error("removing stop button", "error", err, "channel_id", msg.ChannelID)
			}
		}
	}()

	typingCtx, stopTyping := context.WithCancel(ctx)
	defer stopTyping()
	go o.refreshTyping(typingCtx, msg.ChannelID)

	req, recent, err := o.prepareAgentRequest(ctx, msg)
	if err != nil {
		return
	}

	resp, lastStreamedText, err := o.executeAgentRun(ctx, msg, req)
	if err != nil {
		return
	}

	o.deliverResponse(ctx, msg, resp, recent, lastStreamedText)
}

// prepareAgentRequest fetches recent messages and channel data, then builds an AgentRequest.
func (o *Orchestrator) prepareAgentRequest(ctx context.Context, msg *IncomingMessage) (*agent.AgentRequest, []*db.Message, error) {
	recent, err := o.store.GetRecentMessages(ctx, msg.ChannelID, recentMessageLimit)
	if err != nil {
		o.logger.Error("getting recent messages", "error", err, "channel_id", msg.ChannelID)
		return nil, nil, err
	}

	channel, err := o.store.GetChannel(ctx, msg.ChannelID)
	if err != nil {
		o.logger.Error("getting channel", "error", err, "channel_id", msg.ChannelID)
		return nil, nil, err
	}

	req := o.buildAgentRequest(msg.ChannelID, recent, channel)
	req.Prompt = fmt.Sprintf("%s: %s", msg.AuthorName, msg.Content)
	req.AuthorID = msg.AuthorID

	// Fork the session on the first thread message so the thread gets its
	// own session while inheriting the parent's context.
	if channel.ParentID != "" && req.SessionID != "" {
		parent, err := o.store.GetChannel(ctx, channel.ParentID)
		if err == nil && parent != nil && channel.SessionID == parent.SessionID {
			req.ForkSession = true
		}
	}

	return req, recent, nil
}

// executeAgentRun runs the agent with timeout, streaming, and stop-button cancellation.
// Returns the agent response and the last streamed text (for dedup), or an error if the
// run failed and the caller should abort.
func (o *Orchestrator) executeAgentRun(ctx context.Context, msg *IncomingMessage, req *agent.AgentRequest) (*agent.AgentResponse, string, error) {
	runCtx, runCancel := context.WithTimeout(ctx, o.cfg.ContainerTimeout)
	defer runCancel()

	// Register the cancel func so stop button clicks can cancel this run.
	o.activeRuns.Store(msg.ChannelID, runCancel)

	var lastStreamedText string
	if o.cfg.StreamingEnabled {
		req.OnTurn = func(text string) {
			if text == "" {
				return
			}
			lastStreamedText = text
			_ = o.bot.SendMessage(ctx, &OutgoingMessage{
				ChannelID:        msg.ChannelID,
				Content:          text,
				ReplyToMessageID: msg.MessageID,
			})
		}
	}

	resp, err := o.runner.Run(runCtx, req)
	if err != nil {
		if runCtx.Err() == context.Canceled {
			o.logger.Info("run stopped by user", "channel_id", msg.ChannelID)
			_ = o.bot.SendMessage(ctx, &OutgoingMessage{
				ChannelID:        msg.ChannelID,
				Content:          "Run stopped.",
				ReplyToMessageID: msg.MessageID,
			})
			return nil, "", err
		}
		o.logger.Error("running agent", "error", err, "channel_id", msg.ChannelID)
		_ = o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID:        msg.ChannelID,
			Content:          "Sorry, I encountered an error processing your request.",
			ReplyToMessageID: msg.MessageID,
		})
		return nil, "", err
	}

	if resp.Error != "" {
		o.logger.Error("agent returned error", "error", resp.Error, "channel_id", msg.ChannelID)
		_ = o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID:        msg.ChannelID,
			Content:          fmt.Sprintf("Agent error: %s", resp.Error),
			ReplyToMessageID: msg.MessageID,
		})
		return nil, "", fmt.Errorf("agent error: %s", resp.Error)
	}

	return resp, lastStreamedText, nil
}

// deliverResponse sends the final response, records the bot message, and marks messages as processed.
func (o *Orchestrator) deliverResponse(ctx context.Context, msg *IncomingMessage, resp *agent.AgentResponse, recent []*db.Message, lastStreamedText string) {
	if err := o.store.UpdateSessionID(ctx, msg.ChannelID, resp.SessionID); err != nil {
		o.logger.Error("updating session data", "error", err, "channel_id", msg.ChannelID)
	}

	o.logger.Info("outgoing message",
		"channel_id", msg.ChannelID,
		"content", resp.Response,
	)

	// Skip final send if it duplicates the last streamed turn
	if resp.Response != lastStreamedText {
		if err := o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID:        msg.ChannelID,
			Content:          resp.Response,
			ReplyToMessageID: msg.MessageID,
		}); err != nil {
			o.logger.Error("sending response", "error", err, "channel_id", msg.ChannelID)
		}
	}

	ch, err := o.store.GetChannel(ctx, msg.ChannelID)
	if err == nil && ch != nil {
		if insertErr := o.store.InsertMessage(ctx, &db.Message{
			ChatID:     ch.ID,
			ChannelID:  msg.ChannelID,
			MsgID:      generateMessageID(),
			AuthorName: "assistant",
			Content:    resp.Response,
			IsBot:      true,
			CreatedAt:  time.Now().UTC(),
		}); insertErr != nil {
			o.logger.Error("inserting bot response", "error", insertErr, "channel_id", msg.ChannelID)
		}
	}

	ids := make([]int64, len(recent))
	for i, m := range recent {
		ids[i] = m.ID
	}
	if err := o.store.MarkMessagesProcessed(ctx, ids); err != nil {
		o.logger.Error("marking messages processed", "error", err, "channel_id", msg.ChannelID)
	}
}

func (o *Orchestrator) refreshTyping(ctx context.Context, channelID string) {
	if err := o.bot.SendTyping(ctx, channelID); err != nil {
		o.logger.Error("sending typing indicator", "error", err, "channel_id", channelID)
	}

	ticker := time.NewTicker(o.typingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := o.bot.SendTyping(ctx, channelID); err != nil {
				o.logger.Error("refreshing typing indicator", "error", err, "channel_id", channelID)
			}
		}
	}
}

func (o *Orchestrator) buildAgentRequest(channelID string, recent []*db.Message, channel *db.Channel) *agent.AgentRequest {
	var messages []agent.AgentMessage
	// Reverse so oldest first
	for i := len(recent) - 1; i >= 0; i-- {
		m := recent[i]
		role := "user"
		if m.IsBot {
			role = "assistant"
		}
		messages = append(messages, agent.AgentMessage{
			Role:    role,
			Content: fmt.Sprintf("%s: %s", m.AuthorName, m.Content),
		})
	}

	sessionID := ""
	if channel != nil {
		sessionID = channel.SessionID
	}

	dirPath := ""
	if channel != nil {
		dirPath = channel.DirPath
	}

	return &agent.AgentRequest{
		SessionID: sessionID,
		Messages:  messages,
		ChannelID: channelID,
		DirPath:   dirPath,
	}
}
