package api

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/config"
)

// formatTimeout bounds a formatter run: a save waits for it.
const formatTimeout = 15 * time.Second

// maxFormatError caps the formatter output a failed format reports.
const maxFormatError = 2000

// editorCommands runs the host commands the editor's routes need. A nil
// field falls back to running the real command.
type editorCommands struct {
	// format runs command through sh in dir with stdin, and returns its
	// stdout.
	format func(ctx context.Context, dir, command string, stdin []byte) ([]byte, error)
	// lookPath finds a binary on PATH.
	lookPath func(name string) (string, error)
	// stream runs name in dir and hands its stdout to read; the command is
	// killed once read returns.
	stream func(ctx context.Context, dir, name string, args []string, read func(io.Reader) error) error
	// formatTimeout bounds a formatter run; 0 is formatTimeout.
	formatTimeout time.Duration
}

func (c editorCommands) formatTimeoutOr() time.Duration {
	return cmp.Or(c.formatTimeout, formatTimeout)
}

func (c editorCommands) formatFn() func(ctx context.Context, dir, command string, stdin []byte) ([]byte, error) {
	if c.format == nil {
		return execFormat
	}
	return c.format
}

func (c editorCommands) lookPathFn() func(name string) (string, error) {
	if c.lookPath == nil {
		return exec.LookPath
	}
	return c.lookPath
}

func (c editorCommands) streamFn() func(ctx context.Context, dir, name string, args []string, read func(io.Reader) error) error {
	if c.stream == nil {
		return execStream
	}
	return c.stream
}

// execFormat runs command through sh in dir, feeding it stdin. A failure
// carries what the command wrote to stderr.
func execFormat(ctx context.Context, dir, command string, stdin []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

// execStream runs name in dir and hands its stdout to read, then kills it
// if it's still running and waits for it. Its exit error is dropped: a
// search tool exits non-zero for "no match" or an unreadable file, and
// read has seen whatever it printed.
func execStream(ctx context.Context, dir, name string, args []string, read func(io.Reader) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	// StdoutPipe fails only on a Cmd with Stdout set or already started.
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		return err
	}
	readErr := read(out)
	cancel()
	_ = cmd.Wait()
	return readErr
}

// editorConfigResponse is the editor settings in effect for a channel, and
// the config layer each came from (default, global or project).
type editorConfigResponse struct {
	FollowActiveFile     bool   `json:"follow_active_file"`
	FollowActiveFileFrom string `json:"follow_active_file_from"`
	FormatOnSave         bool   `json:"format_on_save"`
	FormatOnSaveFrom     string `json:"format_on_save_from"`
	Formatters           int    `json:"formatters"`
	TabSize              int    `json:"tab_size"`
	InsertSpaces         bool   `json:"insert_spaces"`
	AutoSaveOnBlur       bool   `json:"auto_save_on_blur"`
	PreviewTabs          bool   `json:"preview_tabs"`
}

// editorConfig returns the channel's layered editor config: the defaults
// when the channel or the global config can't be resolved.
func (s *Server) editorConfig(ctx context.Context, channelID string) config.EditorConfig {
	dirPath, parentDirPath, err := s.resolveWorkflowConfigPaths(ctx, channelID)
	if err != nil {
		return config.DefaultEditorConfig()
	}
	cfg := s.configs.merged(dirPath, parentDirPath)
	if cfg == nil {
		return config.DefaultEditorConfig()
	}
	return cfg.Editor
}

func (s *Server) handleEditorConfig(w http.ResponseWriter, r *http.Request) {
	ec := s.editorConfig(r.Context(), r.PathValue("id"))
	writeHTTPJSON(w, http.StatusOK, editorConfigResponse{
		FollowActiveFile:     ec.FollowActiveFile,
		FollowActiveFileFrom: ec.FollowActiveFileFrom,
		FormatOnSave:         ec.FormatOnSave,
		FormatOnSaveFrom:     ec.FormatOnSaveFrom,
		Formatters:           len(ec.Formatters),
		TabSize:              ec.TabSize,
		InsertSpaces:         ec.InsertSpaces,
		AutoSaveOnBlur:       ec.AutoSaveOnBlur,
		PreviewTabs:          ec.PreviewTabs,
	}, s.logger)
}

// formatResult is what formatting a saved file did: the formatted content
// when it changed, or why it couldn't be formatted.
type formatResult struct {
	content []byte
	changed bool
	err     string
}

// formatForSave applies the channel's save-time cleanups to body, then runs
// its formatter for relPath over the result when format on save is on and
// one matches. absPath is the file's path, for the command's {file};
// rootDir is where it runs. A formatter that fails or prints nothing leaves
// the cleaned-up body, with the reason in err.
func (s *Server) formatForSave(ctx context.Context, channelID, rootDir, relPath, absPath string, body []byte) formatResult {
	ec := s.editorConfig(ctx, channelID)
	res := s.runFormatter(ctx, ec, rootDir, relPath, absPath, ec.Tidy(body))
	res.changed = !bytes.Equal(res.content, body)
	return res
}

// runFormatter runs ec's formatter for relPath over body; see formatForSave.
func (s *Server) runFormatter(ctx context.Context, ec config.EditorConfig, rootDir, relPath, absPath string, body []byte) formatResult {
	if !ec.FormatOnSave {
		return formatResult{content: body}
	}
	f, ok := ec.FormatterFor(relPath)
	if !ok {
		return formatResult{content: body}
	}
	timeout := s.editorCmds.formatTimeoutOr()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := strings.ReplaceAll(f.Command, "{file}", shellQuote(absPath))
	out, err := s.editorCmds.formatFn()(ctx, rootDir, command, body)
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return formatResult{content: body, err: fmt.Sprintf("%s timed out after %s", f.Command, timeout)}
	case err != nil:
		msg := err.Error()
		if len(msg) > maxFormatError {
			msg = msg[:maxFormatError] + "…"
		}
		return formatResult{content: body, err: fmt.Sprintf("%s failed: %s", f.Command, msg)}
	case len(out) == 0 && len(body) > 0:
		return formatResult{content: body, err: fmt.Sprintf("%s printed nothing", f.Command)}
	}
	return formatResult{content: out}
}

// shellQuote wraps s in single quotes, so a shell reads it as one word.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
