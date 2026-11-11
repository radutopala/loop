package api

import (
	"cmp"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/osutil"
)

const learnMemoryPayload = `{"name":"lint-first","description":"Lint before pushing","type":"feedback","body":"Run make lint.\n\n**Why:** CI runs it."}`

const learnMemoryContent = "---\nname: lint-first\ndescription: Lint before pushing\nmetadata:\n  type: feedback\n---\n\nRun make lint.\n\n**Why:** CI runs it.\n"

// memorySetup points the server at a temp home and a temp project, and
// returns the project's auto-memory dir.
func (s *ServerSuite) memorySetup() string {
	home, dir := s.T().TempDir(), s.T().TempDir()
	s.srv.sys = homeSystem{home: home}
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", DirPath: dir}, nil)
	return filepath.Join(home, ".claude", "projects", osutil.EncodeClaudeProjectPath(dir), "memory")
}

func (s *ServerSuite) TestApplyLearnMemory() {
	tests := []struct {
		name      string
		index     string // "" = no MEMORY.md
		existing  string // "" = no memory file
		wantIndex string
		wantErr   string
	}{
		{name: "first memory", wantIndex: "- [lint-first](lint-first.md) — Lint before pushing\n"},
		{
			name:      "appends to the index",
			index:     "# Memory\n- [Other](other.md) — x",
			wantIndex: "# Memory\n- [Other](other.md) — x\n- [lint-first](lint-first.md) — Lint before pushing\n",
		},
		{
			name:      "already indexed",
			index:     "- [Lint](lint-first.md) — old\n",
			wantIndex: "- [Lint](lint-first.md) — old\n",
		},
		{name: "exists", index: "keep\n", existing: "mine\n", wantIndex: "keep\n", wantErr: `a memory named "lint-first" already exists`},
		{name: "retry after the index failed", index: "keep\n", existing: learnMemoryContent, wantIndex: "keep\n- [lint-first](lint-first.md) — Lint before pushing\n"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			memDir := s.memorySetup()
			path, indexPath := filepath.Join(memDir, "lint-first.md"), filepath.Join(memDir, "MEMORY.md")
			if tc.index != "" || tc.existing != "" {
				require.NoError(s.T(), os.MkdirAll(memDir, 0o700))
				require.NoError(s.T(), os.WriteFile(indexPath, []byte(tc.index), 0o644))
			}
			if tc.existing != "" {
				require.NoError(s.T(), os.WriteFile(path, []byte(tc.existing), 0o644))
			}

			status, errText := s.applyProposal(db.LearnKindMemory, learnMemoryPayload)
			index, err := os.ReadFile(indexPath)
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.wantIndex, string(index))
			data, err := os.ReadFile(path)
			require.NoError(s.T(), err)
			if tc.wantErr != "" {
				require.Equal(s.T(), db.LearnFailed, status)
				require.Contains(s.T(), errText, tc.wantErr)
				require.Equal(s.T(), "mine\n", string(data))
				return
			}
			require.Equal(s.T(), db.LearnApplied, status, errText)
			require.Equal(s.T(), learnMemoryContent, string(data))
		})
	}
}

func (s *ServerSuite) TestApplyLearnMemoryErrors() {
	memDir := filepath.Join("/home/testuser", ".claude", "projects", osutil.EncodeClaudeProjectPath("/p"), "memory")
	path, indexPath := filepath.Join(memDir, "lint-first.md"), filepath.Join(memDir, "MEMORY.md")
	tests := []struct {
		name     string
		channel  *db.Channel
		fileErr  error // reading the memory file; none = no file yet
		indexErr error // reading MEMORY.md; none = an empty one
		setup    func()
		wantErr  string
	}{
		{name: "no project dir", channel: &db.Channel{ChannelID: "ch-1"}, wantErr: "has no dir_path"},
		{name: "no home", setup: func() { s.sys.Override("UserHomeDir").Return("", errors.New("no home")) }, wantErr: "getting home dir"},
		{name: "memory file unreadable", fileErr: os.ErrPermission, wantErr: "checking the memory file"},
		{name: "index unreadable", indexErr: os.ErrPermission, wantErr: "reading MEMORY.md"},
		{name: "mkdir fails", setup: func() { s.sys.Override("MkdirAll", memDir, os.FileMode(0o700)).Return(os.ErrPermission) }, wantErr: "creating the memory dir"},
		{name: "file write fails", setup: func() {
			s.sys.Override("WriteFile", path, mock.Anything, os.FileMode(0o644)).Return(os.ErrPermission)
		}, wantErr: "writing the memory file"},
		{name: "index write fails", setup: func() {
			s.sys.Override("WriteFile", path, mock.Anything, os.FileMode(0o644)).Return(nil)
			s.sys.On("WriteFile", indexPath, mock.Anything, os.FileMode(0o644)).Return(os.ErrPermission)
		}, wantErr: "writing MEMORY.md"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.srv.sys = s.sys
			ch := tc.channel
			if ch == nil {
				ch = &db.Channel{ChannelID: "ch-1", DirPath: "/p"}
			}
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(ch, nil)
			s.sys.Override("ReadFile", path).Return(nil, cmp.Or(tc.fileErr, os.ErrNotExist))
			s.sys.On("ReadFile", indexPath).Return(nil, tc.indexErr)
			if tc.setup != nil {
				tc.setup()
			}
			status, errText := s.applyProposal(db.LearnKindMemory, learnMemoryPayload)
			require.Equal(s.T(), db.LearnFailed, status)
			require.Contains(s.T(), errText, tc.wantErr)
		})
	}
}

