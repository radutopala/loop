package container

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/docker/docker/api/types/build"
	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/radutopala/loop/internal/osutil"
)

// dockerAPI abstracts the Docker SDK methods used by Client, enabling unit testing.
type dockerAPI interface {
	ContainerCreate(ctx context.Context, config *containertypes.Config, hostConfig *containertypes.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (containertypes.CreateResponse, error)
	ContainerStart(ctx context.Context, container string, options containertypes.StartOptions) error
	ContainerLogs(ctx context.Context, container string, options containertypes.LogsOptions) (io.ReadCloser, error)
	ContainerWait(ctx context.Context, container string, condition containertypes.WaitCondition) (<-chan containertypes.WaitResponse, <-chan error)
	ContainerRemove(ctx context.Context, container string, options containertypes.RemoveOptions) error
	ContainerStop(ctx context.Context, containerID string, options containertypes.StopOptions) error
	ContainerStats(ctx context.Context, containerID string, stream bool) (containertypes.StatsResponseReader, error)
	ContainerList(ctx context.Context, options containertypes.ListOptions) ([]containertypes.Summary, error)
	ImageList(ctx context.Context, options image.ListOptions) ([]image.Summary, error)
	ImageRemove(ctx context.Context, imageID string, options image.RemoveOptions) ([]image.DeleteResponse, error)
	ImageInspectWithRaw(ctx context.Context, imageID string) (image.InspectResponse, []byte, error)
	ImagePull(ctx context.Context, refStr string, options image.PullOptions) (io.ReadCloser, error)
	ContainerInspect(ctx context.Context, containerID string) (containertypes.InspectResponse, error)
	CopyToContainer(ctx context.Context, containerID, dstPath string, content io.Reader, options containertypes.CopyToContainerOptions) error
	NetworkCreate(ctx context.Context, name string, options network.CreateOptions) (network.CreateResponse, error)
	NetworkRemove(ctx context.Context, networkID string) error
	BuildCachePrune(ctx context.Context, opts build.CachePruneOptions) (*build.CachePruneReport, error)
	ImagesPrune(ctx context.Context, pruneFilters filters.Args) (image.PruneReport, error)
	Events(ctx context.Context, options events.ListOptions) (<-chan events.Message, <-chan error)
	Close() error
}

// defaultDockerAPIFactory is the real constructor for creating the underlying Docker API client.
func defaultDockerAPIFactory() (dockerAPI, error) {
	return client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
}

// clientSystem abstracts OS operations needed by Client.
type clientSystem interface {
	UserHomeDir() (string, error)
	Stat(name string) (os.FileInfo, error)
}

// Client implements DockerClient by delegating to the Docker SDK.
// It wraps container lifecycle operations (create/start/stop/rm/list).
// For exec-ing into running containers (interactive PTY sessions), see
// terminal.DockerExecClient instead.
type Client struct {
	api                      dockerAPI
	sys                      clientSystem
	dockerBuildCmd           func(ctx context.Context, contextDir, tag string) ([]byte, error)
	dockerBuildFileCmd       func(ctx context.Context, contextDir, dockerfile, tag string) ([]byte, error)
	dockerBuildFileLabelsCmd func(ctx context.Context, contextDir, dockerfile, tag string, labels map[string]string) ([]byte, error)
	claudeVersionURL         string
	latestClaudeVersion      func() string
	loopVersion              string
}

// NewClient creates a new Client backed by the Docker SDK.
func NewClient() (*Client, error) {
	return NewClientWith(defaultDockerAPIFactory)
}

// NewClientWith creates a new Client using the provided API factory function.
// This allows tests to inject a mock Docker API without global variable overrides.
func NewClientWith(apiFactory func() (dockerAPI, error)) (*Client, error) {
	api, err := apiFactory()
	if err != nil {
		return nil, fmt.Errorf("creating docker client: %w", err)
	}
	c := &Client{
		api:              api,
		sys:              osutil.RealSystem{},
		claudeVersionURL: "https://storage.googleapis.com/claude-code-dist-86c565f3-f756-42ad-8dfa-d59b1c096819/claude-code-releases/latest",
	}
	c.latestClaudeVersion = c.defaultLatestClaudeVersion
	c.dockerBuildCmd = c.defaultDockerBuildCmd
	c.dockerBuildFileLabelsCmd = c.defaultDockerBuildFileLabelsCmd
	c.dockerBuildFileCmd = c.defaultDockerBuildFileCmd
	return c, nil
}

// SetLoopVersion sets the loop version used as a build argument when building
// the agent Docker image. When set, the Dockerfile uses this version instead
// of @latest in `go install`.
func (c *Client) SetLoopVersion(v string) {
	c.loopVersion = v
}

// LatestClaudeVersion returns the latest available Claude Code version string.
func (c *Client) LatestClaudeVersion() string {
	return c.latestClaudeVersion()
}

// Close releases the underlying Docker client resources.
func (c *Client) Close() error {
	return c.api.Close()
}

// ContainerCreate creates a new Docker container from the given config.
func (c *Client) ContainerCreate(ctx context.Context, cfg *ContainerConfig, name string) (string, error) {
	labels := map[string]string{"app": "loop-agent"}
	for k, v := range cfg.Labels {
		labels[k] = v
	}

	containerCfg := &containertypes.Config{
		Image:        cfg.Image,
		AttachStdout: true,
		AttachStderr: true,
		Labels:       labels,
		Env:          cfg.Env,
		Cmd:          cfg.Cmd,
		WorkingDir:   cfg.WorkingDir,
	}

	// Init=true gives every container a tiny tini PID 1 that reaps orphaned
	// grandchildren. Without it, processes whose parent dies before wait()ing
	// (e.g. a `git` reparented when its caller exits) accumulate as zombies
	// under loop-syscallwrap (or claude pre-gate), neither of which is a
	// reaper. Pre-existing for the agent image; flipped here unconditionally.
	initTrue := true
	hostCfg := &containertypes.HostConfig{
		Resources: containertypes.Resources{
			Memory:    cfg.MemoryMB * 1024 * 1024,
			CPUQuota:  int64(cfg.CPUs * 100000),
			CPUPeriod: 100000,
		},
		Binds:       cfg.Binds,
		GroupAdd:    cfg.GroupAdd,
		ExtraHosts:  []string{"host.docker.internal:host-gateway"},
		SecurityOpt: cfg.SecurityOpt,
		CapAdd:      cfg.CapAdd,
		Init:        &initTrue,
	}

	var netCfg *network.NetworkingConfig
	if cfg.NetworkName != "" {
		containerCfg.Hostname = cfg.Hostname
		netCfg = &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				cfg.NetworkName: {
					Aliases: []string{cfg.Hostname},
				},
			},
		}
	}

	resp, err := c.api.ContainerCreate(ctx, containerCfg, hostCfg, netCfg, nil, name)
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// ContainerStatsSummary is a one-shot resource snapshot of a running
// container, digested from the Docker stats endpoint for UI display.
type ContainerStatsSummary struct {
	CPUPercent float64 `json:"cpu_percent"`
	MemUsage   uint64  `json:"mem_usage"`
	MemLimit   uint64  `json:"mem_limit"`
}

