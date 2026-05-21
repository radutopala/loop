package review

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type PRSuite struct {
	suite.Suite
}

func TestPRSuite(t *testing.T) {
	suite.Run(t, new(PRSuite))
}

// recordingRunner captures every (dir, name, args) tuple it's invoked
// with and returns canned responses keyed by the joined args. Missing
// entries return ("", nil). To stub an error, set err on the entry.
type recordingRunner struct {
	calls    []call
	response map[string]callResponse
}

type call struct {
	dir  string
	name string
	args []string
}

type callResponse struct {
	out []byte
	err error
}

func (r *recordingRunner) run(_ context.Context, dir, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, call{dir: dir, name: name, args: args})
	key := name + " " + joinArgs(args)
	if resp, ok := r.response[key]; ok {
		return resp.out, resp.err
	}
	return nil, nil
}

func joinArgs(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}

func (s *PRSuite) TestAddRequiresInputs() {
	g := &GitPR{Run: (&recordingRunner{}).run}
	_, err := g.Add(context.Background(), "", 1)
	require.Error(s.T(), err)
	_, err = g.Add(context.Background(), "/repo", 0)
	require.Error(s.T(), err)
}

func (s *PRSuite) TestAddHappyPath() {
	rr := &recordingRunner{}
	g := &GitPR{Run: rr.run}
	path, err := g.Add(context.Background(), "/repo", 42)
	require.NoError(s.T(), err)
	require.Equal(s.T(), filepath.Join("/repo", ".worktrees", "pr-42"), path)
	require.Len(s.T(), rr.calls, 2)
	require.Equal(s.T(), []string{"fetch", "origin", "refs/pull/42/head"}, rr.calls[0].args)
	require.Equal(s.T(), "/repo", rr.calls[0].dir)
	require.Equal(s.T(), []string{"worktree", "add", "--detach", filepath.Join("/repo", ".worktrees", "pr-42"), "FETCH_HEAD"}, rr.calls[1].args)
}

func (s *PRSuite) TestAddFetchError() {
	rr := &recordingRunner{
		response: map[string]callResponse{
			"git fetch origin refs/pull/7/head": {out: []byte("network down"), err: errors.New("fail")},
		},
	}
	g := &GitPR{Run: rr.run}
	_, err := g.Add(context.Background(), "/repo", 7)
	require.ErrorContains(s.T(), err, "network down")
}

func (s *PRSuite) TestAddWorktreeAlreadyExistsIsOK() {
	target := filepath.Join("/repo", ".worktrees", "pr-9")
	rr := &recordingRunner{
		response: map[string]callResponse{
			fmt.Sprintf("git worktree add --detach %s FETCH_HEAD", target): {
				out: []byte("fatal: '" + target + "' already exists"),
				err: errors.New("exit 128"),
			},
		},
	}
	g := &GitPR{Run: rr.run}
	path, err := g.Add(context.Background(), "/repo", 9)
	require.NoError(s.T(), err)
	require.Equal(s.T(), target, path)
}

func (s *PRSuite) TestAddWorktreeOtherErrorPropagates() {
	target := filepath.Join("/repo", ".worktrees", "pr-9")
	rr := &recordingRunner{
		response: map[string]callResponse{
			fmt.Sprintf("git worktree add --detach %s FETCH_HEAD", target): {
				out: []byte("fatal: bad object"),
				err: errors.New("exit 128"),
			},
		},
	}
	g := &GitPR{Run: rr.run}
	_, err := g.Add(context.Background(), "/repo", 9)
	require.ErrorContains(s.T(), err, "bad object")
}

func (s *PRSuite) TestRefreshRequiresInputs() {
	g := &GitPR{Run: (&recordingRunner{}).run}
	require.Error(s.T(), g.Refresh(context.Background(), "", "/wt", 1))
	require.Error(s.T(), g.Refresh(context.Background(), "/repo", "", 1))
	require.Error(s.T(), g.Refresh(context.Background(), "/repo", "/wt", 0))
}

func (s *PRSuite) TestRefreshHappyPath() {
	rr := &recordingRunner{}
	g := &GitPR{Run: rr.run}
	require.NoError(s.T(), g.Refresh(context.Background(), "/repo", "/wt", 7))
	require.Len(s.T(), rr.calls, 2)
	require.Equal(s.T(), "/wt", rr.calls[0].dir)
	require.Equal(s.T(), []string{"fetch", "origin", "refs/pull/7/head"}, rr.calls[0].args)
	require.Equal(s.T(), "/wt", rr.calls[1].dir)
	require.Equal(s.T(), []string{"checkout", "--detach", "FETCH_HEAD"}, rr.calls[1].args)
}

