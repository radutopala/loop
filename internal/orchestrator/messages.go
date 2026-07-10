package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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

	triggered := msg.IsBotMention || msg.IsReplyToBot || msg.HasPrefix || msg.IsDM

	// Apply permission gate before persisting IsTriggered so denied messages
	// land as plain history rows and never enter the drain queue.
	allowed := true
	if triggered && !o.bot.IsBotUser(msg.AuthorID) && msg.Platform != types.PlatformLocal {
		cfgPerms := o.configPermissionsFor(channel.DirPath)
		role := resolveRole(cfgPerms, channel.Permissions, msg.AuthorID, msg.AuthorRoles)
		if role == "" {
			o.logger.Info("message denied by permissions", "channel_id", msg.ChannelID, "author_id", msg.AuthorID)
			allowed = false
		}
	}

	if err := o.store.InsertMessage(ctx, &db.Message{
		ChatID:      channel.ID,
		ChannelID:   msg.ChannelID,
		MsgID:       msgID,
		AuthorID:    msg.AuthorID,
		AuthorName:  msg.AuthorName,
		Content:     msg.Content,
		IsTriggered: triggered && allowed,
		Priority:    msg.Priority,
		Mode:        msg.Mode,
		CreatedAt:   msg.Timestamp,
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
			Priority:   msg.Priority,
		})
	}

	o.logger.Info("incoming message",
		"channel_id", msg.ChannelID,
		"platform", msg.Platform,
		"author", msg.AuthorName,
		"content", msg.Content,
		"triggered", triggered,
	)

	if !triggered || !allowed {
		return
	}

	// Spawn the drain asynchronously so HandleMessage returns once the row
	// has been persisted. Callers (HTTP interrupt path, bot adapters) rely
	// on insert-then-return ordering — e.g. the interrupt flow inserts X
	// then cancels the active run, and that ordering only works if the
	// insert here is observably complete before HandleMessage returns.
	// The per-channel mutex inside drainChannel serialises concurrent
	// drains, so multiple spawns are safe.
	o.drainAsync(msg.ChannelID, msg)
}

// ResumeChannel drains pending triggered messages for a channel without a
// matching live IncomingMessage. Used during daemon startup to resume rows
// that were left unprocessed when the previous run exited.
func (o *Orchestrator) ResumeChannel(_ context.Context, channelID string) {
	o.drainAsync(channelID, nil)
}

// drainAsync hands the drain to drainSpawn. Production wraps it in a tracked
// goroutine; tests run it inline. The detached background context is
// intentional in the goroutine case — the drain can outlive the request that
// triggered it (e.g. a long agent run started from a short HTTP call).
func (o *Orchestrator) drainAsync(channelID string, incoming *bot.IncomingMessage) {
	o.drainSpawn(func() {
		o.drainChannel(context.Background(), channelID, incoming)
	})
}

