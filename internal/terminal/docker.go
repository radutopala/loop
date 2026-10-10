package terminal

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types"
	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// dockerExecAPI abstracts the Docker SDK exec methods for testing.
type dockerExecAPI interface {
	ContainerExecCreate(ctx context.Context, container string, options containertypes.ExecOptions) (containertypes.ExecCreateResponse, error)
	ContainerExecAttach(ctx context.Context, execID string, options containertypes.ExecAttachOptions) (types.HijackedResponse, error)
	ContainerExecResize(ctx context.Context, execID string, options containertypes.ResizeOptions) error
	ContainerExecInspect(ctx context.Context, execID string) (containertypes.ExecInspect, error)
}

// DockerExecClient implements ExecClient using the Docker SDK.
// It wraps the Docker exec API (docker exec) to create interactive processes
// inside running containers. This is separate from container.Client which
// handles container lifecycle (docker create/start/stop/rm).
type DockerExecClient struct {
	api      dockerExecAPI
	execUser func() string
}

// NewDockerExecClient creates a new DockerExecClient backed by the Docker SDK.
func NewDockerExecClient() (*DockerExecClient, error) {
	return newDockerExecClientWith(func() (dockerExecAPI, error) {
		return client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	})
}

func newDockerExecClientWith(apiFactory func() (dockerExecAPI, error)) (*DockerExecClient, error) {
	api, err := apiFactory()
	if err != nil {
		return nil, err
	}
	return &DockerExecClient{
		api:      api,
		execUser: defaultExecUser,
	}, nil
}

// defaultExecUser returns the "<uid>:<gid>" a docker exec should run as.
//
// It prefers LOOP_HOST_UID/LOOP_HOST_GID from the environment, falling back to the
// current process's own uid/gid. This matters when the loop daemon itself
// runs as root (e.g. inside a container): agent containers run Claude as the
// non-root agent user pinned to LOOP_HOST_UID, and Claude refuses
// --dangerously-skip-permissions under root — so the exec must follow the
// same uid, not root. In the common case (daemon running as your own non-root
// user, LOOP_HOST_UID unset) it falls back to the process uid, which already
// matches the agent user, so behavior is unchanged.
//
// Numeric IDs bypass runc's /etc/passwd lookup at exec creation, which would
// otherwise race against the entrypoint's useradd and fail with
// "unable to find user X: no matching entries in passwd file".
// On Windows, os.Getuid/os.Getgid return -1; Docker Desktop maps file
// permissions transparently, so fall back to the container's root user.
func defaultExecUser() string {
	if u := execUserFromEnv(os.Getenv("LOOP_HOST_UID"), os.Getenv("LOOP_HOST_GID")); u != "" {
		return u
	}
	return formatExecUser(os.Getuid(), os.Getgid())
}

// execUserFromEnv builds "<uid>:<gid>" from LOOP_HOST_UID/LOOP_HOST_GID values, returning
// "" when either is empty or not a non-negative integer (so the caller falls
// back to the process uid/gid).
func execUserFromEnv(uid, gid string) string {
	ui, err := strconv.Atoi(strings.TrimSpace(uid))
	if err != nil || ui < 0 {
		return ""
	}
	gi, err := strconv.Atoi(strings.TrimSpace(gid))
	if err != nil || gi < 0 {
		return ""
	}
	return formatExecUser(ui, gi)
}

// rootExecUser is the user interactive execs are created as, before
// interactiveExecScript drops them.
const rootExecUser = "0:0"

// formatExecUser is the pure helper behind defaultExecUser, split out so the
// Windows uid/gid==-1 fallback can be exercised on POSIX hosts where
// os.Getuid/os.Getgid never return negative values.
func formatExecUser(uid, gid int) string {
	if uid < 0 || gid < 0 {
		return rootExecUser
	}
	return fmt.Sprintf("%d:%d", uid, gid)
}

// interactiveExecScript is how every interactive exec starts: as root, with
// the user it should run as ("<uid>:<gid>", see defaultExecUser) as $1 and
// the requested command after it.
//
// It starts as root so that, in a container with the seccomp gate on, it can
// hand the command to `loop syscallwrap`, whose root parent reads the gate
// token (root-only, so the agent can't) and drops the command to the agent
// user. Without the gate it drops straight to the user with gosu (in the
// agent image) or setpriv, and runs the command as is when it already is
// that user or has neither tool.
//
// First it waits for the user's /etc/passwd entry. The image entrypoint
// creates the container user with useradd, which lands a few tens of
// milliseconds after Docker reports the container started — but the terminal
// opens its exec as soon as ContainerStart returns, so a shell can start
// before /etc/passwd has an entry for its uid, and bash, which resolves its
// user name once at startup, renders "I have no name!@<host>" for the whole
// session. The loop waits ~2s and then runs the command regardless: a uid
// that genuinely has no entry (a custom image ignoring LOOP_HOST_UID) ends up
// exactly where it would otherwise. getent is skipped when the image lacks
// it so nothing spins for images without libc tooling.
const interactiveExecScript = `u="$1"; shift
if command -v getent >/dev/null 2>&1; then i=0; while [ "$i" -lt 100 ] && ! getent passwd "${u%%:*}" >/dev/null 2>&1; do i=$((i+1)); sleep 0.02; done; fi
if [ "$LOOP_GATE_ENABLED" = "1" ] && [ -x /usr/local/bin/loop ]; then exec /usr/local/bin/loop syscallwrap -- "$@"; fi
if [ "$(id -u)" = "${u%%:*}" ]; then exec "$@"; fi
if command -v gosu >/dev/null 2>&1; then exec gosu "$u" "$@"; fi
if command -v setpriv >/dev/null 2>&1; then exec setpriv --reuid="${u%%:*}" --regid="${u#*:}" --clear-groups "$@"; fi
exec "$@"`

