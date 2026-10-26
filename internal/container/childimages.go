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
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/radutopala/loop/internal/buildctx"
	"github.com/radutopala/loop/internal/osutil"
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

	// writeFile writes a file under root, creating its dir. Injectable for
	// tests.
	writeFile func(root, path string, data []byte) error

	// trust is what the owner trusts of each project's build context; nil
	// trusts none, so only Loop's own default Dockerfile builds.
	trust BuildTrust

	// snapshot copies the build context in contextDir, under the project
	// dir root, to a dir to build from, and fingerprints the copy. cleanup
	// removes the copy. Injectable for tests.
	snapshot func(root, contextDir string) (dst string, files buildctx.Files, cleanup func(), err error)

	cascadeMu sync.Mutex // serializes cascades

	// imagesMu guards images, which holds a lock per project image: a
	// build of it (the cascade's or BuildMissing's) holds it, so one image
	// is built once at a time, while builds of other images, and runs on
	// them, go on.
	imagesMu sync.Mutex
	images   map[string]*sync.Mutex
}

// lockImage locks image's build lock, returning its unlock.
func (m *ChildImageManager) lockImage(image string) (unlock func()) {
	m.imagesMu.Lock()
	if m.images == nil {
		m.images = map[string]*sync.Mutex{}
	}
	ref := normalizeImageRef(image)
	l, ok := m.images[ref]
	if !ok {
		l = &sync.Mutex{}
		m.images[ref] = l
	}
	m.imagesMu.Unlock()
	l.Lock()
	return l.Unlock
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
		snapshot:     snapshotContext,
	}
}

// BuildTrust is what the owner trusts of a project's build context,
// .loop/container. Satisfied by *config.TrustStore.
type BuildTrust interface {
	// TrustedBuild is the fingerprint of the build context of the project
	// in dir as the owner last trusted it.
	TrustedBuild(dir string) (buildctx.Files, error)
	// KeepBuild keeps the project in dir trusted across Loop writing the
	// file rel of its build context.
	KeepBuild(dir, rel string, data []byte, mode fs.FileMode) error
}

// SetBuildTrust makes project images build only from the build context the
// owner trusted: an agent can write .loop/container, and the gate and the
// docker proxy run from inside the image.
func (m *ChildImageManager) SetBuildTrust(t BuildTrust) {
	m.trust = t
}

// errUntrustedBuild refuses a build context that isn't the trusted one.
var errUntrustedBuild = errors.New("the project's .loop/container isn't the version you trusted; review it in Settings → Project")

// build builds image with labels from the build context in contextDir, of
// the project in dir. It builds from a copy, and only when the copy is just
// Loop's default Dockerfile or the context the owner trusted for one of
// trustDirs (a worktree's checkout of its project's trusted context is that
// context), so what was checked is what's built.
func (m *ChildImageManager) build(ctx context.Context, image, dir, contextDir string, trustDirs []string, labels map[string]string) error {
	src, files, cleanup, err := m.snapshot(dir, contextDir)
	if err != nil {
		return fmt.Errorf("copying the build context %s: %w", contextDir, err)
	}
	defer cleanup()
	if ok, err := m.trusted(files, trustDirs); err != nil {
		return err
	} else if !ok {
		return errUntrustedBuild
	}
	return m.client.ImageBuildFileLabels(ctx, src, "Dockerfile", image, labels)
}

// trusted reports whether a build context with files may build: it's just
// Loop's default Dockerfile, or the owner trusted it for one of dirs.
func (m *ChildImageManager) trusted(files buildctx.Files, dirs []string) (bool, error) {
	if maps.Equal(files, m.defaultBuild()) {
		return true, nil
	}
	if m.trust == nil {
		return false, nil
	}
	for _, dir := range dirs {
		trusted, err := m.trust.TrustedBuild(dir)
		if err != nil {
			return false, err
		}
		if maps.Equal(files, trusted) {
			return true, nil
		}
	}
	return false, nil
}

