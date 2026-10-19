package pluginsdk

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/tailscale/hujson"
)

// ManifestPath is where a plugin image keeps its manifest.
const ManifestPath = "/loop-plugin/plugin.json"

// Capabilities a manifest may ask for.
const (
	CapFilesRead     = "files.read"
	CapGitRead       = "git.read"
	CapQualityRead   = "quality.read"
	CapAgentReadOnly = "agent.readonly"
	CapAgentWrite    = "agent.write"
	CapSchedule      = "schedule"
	CapUIPanel       = "ui.panel"
	CapUINotify      = "ui.notification.notify"
	CapUIOpenFile    = "ui.open_file"
	CapMemoryWrite   = "memory.write"
	CapEventsSub     = "events.subscribe"
	CapUIRun         = "ui.run"
	CapUIBadge       = "ui.badge"
	CapUICard        = "ui.notification.card"
	CapUICopy        = "ui.copy"
	CapLiveControl   = "live.control"
	CapUISwitch      = "ui.composer_switch"
)

// Loop events a manifest may subscribe to.
const (
	EventMessageCreated  = "message.created"
	EventAgentStatus     = "agent.status"
	EventQualityScanned  = "quality.scanned"
	EventLiveStatus      = "live.status"
	EventLiveSystem      = "live.system"
	EventSettingsChanged = "settings.changed"
)

// SettingsChanged is the data of a settings.changed event: the plugin's
// settings in the channel's project, as GET /channel has them, after a
// config write. They may be the ones the plugin already has.
type SettingsChanged struct {
	Settings map[string]any `json:"settings"`
}

// LiveStatus is the data of a live.status event: the chat's live session
// started, restarted, or ended when Running is false. Reason says why it
// ended or restarted; a restarted process has lost what control requests
// set on it.
type LiveStatus struct {
	Running   bool   `json:"running"`
	SessionID string `json:"session_id,omitempty"`
	Busy      bool   `json:"busy"`
	Reason    string `json:"reason,omitempty"`
}

// LiveSystem is the data of a live.system event: a "system" message of the
// chat's live session whose subtype the manifest lists under
// live.messages.
type LiveSystem struct {
	Subtype string `json:"subtype"`
	// Message is the message as the claude process wrote it.
	Message json.RawMessage `json:"message"`
}

// Manifest is a plugin's plugin.json. Loop checks it at install; reading it
// here checks only that it parses.
type Manifest struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	// API is the Plugin API major the plugin speaks.
	API string `json:"api"`
	// Loop, when set, is a version constraint on the Loop release.
	Loop         string              `json:"loop,omitempty"`
	Capabilities []string            `json:"capabilities,omitempty"`
	Gate         *Gate               `json:"gate,omitempty"`
	Settings     map[string]*Setting `json:"settings,omitempty"`
	Agents       []Agent             `json:"agents,omitempty"`
	Tools        []Tool              `json:"tools,omitempty"`
	Actions      []Action            `json:"actions,omitempty"`
	UI           *UI                 `json:"ui,omitempty"`
	Resources    *Resources          `json:"resources,omitempty"`
	// Events are the Loop events Loop sends to the plugin's POST /events;
	// they need CapEventsSub.
	Events []string `json:"events,omitempty"`
	// ThreadEvents is where an event in a channel's thread goes: "thread"
	// (the thread's container, the default), "channel" or "both".
	ThreadEvents string `json:"thread_events,omitempty"`
	// Live is what the plugin does with its chats' live sessions; it needs
	// CapLiveControl.
	Live *Live `json:"live,omitempty"`
}

// Live lists the live session messages a plugin sends and hears.
type Live struct {
	// Control are the subtypes of the control requests LiveControl may
	// send.
	Control []string `json:"control,omitempty"`
	// Messages are the subtypes of the "system" messages live.system
	// events bring.
	Messages []string `json:"messages,omitempty"`
}

// Gate is the agentgate rules a plugin asks for on top of Loop's baseline.
type Gate struct {
	Commands []CommandRule `json:"commands,omitempty"`
	Files    []FileRule    `json:"files,omitempty"`
}

