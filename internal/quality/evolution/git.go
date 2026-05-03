package evolution

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// commandRunner wraps the bit of os/exec we need so tests can replace
// the real `git` invocation with a recorded fixture.
type commandRunner func(ctx context.Context, dir, name string, args ...string) ([]byte, error)

// ExecReader is the production HistoryReader; it shells out to `git
// log` with a parser-friendly format and decodes the stream into
// CommitFiles. Construct via NewExecReader so tests can inject the
// commandRunner; the zero value of ExecReader is unusable.
type ExecReader struct {
	run commandRunner
}

// NewExecReader returns an ExecReader bound to the real os/exec runner.
func NewExecReader() *ExecReader {
	return &ExecReader{run: defaultRunner}
}

func defaultRunner(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	return cmd.Output()
}

// Read invokes `git log --pretty=format:%H%x00%an%x00%aI --name-only`
// scoped to the requested window and parses the stream. Each commit is
// terminated by a blank line; files are tab/newline separated below the
// header. Empty file lists (merge commits with no files) are skipped.
func (r *ExecReader) Read(ctx context.Context, dirPath string, sinceMonths, maxCommits int) ([]CommitFiles, error) {
	if dirPath == "" {
		return nil, errors.New("evolution: dir path required")
	}
	since := fmt.Sprintf("%d.months.ago", sinceMonths)
	max := strconv.Itoa(maxCommits)
	args := []string{
		"log",
		"--name-only",
		"--no-merges",
		"--pretty=format:COMMIT%x00%H%x00%an%x00%aI",
		"--since=" + since,
		"--max-count=" + max,
	}
	out, err := r.run(ctx, dirPath, "git", args...)
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}
	return parseGitLog(string(out))
}

func parseGitLog(s string) ([]CommitFiles, error) {
	var commits []CommitFiles
	var cur *CommitFiles
	scan := bufio.NewScanner(strings.NewReader(s))
	scan.Buffer(make([]byte, 64*1024), 1024*1024)
	for scan.Scan() {
		line := scan.Text()
		if line == "" {
			if cur != nil && len(cur.Files) > 0 {
				commits = append(commits, *cur)
			}
			cur = nil
			continue
		}
		if strings.HasPrefix(line, "COMMIT\x00") {
			if cur != nil && len(cur.Files) > 0 {
				commits = append(commits, *cur)
			}
			parts := strings.SplitN(line, "\x00", 4)
			if len(parts) != 4 {
				return nil, fmt.Errorf("evolution: malformed commit header: %q", line)
			}
			ts, err := time.Parse(time.RFC3339, parts[3])
			if err != nil {
				return nil, fmt.Errorf("evolution: bad timestamp %q: %w", parts[3], err)
			}
			cur = &CommitFiles{Hash: parts[1], Author: parts[2], Timestamp: ts}
			continue
		}
		if cur != nil {
			cur.Files = append(cur.Files, line)
		}
	}
	if err := scan.Err(); err != nil {
		return nil, fmt.Errorf("evolution: scan: %w", err)
	}
	if cur != nil && len(cur.Files) > 0 {
		commits = append(commits, *cur)
	}
	return commits, nil
}
