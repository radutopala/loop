package install

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/radutopala/loop/internal/agentgate"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/plugin/proxy"
	"github.com/radutopala/loop/internal/plugin/runtime"
	"github.com/radutopala/loop/internal/scheduler"
	"github.com/radutopala/loop/internal/types"
)

var (
	// ErrNotInstalled is returned for a plugin that isn't installed.
	ErrNotInstalled = errors.New("plugin is not installed")
	// ErrDigestChanged is returned when the image an install pulls is not
	// the one the owner inspected and approved.
	ErrDigestChanged = errors.New("the image changed since it was inspected; inspect it again")
	// ErrNotApproved is returned when the owner didn't approve every
	// capability a plugin asks for.
	ErrNotApproved = errors.New("capability not approved")
	// ErrUnknownAction is returned for an action the plugin's manifest
	// doesn't declare.
	ErrUnknownAction = errors.New("the plugin has no such action")
	// ErrNotRunnable is returned for a plugin installed but not runnable,
	// such as a disabled one.
	ErrNotRunnable = errors.New("the plugin is not runnable")
	// ErrUnknownAgent is returned for an agent the plugin's manifest
	// doesn't declare.
	ErrUnknownAgent = errors.New("the plugin has no such agent")
	// ErrUnknownTool is returned for a tool the plugin's manifest doesn't
	// declare, or the calling agent wasn't given.
	ErrUnknownTool = errors.New("no such tool")
	// ErrInvalidInput is returned for a tool call whose input doesn't match
	// the tool's input schema.
	ErrInvalidInput = errors.New("invalid tool input")
	// ErrNotEnabled is returned for a plugin the channel's project doesn't
	// enable, whose container therefore doesn't start there.
	ErrNotEnabled = errors.New("the plugin is not enabled in this project")
)

// Enabled reports whether the plugin pluginID is enabled in the project of
// the channel ch.
type Enabled func(ctx context.Context, pluginID string, ch runtime.Channel) (bool, error)

// Runtime runs installed plugins' containers.
type Runtime interface {
	Ensure(ctx context.Context, p runtime.Plugin, ch runtime.Channel) (runtime.Endpoint, error)
	StopPlugin(ctx context.Context, pluginID string)
	Stop(ctx context.Context, pluginID, channelID string)
	Remove(ctx context.Context, pluginID, channelID string, purge bool) error
	StopIdle(ctx context.Context) bool
	Running() []runtime.Running
	Audit(pluginID, channelID string, limit int) ([]agentgate.AuditEntry, error)
}

// ServiceStore is the store the Service reads plugins and their containers
// from, and keeps their actions' scheduled tasks in.
type ServiceStore interface {
	Store
	ListPluginStates(ctx context.Context) ([]*db.PluginState, error)
	ListPluginContainers(ctx context.Context, pluginID string) ([]*db.PluginContainer, error)
	ListChannels(ctx context.Context) ([]*db.Channel, error)
	SeedPluginTask(ctx context.Context, task *db.ScheduledTask) (bool, error)
	SetPluginTasksPaused(ctx context.Context, pluginID, channelID string, paused bool) error
}

// Caller calls into a running plugin.
type Caller interface {
	Action(ctx context.Context, ep runtime.Endpoint, action string, req proxy.ActionRequest) error
	Tool(ctx context.Context, ep runtime.Endpoint, tool string, req proxy.ToolRequest) (*proxy.ToolResult, error)
	RunDone(ctx context.Context, ep runtime.Endpoint, runID string, req proxy.RunDoneRequest) error
	X(ctx context.Context, ep runtime.Endpoint, req proxy.XRequest) (*proxy.XResponse, error)
	Event(ctx context.Context, ep runtime.Endpoint, evt proxy.Event) error
}

// AgentRun is an agent run a plugin asks for: the agent and the tools it
// gets.
type AgentRun struct {
	Agent plugin.Agent
	Tools []plugin.Tool
}

// Service installs, upgrades and removes plugins, keeping their running
// containers in step, and runs their actions.
type Service struct {
	installer *Installer
	runtime   Runtime
	store     ServiceStore
	caller    Caller
	enabled   Enabled
	now       func() time.Time
}

// NewService returns a Service that starts a plugin's container only in
// the channels whose project enables it.
func NewService(installer *Installer, runtime Runtime, store ServiceStore, caller Caller, enabled Enabled) *Service {
	return &Service{installer: installer, runtime: runtime, store: store, caller: caller, enabled: enabled, now: time.Now}
}

// List returns the installed plugins.
func (s *Service) List(ctx context.Context) ([]*db.PluginState, error) {
	return s.store.ListPluginStates(ctx)
}

