package install

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/plugin/proxy"
	"github.com/radutopala/loop/internal/plugin/runtime"
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
	// ErrUnknownTool is returned for a tool the calling agent wasn't given.
	ErrUnknownTool = errors.New("the agent has no such tool")
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
}

// ServiceStore is the store the Service reads plugins and their containers
// from.
type ServiceStore interface {
	Store
	ListPluginStates(ctx context.Context) ([]*db.PluginState, error)
	ListPluginContainers(ctx context.Context, pluginID string) ([]*db.PluginContainer, error)
}

// Caller calls into a running plugin.
type Caller interface {
	Action(ctx context.Context, ep runtime.Endpoint, action string, req proxy.ActionRequest) error
	Tool(ctx context.Context, ep runtime.Endpoint, tool string, req proxy.ToolRequest) (*proxy.ToolResult, error)
	RunDone(ctx context.Context, ep runtime.Endpoint, runID string, req proxy.RunDoneRequest) error
	X(ctx context.Context, ep runtime.Endpoint, req proxy.XRequest) (*proxy.XResponse, error)
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
}

// NewService returns a Service that starts a plugin's container only in
// the channels whose project enables it.
func NewService(installer *Installer, runtime Runtime, store ServiceStore, caller Caller, enabled Enabled) *Service {
	return &Service{installer: installer, runtime: runtime, store: store, caller: caller, enabled: enabled}
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
	i := slices.IndexFunc(m.Tools, func(t plugin.Tool) bool { return t.Name == tool })
	if i < 0 || !slices.Contains(a.Tools, tool) {
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
	ep, err := s.runtime.Ensure(ctx, runtime.Plugin{Manifest: m, Image: st.Image, ReadFiles: slices.Contains(st.ApprovedCaps, string(plugin.CapFilesRead))}, ch)
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
