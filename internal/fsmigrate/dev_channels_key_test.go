package fsmigrate

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/stretchr/testify/require"
)

// --- dev_channels_key_test covers the migration that drops the removed
// claude_dangerously_load_development_channels setting.

func (s *FSMigrateSuite) TestDropDevChannelsKey() {
	tests := []struct {
		name     string
		config   string
		expected string
	}{
		{
			name: "set key with its comment",
			config: `{
  // Claude
  "claude_model": "claude-opus-5-5",
  "claude_dangerously_load_development_channels": true, // push messages
  "api_addr": ":8222"
}`,
			expected: `{
  // Claude
  "claude_model": "claude-opus-5-5",
  "api_addr": ":8222"
}`,
		},
		{
			name: "comments above the key stay",
			config: `{
  "claude_model": "claude-opus-5-5",
  // let agents push to each other
  "claude_dangerously_load_development_channels": true,
  "api_addr": ":8222"
}`,
			expected: `{
  "claude_model": "claude-opus-5-5",
  // let agents push to each other
  "api_addr": ":8222"
}`,
		},
		{
			name: "first key",
			config: `{
  "claude_dangerously_load_development_channels": true,
  "api_addr": ":8222"
}`,
			expected: `{
  "api_addr": ":8222"
}`,
		},
		{
			name: "last key",
			config: `{
  "api_addr": ":8222",
  "claude_dangerously_load_development_channels": true // push messages
}`,
			expected: `{
  "api_addr": ":8222"
}`,
		},
		{
			name:     "only key",
			config:   `{"claude_dangerously_load_development_channels": true}`,
			expected: `{}`,
		},
		{
			name:     "one line",
			config:   `{"api_addr": ":8222", "claude_dangerously_load_development_channels": true, "log_level": "debug"}`,
			expected: `{"api_addr": ":8222", "log_level": "debug"}`,
		},
		{
			name: "commented-out copy from the example config",
			config: `{
  "api_addr": ":8222",
  //"claude_bin_path": "claude",
  //"claude_dangerously_load_development_channels": false, // when true, pass the flag
  //"claude_batch_disallowed_tools": ["Monitor"],
  "log_level": "debug"
  //"claude_dangerously_load_development_channels": false,
}`,
			expected: `{
  "api_addr": ":8222",
  //"claude_bin_path": "claude",
  //"claude_batch_disallowed_tools": ["Monitor"],
  "log_level": "debug"
}`,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			sys := newFakeSystem()
			configPath := filepath.Join("/loop", "config.json")
			sys.files[configPath] = []byte(tt.config)

			require.NoError(s.T(), dropDevChannelsKey(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}))
			require.Equal(s.T(), tt.expected, string(sys.files[configPath]))
		})
	}
}

func (s *FSMigrateSuite) TestDropDevChannelsKeyMigratesProjectConfigs() {
	sys := newFakeSystem()
	globalPath := "/loop/config.json"
	projectPath := filepath.Join("/work/project", ".loop", "config.json")
	sys.files[globalPath] = []byte(`{"claude_dangerously_load_development_channels": true}`)
	sys.files[projectPath] = []byte(`{"claude_dangerously_load_development_channels": false}`)

	err := dropDevChannelsKey(context.Background(), &Ctx{
		Sys:         sys,
		LoopDir:     "/loop",
		ProjectDirs: []string{"/work/project", "/work/no-config"},
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), `{}`, string(sys.files[globalPath]))
	require.Equal(s.T(), `{}`, string(sys.files[projectPath]))
}

func (s *FSMigrateSuite) TestDropDevChannelsKeyNoOps() {
	tests := []struct {
		name   string
		config string
		absent bool
	}{
		{name: "config is missing", absent: true},
		{name: "key was never set", config: `{"api_addr": ":8222"}`},
		{name: "key only nested", config: `{"mcp": {"claude_dangerously_load_development_channels": true}}`},
		{name: "comment on the line before", config: `{"api_addr": ":8222", //"claude_dangerously_load_development_channels"
  "log_level": "debug"}`},
		{name: "comment mentions it unquoted", config: `{
  // claude_dangerously_load_development_channels is gone
  "api_addr": ":8222"
}`},
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

			require.NoError(s.T(), dropDevChannelsKey(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}))
			if !tt.absent {
				require.Equal(s.T(), tt.config, string(sys.files[configPath]))
			}
		})
	}
}

func (s *FSMigrateSuite) TestDropDevChannelsKeyErrors() {
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
			name:    "config is not an object",
			setup:   func(f *fakeSystem) { f.files[configPath] = []byte(`[true]`) },
			wantErr: "expected JSON object at top level",
		},
		{
			name: "cleaned config cannot be written",
			setup: func(f *fakeSystem) {
				f.files[configPath] = []byte(`{"claude_dangerously_load_development_channels": true}`)
				f.writeErr[configPath+".tmp"] = errors.New("read-only file system")
			},
			wantErr: "writing /loop/config.json",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			sys := newFakeSystem()
			tt.setup(sys)

			err := dropDevChannelsKey(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
			require.Error(s.T(), err)
			require.Contains(s.T(), err.Error(), tt.wantErr)
		})
	}
}