// Containers returns the plugin's containers, one per channel it ran in.
func (s *Service) Containers(ctx context.Context, pluginID string) ([]*db.PluginContainer, error) {
	return s.store.ListPluginContainers(ctx, pluginID)
}

// Stop stops the plugin's container in channelID, keeping its data.
func (s *Service) Stop(ctx context.Context, pluginID, channelID string) {
	s.runtime.Stop(ctx, pluginID, channelID)
}

// Audit returns up to limit of the gate's records for the plugin's
// container in channelID, newest first.
func (s *Service) Audit(pluginID, channelID string, limit int) ([]agentgate.AuditEntry, error) {
	return s.runtime.Audit(pluginID, channelID, limit)
}

// RemoveChannel removes every plugin's container in a deleted channel,
// with its row, keeping its data dir and gate audit log, and reports
// whether it removed any.
func (s *Service) RemoveChannel(ctx context.Context, channelID string) (bool, error) {
	return s.removeWhere(ctx, func(id string) bool { return id == channelID })
}

// PruneChannels removes the plugin containers of channels that no longer
// exist, as RemoveChannel does.
func (s *Service) PruneChannels(ctx context.Context) error {
	channels, err := s.store.ListChannels(ctx)
	if err != nil {
		return fmt.Errorf("listing channels: %w", err)
	}
	exists := make(map[string]bool, len(channels))
	for _, ch := range channels {
		exists[ch.ChannelID] = true
	}
	_, err = s.removeWhere(ctx, func(id string) bool { return !exists[id] })
	return err
}

// removeWhere removes every plugin's container in the channels match picks.
func (s *Service) removeWhere(ctx context.Context, match func(channelID string) bool) (bool, error) {
	containers, err := s.store.ListPluginContainers(ctx, "")
	if err != nil {
		return false, fmt.Errorf("listing plugin containers: %w", err)
	}
	removed := false
	var errs []error
	for _, c := range containers {
		if match(c.ChannelID) {
			errs = append(errs, s.runtime.Remove(ctx, c.PluginID, c.ChannelID, false))
			removed = true
		}
	}
	return removed, errors.Join(errs...)
}

// StopDisabled stops the containers whose channel's project no longer
// enables their plugin, and reports whether it stopped any. A container
// whose project config can't be read keeps running: its config is
// mid-edit, or broken until the owner fixes it.
func (s *Service) StopDisabled(ctx context.Context) bool {
	stopped := false
	for _, r := range s.runtime.Running() {
		if on, err := s.enabled(ctx, r.PluginID, r.Channel); err != nil || on {
			continue
		}
		s.runtime.Stop(ctx, r.PluginID, r.Channel.ID)
		stopped = true
	}
	return stopped
}

// Run syncs the plugins' scheduled tasks, then, every interval until ctx
// ends, stops the containers left idle and those whose project stopped
// enabling their plugin, calling changed when it stopped any, and syncs the
// tasks again, so a new channel gets its own.
func (s *Service) Run(ctx context.Context, interval time.Duration, changed func(), logger *slog.Logger) {
	sync := func() {
		if err := s.SyncSchedules(ctx); err != nil {
			logger.Warn("syncing plugin schedules", "error", err)
		}
	}
	sync()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			idle := s.runtime.StopIdle(ctx)
			if s.StopDisabled(ctx) || idle {
				changed()
			}
			sync()
		}
	}
}

// SyncSchedules keeps the scheduled tasks of plugin actions in step with
// the projects enabling their plugins. In each channel whose project
// enables a plugin the owner approved schedule for, it creates the task of
// each action with a default_schedule, once, and turns back on the tasks
// it paused; elsewhere it pauses them. Only desktop app channels get them:
// threads run their channel's tasks, and plugins don't run in Slack or
// Discord. A channel whose project config can't be read is left as it is.
func (s *Service) SyncSchedules(ctx context.Context) error {
	states, err := s.store.ListPluginStates(ctx)
	if err != nil {
		return fmt.Errorf("listing the plugins: %w", err)
	}
	var channels []*db.Channel
	var errs []error
	for _, st := range states {
		if !slices.Contains(st.ApprovedCaps, string(plugin.CapSchedule)) {
			continue
		}
		m, err := s.installer.Manifest(st)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", st.PluginID, err))
			continue
		}
		if channels == nil {
			if channels, err = s.store.ListChannels(ctx); err != nil {
				return errors.Join(append(errs, fmt.Errorf("listing the channels: %w", err))...)
			}
		}
		for _, c := range channels {
			if c.ParentID == "" && c.Platform == types.PlatformLocal {
				errs = append(errs, s.syncChannelSchedules(ctx, st, m, c))
			}
		}
	}
	return errors.Join(errs...)
}

