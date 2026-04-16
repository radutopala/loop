package workflow

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// LocalBashRunner executes bash scripts on the host without Docker.
// Intended for environments where Docker is unavailable (e.g. CI).
type LocalBashRunner struct{}

// RunBash executes a script via /bin/sh on the host.
func (r *LocalBashRunner) RunBash(ctx context.Context, script, channelID, dirPath string) (string, error) {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	if dirPath != "" {
		clean := filepath.Clean(dirPath)
		if filepath.IsAbs(clean) {
			if info, err := os.Stat(clean); err == nil && info.IsDir() {
				cmd.Dir = clean
			}
		}
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("local bash: %w", err)
	}
	return string(output), nil
}
