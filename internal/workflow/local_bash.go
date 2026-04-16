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
	// If empty, any absolute path is allowed (for backward compatibility in tests).
	SafeDir string
}

// RunBash executes a script via /bin/sh on the host.
func (r *LocalBashRunner) RunBash(ctx context.Context, script, channelID, dirPath string) (string, error) {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	if dirPath != "" {
		absPath, err := filepath.Abs(dirPath)
		if err == nil && (r.SafeDir == "" || strings.HasPrefix(absPath, r.SafeDir)) {
			if info, err := os.Stat(absPath); err == nil && info.IsDir() {
				cmd.Dir = absPath
			}
		}
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("local bash: %w", err)
	}
	return string(output), nil
}