// syncChannelSchedules syncs the scheduled tasks of st's actions in c.
func (s *Service) syncChannelSchedules(ctx context.Context, st *db.PluginState, m *plugin.Manifest, c *db.Channel) error {
	on := st.Status == db.PluginStatusInstalled
	if on {
		var err error
		if on, err = s.enabled(ctx, st.PluginID, runtime.Channel{ID: c.ChannelID, DirPath: c.DirPath}); err != nil {
			return nil
		}
	}
	if !on {
		return s.store.SetPluginTasksPaused(ctx, st.PluginID, c.ChannelID, true)
	}
	for _, a := range m.Actions {
		d := a.DefaultSchedule
		if d == nil {
			continue
		}
		if _, err := s.store.SeedPluginTask(ctx, &db.ScheduledTask{
			ChannelID:    c.ChannelID,
			GuildID:      c.GuildID,
			Schedule:     d.Schedule,
			Type:         d.Type,
			Prompt:       fmt.Sprintf("Runs the %s plugin's %s action.", st.PluginID, a.ID),
			Enabled:      d.Enabled,
			NextRunAt:    scheduler.NextRun(d.Type, d.Schedule, s.now()),
			PluginID:     st.PluginID,
			PluginAction: a.ID,
		}); err != nil {
			return fmt.Errorf("creating %s's %s task in %s: %w", st.PluginID, a.ID, c.ChannelID, err)
		}
	}
	return s.store.SetPluginTasksPaused(ctx, st.PluginID, c.ChannelID, false)
}

// Inspect pulls and validates an image so the owner can review what it
// asks for, installing nothing.
func (s *Service) Inspect(ctx context.Context, ref string, dev bool) (*Candidate, error) {
	c, err := s.installer.Inspect(ctx, ref, dev)
	if err != nil {
		return nil, err
	}
	s.installer.Discard(c)
	return c, nil
}

// Install installs, or upgrades to, the image ref with the capabilities the
// owner approved. A non-empty digest must match what the pull resolves to,
// so the owner gets the image they reviewed. An upgrade stops the plugin's
// containers, which start on the new image when next used.
func (s *Service) Install(ctx context.Context, ref string, dev bool, digest string, approved []plugin.Capability) (*Candidate, error) {
	c, err := s.installer.Inspect(ctx, ref, dev)
	if err != nil {
		return nil, err
	}
	if digest != "" && c.Digest != digest {
		s.installer.Discard(c)
		return nil, ErrDigestChanged
	}
	if err := s.installer.Install(ctx, c, approved); err != nil {
		s.installer.Discard(c)
		return nil, err
	}
	if c.Previous != nil {
		s.runtime.StopPlugin(ctx, c.Manifest.ID)
	}
	return c, nil
}

// Remove removes a plugin's containers in every channel, then the plugin;
// purge also removes its data dirs and gate audit logs.
func (s *Service) Remove(ctx context.Context, pluginID string, purge bool) error {
	st, err := s.store.GetPluginState(ctx, pluginID)
	if err != nil {
		return fmt.Errorf("reading the plugin: %w", err)
	}
	if st == nil {
		return ErrNotInstalled
	}
	containers, err := s.store.ListPluginContainers(ctx, pluginID)
	if err != nil {
		return fmt.Errorf("listing the plugin's containers: %w", err)
	}
	var errs []error
	for _, c := range containers {
		errs = append(errs, s.runtime.Remove(ctx, pluginID, c.ChannelID, purge))
	}
	errs = append(errs, s.installer.Remove(ctx, pluginID, purge))
	return errors.Join(errs...)
}

// RunAction starts the plugin's container in ch, if it isn't running, and
// asks it to run action.
func (s *Service) RunAction(ctx context.Context, pluginID, action string, ch runtime.Channel, req proxy.ActionRequest) error {
	st, m, err := s.runnable(ctx, pluginID)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(m.Actions, func(a plugin.Action) bool { return a.ID == action }) {
		return fmt.Errorf("%w: %s", ErrUnknownAction, action)
	}
	ep, err := s.start(ctx, st, m, ch)
	if err != nil {
		return err
	}
	return s.caller.Action(ctx, ep, action, req)
}