// TestPreviewLearnMemory diffs both files the memory adds to, and writes
// neither.
func (s *ServerSuite) TestPreviewLearnMemory() {
	tests := []struct {
		name     string
		index    string // "" = no MEMORY.md
		existing string // "" = no memory file
		wantDiff string
		wantErr  string
	}{
		{
			name: "new index",
			wantDiff: "--- /dev/null\n+++ FILE\n@@ -0,0 +1,10 @@\n" + prefixLines(learnMemoryContent, "+") +
				"--- /dev/null\n+++ INDEX\n@@ -0,0 +1 @@\n+- [lint-first](lint-first.md) — Lint before pushing\n",
		},
		{
			name: "existing index", index: "# Memory\n",
			wantDiff: "--- /dev/null\n+++ FILE\n@@ -0,0 +1,10 @@\n" + prefixLines(learnMemoryContent, "+") +
				"--- INDEX\n+++ INDEX\n@@ -1 +1,2 @@\n # Memory\n+- [lint-first](lint-first.md) — Lint before pushing\n",
		},
		{name: "exists", index: "# Memory\n", existing: "mine\n", wantErr: "already exists"},
		{
			name: "retry after the index failed", index: "# Memory\n", existing: learnMemoryContent,
			wantDiff: "--- INDEX\n+++ INDEX\n@@ -1 +1,2 @@\n # Memory\n+- [lint-first](lint-first.md) — Lint before pushing\n",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			memDir := s.memorySetup()
			path, indexPath := filepath.Join(memDir, "lint-first.md"), filepath.Join(memDir, "MEMORY.md")
			if tc.index != "" {
				require.NoError(s.T(), os.MkdirAll(memDir, 0o700))
				require.NoError(s.T(), os.WriteFile(indexPath, []byte(tc.index), 0o644))
			}
			if tc.existing != "" {
				require.NoError(s.T(), os.WriteFile(path, []byte(tc.existing), 0o644))
			}
			s.store.On("GetLearnProposal", mock.Anything, int64(7)).Return(&db.LearnProposal{ID: 7, ChannelID: "ch-1", Kind: db.LearnKindMemory, Payload: learnMemoryPayload, Status: db.LearnPending}, nil)

			w := s.serve("GET", "/api/learn/proposals/7/preview", "")
			require.Equal(s.T(), http.StatusOK, w.Code, w.Body.String())
			var got learnPreviewResponse
			require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &got))
			require.Equal(s.T(), path, got.Path)
			want := strings.NewReplacer("FILE", path, "INDEX", indexPath).Replace(tc.wantDiff)
			require.Equal(s.T(), want, got.Diff)
			switch {
			case tc.wantErr != "":
				require.Contains(s.T(), got.Error, tc.wantErr)
			case tc.existing == "":
				require.Empty(s.T(), got.Error)
				require.NoFileExists(s.T(), path, "the preview writes nothing")
			default:
				require.Empty(s.T(), got.Error)
			}
			if tc.index == "" {
				require.NoFileExists(s.T(), indexPath, "the preview writes nothing")
			} else {
				index, err := os.ReadFile(indexPath)
				require.NoError(s.T(), err)
				require.Equal(s.T(), tc.index, string(index), "the preview writes nothing")
			}
		})
	}
}

// prefixLines puts prefix before each line of s.
func prefixLines(s, prefix string) string {
	lines := strings.SplitAfter(s, "\n")
	var b strings.Builder
	for _, l := range lines {
		if l != "" {
			b.WriteString(prefix + l)
		}
	}
	return b.String()
}
