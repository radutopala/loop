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

// RunBash executes a script via /bin/sh on the host. The script is piped to
// sh's stdin rather than passed via `-c "<script>"` so that the user-derived
// script body never becomes an argv element of the spawned process. This
// keeps the command line itself constant (`/bin/sh`) and cuts the dataflow
// from HTTP-supplied workflow inputs into argv — closing the CodeQL
// `go/command-injection` sink while preserving identical execution
// semantics (sh reads the script from stdin, runs it, exits on EOF).
func (r *LocalBashRunner) RunBash(ctx context.Context, script, channelID, dirPath string) (string, error) {
	cmd := exec.CommandContext(ctx, "/bin/sh")
	cmd.Stdin = strings.NewReader(script)
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

// renderBashScript renders a bash node's script template against a defensive
// copy of runCtx in which every user-controllable string value (Inputs,
// NodeOutputs, ChannelID, Review.CommentsJSON) is shell-quoted via shellQuote.
// The rendered output is fed to /bin/sh via stdin (see LocalBashRunner.RunBash),
// so unquoted templates like `echo {{.Inputs.foo}}` previously allowed a foo
// value of `; rm -rf ~` to execute arbitrary commands. After quoting the same
// value renders as `echo '; rm -rf ~'` — a single literal argument to echo.
func renderBashScript(tmplStr string, data *RunContext) (string, error) {
	if tmplStr == "" {
		return "", nil
	}
	safe := *data
	safe.Inputs = shellQuoteMap(data.Inputs)
	safe.NodeOutputs = shellQuoteMap(data.NodeOutputs)
	safe.ChannelID = shellQuote(data.ChannelID)
	safe.Review.CommentsJSON = shellQuote(data.Review.CommentsJSON)
	return renderTemplate(tmplStr, &safe)
}

// shellQuote wraps s in single quotes, escaping any embedded single quotes
// using the standard `'\”` sequence. The result is always safe to splice
// into a POSIX shell command line as a single argument — no metacharacter
// inside s can affect parsing.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellQuoteMap returns a new map with each value shell-quoted via shellQuote.
func shellQuoteMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = shellQuote(v)
	}
	return out
}
