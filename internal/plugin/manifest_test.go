package plugin

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/types"
)

type ManifestSuite struct {
	suite.Suite
}

func TestManifestSuite(t *testing.T) {
	suite.Run(t, new(ManifestSuite))
}

// cogtest is a manifest using every field, as JSON with comments and
// trailing commas.
const cogtest = `{
  // The cognitive test plugin.
  "id": "cogtest",
  "name": "Cognitive test",
  "version": "1.0.0",
  "api": "1",
  "loop": ">=2026.11.0",
  "capabilities": ["files.read", "git.read", "quality.read", "agent.readonly", "schedule", "ui.panel", "ui.notification.notify", "ui.open_file", "memory.write", "events.subscribe", "ui.run"],
  "events": ["message.created"],
  "gate": {
    "commands": [{"commands": ["git"], "args_patterns": ["^log"], "decision": "allow"}],
    "files": [{"paths": ["/data/**"], "operations": ["write"], "decision": "allow"}],
  },
  "settings": {"questions": {"type": "integer", "default": 5}},
  "agents": [{"id": "generate", "read_only": true, "tools": ["submit_questions"]}],
  "tools": [{"name": "submit_questions", "description": "Submit the quiz.", "input_schema": {"type": "object"}}],
  "actions": [{"id": "daily", "default_schedule": {"type": "cron", "schedule": "0 9 * * 1-5", "enabled": true}}, {"id": "now"}],
  "ui": {"panels": [{"id": "quiz", "label": "Cognitive test", "entry": "ui/quiz/index.html", "singleton": true}]},
  "resources": {"memory": "256m", "cpus": 0.5},
}`

func (s *ManifestSuite) TestParseManifest() {
	m, err := ParseManifest([]byte(cogtest))
	require.NoError(s.T(), err)
	require.Equal(s.T(), "cogtest", m.ID)
	require.Equal(s.T(), "1", m.API)
	require.True(s.T(), m.Has(CapAgentReadOnly))
	require.False(s.T(), m.Has(CapAgentWrite))
	require.Equal(s.T(), []Agent{{ID: "generate", ReadOnly: true, Tools: []string{"submit_questions"}}}, m.Agents)
	require.JSONEq(s.T(), `{"type":"object"}`, string(m.Tools[0].InputSchema))
	require.Equal(s.T(), &Schedule{Type: db.TaskTypeCron, Schedule: "0 9 * * 1-5", Enabled: true}, m.Actions[0].DefaultSchedule)
	require.Equal(s.T(), "ui/quiz/index.html", m.UI.Panels[0].Entry)
	require.Equal(s.T(), &Resources{Memory: "256m", CPUs: 0.5}, m.Resources)
	require.Equal(s.T(), "integer", m.Settings["questions"].Type)
}

func (s *ManifestSuite) TestParseManifestErrors() {
	for _, tc := range []struct {
		name    string
		data    string
		wantErr string
	}{
		{name: "not JSON", data: `{`, wantErr: "parsing plugin manifest"},
		{name: "unknown field", data: `{"id": "x1", "hooks": ["a"]}`, wantErr: `unknown field "hooks"`},
		{name: "invalid", data: `{"id": "X"}`, wantErr: `id "X" must match`},
	} {
		s.Run(tc.name, func() {
			_, err := ParseManifest([]byte(tc.data))
			require.ErrorContains(s.T(), err, tc.wantErr)
		})
	}
}

// valid returns a minimal manifest that passes Validate.
func valid() *Manifest {
	return &Manifest{ID: "echo", Name: "Echo", Version: "0.1.0", API: "1"}
}

