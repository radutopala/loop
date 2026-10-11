// Package plugin holds what Loop knows about an installed plugin: its
// manifest, read from the plugin's image, and the rules a manifest must
// follow before Loop installs it.
package plugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/docker/go-units"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/tailscale/hujson"

	"github.com/radutopala/loop/internal/agentgate"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/scheduler"
	"github.com/radutopala/loop/internal/types"
)

// ManifestPath is where a plugin image keeps its manifest. The rest of the
// image's /loop-plugin dir (UI bundles, icon) is relative to it.
const ManifestPath = "/loop-plugin/plugin.json"

// ImageLabel marks an image as a Loop plugin; Loop refuses any image
// without it before reading the manifest.
const ImageLabel = "dev.loop.plugin"

// APIVersions are the Plugin API majors this Loop serves, newest first.
var APIVersions = []string{"1"}

// Capability is something a plugin may do through Loop, approved by the
// owner at install and checked on every call.
type Capability string

const (
	CapFilesRead     Capability = "files.read"
	CapGitRead       Capability = "git.read"
	CapQualityRead   Capability = "quality.read"
	CapAgentReadOnly Capability = "agent.readonly"
	CapAgentWrite    Capability = "agent.write"
	CapSchedule      Capability = "schedule"
	CapUIPanel       Capability = "ui.panel"
	CapUINotify      Capability = "ui.notify"
	CapUIOpenFile    Capability = "ui.open_file"
	CapMemoryWrite   Capability = "memory.write"
	CapEventsSub     Capability = "events.subscribe"
	CapUIRun         Capability = "ui.run"
	CapUIBadge       Capability = "ui.badge"
)

// Capabilities lists every capability a manifest may ask for.
var Capabilities = []Capability{
	CapFilesRead, CapGitRead, CapQualityRead,
	CapAgentReadOnly, CapAgentWrite, CapSchedule,
	CapUIPanel, CapUINotify, CapUIOpenFile, CapMemoryWrite, CapEventsSub,
	CapUIRun, CapUIBadge,
}

// Events lists the Loop events a manifest may subscribe to, each one in a
// channel, as the app's events WebSocket names them.
var Events = []string{"message.created", "agent.status", "quality.scanned"}

// Manifest is a plugin's plugin.json: who it is, what it may do and what it
// contributes to Loop.
type Manifest struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	// API is the Plugin API major the plugin speaks.
	API string `json:"api"`
	// Loop, when set, is a version constraint on the Loop release.
	Loop         string       `json:"loop,omitempty"`
	Capabilities []Capability `json:"capabilities,omitempty"`
	// Gate holds the agentgate rules the plugin asks for on top of Loop's
	// baseline for plugin containers. Allow and deny only.
	Gate     *Gate                             `json:"gate,omitempty"`
	Settings map[string]*config.SchemaProperty `json:"settings,omitempty"`
	Agents   []Agent                           `json:"agents,omitempty"`
	Tools    []Tool                            `json:"tools,omitempty"`
	Actions  []Action                          `json:"actions,omitempty"`
	// Events are the Loop events Loop sends to the plugin's POST /events.
	Events    []string   `json:"events,omitempty"`
	UI        *UI        `json:"ui,omitempty"`
	Resources *Resources `json:"resources,omitempty"`
}

// Gate is a plugin's agentgate rules.
type Gate struct {
	Commands []types.CommandRule `json:"commands,omitempty"`
	Files    []types.FileRule    `json:"files,omitempty"`
}

// Agent is an agent run the plugin may ask Loop for.
type Agent struct {
	ID       string `json:"id"`
	ReadOnly bool   `json:"read_only"`
	// Tools are the plugin's tools (by name) the agent gets.
	Tools []string `json:"tools,omitempty"`
}

// Tool is an MCP tool the plugin serves to its agents.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// schema compiles the tool's input schema.
func (t *Tool) schema() (*jsonschema.Resolved, error) {
	var s jsonschema.Schema
	if err := json.Unmarshal(t.InputSchema, &s); err != nil {
		return nil, err
	}
	return s.Resolve(nil)
}

// ValidateInput checks input, a tool call's arguments, against the tool's
// input schema.
func (t *Tool) ValidateInput(input json.RawMessage) error {
	rs, err := t.schema()
	if err != nil {
		return fmt.Errorf("the tool's input schema: %w", err)
	}
	var v any
	if err := json.Unmarshal(input, &v); err != nil {
		return fmt.Errorf("the input is not JSON: %w", err)
	}
	return rs.Validate(v)
}

// Action is something Loop can trigger in the plugin, on a schedule or by
// hand.
type Action struct {
	ID              string    `json:"id"`
	DefaultSchedule *Schedule `json:"default_schedule,omitempty"`
}