func (s *PRSuite) TestRefreshFetchError() {
	rr := &recordingRunner{
		response: map[string]callResponse{
			"git fetch origin refs/pull/7/head": {out: []byte("net down"), err: errors.New("fail")},
		},
	}
	g := &GitPR{Run: rr.run}
	require.ErrorContains(s.T(), g.Refresh(context.Background(), "/repo", "/wt", 7), "net down")
}

func (s *PRSuite) TestRefreshCheckoutError() {
	rr := &recordingRunner{
		response: map[string]callResponse{
			"git checkout --detach FETCH_HEAD": {out: []byte("conflict"), err: errors.New("exit 1")},
		},
	}
	g := &GitPR{Run: rr.run}
	require.ErrorContains(s.T(), g.Refresh(context.Background(), "/repo", "/wt", 7), "conflict")
}

func (s *PRSuite) TestDiffRequiresInputs() {
	g := &GitPR{Run: (&recordingRunner{}).run}
	_, err := g.Diff(context.Background(), "", "/wt", "main", nil)
	require.Error(s.T(), err)
	_, err = g.Diff(context.Background(), "/repo", "", "main", nil)
	require.Error(s.T(), err)
	_, err = g.Diff(context.Background(), "/repo", "/wt", "", nil)
	require.Error(s.T(), err)
}

func (s *PRSuite) TestDiffHappyPath() {
	rr := &recordingRunner{
		response: map[string]callResponse{
			"git diff origin/main...HEAD": {out: []byte("diff --git a/x b/x\n")},
		},
	}
	g := &GitPR{Run: rr.run}
	out, err := g.Diff(context.Background(), "/repo", "/repo/.worktrees/pr-1", "main", nil)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(out), "diff --git")
	require.Len(s.T(), rr.calls, 2)
	require.Equal(s.T(), "/repo", rr.calls[0].dir)
	require.Equal(s.T(), []string{"fetch", "origin", "main"}, rr.calls[0].args)
	require.Equal(s.T(), "/repo/.worktrees/pr-1", rr.calls[1].dir)
	require.Equal(s.T(), []string{"diff", "origin/main...HEAD"}, rr.calls[1].args)
}

func (s *PRSuite) TestDiffFetchError() {
	rr := &recordingRunner{
		response: map[string]callResponse{
			"git fetch origin main": {out: []byte("bad ref"), err: errors.New("exit 1")},
		},
	}
	g := &GitPR{Run: rr.run}
	_, err := g.Diff(context.Background(), "/repo", "/wt", "main", nil)
	require.ErrorContains(s.T(), err, "bad ref")
}

func (s *PRSuite) TestDiffDiffError() {
	rr := &recordingRunner{
		response: map[string]callResponse{
			"git diff origin/main...HEAD": {out: []byte("bad object"), err: errors.New("exit 128")},
		},
	}
	g := &GitPR{Run: rr.run}
	_, err := g.Diff(context.Background(), "/repo", "/wt", "main", nil)
	require.ErrorContains(s.T(), err, "bad object")
}