func (s *ManifestSuite) TestValidate() {
	allowGit := types.CommandRule{Commands: []string{"git"}, Decision: types.DecisionAllow}
	tool := Tool{Name: "echo", Description: "Echoes.", InputSchema: json.RawMessage(`{"type":"object"}`)}
	for _, tc := range []struct {
		name    string
		edit    func(m *Manifest)
		wantErr []string
	}{
		{name: "minimal", edit: func(*Manifest) {}},
		{name: "everything wrong at the top", edit: func(m *Manifest) {
			m.ID, m.Name, m.Version, m.API, m.Loop = "1x", " ", "one", "9", "~>>"
		}, wantErr: []string{`id "1x" must match`, "name is required", `version "one" is not semver`, `api "9" is not served by this Loop (it serves 1)`, `loop "~>>" is not a version constraint`}},
		{name: "id too short", edit: func(m *Manifest) { m.ID = "e" }, wantErr: []string{`id "e" must match`}},
		{name: "reserved id", edit: func(m *Manifest) { m.ID = "enabled" }, wantErr: []string{`id "enabled" is reserved`}},
		{name: "capabilities", edit: func(m *Manifest) {
			m.Capabilities = []Capability{CapGitRead, "net.open", CapGitRead}
		}, wantErr: []string{`unknown capability "net.open"`, `capability "git.read" is listed twice`}},
		{name: "gate allow and deny", edit: func(m *Manifest) {
			m.Gate = &Gate{Commands: []types.CommandRule{allowGit}, Files: []types.FileRule{{Paths: []string{"/etc/**"}, Decision: types.DecisionDeny}}}
		}},
		{name: "gate approve rules", edit: func(m *Manifest) {
			m.Gate = &Gate{
				Commands: []types.CommandRule{{Commands: []string{"curl"}, Decision: types.DecisionApprove}},
				Files:    []types.FileRule{{Paths: []string{"/x"}, Decision: types.DecisionApprove}},
			}
		}, wantErr: []string{"gate.commands[0]: decision must be allow or deny, not \"approve\"", "gate.files[0]: decision must be allow or deny, not \"approve\""}},
		{name: "gate rule that doesn't compile", edit: func(m *Manifest) {
			m.Gate = &Gate{Commands: []types.CommandRule{{ArgsPatterns: []string{"("}, Decision: types.DecisionAllow}}}
		}, wantErr: []string{"gate: command_rules[0].args_patterns[0]"}},
		{name: "settings", edit: func(m *Manifest) {
			m.Settings = map[string]*config.SchemaProperty{"Bad-Name": {Type: "string"}, "color": {Type: "colour"}, "nothing": nil, "ok": {Type: "boolean"}}
		}, wantErr: []string{`setting "Bad-Name" must match`, `setting "color": type must be one of`, `setting "nothing": type must be one of`}},
		{name: "setting defaults", edit: func(m *Manifest) {
			m.Settings = map[string]*config.SchemaProperty{
				"count": {Type: "integer", Default: "five"},
				"lang":  {Type: "string", Enum: []any{"en", "ro"}, Default: "ro"},
				"code":  {Type: "string", Pattern: "(", Default: "x"},
			}
		}, wantErr: []string{`setting "count": `, `(its default)`, `setting "code": its schema: `}},
		{name: "tools", edit: func(m *Manifest) {
			m.Tools = []Tool{
				tool,
				{Name: "echo", Description: " ", InputSchema: json.RawMessage(`{"type":"string"}`)},
				{Name: "Bad", Description: "x", InputSchema: json.RawMessage(`nope`)},
				{Name: "unresolved", Description: "x", InputSchema: json.RawMessage(`{"type":"object","$ref":"#/missing"}`)},
				{Name: "scored", Description: "x", InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)},
				{Name: "listed", Description: "x", InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"array"}`)},
				{Name: "broken", Description: "x", InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`nope`)},
			}
		}, wantErr: []string{`tools[1]: tool "echo" is declared twice`, "tools[1]: description is required", "tools[1]: input_schema must be a JSON schema of type object", `tools[2]: name "Bad" must match`, "tools[2]: input_schema must be", "tools[3]: input_schema must be", "tools[5]: output_schema must be a JSON schema of type object", "tools[6]: output_schema must be"}},
		{name: "agents", edit: func(m *Manifest) {
			m.Tools = []Tool{tool}
			m.Agents = []Agent{
				{ID: "reader", ReadOnly: true, Tools: []string{"echo"}},
				{ID: "reader", ReadOnly: true},
				{ID: "writer", Tools: []string{"missing"}},
				{ID: "!"},
			}
		}, wantErr: []string{
			`agents[0]: a read-only agent needs capability "agent.readonly"`,
			`agents[1]: agent "reader" is declared twice`,
			`agents[2]: an agent that isn't read_only needs capability "agent.write"`,
			`agents[2]: tool "missing" is not declared in tools`,
			`agents[3]: id "!" must match`,
		}},
		{name: "agent.write covers read-only agents", edit: func(m *Manifest) {
			m.Capabilities = []Capability{CapAgentWrite}
			m.Agents = []Agent{{ID: "reader", ReadOnly: true}, {ID: "writer"}}
		}},
		{name: "actions", edit: func(m *Manifest) {
			m.Actions = []Action{
				{ID: "daily", DefaultSchedule: &Schedule{Type: db.TaskTypeCron, Schedule: "0 9 * * *"}},
				{ID: "daily"},
				{ID: "x!", DefaultSchedule: &Schedule{Type: db.TaskTypeOnce, Schedule: "2026-10-10T09:00:00Z"}},
				{ID: "hourly", DefaultSchedule: &Schedule{Type: db.TaskTypeInterval, Schedule: "an hour"}},
			}
		}, wantErr: []string{
			`actions[0]: a default_schedule needs capability "schedule"`,
			`actions[1]: action "daily" is declared twice`,
			`actions[2]: id "x!" must match`,
			`actions[2]: default_schedule type must be cron or interval, not "once"`,
			`actions[3]: invalid task schedule: interval "an hour"`,
		}},
		{name: "scheduled actions with the capability", edit: func(m *Manifest) {
			m.Capabilities = []Capability{CapSchedule}
			m.Actions = []Action{{ID: "hourly", DefaultSchedule: &Schedule{Type: db.TaskTypeInterval, Schedule: "1h"}}}
		}},
		{name: "ui", edit: func(m *Manifest) {
			m.UI = &UI{Panels: []Panel{
				{ID: "quiz", Label: "Quiz", Entry: "ui/quiz/index.html"},
				{ID: "quiz", Label: " ", Entry: "ui/../secret.html"},
				{ID: "Q", Label: "Q", Entry: "quiz/index.html"},
				{ID: "js", Label: "JS", Entry: "ui/app.js"},
			}}
		}, wantErr: []string{
			`ui.panels needs capability "ui.panel"`,
			`ui.panels[1]: panel "quiz" is declared twice`,
			"ui.panels[1]: label is required",
			`ui.panels[1]: entry "ui/../secret.html" must be an .html file under ui/`,
			`ui.panels[2]: id "Q" must match`,
			`ui.panels[2]: entry "quiz/index.html" must be`,
			`ui.panels[3]: entry "ui/app.js" must be`,
		}},
		{name: "cards", edit: func(m *Manifest) {
			m.UI = &UI{Cards: []Card{
				{ID: "score", Entry: "ui/score.html"},
				{ID: "score", Entry: "ui/../score.html"},
				{ID: "S", Entry: "ui/s.js"},
			}}
		}, wantErr: []string{
			`ui.cards needs capability "ui.notification.card"`,
			`ui.cards[1]: card "score" is declared twice`,
			`ui.cards[1]: entry "ui/../score.html" must be an .html file under ui/`,
			`ui.cards[2]: id "S" must match`,
			`ui.cards[2]: entry "ui/s.js" must be`,
		}},
		{name: "live", edit: func(m *Manifest) {
			m.Live = &Live{Control: []string{"set_mode", "Bad", "set_mode"}, Messages: []string{"mode_state", "mode_state"}}
		}, wantErr: []string{
			`live needs capability "live.control"`,
			`live.control[1]: "Bad" must match`,
			`live.control[2]: "set_mode" is listed twice`,
			`live.messages[1]: "mode_state" is listed twice`,
		}},
		{name: "live with the capability", edit: func(m *Manifest) {
			m.Capabilities = append(m.Capabilities, CapLiveControl)
			m.Live = &Live{Control: []string{"set_mode"}, Messages: []string{"mode_state"}}
		}},
		{name: "composer switches", edit: func(m *Manifest) {
			m.UI = &UI{ComposerSwitches: []ComposerSwitch{
				{ID: "mode", Label: "Mode", Icon: "ui/mode.svg"},
				{ID: "mode", Label: " ", Icon: "ui/../mode.svg", Title: strings.Repeat("t", 201)},
				{ID: "R", Label: strings.Repeat("l", 25), Icon: "mode.svg"},
				{ID: "four", Label: "Four", Icon: "ui/mode.png"},
				{ID: "five", Label: "Five", Icon: "ui/mode.svg"},
			}}
		}, wantErr: []string{
			`ui.composer_switches needs capability "ui.composer_switch"`,
			"ui.composer_switches: at most 4",
			`ui.composer_switches[1]: switch "mode" is declared twice`,
			"ui.composer_switches[1]: label is required, at most 24 characters",
			"ui.composer_switches[1]: title is at most 200 characters",
			`ui.composer_switches[1]: icon "ui/../mode.svg" must be an .svg file under ui/`,
			`ui.composer_switches[2]: id "R" must match`,
			"ui.composer_switches[2]: label is required",
			`ui.composer_switches[2]: icon "mode.svg" must be`,
			`ui.composer_switches[3]: icon "ui/mode.png" must be`,
		}},
		{name: "composer switches with the capability", edit: func(m *Manifest) {
			m.Capabilities = append(m.Capabilities, CapUISwitch)
			m.UI = &UI{ComposerSwitches: []ComposerSwitch{{ID: "mode", Label: "Mode", Icon: "ui/mode.svg", Title: "Mode"}}}
		}},
		{name: "cards with the capability", edit: func(m *Manifest) {
			m.Capabilities = []Capability{CapUICard}
			m.UI = &UI{Cards: []Card{{ID: "score", Entry: "ui/score.html"}}}
		}},
		{name: "empty ui", edit: func(m *Manifest) { m.UI = &UI{} }},
		{name: "events", edit: func(m *Manifest) { m.Events = []string{"message.created", "message.streaming", "message.created"} }, wantErr: []string{
			`events needs capability "events.subscribe"`,
			`events[1]: "message.streaming" is not an event a plugin can subscribe to (message.created, agent.status, quality.scanned, live.status, live.system, settings.changed)`,
			`events[2]: "message.created" is listed twice`,
		}},
		{name: "events with the capability", edit: func(m *Manifest) {
			m.Capabilities = []Capability{CapEventsSub}
			m.Events = slices.Clone(Events)
			m.ThreadEvents = ThreadEventsBoth
		}},
		{name: "thread events", edit: func(m *Manifest) { m.ThreadEvents = "parent" }, wantErr: []string{
			`thread_events: "parent" is not one of thread, channel, both`,
		}},
		{name: "resources", edit: func(m *Manifest) { m.Resources = &Resources{Memory: "lots", CPUs: -1} },
			wantErr: []string{`resources.memory "lots" is not a size like 256m`, "resources.cpus must not be negative"}},
		{name: "zero memory", edit: func(m *Manifest) { m.Resources = &Resources{Memory: "0"} },
			wantErr: []string{`resources.memory "0" is not a size`}},
		{name: "no resources limits", edit: func(m *Manifest) { m.Resources = &Resources{} }},
	} {
		s.Run(tc.name, func() {
			m := valid()
			tc.edit(m)
			err := m.Validate()
			if len(tc.wantErr) == 0 {
				require.NoError(s.T(), err)
				return
			}
			require.ErrorContains(s.T(), err, "invalid plugin manifest")
			for _, want := range tc.wantErr {
				require.ErrorContains(s.T(), err, want)
			}
		})
	}
}

