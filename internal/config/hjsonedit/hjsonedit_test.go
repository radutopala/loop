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
		want    string
	}{
		{
			name: "creates the file",
			path: []string{"mounts"},
			item: "~/.aws:~/.aws:ro",
			want: "{\n  \"mounts\": [\n    \"~/.aws:~/.aws:ro\"\n  ]\n}\n",
		},
		{
			name:    "appends to an existing array, keeping comments",
			initial: "{\n  // my mounts\n  \"mounts\": [\n    \"a:b\", // first\n  ],\n}\n",
			path:    []string{"mounts"},
			item:    "c:d",
			want:    "{\n  // my mounts\n  \"mounts\": [\n    \"a:b\", // first\n    \"c:d\"\n  ],\n}\n",
		},
		{
			name:    "new array after existing keys",
			initial: "{\n  // project\n  \"claude_model\": \"opus\"\n}\n",
			path:    []string{"mounts"},
			item:    "c:d",
			want:    "{\n  // project\n  \"claude_model\": \"opus\",\n  \"mounts\": [\n    \"c:d\"\n  ]\n}\n",
		},
		{
			name:    "empty array gets just the item",
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
			name:    "appends an object laid out like its siblings",
			initial: "{\n  \"bash_shortcuts\": [\n    {\n      \"name\": \"lint\"\n    }\n  ]\n}\n",
			path:    []string{"bash_shortcuts"},
			item:    map[string]any{"args": []string{"-v"}, "name": "test"},
			want:    "{\n  \"bash_shortcuts\": [\n    {\n      \"name\": \"lint\"\n    },\n    {\n      \"args\": [\n        \"-v\"\n      ],\n      \"name\": \"test\"\n    }\n  ]\n}\n",
		},
		{
			name:    "follows the file's tab indentation",
			initial: "{\n\t\"gates\": {\n\t\t\"audit\": {}\n\t}\n}\n",
			path:    []string{"gates", "agentgate", "command_rules"},
			item:    map[string]any{"decision": "approve"},
			want:    "{\n\t\"gates\": {\n\t\t\"audit\": {},\n\t\t\"agentgate\": {\n\t\t\t\"command_rules\": [\n\t\t\t\t{\n\t\t\t\t\t\"decision\": \"approve\"\n\t\t\t\t}\n\t\t\t]\n\t\t}\n\t}\n}\n",
		},
		{
			name:    "an array on one line stays on one line",
			initial: "{\n  \"mounts\": [\"a:b\"]\n}\n",
			path:    []string{"mounts"},
			item:    "c:d",
			want:    "{\n  \"mounts\": [\"a:b\",\"c:d\"]\n}\n",
		},
		{
			name:    "an empty array in a laid-out file is filled one per line",
			initial: "{\n  \"mounts\": []\n}\n",
			path:    []string{"mounts"},
			item:    "c:d",
			want:    "{\n  \"mounts\": [\n    \"c:d\"\n  ]\n}\n",
		},
		{
			name:    "an empty array after a member on its own line",
			initial: "{\n  \"mounts\": []}",
			path:    []string{"mounts"},
			item:    "c:d",
			want:    "{\n  \"mounts\": [\n    \"c:d\"\n  ]}",
		},
		{
			name:    "an empty object in a laid-out file gets the member on its own line",
			initial: "{\n  \"gates\": {}\n}\n",
			path:    []string{"gates", "rules"},
			item:    "x",
			want:    "{\n  \"gates\": {\n    \"rules\": [\n      \"x\"\n    ]\n  }\n}\n",
		},
		{
			name:    "unindented top-level members fall back to two spaces",
			initial: "{\n\"a\": 1\n}",
			path:    []string{"mounts"},
			item:    "c:d",
			want:    "{\n\"a\": 1,\n  \"mounts\": [\n    \"c:d\"\n  ]\n}",
		},
		{
			name:    "an item holding empty containers",
			initial: "{\n  \"tasks\": [\n    {}\n  ]\n}\n",
			path:    []string{"tasks"},
			item:    map[string]any{"args": []string{}, "env": map[string]any{}},
			want:    "{\n  \"tasks\": [\n    {},\n    {\n      \"args\": [],\n      \"env\": {}\n    }\n  ]\n}\n",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			if tc.initial != "" {
				s.write(tc.initial)
			}
			require.NoError(s.T(), Append(osutil.RealSystem{}, s.path, tc.path, tc.item))
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
			err := Append(tc.fs, s.path, tc.path, "x")
			require.ErrorContains(s.T(), err, tc.wantErr)
			require.Equal(s.T(), tc.initial, s.read())
			_, statErr := os.Stat(s.path + ".tmp")
			require.True(s.T(), os.IsNotExist(statErr))
		})
	}
}

// TestMarshalError covers an item that can't be marshaled: it never
// reaches the file.
func (s *HJSONEditSuite) TestMarshalError() {
	s.write("{}")
	err := Append(osutil.RealSystem{}, s.path, []string{"mounts"}, func() {})
	require.ErrorContains(s.T(), err, "json: unsupported type")
	require.Equal(s.T(), "{}", s.read())
}
