package fsmigrate

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/stretchr/testify/require"
)

// --- proxies_block_test covers the migration that moves the top-level
// http_proxy, https_proxy and no_proxy keys into a proxies block.

func (s *FSMigrateSuite) TestMoveProxiesIntoBlock() {
	tests := []struct {
		name     string
		config   string
		expected string
	}{
		{
			name: "keys move in file order, keeping comments and position",
			config: `{
  "api_addr": ":8222",
  // corporate proxy
  "http_proxy": "http://127.0.0.1:3128",
  "copy_files": [],
  "https_proxy": "http://127.0.0.1:3128",
  "no_proxy": [
    "my-service",
    "my-cache"
  ],
  "mounts": []
}`,
			expected: `{
  "api_addr": ":8222",
  "proxies": {
    // corporate proxy
    "http_proxy": "http://127.0.0.1:3128",
    "https_proxy": "http://127.0.0.1:3128",
    "no_proxy": [
      "my-service",
      "my-cache"
    ]
  },
  "copy_files": [],
  "mounts": []
}`,
		},
		{
			name: "last keys with a trailing comma leave none inside the block",
			config: `{
  "api_addr": ":8222",
  "http_proxy": "http://127.0.0.1:3128",
}`,
			expected: `{
  "api_addr": ":8222",
  "proxies": {
    "http_proxy": "http://127.0.0.1:3128"
  }
}`,
		},
		{
			name:     "compact config stays compact",
			config:   `{"api_addr": ":8222", "https_proxy": "http://p:1", "no_proxy": ["db"]}`,
			expected: `{"api_addr": ":8222", "proxies": { "https_proxy": "http://p:1", "no_proxy": ["db"]}}`,
		},
		{
			name: "an existing block wins, gains new keys and joins no_proxy",
			config: `{
  "proxies": {
    "http_proxy": "http://new:1",
    "no_proxy": ["db"]
  },
  "http_proxy": "http://old:1",
  "https_proxy": "http://old:2",
  "no_proxy": ["cache"]
}`,
			expected: `{
  "proxies": {
    "http_proxy": "http://new:1",
    "no_proxy": ["db","cache"],
    "https_proxy": "http://old:2"
  }
}`,
		},
		{
			name:     "no_proxy that cannot be joined keeps the block's value",
			config:   `{"proxies": {"no_proxy": "db"}, "no_proxy": ["cache"]}`,
			expected: `{"proxies": {"no_proxy": "db"}}`,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			sys := newFakeSystem()
			configPath := filepath.Join("/loop", "config.json")
			sys.files[configPath] = []byte(tt.config)

			require.NoError(s.T(), moveProxiesIntoBlock(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}))
			require.Equal(s.T(), tt.expected, string(sys.files[configPath]))
		})
	}
}

func (s *FSMigrateSuite) TestMoveProxiesIntoBlockMigratesProjectConfigs() {
	sys := newFakeSystem()
	globalPath := "/loop/config.json"
	projectPath := filepath.Join("/work/project", ".loop", "config.json")
	sys.files[globalPath] = []byte(`{"http_proxy": "http://p:1"}`)
	sys.files[projectPath] = []byte(`{"no_proxy": ["my-service"]}`)

	err := moveProxiesIntoBlock(context.Background(), &Ctx{
		Sys:         sys,
		LoopDir:     "/loop",
		ProjectDirs: []string{"/work/project", "/work/no-config"},
	})
	require.NoError(s.T(), err)

	require.Equal(s.T(), `{"proxies": {"http_proxy": "http://p:1"}}`, string(sys.files[globalPath]))
	require.Equal(s.T(), `{"proxies": {"no_proxy": ["my-service"]}}`, string(sys.files[projectPath]))
}

func (s *FSMigrateSuite) TestMoveProxiesIntoBlockNoOps() {
	tests := []struct {
		name   string
		config string
		absent bool
	}{
		{name: "config is missing", absent: true},
		{name: "no legacy key", config: `{"api_addr": ":8222", "proxies": {"http_proxy": "http://p:1"}}`},
		{name: "proxies is not an object", config: `{"proxies": "http://p:1", "http_proxy": "http://p:1"}`},
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

			require.NoError(s.T(), moveProxiesIntoBlock(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}))
			if !tt.absent {
				require.Equal(s.T(), tt.config, string(sys.files[configPath]))
			}
		})
	}
}

func (s *FSMigrateSuite) TestMoveProxiesIntoBlockErrors() {
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
			setup:   func(f *fakeSystem) { f.files[configPath] = []byte(`{"http_proxy": `) },
			wantErr: "parsing /loop/config.json",
		},
		{
			name:    "config is not an object",
			setup:   func(f *fakeSystem) { f.files[configPath] = []byte(`["http_proxy"]`) },
			wantErr: "expected JSON object at top level",
		},
		{
			name: "migrated config cannot be written",
			setup: func(f *fakeSystem) {
				f.files[configPath] = []byte(`{"http_proxy": "http://p:1"}`)
				f.writeErr[configPath+".tmp"] = errors.New("read-only file system")
			},
			wantErr: "writing /loop/config.json",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			sys := newFakeSystem()
			tt.setup(sys)

			err := moveProxiesIntoBlock(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
			require.Error(s.T(), err)
			require.Contains(s.T(), err.Error(), tt.wantErr)
		})
	}
}
