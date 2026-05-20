package worktree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/radutopala/loop/internal/osutil"
)

// System abstracts filesystem operations for testability.
type System interface {
	MkdirAll(path string, perm os.FileMode) error
	WriteFile(name string, data []byte, perm os.FileMode) error
	ReadFile(name string) ([]byte, error)
	UserHomeDir() (string, error)
}

// CommandRunner executes a command in a given directory and returns the combined output.
type CommandRunner func(ctx context.Context, dir, name string, args ...string) ([]byte, error)

// CreateResult holds the result of a worktree creation.
type CreateResult struct {
	WorktreePath string
	BranchName   string
}

// ExecCommandRunner is a CommandRunner that uses exec.CommandContext.
func ExecCommandRunner(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

// Creator creates git worktrees with config seeding and session copying.
type Creator struct {
	Sys System
	Run CommandRunner
}

// Create creates a new git worktree from the given parent directory.
// It creates a dedicated branch "worktree/<name>" based on the specified branch,
// seeds the worktree with a .loop/config.json pointing back to the parent,
// and optionally copies the session file for --resume --fork-session.
func (c *Creator) Create(ctx context.Context, dirPath, branch, name, sessionID string) (*CreateResult, error) {
	worktreePath := filepath.Join(dirPath, ".worktrees", name)
	wtBranch := "worktree/" + name

	out, err := c.Run(ctx, dirPath, "git", "worktree", "add", "-b", wtBranch, worktreePath, branch)
	if err != nil {
		return nil, fmt.Errorf("git worktree add failed: %s", strings.TrimSpace(string(out)))
	}

	// Seed worktree config with extra_dirs pointing at the parent project.
	wtLoopDir := filepath.Join(worktreePath, ".loop")
	if err := c.Sys.MkdirAll(wtLoopDir, 0o755); err == nil {
		wtCfg := fmt.Sprintf("{\n  \"extra_dirs\": [\n    %q\n  ]\n}\n", dirPath)
		_ = c.Sys.WriteFile(filepath.Join(wtLoopDir, "config.json"), []byte(wtCfg), 0o644)
	}

	// Copy session file so --resume --fork-session works in the worktree dir.
	if sessionID != "" {
		_ = c.copySessionFile(dirPath, worktreePath, sessionID)
	}

	return &CreateResult{
		WorktreePath: worktreePath,
		BranchName:   wtBranch,
	}, nil
}

// Remove removes a git worktree directory and prunes stale worktree metadata.
// parentDir is the main repository directory (not the worktree itself).
// worktreePath is the absolute path of the worktree to remove.
func (c *Creator) Remove(ctx context.Context, parentDir, worktreePath string) error {
	out, err := c.Run(ctx, parentDir, "git", "worktree", "remove", "--force", "--force", worktreePath)
	if err != nil {
		return fmt.Errorf("git worktree remove failed: %s", strings.TrimSpace(string(out)))
	}
	out, err = c.Run(ctx, parentDir, "git", "worktree", "prune")
	if err != nil {
		return fmt.Errorf("git worktree prune failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// Lock marks a git worktree as locked so `git worktree remove` refuses to
// delete it without --force. Re-locking an already-locked worktree returns
// nil (git's "already locked" error is treated as a no-op).
func (c *Creator) Lock(ctx context.Context, parentDir, worktreePath, reason string) error {
	args := []string{"worktree", "lock", worktreePath}
	if reason != "" {
		args = append(args, "--reason", reason)
	}
	out, err := c.Run(ctx, parentDir, "git", args...)
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(strings.ToLower(msg), "already locked") {
			return nil
		}
		return fmt.Errorf("git worktree lock failed: %s", msg)
	}
	return nil
}

// Unlock removes the lock on a git worktree. Unlocking an already-unlocked
// worktree returns nil (git's "not locked" error is treated as a no-op).
func (c *Creator) Unlock(ctx context.Context, parentDir, worktreePath string) error {
	out, err := c.Run(ctx, parentDir, "git", "worktree", "unlock", worktreePath)
	if err != nil {
		msg := strings.TrimSpace(string(out))
		lower := strings.ToLower(msg)
		if strings.Contains(lower, "not locked") || strings.Contains(lower, "is not locked") {
			return nil
		}
		return fmt.Errorf("git worktree unlock failed: %s", msg)
	}
	return nil
}

func (c *Creator) copySessionFile(parentDirPath, worktreeDirPath, sessionID string) error {
	sessionID = filepath.Base(sessionID)
	if sessionID == "." || sessionID == ".." || sessionID == "" {
		return fmt.Errorf("invalid session ID")
	}

	home, err := c.Sys.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home dir: %w", err)
	}
	srcDir := filepath.Join(home, ".claude", "projects", osutil.EncodeClaudeProjectPath(parentDirPath))
	src := filepath.Join(srcDir, sessionID+".jsonl")
	dstDir := filepath.Join(home, ".claude", "projects", osutil.EncodeClaudeProjectPath(worktreeDirPath))
	dst := filepath.Join(dstDir, sessionID+".jsonl")

	data, err := c.Sys.ReadFile(src)
	if err != nil {
		return fmt.Errorf("reading session file: %w", err)
	}
	if err := c.Sys.MkdirAll(dstDir, 0o755); err != nil {
		return fmt.Errorf("creating project dir: %w", err)
	}
	return c.Sys.WriteFile(dst, data, 0o644)
}