// AgentRun returns the run of agentID that the plugin asks for, once the
// owner approved the capability it needs: agent.readonly or agent.write for
// a read-only agent, agent.write for any other.
func (s *Service) AgentRun(ctx context.Context, pluginID, agentID string) (*AgentRun, error) {
	st, m, err := s.runnable(ctx, pluginID)
	if err != nil {
		return nil, err
	}
	a, ok := findAgent(m, agentID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownAgent, agentID)
	}
	write := slices.Contains(st.ApprovedCaps, string(plugin.CapAgentWrite))
	if !write && (!a.ReadOnly || !slices.Contains(st.ApprovedCaps, string(plugin.CapAgentReadOnly))) {
		return nil, fmt.Errorf("%w: the agent %s needs %s", ErrNotApproved, agentID, agentCap(a))
	}
	run := &AgentRun{Agent: a}
	for _, t := range m.Tools {
		if slices.Contains(a.Tools, t.Name) {
			run.Tools = append(run.Tools, t)
		}
	}
	return run, nil
}

// CallTool forwards an agent's call of tool to the plugin's container in
// ch, starting it if it isn't running. The tool must be one req.Agent was
// given, and the input must match its schema, so a bad call fails without
// waking the plugin.
func (s *Service) CallTool(ctx context.Context, pluginID, tool string, ch runtime.Channel, req proxy.ToolRequest) (*proxy.ToolResult, error) {
	st, m, err := s.runnable(ctx, pluginID)
	if err != nil {
		return nil, err
	}
	a, ok := findAgent(m, req.Agent)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownAgent, req.Agent)
	}
	if !slices.Contains(a.Tools, tool) {
		return nil, fmt.Errorf("%w: %s", ErrUnknownTool, tool)
	}
	return s.callTool(ctx, st, m, tool, ch, req)
}

// WorkflowTool forwards a workflow's call of tool, any tool the plugin's
// manifest declares, to the plugin's container in ch, starting it if it
// isn't running. The input must match the tool's schema.
func (s *Service) WorkflowTool(ctx context.Context, pluginID, tool string, ch runtime.Channel, req proxy.ToolRequest) (*proxy.ToolResult, error) {
	st, m, err := s.runnable(ctx, pluginID)
	if err != nil {
		return nil, err
	}
	return s.callTool(ctx, st, m, tool, ch, req)
}

// callTool checks the input of a call of tool against its schema, then
// forwards the call to the plugin's container in ch.
func (s *Service) callTool(ctx context.Context, st *db.PluginState, m *plugin.Manifest, tool string, ch runtime.Channel, req proxy.ToolRequest) (*proxy.ToolResult, error) {
	i := slices.IndexFunc(m.Tools, func(t plugin.Tool) bool { return t.Name == tool })
	if i < 0 {
		return nil, fmt.Errorf("%w: %s", ErrUnknownTool, tool)
	}
	if err := m.Tools[i].ValidateInput(req.Input); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	ep, err := s.start(ctx, st, m, ch)
	if err != nil {
		return nil, err
	}
	return s.caller.Tool(ctx, ep, tool, req)
}

// Allows returns nil when the plugin may run and the owner approved c for
// it. An empty c checks only that it may run.
func (s *Service) Allows(ctx context.Context, pluginID string, c plugin.Capability) error {
	st, _, err := s.runnable(ctx, pluginID)
	if err != nil {
		return err
	}
	if c != "" && !slices.Contains(st.ApprovedCaps, string(c)) {
		return fmt.Errorf("%w: %s", ErrNotApproved, c)
	}
	return nil
}

// Shows reports whether the plugin pluginID may still show in ch what
// capability c lets it: it's installed, may run, has c approved and ch's
// project enables it.
func (s *Service) Shows(ctx context.Context, pluginID string, c plugin.Capability, ch runtime.Channel) (bool, error) {
	err := s.Allows(ctx, pluginID, c)
	switch {
	case errors.Is(err, ErrNotInstalled), errors.Is(err, ErrNotRunnable), errors.Is(err, ErrNotApproved):
		return false, nil
	case err != nil:
		return false, err
	}
	return s.enabled(ctx, pluginID, ch)
}

// RunDone tells the plugin's container in ch, starting it if it isn't
// running, that its agent run runID ended.
func (s *Service) RunDone(ctx context.Context, pluginID string, ch runtime.Channel, runID string, req proxy.RunDoneRequest) error {
	ep, err := s.ensure(ctx, pluginID, ch)
	if err != nil {
		return err
	}
	return s.caller.RunDone(ctx, ep, runID, req)
}

// X forwards a call from the plugin's UI to its own API on its container in
// ch, starting it if it isn't running.
func (s *Service) X(ctx context.Context, pluginID string, ch runtime.Channel, req proxy.XRequest) (*proxy.XResponse, error) {
	ep, err := s.ensure(ctx, pluginID, ch)
	if err != nil {
		return nil, err
	}
	return s.caller.X(ctx, ep, req)
}

