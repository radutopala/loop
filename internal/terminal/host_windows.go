//go:build windows

package terminal

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/UserExistsError/conpty"
)

// hostExec holds state for a single host exec process on Windows.
type hostExec struct {
	cmdLine string
	dir     string
	env     []string
	cpty    *conpty.ConPty
}

// defaultShell returns the user's preferred shell on Windows.
var defaultShell = func() string {
	if s := os.Getenv("COMSPEC"); s != "" {
		return s
	}
	if p, err := exec.LookPath("powershell.exe"); err == nil {
		return p
	}
	return "cmd.exe"
}

// defaultShellArgs returns the default arguments for the Windows shell.
var defaultShellArgs = func() []string {
	return nil
}

// conptyStart wraps conpty.Start for testing.
var conptyStart = func(commandLine string, opts ...conpty.ConPtyOption) (*conpty.ConPty, error) {
	return conpty.Start(commandLine, opts...)
}

// conptyAvailable wraps conpty.IsConPtyAvailable for testing.
var conptyAvailable = conpty.IsConPtyAvailable

// ExecCreate creates a new exec process on Windows. The dirPath parameter
// is used as the working directory. The process is not started until
// ExecAttach is called.
func (c *HostExecClient) ExecCreate(_ context.Context, dirPath string, cmd []string, _ bool) (string, error) {
	if len(cmd) == 0 {
		shell := defaultShell()
		args := defaultShellArgs()
		cmd = append([]string{shell}, args...)
	}

	// Validate the command resolves to an actual executable on PATH.
	resolvedPath, err := lookPath(cmd[0])
	if err != nil {
		return "", fmt.Errorf("command not found: %s", cmd[0])
	}

	cmd[0] = resolvedPath
	cmdLine := buildCommandLine(cmd)

	id := generateID()
	c.mu.Lock()
	c.execs[id] = &hostExec{
		cmdLine: cmdLine,
		dir:     dirPath,
		env:     os.Environ(),
	}
	c.mu.Unlock()

	return id, nil
}

// ExecAttach starts the exec process with a ConPTY and returns
// a ReadWriteCloser. Close terminates the process and releases handles.
func (c *HostExecClient) ExecAttach(_ context.Context, execID string) (io.ReadWriteCloser, error) {
	if !conptyAvailable() {
		return nil, fmt.Errorf("Windows ConPTY is not available (requires Windows 10 1809+)")
	}

	c.mu.Lock()
	he, ok := c.execs[execID]
	c.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("exec %s not found", execID)
	}

	cpty, err := conptyStart(
		he.cmdLine,
		conpty.ConPtyWorkDir(he.dir),
		conpty.ConPtyEnv(he.env),
	)
	if err != nil {
		return nil, fmt.Errorf("starting conpty: %w", err)
	}

	c.mu.Lock()
	he.cpty = cpty
	c.mu.Unlock()

	return &hostConPTYConn{
		cpty:      cpty,
		closeOnce: sync.Once{},
	}, nil
}

// ExecResize changes the ConPTY dimensions of the exec process.
func (c *HostExecClient) ExecResize(_ context.Context, execID string, height, width uint) error {
	c.mu.Lock()
	he, ok := c.execs[execID]
	c.mu.Unlock()
	if !ok {
		return fmt.Errorf("exec %s not found", execID)
	}
	if he.cpty == nil {
		return fmt.Errorf("exec %s not attached", execID)
	}
	// conpty.Resize takes (width, height) — note the parameter order.
	return he.cpty.Resize(int(width), int(height))
}

// hostConPTYConn wraps a ConPTY as an io.ReadWriteCloser.
type hostConPTYConn struct {
	cpty      *conpty.ConPty
	closeOnce sync.Once
}

func (h *hostConPTYConn) Read(p []byte) (int, error) {
	return h.cpty.Read(p)
}

func (h *hostConPTYConn) Write(p []byte) (int, error) {
	return h.cpty.Write(p)
}

func (h *hostConPTYConn) Close() error {
	var closeErr error
	h.closeOnce.Do(func() {
		closeErr = h.cpty.Close()
	})
	return closeErr
}

// buildCommandLine joins args into a Windows command line string,
// quoting arguments that contain spaces.
func buildCommandLine(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		if strings.ContainsAny(arg, " \t\"") {
			quoted[i] = `"` + strings.ReplaceAll(arg, `"`, `\"`) + `"`
		} else {
			quoted[i] = arg
		}
	}
	return strings.Join(quoted, " ")
}
