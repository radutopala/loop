package pluginsdk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ManifestSuite struct {
	suite.Suite
}

func TestManifestSuite(t *testing.T) {
	suite.Run(t, new(ManifestSuite))
}

func (s *ManifestSuite) TestParse() {
	m, err := ParseManifest([]byte(`{
  // comments and trailing commas are allowed
  "id": "echo",
  "name": "Echo",
  "version": "0.1.0",
  "api": "1",
  "loop": ">=2026.10.0",
  "capabilities": ["agent.readonly", "schedule"],
  "gate": {
    "files": [{ "paths": ["/data/cache/**"], "operations": ["delete"], "decision": "deny" }],
    "commands": [{ "commands": ["curl"], "args_patterns": ["-o"], "decision": "deny", "message": "no" }],
  },
  "settings": { "count": { "type": "integer", "minimum": 1, "maximum": 10, "default": 3, "items": { "type": "string" } } },
  "agents": [{ "id": "say", "read_only": true, "tools": ["echo"] }],
  "tools": [{ "name": "echo", "description": "Returns its input", "input_schema": { "type": "object" } }],
  "actions": [{ "id": "ping" }, { "id": "daily", "default_schedule": { "type": "cron", "schedule": "0 9 * * *", "enabled": true } }],
  "ui": { "panels": [{ "id": "main", "label": "Echo", "entry": "ui/main/index.html", "singleton": true }] },
  "resources": { "memory": "128m", "cpus": 0.5 },
  "unknown": "is ignored",
}`))
	require.NoError(s.T(), err)
	lo, hi := float64(1), float64(10)
	require.Equal(s.T(), &Manifest{
		ID: "echo", Name: "Echo", Version: "0.1.0", API: "1", Loop: ">=2026.10.0",
		Capabilities: []string{CapAgentReadOnly, CapSchedule},
		Gate: &Gate{
			Commands: []CommandRule{{Commands: []string{"curl"}, ArgsPatterns: []string{"-o"}, Decision: "deny", Message: "no"}},
			Files:    []FileRule{{Paths: []string{"/data/cache/**"}, Operations: []string{"delete"}, Decision: "deny"}},
		},
		Settings:  map[string]*Setting{"count": {Type: "integer", Minimum: &lo, Maximum: &hi, Default: float64(3), Items: &Setting{Type: "string"}}},
		Agents:    []Agent{{ID: "say", ReadOnly: true, Tools: []string{"echo"}}},
		Tools:     []Tool{{Name: "echo", Description: "Returns its input", InputSchema: json.RawMessage(`{ "type": "object" }`)}},
		Actions:   []Action{{ID: "ping"}, {ID: "daily", DefaultSchedule: &Schedule{Type: "cron", Schedule: "0 9 * * *", Enabled: true}}},
		UI:        &UI{Panels: []Panel{{ID: "main", Label: "Echo", Entry: "ui/main/index.html", Singleton: true}}},
		Resources: &Resources{Memory: "128m", CPUs: 0.5},
	}, m)
}

func (s *ManifestSuite) TestRead() {
	// The fixture plugin Loop's own tests build.
	m, err := ReadManifest(filepath.Join("..", "test", "component", "testdata", "plugins", "echo", "plugin.json"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), "echo", m.ID)
	require.Equal(s.T(), APIVersion, m.API)

	dir := s.T().TempDir()
	tests := []struct {
		name, content, wantErr string
	}{
		{"missing", "", "reading the plugin manifest"},
		{"not JSON", "{", "parsing the plugin manifest"},
		{"wrong types", `{"id": 1}`, "parsing the plugin manifest"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			p := filepath.Join(dir, tt.name)
			if tt.content != "" {
				require.NoError(s.T(), os.WriteFile(p, []byte(tt.content), 0o600))
			}
			_, err := ReadManifest(p)
			require.ErrorContains(s.T(), err, tt.wantErr)
		})
	}
}
