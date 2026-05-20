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

func (s *PRSuite) TestDiffRequiresInputs() {
	g := &GitPR{Run: (&recordingRunner{}).run}
	_, err := g.Diff(context.Background(), "", "/wt", "main")
	require.Error(s.T(), err)
	_, err = g.Diff(context.Background(), "/repo", "", "main")
	require.Error(s.T(), err)
	_, err = g.Diff(context.Background(), "/repo", "/wt", "")
	require.Error(s.T(), err)
}

func (s *PRSuite) TestDiffHappyPath() {
	rr := &recordingRunner{
		response: map[string]callResponse{
			"git diff origin/main...HEAD": {out: []byte("diff --git a/x b/x\n")},
		},
	}
	g := &GitPR{Run: rr.run}
	out, err := g.Diff(context.Background(), "/repo", "/repo/.worktrees/pr-1", "main")
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
	_, err := g.Diff(context.Background(), "/repo", "/wt", "main")
	require.ErrorContains(s.T(), err, "bad ref")
}

func (s *PRSuite) TestDiffDiffError() {
	rr := &recordingRunner{
		response: map[string]callResponse{
			"git diff origin/main...HEAD": {out: []byte("bad object"), err: errors.New("exit 128")},
		},
	}
	g := &GitPR{Run: rr.run}
	_, err := g.Diff(context.Background(), "/repo", "/wt", "main")
	require.ErrorContains(s.T(), err, "bad object")
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
