package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
)

// gitOut runs git in dir and returns its trimmed stdout.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	require.NoError(t, err, "git %v", args)
	return strings.TrimSpace(string(out))
}

// commitFile writes name=content in dir and commits it with msg.
func commitFile(t *testing.T, dir, name, content, msg string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", msg)
}

// ── handleCommitDiff ──

func (s *ServerSuite) TestCommitDiff() {
	dir := initGitRepo(s.T())
	root := gitOut(s.T(), dir, "rev-parse", "HEAD")
	commitFile(s.T(), dir, "a.txt", "one\ntwo\n", "add a")
	commitFile(s.T(), dir, "a.txt", "one\nTWO\nthree\n", "edit a")
	edit := gitOut(s.T(), dir, "rev-parse", "HEAD")

	// A merge: main gets b.txt, the side branch c.txt; merging brings in c.txt.
	base := gitOut(s.T(), dir, "rev-parse", "--abbrev-ref", "HEAD")
	gitRun(s.T(), dir, "checkout", "-q", "-b", "side")
	commitFile(s.T(), dir, "c.txt", "side\n", "add c")
	gitRun(s.T(), dir, "checkout", "-q", base)
	commitFile(s.T(), dir, "b.txt", "main\n", "add b")
	gitRun(s.T(), dir, "merge", "-q", "--no-ff", "-m", "merge side", "side")
	merge := gitOut(s.T(), dir, "rev-parse", "HEAD")

	cases := []struct {
		name      string
		commit    string
		paths     []string
		additions int
		deletions int
		diffHas   string
	}{
		{name: "regular commit", commit: edit, paths: []string{"a.txt"}, additions: 2, deletions: 1, diffHas: "+TWO"},
		{name: "abbreviated hash", commit: edit[:7], paths: []string{"a.txt"}, additions: 2, deletions: 1, diffHas: "-two"},
		{name: "root commit", commit: root, paths: []string{"README.md"}, additions: 1, diffHas: "+# Test"},
		{name: "merge against first parent", commit: merge, paths: []string{"c.txt"}, additions: 1, diffHas: "+side"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.store.On("GetChannel", mock.Anything, "ch-1").
				Return(&db.Channel{ChannelID: "ch-1", DirPath: dir}, nil).Once()

			rec := s.testRequest("GET", "/api/channels/ch-1/diff?commit="+tc.commit, "")
			require.Equal(s.T(), http.StatusOK, rec.Code, rec.Body.String())

			var resp diffResponse
			require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
			paths := make([]string, 0, len(resp.Files))
			for _, f := range resp.Files {
				paths = append(paths, f.Path)
			}
			require.Equal(s.T(), tc.paths, paths)
			require.Equal(s.T(), tc.additions, resp.TotalAdditions)
			require.Equal(s.T(), tc.deletions, resp.TotalDeletions)
			require.Contains(s.T(), resp.Diff, tc.diffHas)
		})
	}
}

func (s *ServerSuite) TestCommitDiffErrors() {
	dir := initGitRepo(s.T())
	cases := []struct {
		name   string
		commit string
		code   int
		body   string
	}{
		{name: "ref name rejected", commit: "HEAD", code: http.StatusBadRequest, body: "invalid commit hash"},
		{name: "option rejected", commit: "--output=x", code: http.StatusBadRequest, body: "invalid commit hash"},
		{name: "too short", commit: "abc", code: http.StatusBadRequest, body: "invalid commit hash"},
		{name: "unknown commit", commit: "deadbeefdeadbeef", code: http.StatusNotFound, body: "git show failed: fatal:"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.store.On("GetChannel", mock.Anything, "ch-1").
				Return(&db.Channel{ChannelID: "ch-1", DirPath: dir}, nil).Once()

			rec := s.testRequest("GET", "/api/channels/ch-1/diff?commit="+tc.commit, "")
			require.Equal(s.T(), tc.code, rec.Code)
			require.Contains(s.T(), rec.Body.String(), tc.body)
		})
	}
}

func (s *ServerSuite) TestCommitDiffNotARepo() {
	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: s.T().TempDir()}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/diff?commit=deadbeef", "")
	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

// ── handleReadFile ?ref= ──

func (s *ServerSuite) TestReadFileAtCommit() {
	dir := initGitRepo(s.T())
	commitFile(s.T(), dir, "sub/a.txt", "old\n", "add a")
	commitFile(s.T(), dir, "sub/bin.dat", "x\x00y", "add binary")
	first := gitOut(s.T(), dir, "rev-parse", "HEAD")
	commitFile(s.T(), dir, "sub/a.txt", "new\n", "edit a")
	// The file and its directory are gone on disk; the commit still has them.
	require.NoError(s.T(), os.RemoveAll(filepath.Join(dir, "sub")))

	cases := []struct {
		name   string
		query  string
		code   int
		body   string
		binary bool
	}{
		{name: "text as of the commit", query: "path=sub/a.txt&ref=" + first, code: http.StatusOK, body: "old\n"},
		{name: "cleaned path", query: "path=sub/../sub/a.txt&ref=" + first[:8], code: http.StatusOK, body: "old\n"},
		{name: "binary", query: "path=sub/bin.dat&ref=" + first, code: http.StatusOK, binary: true},
		{name: "missing at commit", query: "path=nope.txt&ref=" + first, code: http.StatusNotFound, body: "file not found at commit"},
		{name: "ref name rejected", query: "path=sub/a.txt&ref=HEAD", code: http.StatusBadRequest, body: "invalid commit hash"},
		{name: "traversal rejected", query: "path=../x&ref=" + first, code: http.StatusBadRequest, body: "path traversal not allowed"},
		{name: "empty path", query: "path=&ref=" + first, code: http.StatusBadRequest, body: "path is required"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.store.On("GetChannel", mock.Anything, "ch-1").
				Return(&db.Channel{ChannelID: "ch-1", DirPath: dir}, nil).Once()

			rec := s.testRequest("GET", "/api/channels/ch-1/file?"+tc.query, "")
			require.Equal(s.T(), tc.code, rec.Code)
			if tc.binary {
				require.Equal(s.T(), "true", rec.Header().Get("X-File-Binary"))
				require.Empty(s.T(), rec.Body.String())
				return
			}
			require.Contains(s.T(), rec.Body.String(), tc.body)
		})
	}
}

func (s *ServerSuite) TestReadFileAtCommitTooLarge() {
	dir := initGitRepo(s.T())
	commitFile(s.T(), dir, "big.txt", string(bytes.Repeat([]byte("a"), maxFileSize+1)), "big")
	head := gitOut(s.T(), dir, "rev-parse", "HEAD")

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: dir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/file?path=big.txt&ref="+head, "")
	require.Equal(s.T(), http.StatusRequestEntityTooLarge, rec.Code)
}

func TestCleanRelPath(t *testing.T) {
	cases := []struct {
		in   string
		want string
		err  string
	}{
		{in: "a/b.txt", want: "a/b.txt"},
		{in: "a/./b/../c.txt", want: "a/c.txt"},
		{in: "", err: "path is required"},
		{in: "/etc/passwd", err: "absolute paths are not allowed"},
		{in: "a\x00b", err: "path contains invalid characters"},
		{in: "..", err: "path traversal not allowed"},
		{in: "a/../../b", err: "path traversal not allowed"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := cleanRelPath(tc.in)
			if tc.err != "" {
				require.EqualError(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
