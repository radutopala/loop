// childimages.go implements automatic rebuilds of per-project agent images
// that are based on Loop's own agent image. Projects may override
// `container_image` in their .loop/config.json and build a custom image
// FROM loop-agent (e.g. to bake in a corporate CA or extra tools); without
// this, upgrading Loop rebuilds loop-agent but leaves those child images on
// the old base until someone remembers to rebuild them by hand.
package container

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// ParentIDLabel marks a child image with the image ID of the base it was
// built from. A child is stale exactly when its label differs from the
// current base image ID.
const ParentIDLabel = "loop.parent_id"

// childDockerfileRelPath is the conventional location of a project's agent
// image Dockerfile, relative to the project directory.
var childDockerfileRelPath = filepath.Join(".loop", "container", "Dockerfile")

// ChildProject describes one registered project's image override, resolved
// by the daemon from the channels store + merged project config.
type ChildProject struct {
	DirPath   string // project directory (channel dir_path)
	Image     string // merged container_image for the project
	Autobuild bool   // merged container_image_autobuild (default true)
}

// ChildImageManager rebuilds project images based on Loop's agent image
// whenever that base image changes. Discovery and config resolution are
// injected so this package stays free of db/config dependencies.
type ChildImageManager struct {
	client    DockerClient
	baseImage string
	logger    *slog.Logger

	// listProjects returns the registered projects' image overrides. Wired
	// to the channels store + config merge by the daemon.
	listProjects func(ctx context.Context) ([]ChildProject, error)

	// beginBuild marks an image as being built until the returned func is
	// called, so containers wait for it. Wired to
	// ImageLifecycleManager.BeginBuild by the daemon.
	beginBuild func(images ...string) (end func())

	// readFile is injectable for tests; nil → os.ReadFile.
	readFile func(string) ([]byte, error)

	// writeFile writes a file, creating its dir. Injectable for tests.
	writeFile func(path string, data []byte) error

	mu sync.Mutex // serializes cascades
}

// NewChildImageManager creates a manager for the given base image.
func NewChildImageManager(client DockerClient, baseImage string, listProjects func(ctx context.Context) ([]ChildProject, error), beginBuild func(images ...string) (end func()), logger *slog.Logger) *ChildImageManager {
	return &ChildImageManager{
		client:       client,
		baseImage:    baseImage,
		logger:       logger,
		listProjects: listProjects,
		beginBuild:   beginBuild,
		readFile:     os.ReadFile,
		writeFile:    writeFileMkdir,
	}
}

// RebuildStale rebuilds every eligible child image whose recorded parent ID
// differs from the current base image ID. Eligible means: the project
// overrides container_image, autobuild is not disabled, a Dockerfile exists
// at .loop/container/Dockerfile, and that Dockerfile has a stage FROM the
// base image. Failures are logged per child and never abort the cascade.
//
// The stale children are all marked as building (see beginBuild) before
// handoff is called and the first of them is built, and each stays marked
// until its own build is done, so a container on a project image waits for
// that image's rebuild and nothing else. handoff is called exactly once,
// also when there is nothing to rebuild.
func (m *ChildImageManager) RebuildStale(ctx context.Context, handoff func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	handoff = sync.OnceFunc(handoff)
	defer handoff()

	if m.baseImage == "" {
		return
	}
	parentIDs, err := m.client.ImageList(ctx, m.baseImage)
	if err != nil || len(parentIDs) == 0 {
		m.logger.Warn("child images: base image not found, skipping cascade", "base", m.baseImage, "error", err)
		return
	}
	parentID := parentIDs[0]

	projects, err := m.listProjects(ctx)
	if err != nil {
		m.logger.Warn("child images: listing projects failed", "error", err)
		return
	}

	type staleChild struct {
		image, contextDir string
		end               func()
	}
	var stale []staleChild
	seen := map[string]bool{}
	for _, p := range projects {
		if p.Image == "" || p.Image == m.baseImage || !p.Autobuild || seen[p.Image] {
			continue
		}
		seen[p.Image] = true

		contextDir, err := m.childContext(p.Image, p.DirPath)
		if err != nil {
			continue
		}

		if labels, err := m.client.ImageInspectLabels(ctx, p.Image); err == nil && labels != nil && labels[ParentIDLabel] == parentID {
			continue // already built on the current base
		}
		stale = append(stale, staleChild{image: p.Image, contextDir: contextDir})
	}

	for i := range stale {
		stale[i].end = m.beginBuild(stale[i].image)
	}
	handoff()

	for _, c := range stale {
		m.logger.Info("child images: rebuilding on new base",
			"image", c.image, "base", m.baseImage, "parent_id", parentID)
		err := m.client.ImageBuildFileLabels(ctx, c.contextDir, "Dockerfile", c.image, map[string]string{ParentIDLabel: parentID})
		c.end()
		if err != nil {
			m.logger.Error("child images: rebuild failed", "image", c.image, "error", err)
			continue
		}
		m.logger.Info("child images: rebuilt", "image", c.image)
	}
}

