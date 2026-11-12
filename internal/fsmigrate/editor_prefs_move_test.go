package fsmigrate

import (
	"context"
	"errors"

	"github.com/stretchr/testify/require"
)

// --- editor_prefs_move_test covers the migration that moves
// auto_save_on_blur and preview_tabs from the desktop block to editor.

func (s *FSMigrateSuite) TestMoveEditorPrefs() {
	tests := []struct {
		name     string
		config   string
		expected string
	}{
		{
			name: "editor block created after desktop",
			config: `{
  // app prefs
  "desktop": {
    "theme": "dark",
    // keep tabs
    "preview_tabs": false, // transient tabs
    "auto_save_on_blur": true,
    "islands": true
  },
  "api_addr": ":8222"
}`,
			expected: `{
  // app prefs
  "desktop": {
    "theme": "dark",
    // keep tabs
    "islands": true
  },
  "editor": {
    "preview_tabs": false,
    "auto_save_on_blur": true
  },
  "api_addr": ":8222"
}`,
		},
		{
			name: "desktop is the last member",
			config: `{
  "api_addr": ":8222",
  "desktop": {
    "auto_save_on_blur": true
  }
}`,
			expected: `{
  "api_addr": ":8222",
  "desktop": {
  },
  "editor": {
    "auto_save_on_blur": true
  }
}`,
		},
		{
			name:     "last member with a trailing comma",
			config:   `{"desktop": {"preview_tabs": false},}`,
			expected: `{"desktop": {}, "editor": { "preview_tabs": false},}`,
		},
		{
			name: "into an editor block, whose value wins",
			config: `{
  "editor": {
    "tab_size": 2,
    "preview_tabs": true,
  },
  "desktop": {
    "auto_save_on_blur": true,
    "preview_tabs": false,
    "theme": "light"
  }
}`,
			expected: `{
  "editor": {
    "tab_size": 2,
    "preview_tabs": true,
    "auto_save_on_blur": true,
  },
  "desktop": {
    "theme": "light"
  }
}`,
		},
		{
			name:     "into an empty editor block",
			config:   `{"editor": {}, "desktop": {"preview_tabs": false}}`,
			expected: `{"editor": { "preview_tabs": false}, "desktop": {}}`,
		},
		{
			name:     "key given twice",
			config:   `{"desktop": {"preview_tabs": false, "preview_tabs": true}}`,
			expected: `{"desktop": {}, "editor": { "preview_tabs": false}}`,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			sys := newFakeSystem()
			configPath := "/loop/config.json"
			sys.files[configPath] = []byte(tt.config)

			ctx := &Ctx{Sys: sys, LoopDir: "/loop"}
			require.NoError(s.T(), moveEditorPrefs(context.Background(), ctx))
			require.Equal(s.T(), tt.expected, string(sys.files[configPath]))

			// A second run finds nothing to move.
			require.NoError(s.T(), moveEditorPrefs(context.Background(), ctx))
			require.Equal(s.T(), tt.expected, string(sys.files[configPath]))
		})
	}
}

func (s *FSMigrateSuite) TestMoveEditorPrefsNoOps() {
	tests := []struct {
		name   string
		config string
		absent bool
	}{
		{name: "config is missing", absent: true},
		{name: "no desktop block", config: `{"editor": {"preview_tabs": false}}`},
		{name: "desktop is not an object", config: `{"desktop": true}`},
		{name: "editor is not an object", config: `{"desktop": {"preview_tabs": false}, "editor": []}`},
		{name: "nothing to move", config: `{"desktop": {"theme": "dark"}}`},
		{name: "keys only at the top level", config: `{"preview_tabs": false, "desktop": {}}`},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			sys := newFakeSystem()
			configPath := "/loop/config.json"
			if !tt.absent {
				sys.files[configPath] = []byte(tt.config)
			}
			sys.writeErr[configPath+".tmp"] = errors.New("must not write")

			require.NoError(s.T(), moveEditorPrefs(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}))
			if !tt.absent {
				require.Equal(s.T(), tt.config, string(sys.files[configPath]))
			}
		})
	}
}

func (s *FSMigrateSuite) TestMoveEditorPrefsErrors() {
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
			name: "moved config cannot be written",
			setup: func(f *fakeSystem) {
				f.files[configPath] = []byte(`{"desktop": {"preview_tabs": false}}`)
				f.writeErr[configPath+".tmp"] = errors.New("read-only file system")
			},
			wantErr: "writing /loop/config.json",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			sys := newFakeSystem()
			tt.setup(sys)
			err := moveEditorPrefs(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
			require.ErrorContains(s.T(), err, tt.wantErr)
		})
	}
}