// defaultBuild is the fingerprint of a build context of just the default
// Dockerfile, which needs no trust: it adds nothing to Loop's image.
func (m *ChildImageManager) defaultBuild() buildctx.Files {
	return buildctx.Files{"Dockerfile": buildctx.FileDigest([]byte(defaultDockerfile(m.baseImage)), defaultDockerfileMode)}
}

// defaultDockerfileMode is the mode BuildMissing writes the default
// Dockerfile with.
const defaultDockerfileMode = 0o644

// snapshotContext copies the build context in contextDir to a new temp dir,
// without following a link out of the project dir root.
func snapshotContext(root, contextDir string) (string, buildctx.Files, func(), error) {
	r, err := osutil.OpenRootIn(root, contextDir)
	if err != nil {
		return "", nil, nil, err
	}
	defer r.Close()
	dst, err := os.MkdirTemp("", "loop-build-")
	if err != nil {
		return "", nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dst) }
	files, err := buildctx.Snapshot(r, dst)
	if err != nil {
		cleanup()
		return "", nil, nil, err
	}
	return dst, files, cleanup, nil
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
	m.cascadeMu.Lock()
	defer m.cascadeMu.Unlock()
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
		image, dir, contextDir string
		end                    func()
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
		stale = append(stale, staleChild{image: p.Image, dir: p.DirPath, contextDir: contextDir})
	}

	for i := range stale {
		stale[i].end = m.beginBuild(stale[i].image)
	}
	handoff()

	for _, c := range stale {
		m.rebuildChild(ctx, c.image, c.dir, c.contextDir, parentID)
		c.end()
	}
}

// rebuildChild rebuilds image on base parentID under its build lock,
// unless a build that held it first (BuildMissing's) already did.
func (m *ChildImageManager) rebuildChild(ctx context.Context, image, dir, contextDir, parentID string) {
	defer m.lockImage(image)()
	if labels, err := m.client.ImageInspectLabels(ctx, image); err == nil && labels != nil && labels[ParentIDLabel] == parentID {
		return
	}
	m.logger.Info("child images: rebuilding on new base",
		"image", image, "base", m.baseImage, "parent_id", parentID)
	if err := m.build(ctx, image, dir, contextDir, []string{dir}, map[string]string{ParentIDLabel: parentID}); err != nil {
		m.logger.Error("child images: rebuild failed", "image", image, "error", err)
		return
	}
	m.logger.Info("child images: rebuilt", "image", image)
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

// writeFileMkdir writes data to path, creating path's dir first. Both stay
// in root, the project dir: a symlink the agent planted under its .loop
// can't turn the write onto a host file.
func writeFileMkdir(root, path string, data []byte) error {
	if err := osutil.MkdirAllIn(root, filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return osutil.WriteFileIn(root, path, data, defaultDockerfileMode)
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
	defer m.lockImage(image)()

	// Checked under the image's lock, so a run that waited for another run's build
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
		data := []byte(defaultDockerfile(m.baseImage))
		if err := m.writeFile(projectDir, dockerfile, data); err != nil {
			return fmt.Errorf("writing %s: %w", dockerfile, err)
		}
		m.logger.Info("child images: wrote a Dockerfile FROM the base image", "image", image, "path", dockerfile)
		if m.trust != nil {
			if err := m.trust.KeepBuild(projectDir, filepath.Base(dockerfile), data, defaultDockerfileMode); err != nil {
				m.logger.Warn("child images: keeping the project trusted", "dir", projectDir, "error", err)
			}
		}
		contextDir = filepath.Dir(dockerfile)
	}

	onBuild()
	end := m.beginBuild(image)
	defer end()
	m.logger.Info("child images: building missing image", "image", image, "base", m.baseImage, "parent_id", parentIDs[0])
	trustDirs := slices.DeleteFunc(slices.Clone(dirs), func(d string) bool { return d == "" })
	if err := m.build(ctx, image, projectDir, contextDir, trustDirs, map[string]string{ParentIDLabel: parentIDs[0]}); err != nil {
		return fmt.Errorf("building image %s: %w", image, err)
	}
	m.logger.Info("child images: built", "image", image)
	return nil
}