// Schedule is the scheduled task Loop creates for an action when the plugin
// is enabled in a channel.
type Schedule struct {
	Type     db.TaskType `json:"type"`
	Schedule string      `json:"schedule"`
	Enabled  bool        `json:"enabled"`
}

// UI is what the plugin draws in Loop.
type UI struct {
	Panels []Panel `json:"panels,omitempty"`
}

// Panel is a workspace pane the plugin draws, from a static bundle in its
// image.
type Panel struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Entry is the panel's HTML page, relative to /loop-plugin, under ui/.
	Entry     string `json:"entry"`
	Singleton bool   `json:"singleton,omitempty"`
}

// Resources caps the plugin's container. Loop caps them again.
type Resources struct {
	Memory string  `json:"memory,omitempty"`
	CPUs   float64 `json:"cpus,omitempty"`
}

var (
	idRe       = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)
	toolNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	settingRe  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

// settingTypes are the schema types a plugin setting may have.
var settingTypes = []string{"string", "number", "integer", "boolean", "array", "object"}

// ParseManifest reads a plugin.json (JSON, with comments and trailing commas
// allowed, like Loop's own config) and validates it. Unknown fields are an
// error, so a manifest written for a newer Loop doesn't half-install.
func ParseManifest(data []byte) (*Manifest, error) {
	std, err := hujson.Standardize(bytes.Clone(data))
	if err != nil {
		return nil, fmt.Errorf("parsing plugin manifest: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(std))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("parsing plugin manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate checks the manifest is one Loop can install, returning every
// problem it finds.
func (m *Manifest) Validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if !idRe.MatchString(m.ID) {
		add("id %q must match %s", m.ID, idRe)
	} else if config.PluginIDReserved(m.ID) {
		add("id %q is reserved", m.ID)
	}
	if strings.TrimSpace(m.Name) == "" {
		add("name is required")
	}
	if _, err := semver.StrictNewVersion(m.Version); err != nil {
		add("version %q is not semver: %v", m.Version, err)
	}
	if !slices.Contains(APIVersions, m.API) {
		add("api %q is not served by this Loop (it serves %s)", m.API, strings.Join(APIVersions, ", "))
	}
	if m.Loop != "" {
		if _, err := semver.NewConstraint(m.Loop); err != nil {
			add("loop %q is not a version constraint: %v", m.Loop, err)
		}
	}
	m.validateCapabilities(add)
	m.validateGate(add)
	m.validateSettings(add)
	tools := m.validateTools(add)
	m.validateAgents(add, tools)
	m.validateActions(add)
	m.validateEvents(add)
	m.validateUI(add)
	m.validateResources(add)
	if len(errs) > 0 {
		return fmt.Errorf("invalid plugin manifest: %w", errors.Join(errs...))
	}
	return nil
}

// Has reports whether the manifest asks for capability c.
func (m *Manifest) Has(c Capability) bool {
	return slices.Contains(m.Capabilities, c)
}

func (m *Manifest) validateCapabilities(add func(string, ...any)) {
	seen := map[Capability]bool{}
	for _, c := range m.Capabilities {
		if !slices.Contains(Capabilities, c) {
			add("unknown capability %q", c)
		}
		if seen[c] {
			add("capability %q is listed twice", c)
		}
		seen[c] = true
	}
}

// validateGate refuses approve rules: a plugin is unattended, so nobody is
// there to answer a prompt.
func (m *Manifest) validateGate(add func(string, ...any)) {
	if m.Gate == nil {
		return
	}
	for i, r := range m.Gate.Commands {
		if r.Decision != types.DecisionAllow && r.Decision != types.DecisionDeny {
			add("gate.commands[%d]: decision must be allow or deny, not %q", i, r.Decision)
		}
	}
	for i, r := range m.Gate.Files {
		if r.Decision != types.DecisionAllow && r.Decision != types.DecisionDeny {
			add("gate.files[%d]: decision must be allow or deny, not %q", i, r.Decision)
		}
	}
	if _, err := agentgate.CompilePolicy(types.DecisionDeny, nil, m.Gate.Commands, m.Gate.Files); err != nil {
		add("gate: %v", err)
	}
}

func (m *Manifest) validateSettings(add func(string, ...any)) {
	for name, p := range m.Settings {
		if !settingRe.MatchString(name) {
			add("setting %q must match %s", name, settingRe)
			continue
		}
		if p == nil || !slices.Contains(settingTypes, p.Type) {
			add("setting %q: type must be one of %s", name, strings.Join(settingTypes, ", "))
			continue
		}
		if p.Default != nil {
			if err := m.checkSetting(name, p.Default); err != nil {
				add("%v (its default)", err)
			}
		}
	}
}

// validateTools returns the names of the declared tools.
func (m *Manifest) validateTools(add func(string, ...any)) map[string]bool {
	names := map[string]bool{}
	for i, t := range m.Tools {
		if !toolNameRe.MatchString(t.Name) {
			add("tools[%d]: name %q must match %s", i, t.Name, toolNameRe)
		}
		if names[t.Name] {
			add("tools[%d]: tool %q is declared twice", i, t.Name)
		}
		names[t.Name] = true
		if strings.TrimSpace(t.Description) == "" {
			add("tools[%d]: description is required", i)
		}
		if rs, err := t.schema(); err != nil || rs.Schema().Type != "object" {
			add("tools[%d]: input_schema must be a JSON schema of type object", i)
		}
	}
	return names
}

func (m *Manifest) validateAgents(add func(string, ...any), tools map[string]bool) {
	seen := map[string]bool{}
	for i, a := range m.Agents {
		if !idRe.MatchString(a.ID) {
			add("agents[%d]: id %q must match %s", i, a.ID, idRe)
		}
		if seen[a.ID] {
			add("agents[%d]: agent %q is declared twice", i, a.ID)
		}
		seen[a.ID] = true
		switch {
		case a.ReadOnly && !m.Has(CapAgentReadOnly) && !m.Has(CapAgentWrite):
			add("agents[%d]: a read-only agent needs capability %q", i, CapAgentReadOnly)
		case !a.ReadOnly && !m.Has(CapAgentWrite):
			add("agents[%d]: an agent that isn't read_only needs capability %q", i, CapAgentWrite)
		}
		for _, t := range a.Tools {
			if !tools[t] {
				add("agents[%d]: tool %q is not declared in tools", i, t)
			}
		}
	}
}

func (m *Manifest) validateActions(add func(string, ...any)) {
	seen := map[string]bool{}
	for i, a := range m.Actions {
		if !idRe.MatchString(a.ID) {
			add("actions[%d]: id %q must match %s", i, a.ID, idRe)
		}
		if seen[a.ID] {
			add("actions[%d]: action %q is declared twice", i, a.ID)
		}
		seen[a.ID] = true
		s := a.DefaultSchedule
		if s == nil {
			continue
		}
		if !m.Has(CapSchedule) {
			add("actions[%d]: a default_schedule needs capability %q", i, CapSchedule)
		}
		if s.Type != db.TaskTypeCron && s.Type != db.TaskTypeInterval {
			add("actions[%d]: default_schedule type must be cron or interval, not %q", i, s.Type)
			continue
		}
		if err := scheduler.ValidateSchedule(s.Type, s.Schedule); err != nil {
			add("actions[%d]: %v", i, err)
		}
	}
}

func (m *Manifest) validateEvents(add func(string, ...any)) {
	if len(m.Events) > 0 && !m.Has(CapEventsSub) {
		add("events needs capability %q", CapEventsSub)
	}
	seen := map[string]bool{}
	for i, e := range m.Events {
		if !slices.Contains(Events, e) {
			add("events[%d]: %q is not an event a plugin can subscribe to (%s)", i, e, strings.Join(Events, ", "))
		}
		if seen[e] {
			add("events[%d]: %q is listed twice", i, e)
		}
		seen[e] = true
	}
}

func (m *Manifest) validateUI(add func(string, ...any)) {
	if m.UI == nil {
		return
	}
	if len(m.UI.Panels) > 0 && !m.Has(CapUIPanel) {
		add("ui.panels needs capability %q", CapUIPanel)
	}
	seen := map[string]bool{}
	for i, p := range m.UI.Panels {
		if !idRe.MatchString(p.ID) {
			add("ui.panels[%d]: id %q must match %s", i, p.ID, idRe)
		}
		if seen[p.ID] {
			add("ui.panels[%d]: panel %q is declared twice", i, p.ID)
		}
		seen[p.ID] = true
		if strings.TrimSpace(p.Label) == "" {
			add("ui.panels[%d]: label is required", i)
		}
		if !validEntry(p.Entry) {
			add("ui.panels[%d]: entry %q must be an .html file under ui/", i, p.Entry)
		}
	}
}

// validEntry reports whether entry is a clean relative path to an HTML file
// under ui/.
func validEntry(entry string) bool {
	return path.Clean(entry) == entry && strings.HasPrefix(entry, "ui/") && strings.HasSuffix(entry, ".html")
}

func (m *Manifest) validateResources(add func(string, ...any)) {
	if m.Resources == nil {
		return
	}
	if m.Resources.Memory != "" {
		if n, err := units.RAMInBytes(m.Resources.Memory); err != nil || n <= 0 {
			add("resources.memory %q is not a size like 256m", m.Resources.Memory)
		}
	}
	if m.Resources.CPUs < 0 {
		add("resources.cpus must not be negative")
	}
}
