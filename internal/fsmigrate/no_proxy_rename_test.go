package fsmigrate

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/stretchr/testify/require"
	"github.com/tailscale/hujson"
)

// --- no_proxy_rename_test covers the migration that renames the
// no_proxy_hosts key shipped by v2026.9.16 and v2026.9.17.

// noProxyOf returns the no_proxy entries of a config, and whether the old key
// survived the migration.
func noProxyOf(t require.TestingT, data []byte) (entries []string, legacyLeft bool) {
	std, err := hujson.Standardize(data)
	require.NoError(t, err)
	var parsed map[string]any
	require.NoError(t, json.Unmarshal(std, &parsed))
	for _, e := range parsed["no_proxy"].([]any) {
		entries = append(entries, e.(string))
	}
	_, legacyLeft = parsed["no_proxy_hosts"]
	return entries, legacyLeft
}

func (s *FSMigrateSuite) TestRenameNoProxyHosts() {
	const withComments = `{
  // the services this project's compose stack runs
  "no_proxy_hosts": [
    "my-service",
    "my-cache"
  ],
  "api_addr": ":8222"
}`

	tests := []struct {
		name     string
		config   string
		expected []string
	}{
		{
			name:     "legacy key alone is renamed",
			config:   withComments,
			expected: []string{"my-service", "my-cache"},
		},
		{
			name: "both spellings are folded into one list",
			config: `{
  "no_proxy": [
    "artifacts.internal"
  ],
  "no_proxy_hosts": [
    "my-service"
  ]
}`,
			expected: []string{"artifacts.internal", "my-service"},
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			sys := newFakeSystem()
			configPath := filepath.Join("/loop", "config.json")
			sys.files[configPath] = []byte(tt.config)

			err := renameNoProxyHosts(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
			require.NoError(s.T(), err)

			entries, legacyLeft := noProxyOf(s.T(), sys.files[configPath])
			require.Equal(s.T(), tt.expected, entries)
			require.False(s.T(), legacyLeft, "the old key must be gone")
		})
	}
}

func (s *FSMigrateSuite) TestRenameNoProxyHostsKeepsCommentsAndPosition() {
	sys := newFakeSystem()
	configPath := "/loop/config.json"
	sys.files[configPath] = []byte(`{
  // the services this project's compose stack runs
  "no_proxy_hosts": [
    "my-service",
    "my-cache"
  ],
  "api_addr": ":8222"
}`)

	require.NoError(s.T(), renameNoProxyHosts(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}))

	got := string(sys.files[configPath])
	require.Equal(s.T(), `{
  // the services this project's compose stack runs
  "no_proxy": [
    "my-service",
    "my-cache"
  ],
  "api_addr": ":8222"
}`, got)
}

func (s *FSMigrateSuite) TestRenameNoProxyHostsMigratesProjectConfigs() {
	sys := newFakeSystem()
	globalPath := "/loop/config.json"
	projectPath := filepath.Join("/work/project", ".loop", "config.json")
	sys.files[globalPath] = []byte(`{"no_proxy_hosts": ["corp.internal"]}`)
	sys.files[projectPath] = []byte(`{"no_proxy_hosts": ["my-service"]}`)

	err := renameNoProxyHosts(context.Background(), &Ctx{
		Sys:         sys,
		LoopDir:     "/loop",
		ProjectDirs: []string{"/work/project", "/work/no-config"},
	})
	require.NoError(s.T(), err)

	for path, want := range map[string][]string{globalPath: {"corp.internal"}, projectPath: {"my-service"}} {
		entries, legacyLeft := noProxyOf(s.T(), sys.files[path])
		require.Equal(s.T(), want, entries)
		require.False(s.T(), legacyLeft)
	}
}

func (s *FSMigrateSuite) TestRenameNoProxyHostsNoOps() {
	tests := []struct {
		name   string
		config string
		absent bool
	}{
		{name: "config is missing", absent: true},
		{name: "key was never set", config: `{"api_addr": ":8222"}`},
		{name: "legacy key is not an array", config: `{"no_proxy": [], "no_proxy_hosts": "my-service"}`},
		{name: "current key is not an array", config: `{"no_proxy": "my-service", "no_proxy_hosts": []}`},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			sys := newFakeSystem()
			configPath := "/loop/config.json"
			if !tt.absent {
				sys.files[configPath] = []byte(tt.config)
			}
			// A write of any kind would be a change the migration had no
			// business making.
			sys.writeErr[configPath+".tmp"] = errors.New("must not write")

			err := renameNoProxyHosts(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
			require.NoError(s.T(), err)
			if !tt.absent {
				require.Equal(s.T(), tt.config, string(sys.files[configPath]))
			}
		})
	}
}

func (s *FSMigrateSuite) TestRenameNoProxyHostsErrors() {
	configPath := "/loop/config.json"
	tests := []struct {
		name    string
		setup   func(*fakeSystem)
		wantErr string
	}{
		{
			name:    "config cannot be read",
			setup:   func(f *fakeSystem) { f.readErr[configPath] = errors.New("permission denied") },
			wantErr: "reading /loop/config.json",
		},
		{
			name:    "config is not valid hujson",
			setup:   func(f *fakeSystem) { f.files[configPath] = []byte(`{"no_proxy_hosts": [`) },
			wantErr: "parsing /loop/config.json",
		},
		{
			name:    "config is not an object",
			setup:   func(f *fakeSystem) { f.files[configPath] = []byte(`["my-service"]`) },
			wantErr: "expected JSON object at top level",
		},
		{
			name: "migrated config cannot be written",
			setup: func(f *fakeSystem) {
				f.files[configPath] = []byte(`{"no_proxy_hosts": ["my-service"]}`)
				f.writeErr[configPath+".tmp"] = errors.New("read-only file system")
			},
			wantErr: "writing /loop/config.json",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			sys := newFakeSystem()
			tt.setup(sys)

			err := renameNoProxyHosts(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
			require.Error(s.T(), err)
			require.Contains(s.T(), err.Error(), tt.wantErr)
		})
	}
}

func (s *FSMigrateSuite) TestObjectMemberIndexSkipsNonLiteralNames() {
	// A name that is itself an object cannot come out of the parser, but the
	// lookup must walk past it rather than panic.
	obj := &hujson.Object{Members: []hujson.ObjectMember{
		{Name: hujson.Value{Value: &hujson.Object{}}, Value: hujson.Value{Value: hujson.Literal(`1`)}},
		{Name: hujson.Value{Value: hujson.Literal(`"no_proxy"`)}, Value: hujson.Value{Value: hujson.Literal(`2`)}},
	}}

	require.Equal(s.T(), 1, objectMemberIndex(obj, "no_proxy"))
	require.Equal(s.T(), -1, objectMemberIndex(obj, "missing"))
}

func (s *FSMigrateSuite) TestLoadHJSONAtMissingFile() {
	v, err := loadHJSONAt(newFakeSystem(), filepath.Join("/loop", "nope.json"))
	require.NoError(s.T(), err)
	require.Nil(s.T(), v)
}
