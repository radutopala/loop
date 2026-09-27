package hjsonedit

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/osutil"
)

// failFS is the real filesystem with one operation made to fail.
type failFS struct {
	osutil.RealSystem
	readErr, writeErr, renameErr, mkdirErr error
}

func (f failFS) ReadFile(name string) ([]byte, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.RealSystem.ReadFile(name)
}

func (f failFS) WriteFile(name string, data []byte, perm os.FileMode) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	return f.RealSystem.WriteFile(name, data, perm)
}

func (f failFS) Rename(oldpath, newpath string) error {
	if f.renameErr != nil {
		return f.renameErr
	}
	return f.RealSystem.Rename(oldpath, newpath)
}

func (f failFS) MkdirAll(path string, perm os.FileMode) error {
	if f.mkdirErr != nil {
		return f.mkdirErr
	}
	return f.RealSystem.MkdirAll(path, perm)
}

type HJSONEditSuite struct {
	suite.Suite
	path string
}

func TestHJSONEditSuite(t *testing.T) {
	suite.Run(t, new(HJSONEditSuite))
}

func (s *HJSONEditSuite) SetupTest() {
	s.path = filepath.Join(s.T().TempDir(), ".loop", "config.json")
}

func (s *HJSONEditSuite) write(content string) {
	require.NoError(s.T(), os.MkdirAll(filepath.Dir(s.path), 0o755))
	require.NoError(s.T(), os.WriteFile(s.path, []byte(content), 0o644))
}

func (s *HJSONEditSuite) read() string {
	data, err := os.ReadFile(s.path)
	require.NoError(s.T(), err)
	return string(data)
}

func (s *HJSONEditSuite) TestAppend() {
	tests := []struct {
		name    string
		initial string // "" = no file
		path    []string
		item    any
		seed    []any
		want    string
	}{
		{
			name: "creates the file",
			path: []string{"mounts"},
			item: "~/.aws:~/.aws:ro",
			want: `{"mounts":["~/.aws:~/.aws:ro"]}` + "\n",
		},
		{
			name:    "appends to an existing array, keeping comments",
			initial: "{\n  // my mounts\n  \"mounts\": [\n    \"a:b\", // first\n  ],\n}\n",
			path:    []string{"mounts"},
			item:    "c:d",
			want:    "{\n  // my mounts\n  \"mounts\": [\n    \"a:b\", // first\n\"c:d\"\n  ],\n}\n",
		},
		{
			name:    "new array starts with the seed",
			initial: "{\n  // project\n  \"claude_model\": \"opus\"\n}\n",
			path:    []string{"mounts"},
			item:    "c:d",
			seed:    []any{"a:b"},
			want:    "{\n  // project\n  \"claude_model\": \"opus\",\"mounts\":[\"a:b\",\"c:d\"]\n}\n",
		},
		{
			// An empty project list keeps the global one, so it's seeded too.
			name:    "empty array starts with the seed",
			initial: `{"mounts": []}`,
			path:    []string{"mounts"},
			item:    "c:d",
			seed:    []any{"a:b"},
			want:    `{"mounts": ["a:b","c:d"]}`,
		},
		{
			name:    "empty array without a seed gets just the item",
			initial: `{"mounts": []}`,
			path:    []string{"mounts"},
			item:    "c:d",
			want:    `{"mounts": ["c:d"]}`,
		},
		{
			name:    "creates missing nested objects",
			initial: "{\"gates\": {\"audit\": {}}}",
			path:    []string{"gates", "agentgate", "command_rules"},
			item:    map[string]any{"commands": []string{"git"}, "decision": "approve"},
			want:    `{"gates": {"audit": {},"agentgate":{"command_rules":[{"commands":["git"],"decision":"approve"}]}}}`,
		},
		{
			name:    "escapes pointer keys",
			initial: "{}",
			path:    []string{"a/b~c"},
			item:    1,
			want:    `{"a/b~c":[1]}`,
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			if tc.initial != "" {
				s.write(tc.initial)
			}
			require.NoError(s.T(), Append(osutil.RealSystem{}, s.path, tc.path, tc.item, tc.seed))
			require.Equal(s.T(), tc.want, s.read())
			_, err := os.Stat(s.path + ".tmp")
			require.True(s.T(), os.IsNotExist(err))
		})
	}
}

func (s *HJSONEditSuite) TestAppendErrors() {
	boom := errors.New("boom")
	tests := []struct {
		name    string
		initial string
		fs      FS
		path    []string
		wantErr string
	}{
		{"empty path", "{}", osutil.RealSystem{}, nil, "empty path"},
		{"read error", "{}", failFS{readErr: boom}, []string{"mounts"}, "reading"},
		{"invalid hjson", "{", osutil.RealSystem{}, []string{"mounts"}, "parsing"},
		{"top level not an object", "[]", osutil.RealSystem{}, []string{"mounts"}, "the top level is not an object"},
		{"parent not an object", `{"gates": []}`, osutil.RealSystem{}, []string{"gates", "agentgate"}, "gates is not an object"},
		{"target not an array", `{"mounts": null}`, osutil.RealSystem{}, []string{"mounts"}, "mounts is not an array"},
		{"mkdir error", "{}", failFS{mkdirErr: boom}, []string{"mounts"}, "creating"},
		{"write error", "{}", failFS{writeErr: boom}, []string{"mounts"}, "writing"},
		{"rename error", "{}", failFS{renameErr: boom}, []string{"mounts"}, "replacing"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.write(tc.initial)
			err := Append(tc.fs, s.path, tc.path, "x", nil)
			require.ErrorContains(s.T(), err, tc.wantErr)
			require.Equal(s.T(), tc.initial, s.read())
			_, statErr := os.Stat(s.path + ".tmp")
			require.True(s.T(), os.IsNotExist(statErr))
		})
	}
}

// TestPatchError covers a patch hujson rejects: an item that can't be
// marshaled never reaches the file.
func (s *HJSONEditSuite) TestPatchError() {
	s.write("{}")
	err := Append(osutil.RealSystem{}, s.path, []string{"mounts"}, func() {}, nil)
	require.Error(s.T(), err)
	require.Equal(s.T(), "{}", s.read())
}
