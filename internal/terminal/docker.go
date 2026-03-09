package terminal

import (
	"context"
	"io"
	"os"

	"github.com/docker/docker/api/types"
	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// osGetenv is a shim for os.Getenv, overridable in tests.
var osGetenv = os.Getenv

// dockerExecAPI abstracts the Docker SDK exec methods for testing.
type dockerExecAPI interface {
	ContainerExecCreate(ctx context.Context, container string, options containertypes.ExecOptions) (containertypes.ExecCreateResponse, error)
	ContainerExecAttach(ctx context.Context, execID string, options containertypes.ExecAttachOptions) (types.HijackedResponse, error)
	ContainerExecResize(ctx context.Context, execID string, options containertypes.ResizeOptions) error
}

// DockerExecClient implements ExecClient using the Docker SDK.
// It wraps the Docker exec API (docker exec) to create interactive processes
// inside running containers. This is separate from container.Client which
// handles container lifecycle (docker create/start/stop/rm).
type DockerExecClient struct {
	api dockerExecAPI
}

// newDockerExecClientFunc creates the underlying Docker API client.
// It can be overridden in tests.
var newDockerExecClientFunc = func() (dockerExecAPI, error) {
	return client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
}

// NewDockerExecClient creates a new DockerExecClient backed by the Docker SDK.
func NewDockerExecClient() (*DockerExecClient, error) {
	api, err := newDockerExecClientFunc()
	if err != nil {
		return nil, err
	}
	return &DockerExecClient{api: api}, nil
}

// ContainerExecCreate creates a new exec process in the container with the
// given command and TTY setting. The exec runs as the host user (matching the
// container's non-root agent user created by the entrypoint).
func (c *DockerExecClient) ContainerExecCreate(ctx context.Context, containerID string, cmd []string, tty bool) (string, error) {
	resp, err := c.api.ContainerExecCreate(ctx, containerID, containertypes.ExecOptions{
		User:         osGetenv("USER"),
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

// ContainerExecAttach attaches to an exec process and returns an
// io.ReadWriteCloser over the hijacked connection.
func (c *DockerExecClient) ContainerExecAttach(ctx context.Context, execID string) (io.ReadWriteCloser, error) {
	resp, err := c.api.ContainerExecAttach(ctx, execID, containertypes.ExecAttachOptions{
		Tty: true,
	})
	if err != nil {
		return nil, err
	}
	return &hijackedConn{resp: resp}, nil
}

// ContainerExecResize changes the PTY dimensions of the exec process.
func (c *DockerExecClient) ContainerExecResize(ctx context.Context, execID string, height, width uint) error {
	return c.api.ContainerExecResize(ctx, execID, containertypes.ResizeOptions{
		Height: height,
		Width:  width,
	})
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
