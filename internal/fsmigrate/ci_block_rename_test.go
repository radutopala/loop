package fsmigrate

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/stretchr/testify/require"
)

// --- ci_block_rename_test covers the migration that renames the ci config
// block to pr.

func (s *FSMigrateSuite) TestRenameCIBlock() {
	tests := []struct {
		name     string
		config   string
		expected string
	}{
		{
			name: "block with its comments",
			config: `{
  // PR watching
  "ci": {"auto_fix": false}, // not here
  "api_addr": ":8222"
}`,
			expected: `{
  // PR watching
  "pr": {"auto_fix": false}, // not here
  "api_addr": ":8222"
}`,
		},
		{
			name:     "pr set already wins",
			config:   `{"pr": {"auto_fix": true}, "api_addr": ":8222", "ci": {"auto_fix": false}}`,
			expected: `{"pr": {"auto_fix": true}, "api_addr": ":8222"}`,
		},
		{
			name:     "key given twice",
			config:   `{"ci": {"auto_fix": false}, "ci": {"max_attempts": 2}}`,
			expected: `{"pr": {"auto_fix": false}}`,
		},
		{
			name: "commented-out copy from the example config",
			config: `{
  "api_addr": ":8222",
  //"ci": {
  //  "auto_fix": true
  //},
  "log_level": "debug"
  // "ci" : {}
}`,
			expected: `{
  "api_addr": ":8222",
  //"pr": {
  //  "auto_fix": true
  //},
  "log_level": "debug"
  // "pr" : {}
}`,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			sys := newFakeSystem()
			configPath := filepath.Join("/loop", "config.json")
			sys.files[configPath] = []byte(tt.config)

			require.NoError(s.T(), renameCIBlock(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}))
			require.Equal(s.T(), tt.expected, string(sys.files[configPath]))
		})
	}
}

func (s *FSMigrateSuite) TestRenameCIBlockMigratesProjectsAndTrust() {
	sys := newFakeSystem()
	globalPath := "/loop/config.json"
	projectPath := filepath.Join("/work/project", ".loop", "config.json")
	sys.files[globalPath] = []byte(`{"ci": {"poll_sec": 30}}`)
	sys.files[projectPath] = []byte(`{"ci": {"auto_fix": false}}`)

	var renamed []string
	err := renameCIBlock(context.Background(), &Ctx{
		Sys:         sys,
		LoopDir:     "/loop",
		ProjectDirs: []string{"/work/project", "/work/no-config"},
		RenameTrustedField: func(from, to string) error {
			renamed = append(renamed, from+"->"+to)
			return nil
		},
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), `{"pr": {"poll_sec": 30}}`, string(sys.files[globalPath]))
	require.Equal(s.T(), `{"pr": {"auto_fix": false}}`, string(sys.files[projectPath]))
	require.Equal(s.T(), []string{"ci->pr"}, renamed)
}

func (s *FSMigrateSuite) TestRenameCIBlockNoOps() {
	tests := []struct {
		name   string
		config string
		absent bool
	}{
		{name: "config is missing", absent: true},
		{name: "key was never set", config: `{"api_addr": ":8222"}`},
		{name: "key only nested", config: `{"mcp": {"ci": true}}`},
		{name: "comment mentions it unquoted", config: `{
  // ci is pr now
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

			require.NoError(s.T(), renameCIBlock(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}))
			if !tt.absent {
				require.Equal(s.T(), tt.config, string(sys.files[configPath]))
			}
		})
	}
}

func (s *FSMigrateSuite) TestRenameCIBlockErrors() {
	configPath := "/loop/config.json"
	tests := []struct {
		name    string
		setup   func(*fakeSystem)
		trust   error
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
			name: "renamed config cannot be written",
			setup: func(f *fakeSystem) {
				f.files[configPath] = []byte(`{"ci": {}}`)
				f.writeErr[configPath+".tmp"] = errors.New("read-only file system")
			},
			wantErr: "writing /loop/config.json",
		},
		{
			name:    "trust cannot be renamed",
			setup:   func(*fakeSystem) {},
			trust:   errors.New("trust file locked"),
			wantErr: "trust file locked",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			sys := newFakeSystem()
			tt.setup(sys)

			err := renameCIBlock(context.Background(), &Ctx{
				Sys:                sys,
				LoopDir:            "/loop",
				RenameTrustedField: func(string, string) error { return tt.trust },
			})
			require.ErrorContains(s.T(), err, tt.wantErr)
		})
	}
}
