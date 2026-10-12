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
)

// Loop events a manifest may subscribe to.
const (
	EventMessageCreated = "message.created"
	EventAgentStatus    = "agent.status"
	EventQualityScanned = "quality.scanned"
)

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
}

// Action is something Loop triggers in the plugin, by hand or on a
// schedule.
type Action struct {
	ID              string    `json:"id"`
	DefaultSchedule *Schedule `json:"default_schedule,omitempty"`
}

// Schedule is the task Loop creates for an action in each channel whose
// project enables the plugin.
type Schedule struct {
	// Type is cron or interval.
	Type     string `json:"type"`
	Schedule string `json:"schedule"`
	Enabled  bool   `json:"enabled"`
}

// UI is what the plugin draws in Loop.
type UI struct {
	Panels []Panel `json:"panels,omitempty"`
	Cards  []Card  `json:"cards,omitempty"`
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
