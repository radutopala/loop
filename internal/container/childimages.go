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

	// readFile is injectable for tests; nil → os.ReadFile.
	readFile func(string) ([]byte, error)

	mu sync.Mutex // serializes cascades
}

// NewChildImageManager creates a manager for the given base image.
func NewChildImageManager(client DockerClient, baseImage string, listProjects func(ctx context.Context) ([]ChildProject, error), logger *slog.Logger) *ChildImageManager {
	return &ChildImageManager{
		client:       client,
		baseImage:    baseImage,
		logger:       logger,
		listProjects: listProjects,
		readFile:     os.ReadFile,
	}
}

// RebuildStale rebuilds every eligible child image whose recorded parent ID
// differs from the current base image ID. Eligible means: the project
// overrides container_image, autobuild is not disabled, a Dockerfile exists
// at .loop/container/Dockerfile, and that Dockerfile has a stage FROM the
// base image. Failures are logged per child and never abort the cascade.
func (m *ChildImageManager) RebuildStale(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()

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

	seen := map[string]bool{}
	for _, p := range projects {
		if p.Image == "" || p.Image == m.baseImage || !p.Autobuild || seen[p.Image] {
			continue
		}
		seen[p.Image] = true

		dockerfile := filepath.Join(p.DirPath, childDockerfileRelPath)
		data, err := m.readFile(dockerfile)
		if err != nil {
			m.logger.Debug("child images: no Dockerfile, skipping", "image", p.Image, "path", dockerfile)
			continue
		}
		if !dockerfileFromBase(string(data), m.baseImage) {
			m.logger.Info("child images: Dockerfile not based on Loop's image, skipping",
				"image", p.Image, "path", dockerfile, "base", m.baseImage)
			continue
		}

		if labels, err := m.client.ImageInspectLabels(ctx, p.Image); err == nil && labels != nil && labels[ParentIDLabel] == parentID {
			continue // already built on the current base
		}

		m.logger.Info("child images: rebuilding on new base",
			"image", p.Image, "base", m.baseImage, "parent_id", parentID)
		contextDir := filepath.Dir(dockerfile)
		if err := m.client.ImageBuildFileLabels(ctx, contextDir, "Dockerfile", p.Image, map[string]string{ParentIDLabel: parentID}); err != nil {
			m.logger.Error("child images: rebuild failed", "image", p.Image, "error", err)
			continue
		}
		m.logger.Info("child images: rebuilt", "image", p.Image)
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
	norm := func(r string) string {
		if !strings.Contains(r, ":") {
			return r + ":latest"
		}
		return r
	}
	return norm(a) == norm(b)
}