// When a commented line is far outside the default 3-line context window,
// Diff first runs `-U0` to discover changed ranges, then re-runs with the
// computed `-U<n>` so the comment lands inside a hunk.
func (s *PRSuite) TestDiffWidensContextForFarComments() {
	rr := &recordingRunner{
		response: map[string]callResponse{
			"git diff -U0 origin/main...HEAD": {
				out: []byte("diff --git a/foo.go b/foo.go\n@@ -10,1 +10,1 @@\n-old\n+new\n"),
			},
			"git diff -U92 origin/main...HEAD": {out: []byte("widened diff")},
		},
	}
	g := &GitPR{Run: rr.run}
	out, err := g.Diff(context.Background(), "/repo", "/wt", "main", []*Comment{
		{Path: "foo.go", Line: 102, Side: "RIGHT"},
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "widened diff", string(out))
	// fetch + -U0 probe + final widened diff
	require.Len(s.T(), rr.calls, 3)
	require.Equal(s.T(), []string{"diff", "-U0", "origin/main...HEAD"}, rr.calls[1].args)
	require.Equal(s.T(), []string{"diff", "-U92", "origin/main...HEAD"}, rr.calls[2].args)
}

// Comments within default unified context don't trigger a -U widen.
func (s *PRSuite) TestDiffSkipsWidenWhenDefaultCovers() {
	rr := &recordingRunner{
		response: map[string]callResponse{
			"git diff -U0 origin/main...HEAD": {
				out: []byte("diff --git a/foo.go b/foo.go\n@@ -10,1 +10,1 @@\n-old\n+new\n"),
			},
			"git diff origin/main...HEAD": {out: []byte("default diff")},
		},
	}
	g := &GitPR{Run: rr.run}
	out, err := g.Diff(context.Background(), "/repo", "/wt", "main", []*Comment{
		{Path: "foo.go", Line: 12, Side: "RIGHT"},
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "default diff", string(out))
	require.Equal(s.T(), []string{"diff", "origin/main...HEAD"}, rr.calls[2].args)
}

// Comments on files that aren't in the diff (orphan-by-path) don't
// force a -U widen for the files that are.
func (s *PRSuite) TestDiffIgnoresCommentsOnUnchangedFiles() {
	rr := &recordingRunner{
		response: map[string]callResponse{
			"git diff -U0 origin/main...HEAD": {
				out: []byte("diff --git a/foo.go b/foo.go\n@@ -10,1 +10,1 @@\n-old\n+new\n"),
			},
			"git diff origin/main...HEAD": {out: []byte("default diff")},
		},
	}
	g := &GitPR{Run: rr.run}
	_, err := g.Diff(context.Background(), "/repo", "/wt", "main", []*Comment{
		{Path: "unrelated.go", Line: 999, Side: "RIGHT"},
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"diff", "origin/main...HEAD"}, rr.calls[2].args)
}

// LEFT-side comments anchor to the old-file range from the @@ header.
func (s *PRSuite) TestDiffWidensForLeftSideComment() {
	rr := &recordingRunner{
		response: map[string]callResponse{
			"git diff -U0 origin/main...HEAD": {
				out: []byte("diff --git a/foo.go b/foo.go\n@@ -50,2 +50,1 @@\n-a\n-b\n+merged\n"),
			},
			"git diff -U29 origin/main...HEAD": {out: []byte("widened")},
		},
	}
	g := &GitPR{Run: rr.run}
	out, err := g.Diff(context.Background(), "/repo", "/wt", "main", []*Comment{
		{Path: "foo.go", Line: 21, Side: "LEFT"},
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "widened", string(out))
}

// -U0 pre-pass failure propagates.
func (s *PRSuite) TestDiffSkinnyPassError() {
	rr := &recordingRunner{
		response: map[string]callResponse{
			"git diff -U0 origin/main...HEAD": {out: []byte("u0 boom"), err: errors.New("exit 1")},
		},
	}
	g := &GitPR{Run: rr.run}
	_, err := g.Diff(context.Background(), "/repo", "/wt", "main", []*Comment{
		{Path: "foo.go", Line: 1, Side: "RIGHT"},
	})
	require.ErrorContains(s.T(), err, "u0 boom")
}

func (s *PRSuite) TestRemoveRequiresInputs() {
	g := &GitPR{Run: (&recordingRunner{}).run}
	require.Error(s.T(), g.Remove(context.Background(), "", "/x"))
	require.Error(s.T(), g.Remove(context.Background(), "/repo", ""))
}

func (s *PRSuite) TestRemoveHappyPath() {
	rr := &recordingRunner{}
	g := &GitPR{Run: rr.run}
	require.NoError(s.T(), g.Remove(context.Background(), "/repo", "/repo/.worktrees/pr-1"))
	require.Len(s.T(), rr.calls, 2)
	require.Equal(s.T(), []string{"worktree", "remove", "--force", "/repo/.worktrees/pr-1"}, rr.calls[0].args)
	require.Equal(s.T(), []string{"worktree", "prune"}, rr.calls[1].args)
}

func (s *PRSuite) TestRemoveAlreadyGoneStillPrunes() {
	rr := &recordingRunner{
		response: map[string]callResponse{
			"git worktree remove --force /repo/.worktrees/pr-1": {
				out: []byte("fatal: '/repo/.worktrees/pr-1' is not a working tree"),
				err: errors.New("exit 128"),
			},
		},
	}
	g := &GitPR{Run: rr.run}
	require.NoError(s.T(), g.Remove(context.Background(), "/repo", "/repo/.worktrees/pr-1"))
	require.Len(s.T(), rr.calls, 2) // remove + prune both attempted
}

func (s *PRSuite) TestRemovePropagatesOtherErrors() {
	rr := &recordingRunner{
		response: map[string]callResponse{
			"git worktree remove --force /repo/.worktrees/pr-1": {
				out: []byte("fatal: refusing to do anything"),
				err: errors.New("exit 1"),
			},
		},
	}
	g := &GitPR{Run: rr.run}
	err := g.Remove(context.Background(), "/repo", "/repo/.worktrees/pr-1")
	require.ErrorContains(s.T(), err, "refusing")
}

func (s *PRSuite) TestRemovePruneError() {
	rr := &recordingRunner{
		response: map[string]callResponse{
			"git worktree prune": {out: []byte("prune busted"), err: errors.New("exit 1")},
		},
	}
	g := &GitPR{Run: rr.run}
	err := g.Remove(context.Background(), "/repo", "/repo/.worktrees/pr-1")
	require.ErrorContains(s.T(), err, "prune busted")
}

// `diff --git` header lacking the ` b/` portion (e.g. a malformed
// stream from a rebase recording) drops the parser into a no-op state
// until the next valid header, so subsequent hunk lines don't get
// attributed to the wrong file. Also covers the leading-hunk case
// where a hunk header arrives before any diff --git line.
func (s *PRSuite) TestParseChangedRangesMalformedHeader() {
	diff := []byte("@@ -1,1 +1,1 @@\n" + // hunk before any diff --git
		"diff --git x.go\n" + // header without ` b/`
		"@@ -1,1 +1,1 @@\n" + // hunk that must be ignored
		"diff --git a/y.go b/y.go\n" +
		"@@ -1,1 +1,1 @@\n",
	)
	r := parseChangedRanges(diff)
	require.NotContains(s.T(), r, "x.go")
	require.Contains(s.T(), r, "y.go")
}

// `@@ -1,0 +1,N @@` (pure insertion) and `@@ -1,N +1,0 @@` (pure
// deletion) carry a zero count on one side. parseChangedRanges
// collapses each to a single-line span so the side stays addressable.
func (s *PRSuite) TestParseChangedRangesZeroCountHunks() {
	diff := []byte("diff --git a/ins.go b/ins.go\n" +
		"@@ -0,0 +5,2 @@\n" +
		"diff --git a/del.go b/del.go\n" +
		"@@ -7,3 +0,0 @@\n",
	)
	r := parseChangedRanges(diff)
	require.Equal(s.T(), [][2]int{{5, 6}}, r["ins.go"].newSpans)
	require.Equal(s.T(), [][2]int{{0, 0}}, r["ins.go"].oldSpans)
	require.Equal(s.T(), [][2]int{{0, 0}}, r["del.go"].newSpans)
	require.Equal(s.T(), [][2]int{{7, 9}}, r["del.go"].oldSpans)
}

// computeContextNeeded must tolerate the edge cases that ShouldRediff
// pre-screens away: nil entries in the comment slice, comments on files
// that landed in the diff but produced no spans for the requested side,
// and comments that already fall inside an existing span.
func (s *PRSuite) TestComputeContextNeededEdges() {
	diff := []byte("diff --git a/x.go b/x.go\n@@ -10,3 +10,3 @@\n")
	// Nil entry, in-span comment (dist=0), and a side that has no spans
	// should all keep needed at 0 without panicking.
	needed := computeContextNeeded(diff, []*Comment{
		nil,
		{Path: "x.go", Line: 11, Side: "RIGHT"},
	})
	require.Equal(s.T(), 0, needed)

	// File in the diff but no hunks recorded (mode-only / binary patch):
	// both sides are empty, so any comment on it widens nothing.
	binaryOnly := []byte("diff --git a/bin.bin b/bin.bin\n")
	needed = computeContextNeeded(binaryOnly, []*Comment{
		{Path: "bin.bin", Line: 1, Side: "RIGHT"},
	})
	require.Equal(s.T(), 0, needed)
}

func (s *PRSuite) TestShouldRediff() {
	diff := []byte("diff --git a/x.go b/x.go\n" +
		"--- a/x.go\n" +
		"+++ b/x.go\n" +
		"@@ -10,3 +10,3 @@\n" +
		" a\n-b\n+B\n c\n",
	)
	cases := []struct {
		name string
		path string
		line int
		side string
		want bool
	}{
		{"inside hunk RIGHT", "x.go", 11, "RIGHT", false},
		{"outside hunk RIGHT", "x.go", 50, "RIGHT", true},
		{"inside hunk LEFT", "x.go", 10, "LEFT", false},
		{"outside hunk LEFT", "x.go", 100, "LEFT", true},
		{"path absent", "other.go", 1, "RIGHT", false},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, ShouldRediff(diff, tc.path, tc.line, tc.side))
		})
	}
}
