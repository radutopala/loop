package terminal

import (
	"context"
	"fmt"
	"io"
	"os"

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

// defaultExecUser returns "<uid>:<gid>" of the current process. Numeric IDs
// bypass runc's /etc/passwd lookup at exec creation, which would otherwise
// race against the entrypoint's useradd and fail with
// "unable to find user X: no matching entries in passwd file".
// On Windows, os.Getuid/os.Getgid return -1; Docker Desktop maps file
// permissions transparently, so fall back to the container's root user.
func defaultExecUser() string {
	return formatExecUser(os.Getuid(), os.Getgid())
}

// formatExecUser is the pure helper behind defaultExecUser, split out so the
// Windows uid/gid==-1 fallback can be exercised on POSIX hosts where
// os.Getuid/os.Getgid never return negative values.
func formatExecUser(uid, gid int) string {
	if uid < 0 || gid < 0 {
		return "0:0"
	}
	return fmt.Sprintf("%d:%d", uid, gid)
}

// DefaultShellCmd returns a /bin/bash command that writes its PID to pidFile
// for reliable process group cleanup inside the container. Bash is started
// with an explicit --rcfile so image-baked aliases (e.g. `claude` →
// `loop syscallwrap -- claude`) load even when the user's own ~/.bashrc
// overrides the default; the rcfile itself sources ~/.bashrc first.
func (c *DockerExecClient) DefaultShellCmd(pidFile string) []string {
	return []string{"/bin/bash", "-c", fmt.Sprintf("echo $$ > %s; exec /bin/bash --rcfile /etc/loop/bashrc -i", pidFile)}
}

// ExecCreate creates a new exec process in the container with the
// given command and TTY setting. The exec runs as the host UID:GID (matching
// the container's non-root agent user created by the entrypoint). Numeric
// IDs avoid a name lookup in /etc/passwd, which would race against the
// entrypoint's useradd.
// If cmd is empty, defaults to /bin/sh.
func (c *DockerExecClient) ExecCreate(ctx context.Context, containerID string, cmd []string, tty bool) (string, error) {
	if len(cmd) == 0 {
		cmd = []string{"/bin/sh"}
	}
	resp, err := c.api.ContainerExecCreate(ctx, containerID, containertypes.ExecOptions{
		User:         c.execUser(),
		Cmd:          cmd,
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
