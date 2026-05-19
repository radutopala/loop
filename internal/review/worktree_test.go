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

type WorktreeSuite struct {
	suite.Suite
}

func TestWorktreeSuite(t *testing.T) {
	suite.Run(t, new(WorktreeSuite))
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

func (s *WorktreeSuite) TestAddRequiresInputs() {
	g := &GitPRWorktree{Run: (&recordingRunner{}).run}
	_, err := g.Add(context.Background(), "", 1)
	require.Error(s.T(), err)
	_, err = g.Add(context.Background(), "/repo", 0)
	require.Error(s.T(), err)
}

func (s *WorktreeSuite) TestAddHappyPath() {
	rr := &recordingRunner{}
	g := &GitPRWorktree{Run: rr.run}
	path, err := g.Add(context.Background(), "/repo", 42)
	require.NoError(s.T(), err)
	require.Equal(s.T(), filepath.Join("/repo", ".worktrees", "pr-42"), path)
	require.Len(s.T(), rr.calls, 2)
	require.Equal(s.T(), []string{"fetch", "origin", "refs/pull/42/head"}, rr.calls[0].args)
	require.Equal(s.T(), "/repo", rr.calls[0].dir)
	require.Equal(s.T(), []string{"worktree", "add", "--detach", filepath.Join("/repo", ".worktrees", "pr-42"), "FETCH_HEAD"}, rr.calls[1].args)
}

func (s *WorktreeSuite) TestAddFetchError() {
	rr := &recordingRunner{
		response: map[string]callResponse{
			"git fetch origin refs/pull/7/head": {out: []byte("network down"), err: errors.New("fail")},
		},
	}
	g := &GitPRWorktree{Run: rr.run}
	_, err := g.Add(context.Background(), "/repo", 7)
	require.ErrorContains(s.T(), err, "network down")
}

func (s *WorktreeSuite) TestAddWorktreeAlreadyExistsIsOK() {
	target := filepath.Join("/repo", ".worktrees", "pr-9")
	rr := &recordingRunner{
		response: map[string]callResponse{
			fmt.Sprintf("git worktree add --detach %s FETCH_HEAD", target): {
				out: []byte("fatal: '" + target + "' already exists"),
				err: errors.New("exit 128"),
			},
		},
	}
	g := &GitPRWorktree{Run: rr.run}
	path, err := g.Add(context.Background(), "/repo", 9)
	require.NoError(s.T(), err)
	require.Equal(s.T(), target, path)
}

func (s *WorktreeSuite) TestAddWorktreeOtherErrorPropagates() {
	target := filepath.Join("/repo", ".worktrees", "pr-9")
	rr := &recordingRunner{
		response: map[string]callResponse{
			fmt.Sprintf("git worktree add --detach %s FETCH_HEAD", target): {
				out: []byte("fatal: bad object"),
				err: errors.New("exit 128"),
			},
		},
	}
	g := &GitPRWorktree{Run: rr.run}
	_, err := g.Add(context.Background(), "/repo", 9)
	require.ErrorContains(s.T(), err, "bad object")
}

func (s *WorktreeSuite) TestRemoveRequiresInputs() {
	g := &GitPRWorktree{Run: (&recordingRunner{}).run}
	require.Error(s.T(), g.Remove(context.Background(), "", "/x"))
	require.Error(s.T(), g.Remove(context.Background(), "/repo", ""))
}

func (s *WorktreeSuite) TestRemoveHappyPath() {
	rr := &recordingRunner{}
	g := &GitPRWorktree{Run: rr.run}
	require.NoError(s.T(), g.Remove(context.Background(), "/repo", "/repo/.worktrees/pr-1"))
	require.Len(s.T(), rr.calls, 2)
	require.Equal(s.T(), []string{"worktree", "remove", "--force", "/repo/.worktrees/pr-1"}, rr.calls[0].args)
	require.Equal(s.T(), []string{"worktree", "prune"}, rr.calls[1].args)
}

func (s *WorktreeSuite) TestRemoveAlreadyGoneStillPrunes() {
	rr := &recordingRunner{
		response: map[string]callResponse{
			"git worktree remove --force /repo/.worktrees/pr-1": {
				out: []byte("fatal: '/repo/.worktrees/pr-1' is not a working tree"),
				err: errors.New("exit 128"),
			},
		},
	}
	g := &GitPRWorktree{Run: rr.run}
	require.NoError(s.T(), g.Remove(context.Background(), "/repo", "/repo/.worktrees/pr-1"))
	require.Len(s.T(), rr.calls, 2) // remove + prune both attempted
}

func (s *WorktreeSuite) TestRemovePropagatesOtherErrors() {
	rr := &recordingRunner{
		response: map[string]callResponse{
			"git worktree remove --force /repo/.worktrees/pr-1": {
				out: []byte("fatal: refusing to do anything"),
				err: errors.New("exit 1"),
			},
		},
	}
	g := &GitPRWorktree{Run: rr.run}
	err := g.Remove(context.Background(), "/repo", "/repo/.worktrees/pr-1")
	require.ErrorContains(s.T(), err, "refusing")
}

func (s *WorktreeSuite) TestRemovePruneError() {
	rr := &recordingRunner{
		response: map[string]callResponse{
			"git worktree prune": {out: []byte("prune busted"), err: errors.New("exit 1")},
		},
	}
	g := &GitPRWorktree{Run: rr.run}
	err := g.Remove(context.Background(), "/repo", "/repo/.worktrees/pr-1")
	require.ErrorContains(s.T(), err, "prune busted")
}