// fromLineRe matches Dockerfile FROM instructions, capturing the image ref
// (ignoring --platform flags and stage aliases).
var fromLineRe = regexp.MustCompile(`(?i)^\s*FROM\s+(?:--platform=\S+\s+)?(\S+)`)

// dockerfileFromBase reports whether any stage of the Dockerfile is based on
// the given image. "loop-agent" and "loop-agent:latest" are treated as
// equivalent when the base uses the :latest tag.
func dockerfileFromBase(content, base string) bool {
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		mm := fromLineRe.FindStringSubmatch(sc.Text())
		if mm == nil {
			continue
		}
		if imageRefEquals(mm[1], base) {
			return true
		}
	}
	return false
}

// imageRefEquals compares two image references, treating a missing tag as
// ":latest" on either side.
func imageRefEquals(a, b string) bool {
	return normalizeImageRef(a) == normalizeImageRef(b)
}

// normalizeImageRef tags an untagged image reference ":latest".
func normalizeImageRef(r string) string {
	if !strings.Contains(r, ":") {
		return r + ":latest"
	}
	return r
}

// errNotFromBase is childContext's error for a Dockerfile with no stage
// FROM the base image.
var errNotFromBase = errors.New("dockerfile not based on Loop's image")

// childContext returns the build context of image's Dockerfile in dir,
// .loop/container, when that Dockerfile has a stage FROM the base image. It
// returns the read error when there's no Dockerfile, errNotFromBase when
// it isn't FROM the base.
func (m *ChildImageManager) childContext(image, dir string) (string, error) {
	dockerfile := filepath.Join(dir, childDockerfileRelPath)
	data, err := m.readFile(dockerfile)
	if err != nil {
		m.logger.Debug("child images: no Dockerfile, skipping", "image", image, "path", dockerfile)
		return "", err
	}
	if !dockerfileFromBase(string(data), m.baseImage) {
		m.logger.Info("child images: Dockerfile not based on Loop's image, skipping",
			"image", image, "path", dockerfile, "base", m.baseImage)
		return "", errNotFromBase
	}
	return filepath.Dir(dockerfile), nil
}

// writeFileMkdir writes data to path, creating path's dir first.
func writeFileMkdir(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// defaultDockerfile is the Dockerfile BuildMissing writes for a project that
// sets container_image but has none: just Loop's image, to add to.
func defaultDockerfile(base string) string {
	return "# Written by Loop: this project sets container_image, and Loop builds that\n" +
		"# image from this file. Add what the project's agents need below; Loop\n" +
		"# rebuilds the image whenever its own agent image changes.\n" +
		"FROM " + base + "\n"
}

// BuildMissing builds a project image that doesn't exist yet, so a project
// that has just set container_image runs on it instead of failing to create
// the container; RebuildStale only runs after a base image build. dirs are
// the dirs to look for .loop/container/Dockerfile in, first match wins: a
// worktree's own, then its parent project's. When none of them has a
// Dockerfile, one FROM the base image is written to the last of dirs, the
// project's. It does nothing when the image exists, isn't a project image,
// autobuild is off, the base image is missing (the container create then
// fails with its own error), or a Dockerfile exists but isn't FROM the base.
// onBuild is called before the build starts.
func (m *ChildImageManager) BuildMissing(ctx context.Context, image string, autobuild bool, dirs []string, onBuild func()) error {
	if image == "" || m.baseImage == "" || imageRefEquals(image, m.baseImage) || !autobuild {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	// Checked under the lock, so a run that waited for another run's build
	// of the same image doesn't build it again.
	if ids, err := m.client.ImageList(ctx, image); err != nil || len(ids) > 0 {
		return nil
	}
	parentIDs, err := m.client.ImageList(ctx, m.baseImage)
	if err != nil || len(parentIDs) == 0 {
		return nil
	}

	contextDir, projectDir, anyDockerfile := "", "", false
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		projectDir = dir
		c, err := m.childContext(image, dir)
		if err == nil {
			contextDir = c
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			anyDockerfile = true
		}
	}
	if contextDir == "" {
		if anyDockerfile || projectDir == "" {
			return nil
		}
		dockerfile := filepath.Join(projectDir, childDockerfileRelPath)
		if err := m.writeFile(dockerfile, []byte(defaultDockerfile(m.baseImage))); err != nil {
			return fmt.Errorf("writing %s: %w", dockerfile, err)
		}
		m.logger.Info("child images: wrote a Dockerfile FROM the base image", "image", image, "path", dockerfile)
		contextDir = filepath.Dir(dockerfile)
	}

	onBuild()
	end := m.beginBuild(image)
	defer end()
	m.logger.Info("child images: building missing image", "image", image, "base", m.baseImage, "parent_id", parentIDs[0])
	if err := m.client.ImageBuildFileLabels(ctx, contextDir, "Dockerfile", image, map[string]string{ParentIDLabel: parentIDs[0]}); err != nil {
		return fmt.Errorf("building image %s: %w", image, err)
	}
	m.logger.Info("child images: built", "image", image)
	return nil
}
