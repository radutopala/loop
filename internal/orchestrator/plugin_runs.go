package orchestrator

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/plugin/proxy"
	"github.com/radutopala/loop/internal/plugin/runtime"
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

	// tools is the JSON of Tools; threadID is the plugin's hidden thread
	// the run is queued in, parent the channel it's a thread of.
	tools    string
	threadID string
	parent   *db.Channel
}

// PluginRunStore keeps plugin runs from when they're queued until they
// end, so a restart can pick them up.
type PluginRunStore interface {
	InsertPluginRun(ctx context.Context, r *db.PluginRun) error
	DeletePluginRun(ctx context.Context, runID string) error
	ListPluginRuns(ctx context.Context) ([]*db.PluginRun, error)
}

// SetPluginRunStore configures where plugin runs are kept; left unset,
// they're kept in memory only and a restart loses them.
func (o *Orchestrator) SetPluginRunStore(s PluginRunStore) {
	o.pluginRunStore = s
}

// PluginRunNotifier tells a plugin's container in ch that its agent run
// runID ended.
type PluginRunNotifier interface {
	RunDone(ctx context.Context, pluginID string, ch runtime.Channel, runID string, req proxy.RunDoneRequest) error
}

// SetPluginRunNotifier configures what tells plugins their agent runs
// ended; left unset, they aren't told.
func (o *Orchestrator) SetPluginRunNotifier(n PluginRunNotifier) {
	o.pluginNotifier = n
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
	tools, err := json.Marshal(run.Tools)
	if err != nil {
		return "", "", fmt.Errorf("encoding the plugin's tools: %w", err)
	}
	runID = generateMessageID()
	run.tools, run.threadID, run.parent = string(tools), h.ChannelID, parent
	if o.pluginRunStore != nil {
		if err := o.pluginRunStore.InsertPluginRun(ctx, &db.PluginRun{
			RunID: runID, PluginID: run.PluginID, Agent: run.Agent, ReadOnly: run.ReadOnly, Prompt: run.Prompt,
			Model: run.Model, Effort: run.Effort, Tools: run.tools, ThreadID: h.ChannelID, ChannelID: parent.ChannelID,
		}); err != nil {
			return "", "", fmt.Errorf("storing the run: %w", err)
		}
	}
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
	req.AgentID = agent.PluginAgentID(run.PluginID, run.Agent)
	req.ReadOnly = run.ReadOnly
	req.SessionID, req.ForkSession, req.ResumeAt = "", false, ""
	req.Model = cmp.Or(run.Model, parent.ModelOverride)
	req.Effort = cmp.Or(run.Effort, parent.EffortOverride)
	req.SystemPrompt = fmt.Sprintf(pluginSystemPrompt, run.PluginID)
	req.MCPArgs = []string{"--plugin-id", run.PluginID, "--plugin-tools", run.tools}
	o.activePluginRuns.Store(msg.ChannelID, ActivePluginRun{ID: msg.MessageID, Run: *run})
	return nil
}

// pluginRunDone forgets the plugin run msg started, once it has ended or
// was dropped, and tells the plugin, failed when runErr is set. A run the
// shutdown cut short stays stored: the plugin couldn't be told before Loop
// exits, so RecoverPluginRuns fails it on the next start.
func (o *Orchestrator) pluginRunDone(ctx context.Context, msg *bot.IncomingMessage, runErr error) {
	if msg.AuthorID != pluginAuthorID {
		return
	}
	v, ok := o.pluginRuns.LoadAndDelete(msg.MessageID)
	if a, active := o.ActivePluginRun(msg.ChannelID); active && a.ID == msg.MessageID {
		o.activePluginRuns.Delete(msg.ChannelID)
	}
	if ok && !o.stopping() {
		o.endPluginRun(ctx, msg.MessageID, v.(*PluginRun), runErr)
	}
}

// errPluginRunInterrupted fails the plugin runs Loop stopped in the middle
// of.
var errPluginRunInterrupted = errors.New("loop stopped before the run ended")

// RecoverPluginRuns picks up the plugin runs the prior daemon run left:
// those whose message still waits in their thread's queue resume with it,
// the rest fail and their plugin is told. Call it at startup after
// ResetStaleRunningMessages, before the queues resume.
func (o *Orchestrator) RecoverPluginRuns(ctx context.Context) {
	if o.pluginRunStore == nil {
		return
	}
	runs, err := o.pluginRunStore.ListPluginRuns(ctx)
	if err != nil {
		o.logger.Error("plugin: listing the runs to recover", "error", err)
		return
	}
	for _, r := range runs {
		parent, err := o.store.GetChannel(ctx, r.ChannelID)
		if err != nil {
			o.logger.Error("plugin: looking up a run's channel", "run_id", r.RunID, "channel_id", r.ChannelID, "error", err)
			continue
		}
		run := &PluginRun{
			PluginID: r.PluginID, Agent: r.Agent, ReadOnly: r.ReadOnly, Prompt: r.Prompt, Model: r.Model, Effort: r.Effort,
			tools: r.Tools, threadID: r.ThreadID, parent: parent,
		}
		// Only StartPluginRun writes the column, from Tools.
		_ = json.Unmarshal([]byte(r.Tools), &run.Tools)
		if r.Queued && parent != nil {
			o.pluginRuns.Store(r.RunID, run)
			o.logger.Info("plugin: run resumes", "plugin", r.PluginID, "run_id", r.RunID, "thread_id", r.ThreadID)
			continue
		}
		o.logger.Warn("plugin: run interrupted by the prior daemon run", "plugin", r.PluginID, "run_id", r.RunID, "thread_id", r.ThreadID)
		o.endPluginRun(ctx, r.RunID, run, errPluginRunInterrupted)
	}
}

// endPluginRun drops the stored run runID and tells its plugin it ended,
// failed when runErr is set; a run whose channel is gone has no container
// to tell. The plugin is told in the background: it may have to start its
// container first.
func (o *Orchestrator) endPluginRun(ctx context.Context, runID string, run *PluginRun, runErr error) {
	if o.pluginRunStore != nil {
		if err := o.pluginRunStore.DeletePluginRun(ctx, runID); err != nil {
			o.logger.Warn("plugin: dropping the ended run", "plugin", run.PluginID, "run_id", runID, "error", err)
		}
	}
	if o.pluginNotifier == nil || run.parent == nil {
		return
	}
	req := proxy.RunDoneRequest{Status: proxy.RunStatusDone}
	if runErr != nil {
		req = proxy.RunDoneRequest{Status: proxy.RunStatusFailed, Error: runErr.Error()}
	}
	ctx = context.WithoutCancel(ctx)
	go func() {
		ch := runtime.Channel{ID: run.parent.ChannelID, DirPath: run.parent.DirPath}
		if err := o.pluginNotifier.RunDone(ctx, run.PluginID, ch, runID, req); err != nil {
			o.logger.Warn("plugin: telling the plugin its run ended", "plugin", run.PluginID, "run_id", runID, "error", err)
		}
	}()
}
