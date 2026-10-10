package orchestrator

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/types"
)

// The author of the message that starts a plugin's agent run. The id tags
// the run's agent.status events with the "plugin" trigger (see runTrigger)
// and is the only author a plugin thread runs (see the plugin hiddenKind).
const pluginAuthorID = "loop-plugin"

// pluginSystemPrompt is the system prompt of a plugin's agent run; %s is
// the plugin's id.
const pluginSystemPrompt = "You are an agent the Loop plugin %q started. Do the task its prompt gives you; " +
	"its tools are the MCP tools named plugin_*. Nobody reads your replies as a chat: " +
	"when the task is done, end with a short summary."

// PluginRun is an agent run a plugin asked for.
type PluginRun struct {
	PluginID string
	// Agent is the id of the plugin's agent the run is, ReadOnly whether
	// it's a read-only one.
	Agent    string
	ReadOnly bool
	Prompt   string
	// Model and Effort override the channel's when set.
	Model  string
	Effort string
	// Tools are the plugin's tools the agent gets.
	Tools []plugin.Tool

	// threadID is the plugin's hidden thread the run is queued in.
	threadID string
}

// ActivePluginRun is the plugin run going on in a plugin's hidden thread.
type ActivePluginRun struct {
	// ID is the run's id, as StartPluginRun returned it.
	ID  string
	Run PluginRun
}

// StartPluginRun queues run in parent's hidden thread for the plugin,
// created on first use, and returns the run's id and the thread's. The
// plugin's runs in parent take turns on that thread's queue.
func (o *Orchestrator) StartPluginRun(ctx context.Context, parent *db.Channel, run PluginRun) (runID, threadID string, err error) {
	h, err := o.ensureHiddenThread(ctx, parent, db.PluginKind(run.PluginID))
	if err != nil {
		return "", "", err
	}
	runID = generateMessageID()
	run.threadID = h.ChannelID
	o.pluginRuns.Store(runID, &run)
	o.logger.Info("plugin: run queued", "plugin", run.PluginID, "agent", run.Agent, "channel_id", parent.ChannelID, "thread_id", h.ChannelID, "run_id", runID)
	o.HandleMessage(ctx, &bot.IncomingMessage{
		ChannelID:  h.ChannelID,
		GuildID:    h.GuildID,
		MessageID:  runID,
		AuthorID:   pluginAuthorID,
		AuthorName: run.PluginID,
		Content:    run.Prompt,
		HasPrefix:  true,
		Platform:   types.PlatformLocal,
		Timestamp:  o.timeNow().UTC(),
	})
	return runID, h.ChannelID, nil
}

// ActivePluginRun returns the plugin run going on in hidden thread
// threadID, if any.
func (o *Orchestrator) ActivePluginRun(threadID string) (ActivePluginRun, bool) {
	v, ok := o.activePluginRuns.Load(threadID)
	if !ok {
		return ActivePluginRun{}, false
	}
	return v.(ActivePluginRun), true
}

// admitPluginRun refuses a message in plugin thread threadID that isn't a
// plugin run StartPluginRun queued there: a plugin thread runs nothing
// else.
func (o *Orchestrator) admitPluginRun(threadID string, msg *bot.IncomingMessage) error {
	if msg.AuthorID == pluginAuthorID {
		if v, ok := o.pluginRuns.Load(msg.MessageID); ok && v.(*PluginRun).threadID == threadID {
			return nil
		}
	}
	return fmt.Errorf("plugin thread %s: only a plugin's own agent runs run here", threadID)
}

// applyPluginRequest shapes req for the plugin run msg starts, admitted by
// admitPluginRun, and marks it active in msg's thread. Each run starts a
// session of its own.
func (o *Orchestrator) applyPluginRequest(req *agent.AgentRequest, msg *bot.IncomingMessage, parent *db.Channel) error {
	v, _ := o.pluginRuns.Load(msg.MessageID)
	run := v.(*PluginRun)
	tools, err := json.Marshal(run.Tools)
	if err != nil {
		return fmt.Errorf("encoding the plugin's tools: %w", err)
	}
	req.AgentID = agent.PluginAgentID(run.PluginID, run.Agent)
	req.ReadOnly = run.ReadOnly
	req.SessionID, req.ForkSession, req.ResumeAt = "", false, ""
	req.Model = cmp.Or(run.Model, parent.ModelOverride)
	req.Effort = cmp.Or(run.Effort, parent.EffortOverride)
	req.SystemPrompt = fmt.Sprintf(pluginSystemPrompt, run.PluginID)
	req.MCPArgs = []string{"--plugin-id", run.PluginID, "--plugin-tools", string(tools)}
	o.activePluginRuns.Store(msg.ChannelID, ActivePluginRun{ID: msg.MessageID, Run: *run})
	return nil
}

// pluginRunDone forgets the plugin run msg started, once it has ended or
// was dropped.
func (o *Orchestrator) pluginRunDone(msg *bot.IncomingMessage) {
	if msg.AuthorID != pluginAuthorID {
		return
	}
	o.pluginRuns.Delete(msg.MessageID)
	if a, ok := o.ActivePluginRun(msg.ChannelID); ok && a.ID == msg.MessageID {
		o.activePluginRuns.Delete(msg.ChannelID)
	}
}
