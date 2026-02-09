package container

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/docker/docker/api/types"
	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// dockerAPI abstracts the Docker SDK methods used by Client, enabling unit testing.
type dockerAPI interface {
	ContainerCreate(ctx context.Context, config *containertypes.Config, hostConfig *containertypes.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (containertypes.CreateResponse, error)
	ContainerAttach(ctx context.Context, container string, options containertypes.AttachOptions) (types.HijackedResponse, error)
	ContainerStart(ctx context.Context, container string, options containertypes.StartOptions) error
	ContainerWait(ctx context.Context, container string, condition containertypes.WaitCondition) (<-chan containertypes.WaitResponse, <-chan error)
	ContainerRemove(ctx context.Context, container string, options containertypes.RemoveOptions) error
	ContainerList(ctx context.Context, options containertypes.ListOptions) ([]containertypes.Summary, error)
	ImageList(ctx context.Context, options image.ListOptions) ([]image.Summary, error)
	ImagePull(ctx context.Context, refStr string, options image.PullOptions) (io.ReadCloser, error)
	Close() error
}

// newDockerClientFunc is the constructor used to create the underlying Docker API client.
// It can be overridden in tests.
var newDockerClientFunc = func() (dockerAPI, error) {
	return client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
}

// Client implements DockerClient by delegating to the Docker SDK.
type Client struct {
	api dockerAPI
}

// NewClient creates a new Client backed by the Docker SDK.
func NewClient() (*Client, error) {
	api, err := newDockerClientFunc()
	if err != nil {
		return nil, fmt.Errorf("creating docker client: %w", err)
	}
	return &Client{api: api}, nil
}

// Close releases the underlying Docker client resources.
func (c *Client) Close() error {
	return c.api.Close()
}

// ContainerCreate creates a new Docker container from the given config.
func (c *Client) ContainerCreate(ctx context.Context, cfg *ContainerConfig, name string) (string, error) {
	containerCfg := &containertypes.Config{
		Image:        cfg.Image,
		AttachStdout: true,
		AttachStderr: true,
		Labels:       map[string]string{"app": "loop-agent"},
		Env:          cfg.Env,
		Cmd:          cfg.Cmd,
		WorkingDir:   cfg.WorkingDir,
	}

	hostCfg := &containertypes.HostConfig{
		Resources: containertypes.Resources{
			Memory:    cfg.MemoryMB * 1024 * 1024,
			CPUQuota:  int64(cfg.CPUs * 100000),
			CPUPeriod: 100000,
		},
		Binds:      cfg.Binds,
		GroupAdd:   cfg.GroupAdd,
		ExtraHosts: []string{"host.docker.internal:host-gateway"},
	}

	resp, err := c.api.ContainerCreate(ctx, containerCfg, hostCfg, nil, nil, name)
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// ContainerAttach attaches to the container's stdout and returns a reader for output.
// It demultiplexes the Docker stream so the caller receives clean stdout bytes.
func (c *Client) ContainerAttach(ctx context.Context, containerID string) (io.Reader, error) {
	resp, err := c.api.ContainerAttach(ctx, containerID, containertypes.AttachOptions{
		Stream: true,
		Stdout: true,
		Stderr: true,
	})
	if err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()
	go func() {
		_, copyErr := stdcopy.StdCopy(pw, pw, resp.Reader)
		pw.CloseWithError(copyErr)
	}()
	return pr, nil
}

// ContainerStart starts the specified container.
func (c *Client) ContainerStart(ctx context.Context, containerID string) error {
	return c.api.ContainerStart(ctx, containerID, containertypes.StartOptions{})
}

// ContainerWait waits for the container to reach a "not-running" state and
// converts the Docker SDK response into our WaitResponse type.
func (c *Client) ContainerWait(ctx context.Context, containerID string) (<-chan WaitResponse, <-chan error) {
	dockerWaitCh, dockerErrCh := c.api.ContainerWait(ctx, containerID, containertypes.WaitConditionNotRunning)

	waitCh := make(chan WaitResponse, 1)
	errCh := make(chan error, 1)

	go func() {
		defer close(waitCh)
		defer close(errCh)

		select {
		case wr, ok := <-dockerWaitCh:
			if !ok {
				return
			}
			var waitErr error
			if wr.Error != nil {
				waitErr = fmt.Errorf("%s", wr.Error.Message)
			}
			waitCh <- WaitResponse{
				StatusCode: wr.StatusCode,
				Error:      waitErr,
			}
		case err, ok := <-dockerErrCh:
			if !ok {
				return
			}
			errCh <- err
		}
	}()

	return waitCh, errCh
}

// ContainerRemove forcefully removes the specified container.
func (c *Client) ContainerRemove(ctx context.Context, containerID string) error {
	return c.api.ContainerRemove(ctx, containerID, containertypes.RemoveOptions{Force: true})
}

// ImageList returns the IDs of images matching the given reference.
func (c *Client) ImageList(ctx context.Context, imageName string) ([]string, error) {
	f := filters.NewArgs()
	f.Add("reference", imageName)

	images, err := c.api.ImageList(ctx, image.ListOptions{Filters: f})
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(images))
	for _, img := range images {
		ids = append(ids, img.ID)
	}
	return ids, nil
}

// ImagePull pulls the specified image and drains the response.
func (c *Client) ImagePull(ctx context.Context, imageName string) error {
	reader, err := c.api.ImagePull(ctx, imageName, image.PullOptions{})
	if err != nil {
		return err
	}
	defer reader.Close()

	_, err = io.Copy(io.Discard, reader)
	return err
}

// dockerBuildCmd executes `docker build` via the CLI. Using the CLI instead of
// the Docker SDK avoids "configured logging driver does not support reading"
// errors because the CLI uses BuildKit by default.
var dockerBuildCmd = func(ctx context.Context, contextDir, tag string) ([]byte, error) {
	return exec.CommandContext(ctx, "docker", "build", "-t", tag, contextDir).CombinedOutput()
}

// ImageBuild builds a Docker image from the given context directory.
func (c *Client) ImageBuild(ctx context.Context, contextDir, tag string) error {
	output, err := dockerBuildCmd(ctx, contextDir, tag)
	if err != nil {
		return fmt.Errorf("building image: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// ContainerList returns the IDs of containers matching the given label.
func (c *Client) ContainerList(ctx context.Context, labelKey, labelValue string) ([]string, error) {
	f := filters.NewArgs()
	f.Add("label", fmt.Sprintf("%s=%s", labelKey, labelValue))

	containers, err := c.api.ContainerList(ctx, containertypes.ListOptions{
		All:     true,
		Filters: f,
	})
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(containers))
	for _, ctr := range containers {
		ids = append(ids, ctr.ID)
	}
	return ids, nil
}