// CommandRule allows or denies running commands.
type CommandRule struct {
	Commands     []string `json:"commands,omitempty"`
	ArgsPatterns []string `json:"args_patterns,omitempty"`
	Decision     string   `json:"decision"`
	Message      string   `json:"message,omitempty"`
}

// FileRule allows or denies file operations on paths.
type FileRule struct {
	Paths      []string `json:"paths,omitempty"`
	Operations []string `json:"operations,omitempty"`
	Decision   string   `json:"decision"`
	Message    string   `json:"message,omitempty"`
}

// Setting is the schema of one of the plugin's settings.
type Setting struct {
	Type                 string              `json:"type"`
	Title                string              `json:"title,omitempty"`
	Description          string              `json:"description,omitempty"`
	Enum                 []any               `json:"enum,omitempty"`
	Default              any                 `json:"default,omitempty"`
	Minimum              *float64            `json:"minimum,omitempty"`
	Maximum              *float64            `json:"maximum,omitempty"`
	Pattern              string              `json:"pattern,omitempty"`
	Items                *Setting            `json:"items,omitempty"`
	Properties           map[string]*Setting `json:"properties,omitempty"`
	AdditionalProperties *Setting            `json:"additionalProperties,omitempty"`
}

// Agent is an agent run the plugin may ask Loop for.
type Agent struct {
	ID       string `json:"id"`
	ReadOnly bool   `json:"read_only"`
	// Tools are the plugin's tools, by name, the agent gets.
	Tools []string `json:"tools,omitempty"`
}

// Tool is a tool the plugin serves to its agents.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	// OutputSchema, if set, is the schema of the StructuredContent every
	// result that isn't an error then carries.
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
}

// Action is something Loop triggers in the plugin, by hand or on a
// schedule.
type Action struct {
	ID              string    `json:"id"`
	DefaultSchedule *Schedule `json:"default_schedule,omitempty"`
}

// Schedule is an action's default schedule: the one its task gets in a
// project whose config asks for it, under plugins.<id>.schedules, without
// setting its own.
type Schedule struct {
	// Type is cron or interval.
	Type     string `json:"type"`
	Schedule string `json:"schedule"`
	// Enabled is ignored: the project config turns the task on or off.
	Enabled bool `json:"enabled,omitempty"`
}

// UI is what the plugin draws in Loop.
type UI struct {
	Panels []Panel `json:"panels,omitempty"`
	Cards  []Card  `json:"cards,omitempty"`
	// ComposerSwitches need CapUISwitch.
	ComposerSwitches []ComposerSwitch `json:"composer_switches,omitempty"`
}

// Panel is a pane the plugin draws from a static bundle in its image.
type Panel struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Entry is the panel's HTML page, relative to /loop-plugin.
	Entry     string `json:"entry"`
	Singleton bool   `json:"singleton,omitempty"`
}

// Card is what the plugin posts to its channel's notification drawer with
// PostCard: a page from its bundle, drawn with the data it was posted with.
type Card struct {
	ID string `json:"id"`
	// Entry is the card's HTML page, relative to /loop-plugin.
	Entry string `json:"entry"`
}

// ComposerSwitch is an on/off pill the plugin adds to the chat composer; a
// click calls the Server's Switches[ID].
type ComposerSwitch struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Icon is the pill's SVG icon, relative to /loop-plugin, under ui/, at
	// most 16 KB.
	Icon string `json:"icon"`
	// Title is the pill's tooltip until the plugin sets its own.
	Title string `json:"title,omitempty"`
}

// Resources caps the plugin's container.
type Resources struct {
	Memory string  `json:"memory,omitempty"`
	CPUs   float64 `json:"cpus,omitempty"`
}

// ParseManifest reads a plugin.json: JSON with comments and trailing commas
// allowed.
func ParseManifest(data []byte) (*Manifest, error) {
	std, err := hujson.Standardize(data)
	if err != nil {
		return nil, fmt.Errorf("parsing the plugin manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(std, &m); err != nil {
		return nil, fmt.Errorf("parsing the plugin manifest: %w", err)
	}
	return &m, nil
}

// ReadManifest reads the manifest at path, such as ManifestPath inside the
// plugin's image.
func ReadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the plugin manifest: %w", err)
	}
	return ParseManifest(data)
}