func (s *ManifestSuite) TestCard() {
	m := &Manifest{UI: &UI{Cards: []Card{{ID: "score", Entry: "ui/score.html"}}}}
	c, ok := m.Card("score")
	require.True(s.T(), ok)
	require.Equal(s.T(), "ui/score.html", c.Entry)
	_, ok = m.Card("other")
	require.False(s.T(), ok)
	_, ok = (&Manifest{}).Card("score")
	require.False(s.T(), ok)
}

func (s *ManifestSuite) TestComposerSwitch() {
	m := &Manifest{UI: &UI{ComposerSwitches: []ComposerSwitch{{ID: "mode", Label: "Mode", Icon: "ui/mode.svg"}}}}
	sw, ok := m.ComposerSwitch("mode")
	require.True(s.T(), ok)
	require.Equal(s.T(), "ui/mode.svg", sw.Icon)
	_, ok = m.ComposerSwitch("other")
	require.False(s.T(), ok)
	_, ok = (&Manifest{}).ComposerSwitch("mode")
	require.False(s.T(), ok)
}

func (s *ManifestSuite) TestLiveControlAndMessage() {
	m := &Manifest{Live: &Live{Control: []string{"set_mode"}, Messages: []string{"mode_state"}}}
	require.True(s.T(), m.LiveControl("set_mode"))
	require.False(s.T(), m.LiveControl("interrupt"))
	require.True(s.T(), m.LiveMessage("mode_state"))
	require.False(s.T(), m.LiveMessage("status"))
	require.False(s.T(), (&Manifest{}).LiveControl("set_mode"))
	require.False(s.T(), (&Manifest{}).LiveMessage("mode_state"))
}