// ContainerStats fetches a single stats sample for the container and digests
// it into CPU% and memory numbers. stream=false makes the daemon prime the
// precpu sample itself, so the CPU delta is meaningful without a second read.
func (c *Client) ContainerStats(ctx context.Context, containerID string) (*ContainerStatsSummary, error) {
	resp, err := c.api.ContainerStats(ctx, containerID, false)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var st containertypes.StatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return nil, fmt.Errorf("decoding container stats: %w", err)
	}
	return digestStats(&st), nil
}

// digestStats converts a raw stats sample into the UI summary using the
// standard docker-cli formulas.
func digestStats(st *containertypes.StatsResponse) *ContainerStatsSummary {
	s := &ContainerStatsSummary{}
	cpuDelta := float64(st.CPUStats.CPUUsage.TotalUsage) - float64(st.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(st.CPUStats.SystemUsage) - float64(st.PreCPUStats.SystemUsage)
	if cpuDelta > 0 && sysDelta > 0 {
		cpus := float64(st.CPUStats.OnlineCPUs)
		if cpus == 0 {
			cpus = float64(len(st.CPUStats.CPUUsage.PercpuUsage))
		}
		s.CPUPercent = cpuDelta / sysDelta * cpus * 100
	}
	// Subtract the page cache the kernel can reclaim (docker-cli parity:
	// cgroup v2 inactive_file, cgroup v1 total_inactive_file).
	mem := st.MemoryStats.Usage
	if v, ok := st.MemoryStats.Stats["inactive_file"]; ok && v < mem {
		mem -= v
	} else if v, ok := st.MemoryStats.Stats["total_inactive_file"]; ok && v < mem {
		mem -= v
	}
	s.MemUsage = mem
	s.MemLimit = st.MemoryStats.Limit
	return s
}

// ContainerInspect returns detailed container information.
func (c *Client) ContainerInspect(ctx context.Context, containerID string) (containertypes.InspectResponse, error) {
	return c.api.ContainerInspect(ctx, containerID)
}

// NetworkEnsure creates a Docker bridge network if it doesn't already exist.
func (c *Client) NetworkEnsure(ctx context.Context, name string) error {
	_, err := c.api.NetworkCreate(ctx, name, network.CreateOptions{
		Driver: "bridge",
	})
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return err
	}
	return nil
}

