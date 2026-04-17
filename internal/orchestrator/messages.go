package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/randutil"
	"github.com/radutopala/loop/internal/types"
)

// HandleMessage processes an incoming chat message.
func (o *Orchestrator) HandleMessage(ctx context.Context, msg *bot.IncomingMessage) {
	active, err := o.store.IsChannelActive(ctx, msg.ChannelID)
	if err != nil {
		o.logger.Error("checking channel active", "error", err, "channel_id", msg.ChannelID)
		return
	}
	if !active {
		if !o.resolveThread(ctx, msg.ChannelID) {
			if msg.IsDM || msg.IsBotMention || msg.HasPrefix || msg.IsReplyToBot {
				name := o.resolveChannelName(ctx, msg.ChannelID, msg.IsDM)
				dirPath := filepath.Join(o.currentConfig().LoopDir, msg.ChannelID, "work")
				if err := o.store.UpsertChannel(ctx, &db.Channel{
					ChannelID: msg.ChannelID,
					GuildID:   msg.GuildID,
					Name:      name,
					DirPath:   dirPath,
					Platform:  msg.Platform,
					Active:    true,
				}); err != nil {
					o.logger.Error("auto-creating channel", "error", err)
					return
				}
				o.logger.Info("auto-created channel", "channel_id", msg.ChannelID, "platform", msg.Platform, "name", name)
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
		msg.MessageID = msgID
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

	if o.events != nil {
		o.events.BroadcastMessageCreated(msg.ChannelID, events.MessageEventData{
			MsgID:      msgID,
			AuthorID:   msg.AuthorID,
			AuthorName: msg.AuthorName,
			Content:    msg.Content,
		})
	}

	o.logger.Info("incoming message",
		"channel_id", msg.ChannelID,
		"platform", msg.Platform,
		"author", msg.AuthorName,
		"content", msg.Content,
		"triggered", msg.IsBotMention || msg.IsReplyToBot || msg.HasPrefix || msg.IsDM,
	)

	triggered := msg.IsBotMention || msg.IsReplyToBot || msg.HasPrefix || msg.IsDM
	if !triggered {
		return
	}

	// Allow the bot's own self-mentions (e.g. from create_thread MCP tool) to bypass permissions.
	// Local platform is inherently trusted — the user is running on their own machine.
	if !o.bot.IsBotUser(msg.AuthorID) && msg.Platform != types.PlatformLocal {
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

func (o *Orchestrator) processTriggeredMessage(ctx context.Context, msg *bot.IncomingMessage) {
	o.queue.Acquire(msg.ChannelID)
	defer o.queue.Release(msg.ChannelID)

	req, recent, err := o.prepareAgentRequest(ctx, msg)
	if err != nil {
		return
	}

	// If the queued trigger message was deleted while waiting in the queue,
	// skip dispatch. The DB row is gone and the frontend already removed it
	// via the message.deleted WS event. Only trust MsgID-based matching when
	// recent actually carries MsgIDs — otherwise the check can't distinguish
	// deletion from legacy rows and we fall through.
	if msg.MessageID != "" && hasAnyMsgID(recent) && !containsMsgID(recent, msg.MessageID) {
		o.logger.Info("queued message deleted before dispatch; skipping run",
			"channel_id", msg.ChannelID, "msg_id", msg.MessageID)
		return
	}

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

	resp, lastStreamedText, runID, err := o.executeAgentRun(ctx, msg, req)
	if err != nil {
		// Mark the trigger message as processed even on error/stop so the
		// frontend doesn't keep showing it as "processing" when the next
		// queued message starts.
		o.markTriggerProcessed(ctx, msg, recent)
		return
	}

	o.deliverResponse(ctx, msg, resp, recent, lastStreamedText, runID)
}

// containsMsgID reports whether any message in recent has the given MsgID.
func containsMsgID(recent []*db.Message, msgID string) bool {
	for _, m := range recent {
		if m.MsgID == msgID {
			return true
		}
	}
	return false
}

// hasAnyMsgID reports whether any message in recent has a non-empty MsgID.
func hasAnyMsgID(recent []*db.Message) bool {
	for _, m := range recent {
		if m.MsgID != "" {
			return true
		}
	}
	return false
}

// prepareAgentRequest fetches recent messages and channel data, then builds an AgentRequest.
func (o *Orchestrator) prepareAgentRequest(ctx context.Context, msg *bot.IncomingMessage) (*agent.AgentRequest, []*db.Message, error) {
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
	req.Prompt = formatMessageContent(msg.AuthorName, msg.Content)
	req.AuthorID = msg.AuthorID
	req.PlanMode = msg.Mode == "plan"

	// Fork the session on the first thread message so the thread gets its
	// own session while inheriting the parent's context.
	if channel.ParentID != "" {
		parent, err := o.store.GetChannel(ctx, channel.ParentID)
		if err == nil && parent != nil {
			if req.SessionID != "" && channel.SessionID == parent.SessionID {
				req.ForkSession = true
			}
			// Pass parent's DirPath so the runner can mount it for worktree containers.
			if channel.Worktree && parent.DirPath != "" {
				req.ParentDirPath = parent.DirPath
			}
		}
	}

	// When running in a worktree, tell the agent its working directory so it
	// uses the correct absolute paths instead of drifting to the main repo.
	if channel != nil && channel.Worktree && channel.DirPath != "" {
		dirHint := fmt.Sprintf(
			"IMPORTANT: Your working directory is %s. Always use absolute paths under this directory for all file operations.",
			channel.DirPath,
		)
		req.Prompt = dirHint + "\n\n" + req.Prompt
	}

	return req, recent, nil
}

// executeAgentRun runs the agent with timeout, streaming, and stop-button cancellation.
// Returns the agent response and the last streamed text (for dedup), or an error if the
// run failed and the caller should abort.
func (o *Orchestrator) executeAgentRun(ctx context.Context, msg *bot.IncomingMessage, req *agent.AgentRequest) (*agent.AgentResponse, string, string, error) {
	cfg := o.currentConfig()
	runCtx, runCancel := context.WithTimeout(ctx, cfg.ContainerTimeout)
	defer runCancel()

	runID := randutil.HexID(8)

	// Register the cancel func so stop button clicks can cancel this run.
	o.activeRuns.Store(msg.ChannelID, runCancel)

	var tracker *streamTracker
	if cfg.StreamingEnabled {
		tracker = newStreamTracker(func(text string) {
			if err := o.bot.SendMessage(ctx, &bot.OutgoingMessage{
				ChannelID:        msg.ChannelID,
				Content:          text,
				ReplyToMessageID: msg.MessageID,
			}); err != nil {
				o.logger.Error("streaming send failed", "error", err, "channel_id", msg.ChannelID)
			}
			storeBotMessage(ctx, o.store, o.events, msg.ChannelID, text)
		})
		req.OnTurn = tracker.OnTurn
		if o.events != nil {
			req.OnToolUse = func(name, input string) {
				o.events.BroadcastToolUse(msg.ChannelID, events.ToolUseEventData{
					ToolName: name,
					Input:    input,
				})
				if name == "AskUserQuestion" {
					var data events.AskUserQuestionEventData
					if err := json.Unmarshal([]byte(input), &data); err == nil && len(data.Questions) > 0 {
						o.events.BroadcastAskUser(msg.ChannelID, data)
					}
				}
				if name == "ExitPlanMode" {
					var data events.ExitPlanModeEventData
					if err := json.Unmarshal([]byte(input), &data); err == nil && data.Plan != "" {
						o.events.BroadcastExitPlan(msg.ChannelID, data)
					}
				}
				if name == "TodoWrite" {
					var data events.TodoWriteEventData
					if err := json.Unmarshal([]byte(input), &data); err == nil && len(data.Todos) > 0 {
						o.events.BroadcastTodoWrite(msg.ChannelID, data)
					}
				}
			}
			req.OnActivity = func(activity, detail string) {
				data := events.AgentActivityEventData{Activity: activity}
				if activity == "model" {
					data.Model = detail
				} else {
					data.Description = detail
				}
				o.events.BroadcastAgentActivity(msg.ChannelID, data)
			}
		}
	}

	if o.events != nil {
		o.events.BroadcastAgentStatus(msg.ChannelID, events.AgentStatusEventData{Status: "running", RunID: runID, TriggerContent: msg.Content})
	}

	resp, err := o.runner.Run(runCtx, req)
	if err != nil {
		if o.events != nil {
			o.events.BroadcastAgentStatus(msg.ChannelID, events.AgentStatusEventData{Status: "error", RunID: runID, Error: err.Error()})
		}
		if runCtx.Err() == context.Canceled {
			o.logger.Info("run stopped by user", "channel_id", msg.ChannelID)
			_ = o.bot.SendMessage(ctx, &bot.OutgoingMessage{
				ChannelID:        msg.ChannelID,
				Content:          "Run stopped.",
				ReplyToMessageID: msg.MessageID,
			})
			return nil, "", runID, err
		}
		o.logger.Error("running agent", "error", err, "channel_id", msg.ChannelID)
		_ = o.bot.SendMessage(ctx, &bot.OutgoingMessage{
			ChannelID:        msg.ChannelID,
			Content:          "Sorry, I encountered an error processing your request.",
			ReplyToMessageID: msg.MessageID,
		})
		return nil, "", runID, err
	}

	if resp.Error != "" {
		if o.events != nil {
			o.events.BroadcastAgentStatus(msg.ChannelID, events.AgentStatusEventData{Status: "error", RunID: runID, Error: resp.Error})
		}
		o.logger.Error("agent returned error", "error", resp.Error, "channel_id", msg.ChannelID)
		_ = o.bot.SendMessage(ctx, &bot.OutgoingMessage{
			ChannelID:        msg.ChannelID,
			Content:          fmt.Sprintf("Agent error: %s", resp.Error),
			ReplyToMessageID: msg.MessageID,
		})
		return nil, "", runID, fmt.Errorf("agent error: %s", resp.Error)
	}

	var lastText string
	if tracker != nil {
		lastText = tracker.lastText
	}
	return resp, lastText, runID, nil
}

// deliverResponse sends the final response, records the bot message, and marks messages as processed.
func (o *Orchestrator) deliverResponse(ctx context.Context, msg *bot.IncomingMessage, resp *agent.AgentResponse, recent []*db.Message, lastStreamedText, runID string) {
	if err := o.store.UpdateSessionID(ctx, msg.ChannelID, resp.SessionID); err != nil {
		o.logger.Error("updating session data", "error", err, "channel_id", msg.ChannelID)
	}

	o.logger.Info("outgoing message",
		"channel_id", msg.ChannelID,
		"platform", msg.Platform,
		"content", resp.Response,
	)

	// Skip final send/store/broadcast if it duplicates the last streamed turn
	// (already stored and broadcast during streaming).
	isDuplicate := lastStreamedText != "" && resp.Response == lastStreamedText
	if !isDuplicate {
		if err := o.bot.SendMessage(ctx, &bot.OutgoingMessage{
			ChannelID:        msg.ChannelID,
			Content:          resp.Response,
			ReplyToMessageID: msg.MessageID,
		}); err != nil {
			o.logger.Error("sending response", "error", err, "channel_id", msg.ChannelID)
		}
		storeBotMessage(ctx, o.store, o.events, msg.ChannelID, resp.Response)
	}

	// Only mark messages up to (and including) the trigger message as processed.
	// Messages queued after the trigger remain unprocessed so the frontend can
	// still show them as "queued" or "processing".
	toMark := recent
	for i, m := range recent {
		if m.MsgID == msg.MessageID {
			toMark = recent[i:]
			break
		}
	}
	ids := make([]int64, len(toMark))
	msgIDs := make([]string, 0, len(toMark))
	for i, m := range toMark {
		ids[i] = m.ID
		if m.MsgID != "" {
			msgIDs = append(msgIDs, m.MsgID)
		}
	}
	if err := o.store.MarkMessagesProcessed(ctx, ids); err != nil {
		o.logger.Error("marking messages processed", "error", err, "channel_id", msg.ChannelID)
	}
	// Broadcast messages.processed BEFORE agent completed status so the frontend
	// clears labels before isRunning goes false (avoids a brief "queued" flash).
	if o.events != nil && len(msgIDs) > 0 {
		o.events.BroadcastMessagesProcessed(msg.ChannelID, events.MessagesProcessedData{
			MsgIDs: msgIDs,
		})
	}
	if o.events != nil {
		o.events.BroadcastAgentStatus(msg.ChannelID, events.AgentStatusEventData{
			Status:     "completed",
			RunID:      runID,
			DurationMs: resp.DurationMs,
			NumTurns:   resp.NumTurns,
			StopReason: resp.StopReason,
			Model:      resp.Model,
		})
	}
}

// markTriggerProcessed marks the trigger message (and any earlier unprocessed
// messages) as processed after a failed/stopped run so the frontend doesn't
// keep showing it as "processing" when the next queued message starts.
func (o *Orchestrator) markTriggerProcessed(ctx context.Context, msg *bot.IncomingMessage, recent []*db.Message) {
	if len(recent) == 0 {
		return
	}
	toMark := recent
	for i, m := range recent {
		if m.MsgID == msg.MessageID {
			toMark = recent[i:]
			break
		}
	}
	ids := make([]int64, len(toMark))
	msgIDs := make([]string, 0, len(toMark))
	for i, m := range toMark {
		ids[i] = m.ID
		if m.MsgID != "" {
			msgIDs = append(msgIDs, m.MsgID)
		}
	}
	if err := o.store.MarkMessagesProcessed(ctx, ids); err != nil {
		o.logger.Error("marking trigger messages processed after error", "error", err, "channel_id", msg.ChannelID)
	}
	if o.events != nil && len(msgIDs) > 0 {
		o.events.BroadcastMessagesProcessed(msg.ChannelID, events.MessagesProcessedData{
			MsgIDs: msgIDs,
		})
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
			Content: formatMessageContent(m.AuthorName, m.Content),
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
		AgentID:   "chat",
	}
}

// formatMessageContent formats a message for the agent prompt.
// Slash-command messages are passed through without the author prefix.
func formatMessageContent(authorName, content string) string {
	if strings.HasPrefix(content, "/") {
		return content
	}
	return fmt.Sprintf("%s: %s", authorName, content)
}