// wrapInteractiveExec wraps cmd in interactiveExecScript. The command is
// passed as positional arguments rather than interpolated into the script so
// no argument needs quoting, and the script's exec replaces the wrapper,
// leaving the process tree (and the PID the shell writes to its pid file)
// unchanged.
func wrapInteractiveExec(user string, cmd []string) []string {
	return append([]string{"/bin/sh", "-c", interactiveExecScript, "sh", user}, cmd...)
}

// DefaultShellCmd returns a /bin/bash command that writes its PID to pidFile
// for reliable process group cleanup inside the container. Bash is started
// with an explicit --rcfile so image-baked settings load even when the user's
// own ~/.bashrc overrides the default; the rcfile itself sources ~/.bashrc
// first.
func (c *DockerExecClient) DefaultShellCmd(pidFile string) []string {
	return []string{"/bin/bash", "-c", fmt.Sprintf("echo $$ > %s; exec /bin/bash --rcfile /etc/loop/bashrc -i", pidFile)}
}

// PidFileCmd returns cmd wrapped to write its PID to pidFile first, so an
// explicit command can be killed the way the default shell is.
func (c *DockerExecClient) PidFileCmd(pidFile string, cmd []string) []string {
	return append([]string{"/bin/sh", "-c", `echo $$ > "$0"; exec "$@"`, pidFile}, cmd...)
}

// ExecCreate creates a new exec process in the container with the
// given command and TTY setting. The command runs as the host UID:GID
// (matching the container's non-root agent user created by the entrypoint):
// non-TTY execs are created as that user, interactive ones as root and
// dropped to it (see interactiveExecScript). Numeric IDs avoid a name lookup
// in /etc/passwd, which would race against the entrypoint's useradd.
// If cmd is empty, defaults to /bin/sh.
func (c *DockerExecClient) ExecCreate(ctx context.Context, containerID string, cmd []string, tty bool) (string, error) {
	return c.ExecCreateWithEnv(ctx, containerID, cmd, nil, tty)
}

// ExecCreateWithEnv is ExecCreate with extra environment variables attached
// to the exec. Used by the terminal Manager to stamp LOOP_TERMINAL_LEAF on
// terminal-pane execs so the in-container dockerproxy can attribute approval
// prompts to the originating pane (vs. the chat agent / other terminals).
func (c *DockerExecClient) ExecCreateWithEnv(ctx context.Context, containerID string, cmd, env []string, tty bool) (string, error) {
	if len(cmd) == 0 {
		cmd = []string{"/bin/sh"}
	}
	// Only interactive execs are shells a user (or an interactive claude)
	// works in, so only they go through the gate. Non-TTY execs (e.g. the
	// session kill helper) run as the user directly and skip the extra layer.
	user := c.execUser()
	if tty {
		cmd = wrapInteractiveExec(user, cmd)
		user = rootExecUser
	}
	resp, err := c.api.ContainerExecCreate(ctx, containerID, containertypes.ExecOptions{
		User:         user,
		Cmd:          cmd,
		Env:          env,
		Tty:          tty,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// ExecAttach attaches to an exec process and returns an
// io.ReadWriteCloser over the hijacked connection.
func (c *DockerExecClient) ExecAttach(ctx context.Context, execID string) (io.ReadWriteCloser, error) {
	resp, err := c.api.ContainerExecAttach(ctx, execID, containertypes.ExecAttachOptions{
		Tty: true,
	})
	if err != nil {
		return nil, err
	}
	return &hijackedConn{resp: resp}, nil
}

// ExecResize changes the PTY dimensions of the exec process.
func (c *DockerExecClient) ExecResize(ctx context.Context, execID string, height, width uint) error {
	return c.api.ContainerExecResize(ctx, execID, containertypes.ResizeOptions{
		Height: height,
		Width:  width,
	})
}

// ExecInspectPid returns the PID of the exec process inside the container.
func (c *DockerExecClient) ExecInspectPid(ctx context.Context, execID string) (int, error) {
	info, err := c.api.ContainerExecInspect(ctx, execID)
	if err != nil {
		return 0, err
	}
	return info.Pid, nil
}

// hijackedConn wraps a Docker HijackedResponse as an io.ReadWriteCloser.
// Reads use the buffered reader (which may hold data from the initial
// handshake), while writes go directly to the underlying connection.
type hijackedConn struct {
	resp types.HijackedResponse
}

func (h *hijackedConn) Read(p []byte) (int, error) {
	return h.resp.Reader.Read(p)
}

func (h *hijackedConn) Write(p []byte) (int, error) {
	return h.resp.Conn.Write(p)
}

func (h *hijackedConn) Close() error {
	h.resp.Close()
	return nil
}