func (s *ManifestSuite) TestToolValidateInput() {
	t := Tool{Name: "echo", InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`)}
	tests := []struct {
		name    string
		tool    Tool
		input   string
		wantErr string
	}{
		{name: "valid", tool: t, input: `{"text":"hi"}`},
		{name: "missing field", tool: t, input: `{}`, wantErr: "text"},
		{name: "wrong type", tool: t, input: `{"text":1}`, wantErr: "text"},
		{name: "not json", tool: t, input: `{`, wantErr: "the input is not JSON"},
		{name: "bad schema", tool: Tool{InputSchema: json.RawMessage(`nope`)}, input: `{}`, wantErr: "the tool's input schema"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			err := tt.tool.ValidateInput(json.RawMessage(tt.input))
			if tt.wantErr == "" {
				require.NoError(s.T(), err)
				return
			}
			require.ErrorContains(s.T(), err, tt.wantErr)
		})
	}
}

func (s *ManifestSuite) TestToolValidateOutput() {
	scored := Tool{Name: "score", OutputSchema: json.RawMessage(`{"type":"object","properties":{"score":{"type":"integer"}},"required":["score"]}`)}
	plain := Tool{Name: "echo"}
	tests := []struct {
		name       string
		tool       Tool
		structured string
		wantErr    string
	}{
		{name: "valid", tool: scored, structured: `{"score":7}`},
		{name: "off schema", tool: scored, structured: `{"score":"high"}`, wantErr: "score"},
		{name: "missing", tool: scored, wantErr: "the result has no structured_content, which the tool's output_schema requires"},
		{name: "none without a schema", tool: plain},
		{name: "any object without a schema", tool: plain, structured: `{"a":[1]}`},
		{name: "not an object", tool: plain, structured: `"x"`, wantErr: "structured_content must be a JSON object"},
		{name: "not json", tool: plain, structured: `{`, wantErr: "structured_content is not JSON"},
		{name: "bad schema", tool: Tool{OutputSchema: json.RawMessage(`nope`)}, structured: `{}`, wantErr: "the tool's output schema"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			var structured json.RawMessage
			if tt.structured != "" {
				structured = json.RawMessage(tt.structured)
			}
			err := tt.tool.ValidateOutput(structured)
			if tt.wantErr == "" {
				require.NoError(s.T(), err)
				return
			}
			require.ErrorContains(s.T(), err, tt.wantErr)
		})
	}
}

func (s *ManifestSuite) TestThreadEventsTo() {
	tests := []struct {
		value           string
		thread, channel bool
	}{
		{value: "", thread: true},
		{value: ThreadEventsThread, thread: true},
		{value: ThreadEventsChannel, channel: true},
		{value: ThreadEventsBoth, thread: true, channel: true},
	}
	for _, tt := range tests {
		s.Run(tt.value, func() {
			thread, channel := (&Manifest{ThreadEvents: tt.value}).ThreadEventsTo()
			require.Equal(s.T(), tt.thread, thread)
			require.Equal(s.T(), tt.channel, channel)
		})
	}
}

func (s *ManifestSuite) TestScheduleProblems() {
	m := &Manifest{Actions: []Action{
		{ID: "daily", DefaultSchedule: &Schedule{Type: db.TaskTypeCron, Schedule: "0 9 * * *"}},
		{ID: "now"},
	}}
	tests := []struct {
		name   string
		layers []map[string]config.PluginSchedule
		want   []string
	}{
		{name: "none"},
		{
			name:   "the default schedule",
			layers: []map[string]config.PluginSchedule{{"daily": {}}},
		},
		{
			name:   "its own schedule",
			layers: []map[string]config.PluginSchedule{{"now": {Type: "interval", Schedule: "1h"}}},
		},
		{
			name: "every problem, by action",
			layers: []map[string]config.PluginSchedule{{
				"now":   {},
				"daily": {Type: "once", Schedule: "1h"},
				"gone":  {},
			}},
			want: []string{
				`plugins.p.schedules.daily: type must be cron or interval, not "once"`,
				"plugins.p.schedules.gone: no such action",
				"plugins.p.schedules.now: invalid task schedule",
			},
		},
		{
			name: "a later layer replaces the entry",
			layers: []map[string]config.PluginSchedule{
				{"daily": {Schedule: "not cron"}},
				{"daily": {Schedule: "0 8 * * *"}},
			},
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			got := m.ScheduleProblems("p", tt.layers...)
			require.Len(s.T(), got, len(tt.want))
			for i, w := range tt.want {
				require.Contains(s.T(), got[i], w)
			}
		})
	}
}