// Deliver hands evt, a Loop event in ch or, when thread is set, in that
// thread of ch, to each plugin that subscribes to its type, has
// events.subscribe approved and is enabled by the project, starting its
// container if it isn't running. A thread's event goes to the thread's
// container, the channel's or both, as the plugin's thread_events says. It
// returns each plugin's failure.
func (s *Service) Deliver(ctx context.Context, ch runtime.Channel, thread *runtime.Channel, evt proxy.Event) error {
	states, err := s.store.ListPluginStates(ctx)
	if err != nil {
		return fmt.Errorf("listing plugins: %w", err)
	}
	var errs []error
	for _, st := range states {
		if st.Status != db.PluginStatusInstalled || !slices.Contains(st.ApprovedCaps, string(plugin.CapEventsSub)) {
			continue
		}
		m, err := s.installer.Manifest(st)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", st.PluginID, err))
			continue
		}
		if !slices.Contains(m.Events, evt.Type) {
			continue
		}
		targets := []runtime.Channel{ch}
		if thread != nil {
			toThread, toChannel := m.ThreadEventsTo()
			targets = targets[:0]
			if toThread {
				targets = append(targets, *thread)
			}
			if toChannel {
				targets = append(targets, ch)
			}
		}
		for _, target := range targets {
			if err := s.deliver(ctx, st, m, target, evt); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", st.PluginID, err))
			}
		}
	}
	return errors.Join(errs...)
}

// deliver hands evt to the plugin st's container in ch, unless ch's
// project doesn't enable it.
func (s *Service) deliver(ctx context.Context, st *db.PluginState, m *plugin.Manifest, ch runtime.Channel, evt proxy.Event) error {
	ep, err := s.start(ctx, st, m, ch)
	if errors.Is(err, ErrNotEnabled) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.caller.Event(ctx, ep, evt)
}

// UI returns the dir the plugin's image was extracted to, which holds its
// panels' files, and its manifest.
func (s *Service) UI(ctx context.Context, pluginID string) (string, *plugin.Manifest, error) {
	st, m, err := s.runnable(ctx, pluginID)
	if err != nil {
		return "", nil, err
	}
	return s.installer.Dir(st), m, nil
}

// ensure starts the plugin's container in ch, if it isn't running.
func (s *Service) ensure(ctx context.Context, pluginID string, ch runtime.Channel) (runtime.Endpoint, error) {
	st, m, err := s.runnable(ctx, pluginID)
	if err != nil {
		return runtime.Endpoint{}, err
	}
	return s.start(ctx, st, m, ch)
}

// start starts the container of the plugin st runs in ch, if it isn't
// running and ch's project enables it.
func (s *Service) start(ctx context.Context, st *db.PluginState, m *plugin.Manifest, ch runtime.Channel) (runtime.Endpoint, error) {
	on, err := s.enabled(ctx, m.ID, ch)
	if err != nil {
		return runtime.Endpoint{}, fmt.Errorf("reading the plugin's config: %w", err)
	}
	if !on {
		return runtime.Endpoint{}, ErrNotEnabled
	}
	ep, err := s.runtime.Ensure(ctx, runtime.Plugin{Manifest: m, Image: st.Image}, ch)
	if err != nil {
		return runtime.Endpoint{}, fmt.Errorf("starting the plugin: %w", err)
	}
	return ep, nil
}

// runnable returns an installed plugin that may run, and its manifest.
func (s *Service) runnable(ctx context.Context, pluginID string) (*db.PluginState, *plugin.Manifest, error) {
	st, err := s.store.GetPluginState(ctx, pluginID)
	if err != nil {
		return nil, nil, fmt.Errorf("reading the plugin: %w", err)
	}
	if st == nil {
		return nil, nil, ErrNotInstalled
	}
	if st.Status != db.PluginStatusInstalled {
		return nil, nil, fmt.Errorf("%w: it is %s", ErrNotRunnable, st.Status)
	}
	m, err := s.installer.Manifest(st)
	if err != nil {
		return nil, nil, err
	}
	return st, m, nil
}

func findAgent(m *plugin.Manifest, id string) (plugin.Agent, bool) {
	i := slices.IndexFunc(m.Agents, func(a plugin.Agent) bool { return a.ID == id })
	if i < 0 {
		return plugin.Agent{}, false
	}
	return m.Agents[i], true
}

// agentCap is the capability agent a's runs need at least.
func agentCap(a plugin.Agent) plugin.Capability {
	if a.ReadOnly {
		return plugin.CapAgentReadOnly
	}
	return plugin.CapAgentWrite
}
