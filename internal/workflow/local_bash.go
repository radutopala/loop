package workflow

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// LocalBashRunner executes bash scripts on the host without Docker.
// Intended for environments where Docker is unavailable (e.g. CI).
type LocalBashRunner struct {
	// SafeDir is the root directory that dirPath must resolve within.
	SafeDir string
}

// SafeDir validates that dirPath is within the runner's SafeDir and returns the
// cleaned absolute path. Returns ("", false) if the path is outside SafeDir.
func (r *LocalBashRunner) safePath(dirPath string) (string, bool) {
	if dirPath == "" || r.SafeDir == "" {
		return "", false
	}
	absPath, err := filepath.Abs(dirPath)
	if err != nil {
		return "", false
	}
	if !strings.HasPrefix(absPath, r.SafeDir) {
		return "", false
	}
	return absPath, true
}

// RunBash executes a script via /bin/sh on the host.
func (r *LocalBashRunner) RunBash(ctx context.Context, script, channelID, dirPath string) (string, error) {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	if safe, ok := r.safePath(dirPath); ok {
		if info, err := os.Stat(safe); err == nil && info.IsDir() {
			cmd.Dir = safe
		}
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("local bash: %w", err)
	}
	return string(output), nil
}