// drainChannel pulls and processes triggered messages for one channel in
// priority order. Within a channel only one drain runs at a time (per-channel
// mutex). Across channels drains run independently — channel B's drain is not
// blocked by channel A's in-flight agent run.
//
// incoming may be nil when called from a path other than a fresh inbound
// message (e.g. startup resume). When non-nil, its bot-side fields are
// merged into the claimed row in processClaimedMessage if msg_ids match.
func (o *Orchestrator) drainChannel(ctx context.Context, channelID string, incoming *bot.IncomingMessage) {
	lockVal, _ := o.channelLocks.LoadOrStore(channelID, &sync.Mutex{})
	lock := lockVal.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	for {
		// Pause the drain while the channel is parked on an ExitPlanMode or
		// AskUserQuestion card. The agent presented a plan or set of
		// questions; any messages queued behind the trigger (or newly typed
		// after) wait for the user to resolve via
		// POST /api/channels/{id}/plan/resolve or /ask/resolve, which clears
		// the corresponding flag.
		if o.IsChannelPlanned(channelID) || o.IsChannelAsked(channelID) {
			return
		}

		row, err := o.store.ClaimNextPending(ctx, channelID)
		if err != nil {
			o.logger.Error("claiming next pending message", "error", err, "channel_id", channelID)
			return
		}
		if row == nil {
			return
		}

		o.processClaimedMessage(ctx, row, incoming)

		if err := o.store.ReleaseRunningMessage(ctx, row.ID, true); err != nil {
			o.logger.Error("releasing running message", "error", err, "channel_id", channelID, "id", row.ID)
		}
	}
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

// processClaimedMessage runs the agent on a row already claimed
// (is_running=1) by drainChannel. The row is the source of truth for
// AuthorID/Content/Mode/MsgID; incoming carries the bot-side fields
// (Platform, IsBotMention …) when the row matches the inbound message.
// For rows claimed from earlier inserts (e.g. priority-bumped interrupts
// or restart resume), incoming may be a synthesized minimal message.
func (o *Orchestrator) processClaimedMessage(ctx context.Context, row *db.Message, incoming *bot.IncomingMessage) {
	msg := &bot.IncomingMessage{
		ChannelID:  row.ChannelID,
		AuthorID:   row.AuthorID,
		AuthorName: row.AuthorName,
		Content:    row.Content,
		MessageID:  row.MsgID,
		Mode:       row.Mode,
		Priority:   row.Priority,
		Timestamp:  row.CreatedAt,
	}
	if incoming != nil && incoming.MessageID == row.MsgID {
		msg.GuildID = incoming.GuildID
		msg.Platform = incoming.Platform
		msg.IsBotMention = incoming.IsBotMention
		msg.IsReplyToBot = incoming.IsReplyToBot
		msg.HasPrefix = incoming.HasPrefix
		msg.IsDM = incoming.IsDM
		msg.AuthorRoles = incoming.AuthorRoles
	}

	req, recent, channel, err := o.prepareAgentRequest(ctx, msg)
	if err != nil {
		return
	}

	// Send stop button (non-fatal if it fails)
	stopMsgID, stopErr := o.bot.SendStopButton(ctx, msg.ChannelID, msg.ChannelID)
	if stopErr != nil {
		o.logger.Error("sending stop button", "error", stopErr, "channel_id", msg.ChannelID)
	}
	defer func() {
		o.activeRuns.Delete(msg.ChannelID)
		o.activeRunMsgIDs.Delete(msg.ChannelID)
		if stopMsgID != "" {
			if err := o.bot.RemoveStopButton(ctx, msg.ChannelID, stopMsgID); err != nil {
				o.logger.Error("removing stop button", "error", err, "channel_id", msg.ChannelID)
			}
		}
	}()

	o.activeRunMsgIDs.Store(msg.ChannelID, msg.MessageID)

	typingCtx, stopTyping := context.WithCancel(ctx)
	defer stopTyping()
	go o.refreshTyping(typingCtx, msg.ChannelID)

	resp, lastStreamedText, runID, finish, err := o.executeAgentRun(ctx, msg, req, channel)
	if err != nil {
		// Mark the trigger message as processed even on error/stop so the
		// frontend doesn't keep showing it as "processing" when the next
		// queued message starts. Order matters: messages.processed must land
		// at the FE BEFORE agent.status non-running, otherwise the
		// refetchHead the FE kicks off on status non-running can race the
		// is_processed=1 DB write and overwrite the locally-applied flip
		// with stale fetched rows.
		o.markTriggerProcessed(ctx, msg, recent)
		if finish != nil && o.events != nil {
			trigger := ""
			if o.bot.IsBotUser(msg.AuthorID) {
				trigger = "bot"
			}
			o.events.BroadcastAgentStatus(msg.ChannelID, events.AgentStatusEventData{
				Status:  finish.status,
				RunID:   runID,
				Error:   finish.errMsg,
				Trigger: trigger,
				MsgID:   msg.MessageID,
			})
		}
		return
	}

	o.deliverResponse(ctx, msg, resp, recent, lastStreamedText, runID)
}

// runFinishStatus carries the deferred agent.status broadcast info from
// executeAgentRun back to processClaimedMessage, which fires it AFTER
// markTriggerProcessed so the FE sees messages.processed before the
// refetchHead triggered by the status event.
type runFinishStatus struct {
	status string
	errMsg string
}

// prepareAgentRequest fetches recent messages and channel data, then builds an AgentRequest.
func (o *Orchestrator) prepareAgentRequest(ctx context.Context, msg *bot.IncomingMessage) (*agent.AgentRequest, []*db.Message, *db.Channel, error) {
	recent, err := o.store.GetRecentMessages(ctx, msg.ChannelID, recentMessageLimit)
	if err != nil {
		o.logger.Error("getting recent messages", "error", err, "channel_id", msg.ChannelID)
		return nil, nil, nil, err
	}

	channel, err := o.store.GetChannel(ctx, msg.ChannelID)
	if err != nil {
		o.logger.Error("getting channel", "error", err, "channel_id", msg.ChannelID)
		return nil, nil, nil, err
	}
	if channel == nil {
		// Channel was deleted between message enqueue and drain — nothing
		// useful to do; surface as an error so the caller marks the message
		// processed and the drain loop moves on instead of dereferencing nil.
		return nil, nil, nil, fmt.Errorf("channel %s not found", msg.ChannelID)
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
	if channel.Worktree && channel.DirPath != "" {
		dirHint := fmt.Sprintf(
			"IMPORTANT: Your working directory is %s. Always use absolute paths under this directory for all file operations.",
			channel.DirPath,
		)
		req.Prompt = dirHint + "\n\n" + req.Prompt
	}

	return req, recent, channel, nil
}

// executeAgentRun runs the agent with timeout, streaming, and stop-button cancellation.
// Returns the agent response and the last streamed text (for dedup), or an error if the
// run failed and the caller should abort. The finish field carries the deferred
// agent.status non-running broadcast (completed/error); the caller fires it AFTER
// markTriggerProcessed so the FE sees messages.processed before refetchHead.
func (o *Orchestrator) executeAgentRun(ctx context.Context, msg *bot.IncomingMessage, req *agent.AgentRequest, channel *db.Channel) (*agent.AgentResponse, string, string, *runFinishStatus, error) {
	chatID := int64(0)
	if channel != nil {
		chatID = channel.ID
	}
	cfg := o.currentConfig()
	runCtx, runCancel := context.WithTimeout(ctx, cfg.ContainerTimeout)
	defer runCancel()

	runID := randutil.HexID(8)

	// When the trigger comes from the bot itself (e.g. an agent posting via the
	// send_message/create_thread MCP tools re-entering HandleMessage), tag the
	// broadcasts so the renderer can suppress the dock bounce — these are
	// indirect chains, not user-actionable like a real human reply.
	trigger := ""
	if o.bot.IsBotUser(msg.AuthorID) {
		trigger = "bot"
	}

	// Register the cancel func so stop button clicks can cancel this run.
	o.activeRuns.Store(msg.ChannelID, runCancel)

	// Set when the agent emits ExitPlanMode mid-turn. The run is cancelled at
	// the matching tool_result (see gateToolUses) so the plan card lands as
	// the end-of-turn artifact instead of the agent continuing past it under
	// --dangerously-skip-permissions; this flag makes the resulting
	// context.Canceled report as a clean `completed`, not an error.
	var selfInitiatedPlan atomic.Bool

	// Set when the agent emits AskUserQuestion mid-turn. Cancelled at the
	// matching tool_result (see gateToolUses) so the ask card is the
	// end-of-turn artifact; the user's answer arrives via the /ask/resolve
	// endpoint as a priority-bumped continuation message.
	var selfInitiatedAsk atomic.Bool

	// pendingTaskCreates pairs an OnToolUse for TaskCreate with the matching
	// OnToolResult so we can extract the harness-assigned id (only present
	// in the result text) and surface the new task to the FE.
	var pendingTaskCreates sync.Map // map[toolUseID]inputJSON string

	// gateToolUses holds the toolUseIDs of AskUserQuestion / ExitPlanMode
	// calls whose card has been surfaced. The run is cancelled when the
	// MATCHING tool_result arrives — not at the tool_use — so the
	// permission_prompt deny result is persisted into the session transcript
	// first. Cancelling at the tool_use left a dangling tool_use in the
	// session, which the model read as an interrupted attempt and retried
	// after resume (re-asking the same question).
	var gateToolUses sync.Map // map[toolUseID]struct{}

	tracker := newStreamTracker(func(text string) {
		if err := o.bot.SendMessage(ctx, &bot.OutgoingMessage{
			ChannelID:        msg.ChannelID,
			Content:          text,
			ReplyToMessageID: msg.MessageID,
		}); err != nil {
			o.logger.Error("streaming send failed", "error", err, "channel_id", msg.ChannelID)
		}
		storeBotMessage(ctx, o.store, o.events, msg.ChannelID, text, msg.MessageID)
	})
	req.OnTurn = tracker.OnTurn
	if o.events != nil {
		req.OnToolUse = func(toolUseID, name, input string) {
			storeAgentEvent(ctx, o.store, chatID, msg.ChannelID, &db.Message{
				Kind:         db.MessageKindToolUse,
				ToolUseID:    toolUseID,
				ToolName:     name,
				Content:      input,
				TriggerMsgID: msg.MessageID,
			}, o.logger.Warn)
			o.events.BroadcastToolUse(msg.ChannelID, events.ToolUseEventData{
				ToolUseID: toolUseID,
				ToolName:  name,
				Input:     input,
			})
			if name == "AskUserQuestion" {
				var data events.AskUserQuestionEventData
				if err := json.Unmarshal([]byte(input), &data); err == nil && len(data.Questions) > 0 {
					// Park before broadcasting so the drain loop sees the
					// flag the moment the run wraps up. The cancel happens
					// when this call's tool_result (the permission_prompt
					// deny) lands — see OnToolResult — so the resolution is
					// persisted in the session before teardown and the model
					// doesn't see a dangling, retryable attempt on resume.
					o.markAskedChannel(msg.ChannelID, data)
					o.events.BroadcastAskUser(msg.ChannelID, data)
					selfInitiatedAsk.Store(true)
					gateToolUses.Store(toolUseID, struct{}{})
				}
			}
			if name == "ExitPlanMode" {
				var data events.ExitPlanModeEventData
				if err := json.Unmarshal([]byte(input), &data); err == nil && data.Plan != "" {
					// Park the channel before broadcasting so the drain
					// loop sees the flag the moment the run wraps up
					// (relevant for the user-picked-plan-pill path, where
					// the agent halts naturally and the drain races back
					// to claim any queued sibling messages).
					o.markPlannedChannel(msg.ChannelID, data)
					o.events.BroadcastExitPlan(msg.ChannelID, data)
					// Cancelled at the matching tool_result (the
					// permission_prompt deny), like AskUserQuestion above, so
					// the plan card is the end-of-turn artifact and the agent
					// cannot execute the plan before the user approves it.
					// The user's approval arrives via /plan/resolve.
					selfInitiatedPlan.Store(true)
					gateToolUses.Store(toolUseID, struct{}{})
				}
			}
			if name == "TaskUpdate" {
				if list, ok := o.tasks.applyUpdate(msg.ChannelID, input); ok {
					o.events.BroadcastAgentTasks(msg.ChannelID, events.AgentTasksEventData{Tasks: list})
				}
			}
			if name == "TaskCreate" {
				pendingTaskCreates.Store(toolUseID, input)
			}
		}
		req.OnThinking = func(text string) {
			storeAgentEvent(ctx, o.store, chatID, msg.ChannelID, &db.Message{
				Kind:         db.MessageKindThinking,
				Content:      text,
				TriggerMsgID: msg.MessageID,
			}, o.logger.Warn)
			o.events.BroadcastAgentThinking(msg.ChannelID, events.AgentThinkingEventData{Text: text})
		}
		req.OnToolResult = func(toolUseID, output string, isError bool) {
			storeAgentEvent(ctx, o.store, chatID, msg.ChannelID, &db.Message{
				Kind:         db.MessageKindToolResult,
				ToolUseID:    toolUseID,
				Content:      output,
				IsError:      isError,
				TriggerMsgID: msg.MessageID,
			}, o.logger.Warn)
			if inputRaw, ok := pendingTaskCreates.LoadAndDelete(toolUseID); ok && !isError {
				if list, ok := o.tasks.applyCreate(msg.ChannelID, inputRaw.(string), output); ok {
					o.events.BroadcastAgentTasks(msg.ChannelID, events.AgentTasksEventData{Tasks: list})
				}
			}
			// The gate tool's deny result is now persisted in the session —
			// stop the run so the ask/plan card is the end-of-turn artifact.
			if _, ok := gateToolUses.LoadAndDelete(toolUseID); ok {
				runCancel()
			}
			o.events.BroadcastToolResult(msg.ChannelID, events.ToolResultEventData{
				ToolUseID: toolUseID,
				Output:    output,
				IsError:   isError,
			})
		}
		req.OnActivity = func(activity, detail string) {
			data := events.AgentActivityEventData{Activity: activity}
			if activity == "model" {
				data.Model = detail
			} else {
				data.Description = detail
			}
			if activity == "compacting" {
				storeAgentEvent(ctx, o.store, chatID, msg.ChannelID, &db.Message{
					Kind:         db.MessageKindCompacting,
					TriggerMsgID: msg.MessageID,
				}, o.logger.Warn)
			}
			o.events.BroadcastAgentActivity(msg.ChannelID, data)
		}
	}

	if o.events != nil {
		o.events.BroadcastAgentStatus(msg.ChannelID, events.AgentStatusEventData{Status: "running", RunID: runID, TriggerContent: msg.Content, Trigger: trigger, MsgID: msg.MessageID})
	}

	resp, err := o.runner.Run(runCtx, req)
	if err != nil {
		if selfInitiatedPlan.Load() {
			o.logger.Info("run stopped for self-initiated plan mode", "channel_id", msg.ChannelID)
			return nil, "", runID, &runFinishStatus{status: "completed"}, err
		}
		if selfInitiatedAsk.Load() {
			o.logger.Info("run stopped for AskUserQuestion", "channel_id", msg.ChannelID)
			return nil, "", runID, &runFinishStatus{status: "completed"}, err
		}
		if runCtx.Err() == context.Canceled {
			o.logger.Info("run stopped by user", "channel_id", msg.ChannelID)
			_ = o.bot.SendMessage(ctx, &bot.OutgoingMessage{
				ChannelID:        msg.ChannelID,
				Content:          "Run stopped.",
				ReplyToMessageID: msg.MessageID,
			})
			return nil, "", runID, &runFinishStatus{status: "error", errMsg: err.Error()}, err
		}
		o.logger.Error("running agent", "error", err, "channel_id", msg.ChannelID)
		if notice, scheduled := o.maybeScheduleSessionLimitRetry(ctx, msg, err.Error()); scheduled {
			_ = o.bot.SendMessage(ctx, &bot.OutgoingMessage{
				ChannelID:        msg.ChannelID,
				Content:          notice,
				ReplyToMessageID: msg.MessageID,
			})
			storeBotMessage(ctx, o.store, o.events, msg.ChannelID, notice, msg.MessageID)
			return nil, "", runID, &runFinishStatus{status: "completed"}, err
		}
		_ = o.bot.SendMessage(ctx, &bot.OutgoingMessage{
			ChannelID:        msg.ChannelID,
			Content:          "Sorry, I encountered an error processing your request.",
			ReplyToMessageID: msg.MessageID,
		})
		return nil, "", runID, &runFinishStatus{status: "error", errMsg: err.Error()}, err
	}

	if resp.Error != "" {
		o.logger.Error("agent returned error", "error", resp.Error, "channel_id", msg.ChannelID)
		_ = o.bot.SendMessage(ctx, &bot.OutgoingMessage{
			ChannelID:        msg.ChannelID,
			Content:          fmt.Sprintf("Agent error: %s", resp.Error),
			ReplyToMessageID: msg.MessageID,
		})
		return nil, "", runID, &runFinishStatus{status: "error", errMsg: resp.Error}, fmt.Errorf("agent error: %s", resp.Error)
	}

	return resp, tracker.lastText, runID, nil, nil
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
		storeBotMessage(ctx, o.store, o.events, msg.ChannelID, resp.Response, msg.MessageID)
	}

	// Mark the trigger and any older non-queued history as processed. Skip
	// rows that are still queued (IsTriggered && !IsProcessed) other than the
	// trigger itself — a priority-bumped interrupt can run ahead of an older
	// queued row, and clearing those would drop them from the drain queue.
	toMark := recent
	for i, m := range recent {
		if m.MsgID == msg.MessageID {
			toMark = recent[i:]
			break
		}
	}
	ids := make([]int64, 0, len(toMark))
	msgIDs := make([]string, 0, len(toMark))
	for _, m := range toMark {
		if m.MsgID != msg.MessageID && m.IsTriggered && !m.IsProcessed {
			continue
		}
		ids = append(ids, m.ID)
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
		trigger := ""
		if o.bot.IsBotUser(msg.AuthorID) {
			trigger = "bot"
		}
		o.events.BroadcastAgentStatus(msg.ChannelID, events.AgentStatusEventData{
			Status:     "completed",
			RunID:      runID,
			DurationMs: resp.DurationMs,
			NumTurns:   resp.NumTurns,
			StopReason: resp.StopReason,
			Model:      resp.Model,
			Trigger:    trigger,
			MsgID:      msg.MessageID,
		})
	}
	// Match the FE's "clear on status=completed" so the next turn starts
	// from an empty list; without this the registry would grow unbounded
	// per channel across runs.
	o.tasks.clear(msg.ChannelID)
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
	ids := make([]int64, 0, len(toMark))
	msgIDs := make([]string, 0, len(toMark))
	for _, m := range toMark {
		if m.MsgID != msg.MessageID && m.IsTriggered && !m.IsProcessed {
			continue
		}
		ids = append(ids, m.ID)
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