// NetworkRemove removes a Docker network by name.
func (c *Client) NetworkRemove(ctx context.Context, name string) error {
	return c.api.NetworkRemove(ctx, name)
}

// ContainerLogs retrieves the container's stdout/stderr logs after it exits.
// It demultiplexes the Docker stream so the caller receives clean output bytes.
func (c *Client) ContainerLogs(ctx context.Context, containerID string) (io.Reader, error) {
	resp, err := c.api.ContainerLogs(ctx, containerID, containertypes.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
	})
	if err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()
	go func() {
		_, copyErr := stdcopy.StdCopy(pw, pw, resp)
		resp.Close()
		pw.CloseWithError(copyErr)
	}()
	return pr, nil
}

// ContainerLogsFollow follows the container's stdout/stderr logs in real-time.
// The returned ReadCloser streams log output as it is produced. The caller
// must close the reader when done.
func (c *Client) ContainerLogsFollow(ctx context.Context, containerID string) (io.ReadCloser, error) {
	resp, err := c.api.ContainerLogs(ctx, containerID, containertypes.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
	})
	if err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()
	go func() {
		_, copyErr := stdcopy.StdCopy(pw, pw, resp)
		resp.Close()
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

// ContainerRemove forcefully removes the specified container along with its
// anonymous volumes. Named volumes (e.g. the ollama model cache) are not
// touched — Docker's RemoveVolumes only drops volumes the container itself
// owned implicitly. Without this flag every agent container leaks ~5
// anonymous volumes that accumulate until the disk fills up.
func (c *Client) ContainerRemove(ctx context.Context, containerID string) error {
	return c.api.ContainerRemove(ctx, containerID, containertypes.RemoveOptions{Force: true, RemoveVolumes: true})
}

// ContainerStop stops the specified container with a 10-second grace period
// (SIGTERM → wait → SIGKILL).
func (c *Client) ContainerStop(ctx context.Context, containerID string) error {
	timeout := 10
	return c.api.ContainerStop(ctx, containerID, containertypes.StopOptions{Timeout: &timeout})
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

// RemoveImageAndContainers stops and removes all containers (running and stopped)
// that reference the given image, then removes the image itself.
func (c *Client) RemoveImageAndContainers(ctx context.Context, imageName string) error {
	// Find all containers (including stopped) using this image.
	f := filters.NewArgs()
	f.Add("ancestor", imageName)
	containers, err := c.api.ContainerList(ctx, containertypes.ListOptions{Filters: f, All: true})
	if err != nil {
		return fmt.Errorf("listing containers for image %s: %w", imageName, err)
	}
	for _, ctr := range containers {
		if err := c.api.ContainerRemove(ctx, ctr.ID, containertypes.RemoveOptions{Force: true, RemoveVolumes: true}); err != nil {
			return fmt.Errorf("removing container %s: %w", ctr.ID[:12], err)
		}
	}

	// Remove the image.
	ids, err := c.ImageList(ctx, imageName)
	if err != nil {
		return fmt.Errorf("listing image %s: %w", imageName, err)
	}
	for _, id := range ids {
		if _, err := c.api.ImageRemove(ctx, id, image.RemoveOptions{Force: true}); err != nil {
			return fmt.Errorf("removing image %s: %w", id[:12], err)
		}
	}
	return nil
}

// ImageInspectLabels returns the labels of the given image.
func (c *Client) ImageInspectLabels(ctx context.Context, imageName string) (map[string]string, error) {
	ids, err := c.ImageList(ctx, imageName)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	inspect, _, err := c.api.ImageInspectWithRaw(ctx, ids[0])
	if err != nil {
		return nil, err
	}
	if inspect.Config == nil {
		return nil, nil
	}
	return inspect.Config.Labels, nil
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

// defaultLatestClaudeVersion fetches the latest Claude CLI version string.
// Falls back to a timestamp if the lookup fails, which busts the cache.
func (c *Client) defaultLatestClaudeVersion() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.claudeVersionURL, nil)
	if err != nil {
		return fmt.Sprintf("unknown-%d", time.Now().Unix())
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Sprintf("unknown-%d", time.Now().Unix())
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil || resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("unknown-%d", time.Now().Unix())
	}
	return strings.TrimSpace(string(body))
}

// defaultDockerBuildCmd executes `docker build` via the CLI. Using the CLI
// instead of the Docker SDK avoids "configured logging driver does not support
// reading" errors because the CLI uses BuildKit by default.
func (c *Client) defaultDockerBuildCmd(ctx context.Context, contextDir, tag string) ([]byte, error) {
	claudeVersion := "CLAUDE_VERSION=" + c.latestClaudeVersion()
	args := []string{"build", "--build-arg", claudeVersion}
	if c.loopVersion != "" && c.loopVersion != "dev" && !strings.Contains(c.loopVersion, "-g") && !strings.Contains(c.loopVersion, "-dirty") {
		args = append(args, "--build-arg", "LOOP_VERSION="+c.loopVersion)
	} else {
		args = append(args, "--build-arg", "LOOP_VERSION=main", "--no-cache")
	}
	args = append(args, "--label", "loop.built_at="+time.Now().UTC().Format(time.RFC3339))
	if gitconfigPath := c.gitconfigSecretPath(); gitconfigPath != "" {
		args = append(args, "--secret", "id=gitconfig,src="+gitconfigPath)
	}
	args = append(args, "-t", tag, contextDir)
	return exec.CommandContext(ctx, "docker", args...).CombinedOutput()
}

// gitconfigSecretPath returns the path to ~/.gitconfig if it exists, or "" otherwise.
func (c *Client) gitconfigSecretPath() string {
	home, err := c.sys.UserHomeDir()
	if err != nil {
		return ""
	}
	p := filepath.Join(home, ".gitconfig")
	if _, err := c.sys.Stat(p); err != nil {
		return ""
	}
	return p
}

// ImageBuild builds a Docker image from the given context directory.
func (c *Client) ImageBuild(ctx context.Context, contextDir, tag string) error {
	output, err := c.dockerBuildCmd(ctx, contextDir, tag)
	if err != nil {
		return fmt.Errorf("building image: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// ImageBuildFileLabels builds a Docker image from a specific Dockerfile in
// the context directory, attaching the given labels. Used by the child-image
// cascade to stamp loop.parent_id at build time.
func (c *Client) ImageBuildFileLabels(ctx context.Context, contextDir, dockerfile, tag string, labels map[string]string) error {
	output, err := c.dockerBuildFileLabelsCmd(ctx, contextDir, dockerfile, tag, labels)
	if err != nil {
		return fmt.Errorf("building image: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// ImageBuildFile builds a Docker image from a specific Dockerfile in the context directory.
func (c *Client) ImageBuildFile(ctx context.Context, contextDir, dockerfile, tag string) error {
	output, err := c.dockerBuildFileCmd(ctx, contextDir, dockerfile, tag)
	if err != nil {
		return fmt.Errorf("building image: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// PruneBuildCache drops BuildKit cache entries not used in the past unusedFor
// window and returns the number of bytes reclaimed. Called after a successful
// image build so frequently-used layers stay warm but stale entries don't
// accumulate across many `make restart` / daemon-restart cycles. Without this,
// a single Loop install can balloon to 100GB+ of build cache over months. Also
// exposed via the settings "reclaim Docker space" action (with unusedFor=0 to
// drop all currently-unused cache). Note: BuildKit cache is daemon-global and
// not tagged per-project, so this is not scoped to Loop's own builds.
func (c *Client) PruneBuildCache(ctx context.Context, unusedFor time.Duration) (uint64, error) {
	f := filters.NewArgs()
	f.Add("unused-for", unusedFor.String())
	report, err := c.api.BuildCachePrune(ctx, build.CachePruneOptions{Filters: f})
	if err != nil {
		return 0, fmt.Errorf("pruning build cache: %w", err)
	}
	if report == nil {
		return 0, nil
	}
	return report.SpaceReclaimed, nil
}

// PruneDanglingImages removes dangling (untagged) images and returns the
// number of bytes reclaimed. Scoped to dangling images so tagged images still
// referenced by Loop (loop-agent, project images) are preserved — the common
// case is layers orphaned by repeated image rebuilds.
func (c *Client) PruneDanglingImages(ctx context.Context) (uint64, error) {
	f := filters.NewArgs()
	f.Add("dangling", "true")
	report, err := c.api.ImagesPrune(ctx, f)
	if err != nil {
		return 0, fmt.Errorf("pruning dangling images: %w", err)
	}
	return report.SpaceReclaimed, nil
}

func (c *Client) defaultDockerBuildFileLabelsCmd(ctx context.Context, contextDir, dockerfile, tag string, labels map[string]string) ([]byte, error) {
	args := []string{"build", "-f", filepath.Join(contextDir, dockerfile)}
	for _, k := range slices.Sorted(maps.Keys(labels)) {
		args = append(args, "--label", k+"="+labels[k])
	}
	args = append(args, "-t", tag, contextDir)
	return exec.CommandContext(ctx, "docker", args...).CombinedOutput()
}

func (c *Client) defaultDockerBuildFileCmd(ctx context.Context, contextDir, dockerfile, tag string) ([]byte, error) {
	args := []string{"build", "-f", filepath.Join(contextDir, dockerfile), "-t", tag, contextDir}
	return exec.CommandContext(ctx, "docker", args...).CombinedOutput()
}

// ContainerList returns the IDs of running containers matching the given label.
func (c *Client) ContainerList(ctx context.Context, labelKey, labelValue string) ([]string, error) {
	f := filters.NewArgs()
	f.Add("label", fmt.Sprintf("%s=%s", labelKey, labelValue))

	containers, err := c.api.ContainerList(ctx, containertypes.ListOptions{
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

// ListContainerInfos returns metadata for all loop-managed containers
// (both agent/shell containers labeled "app=loop-agent" and Chrome containers
// labeled "loop-chrome"), including stopped ones. Used to restore the registry
// after a daemon restart and for reconciliation.
func (c *Client) ListContainerInfos(ctx context.Context) ([]*ContainerInfo, error) {
	// Query agent/shell containers (including stopped).
	agentFilter := filters.NewArgs()
	agentFilter.Add("label", "app="+ContainerLabel)
	agentContainers, err := c.api.ContainerList(ctx, containertypes.ListOptions{All: true, Filters: agentFilter})
	if err != nil {
		return nil, fmt.Errorf("listing agent containers: %w", err)
	}

	// Query Chrome containers (including stopped).
	chromeFilter := filters.NewArgs()
	chromeFilter.Add("label", "loop-chrome")
	chromeContainers, err := c.api.ContainerList(ctx, containertypes.ListOptions{All: true, Filters: chromeFilter})
	if err != nil {
		return nil, fmt.Errorf("listing chrome containers: %w", err)
	}

	var result []*ContainerInfo
	for _, ctr := range agentContainers {
		channelID := ctr.Labels[ChannelLabelKey]
		if channelID == "" {
			continue
		}
		cType := ContainerType(ctr.Labels[ContainerTypeKey])
		if cType == "" {
			cType = ContainerTypeAgent // default for containers created before the label existed
		}
		var name string
		if len(ctr.Names) > 0 {
			name = strings.TrimPrefix(ctr.Names[0], "/")
		}
		result = append(result, &ContainerInfo{
			ContainerID:   ctr.ID,
			ChannelID:     channelID,
			Type:          cType,
			Status:        dockerStateToStatus(ctr.State),
			ContainerName: name,
			InstanceID:    ctr.Labels[InstanceLabelKey],
			CreatedAt:     time.Unix(ctr.Created, 0),
		})
	}
	for _, ctr := range chromeContainers {
		channelID := ctr.Labels["loop-chrome"]
		if channelID == "" {
			continue
		}
		var name string
		if len(ctr.Names) > 0 {
			name = strings.TrimPrefix(ctr.Names[0], "/")
		}
		result = append(result, &ContainerInfo{
			ContainerID:   ctr.ID,
			ChannelID:     channelID,
			Type:          ContainerTypeChrome,
			Status:        dockerStateToStatus(ctr.State),
			ContainerName: name,
			CreatedAt:     time.Unix(ctr.Created, 0),
		})
	}
	return result, nil
}

// OOMEventStreamer is implemented by container clients that can stream
// container out-of-memory events. Used to optionally wire up OOMWatcher —
// a docker client that doesn't implement it (e.g. a minimal test double)
// simply skips OOM notifications rather than requiring every caller to
// implement Events.
type OOMEventStreamer interface {
	OOMEvents(ctx context.Context) (<-chan events.Message, <-chan error)
}

// OOMEvents streams Docker "container oom" events for loop-managed agent
// containers (labeled app=loop-agent). The kernel emits this event when a
// process inside the container's cgroup is killed for exceeding its memory
// limit, even if the container itself keeps running (e.g. only the Claude
// process was reaped) — so, unlike a container "die" event, this is the only
// signal available for that case.
func (c *Client) OOMEvents(ctx context.Context) (<-chan events.Message, <-chan error) {
	f := filters.NewArgs()
	f.Add("type", string(events.ContainerEventType))
	f.Add("event", "oom")
	f.Add("label", "app="+ContainerLabel)
	return c.api.Events(ctx, events.ListOptions{Filters: f})
}

// dockerStateToStatus maps Docker's container state string to a ContainerStatus.
// Docker states: "created", "restarting", "running", "removing", "paused", "exited", "dead".
func dockerStateToStatus(state string) ContainerStatus {
	if state == "running" {
		return ContainerStatusRunning
	}
	return ContainerStatusStopped
}

// CopyToContainer copies a tar archive into the container at the given path.
func (c *Client) CopyToContainer(ctx context.Context, containerID, dstPath string, content io.Reader) error {
	return c.api.CopyToContainer(ctx, containerID, dstPath, content, containertypes.CopyToContainerOptions{})
}
