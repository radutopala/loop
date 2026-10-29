// Package install installs plugins: it pulls a plugin's image, pins it to
// its digest, extracts and validates the image's /loop-plugin dir, and
// records the plugin once the owner approves its capabilities.
package install

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/distribution/reference"
	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin"
)

// DockerClient is the part of the Docker API the Installer uses.
type DockerClient interface {
	ImagePull(ctx context.Context, refStr string, options image.PullOptions) (io.ReadCloser, error)
	ImageInspect(ctx context.Context, imageID string, inspectOpts ...client.ImageInspectOption) (image.InspectResponse, error)
	ContainerCreate(ctx context.Context, config *containertypes.Config, hostConfig *containertypes.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (containertypes.CreateResponse, error)
	CopyFromContainer(ctx context.Context, containerID, srcPath string) (io.ReadCloser, containertypes.PathStat, error)
	ContainerRemove(ctx context.Context, container string, options containertypes.RemoveOptions) error
}

// Store records installed plugins.
type Store interface {
	GetPluginState(ctx context.Context, pluginID string) (*db.PluginState, error)
	UpsertPluginState(ctx context.Context, st *db.PluginState) error
	DeletePluginState(ctx context.Context, pluginID string) error
}

const (
	// pluginDir is the dir of a plugin image Loop extracts.
	pluginDir = "/loop-plugin"
	// dataDir must exist in a plugin image, owned by its USER.
	dataDir = "/data"
	// maxExtract caps the size of an extracted /loop-plugin dir.
	maxExtract = 50 << 20
)

// Candidate is a plugin image pulled, extracted and validated, waiting for
// the owner to approve its capabilities.
type Candidate struct {
	Manifest *plugin.Manifest
	// Image is the image to run: the pulled repo pinned to its digest, or
	// for a dev install the local image's id.
	Image string
	// Digest is the image's repo digest; empty for a dev install.
	Digest string
	Dev    bool
	// Dir is where the image's /loop-plugin dir was extracted.
	Dir string
	// Previous is the plugin's state when it is already installed, so an
	// upgrade can show what changed.
	Previous *db.PluginState
}

// NewCapabilities returns the capabilities the candidate asks for that the
// owner hasn't approved before.
func (c *Candidate) NewCapabilities() []plugin.Capability {
	var out []plugin.Capability
	for _, cap := range c.Manifest.Capabilities {
		if c.Previous == nil || !slices.Contains(c.Previous.ApprovedCaps, string(cap)) {
			out = append(out, cap)
		}
	}
	return out
}

// Installer installs and removes plugins.
type Installer struct {
	docker DockerClient
	store  Store
	// root is the dir plugins keep host files in, ~/.loop/plugins.
	root string
	// loopVersion is this Loop's release, checked against a manifest's loop
	// constraint; a dev build that isn't a version skips the check.
	loopVersion string
	// removeAll deletes a plugin's files on Remove.
	removeAll func(path string) error
}

// NewInstaller returns an Installer keeping extracted plugins under root.
func NewInstaller(docker DockerClient, store Store, root, loopVersion string) *Installer {
	return &Installer{docker: docker, store: store, root: root, loopVersion: loopVersion, removeAll: os.RemoveAll}
}

// Inspect pulls ref (unless dev, which uses a local image as is), extracts
// its /loop-plugin dir and validates it, without installing anything.
func (in *Installer) Inspect(ctx context.Context, ref string, dev bool) (*Candidate, error) {
	if !dev {
		if err := plugin.PullImage(ctx, in.docker, ref); err != nil {
			return nil, err
		}
	}
	info, err := in.docker.ImageInspect(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("inspecting the plugin image: %w", err)
	}
	cfg := info.Config
	if cfg == nil || cfg.Labels[plugin.ImageLabel] != "1" {
		return nil, fmt.Errorf("image %s is not a Loop plugin: it has no %s=1 label", ref, plugin.ImageLabel)
	}
	uid, err := imageUID(cfg.User)
	if err != nil {
		return nil, err
	}
	c := &Candidate{Image: info.ID, Dev: dev}
	if !dev {
		if c.Image, c.Digest, err = pinned(ref, info.RepoDigests); err != nil {
			return nil, err
		}
	}

	created, err := in.docker.ContainerCreate(ctx, &containertypes.Config{Image: info.ID, Cmd: []string{"true"}}, nil, nil, nil, "")
	if err != nil {
		return nil, fmt.Errorf("creating a container to extract the plugin: %w", err)
	}
	defer func() {
		_ = in.docker.ContainerRemove(context.WithoutCancel(ctx), created.ID, containertypes.RemoveOptions{Force: true})
	}()

	if err := in.checkData(ctx, created.ID, uid); err != nil {
		return nil, err
	}
	staging, err := in.extract(ctx, created.ID)
	if err != nil {
		return nil, err
	}
	if c.Manifest, err = in.validate(staging); err != nil {
		_ = os.RemoveAll(staging)
		return nil, err
	}
	if c.Dir, err = in.place(staging, c.Manifest.ID, c.Image); err != nil {
		return nil, err
	}
	if c.Previous, err = in.store.GetPluginState(ctx, c.Manifest.ID); err != nil {
		return nil, fmt.Errorf("reading the installed plugin: %w", err)
	}
	return c, nil
}

// pinned returns ref's repo pinned to the digest it was pulled at.
func pinned(ref string, repoDigests []string) (string, string, error) {
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return "", "", fmt.Errorf("parsing image %q: %w", ref, err)
	}
	for _, rd := range repoDigests {
		d, err := reference.ParseNormalizedNamed(rd)
		if err != nil {
			continue
		}
		if canonical, ok := d.(reference.Canonical); ok && d.Name() == named.Name() {
			return canonical.String(), canonical.Digest().String(), nil
		}
	}
	return "", "", fmt.Errorf("image %s has no digest for %s; install a pushed image, or use dev for a local one", ref, named.Name())
}

// imageUID returns the numeric, non-root uid an image's USER names.
func imageUID(user string) (int, error) {
	name, _, _ := strings.Cut(user, ":")
	uid, err := strconv.Atoi(name)
	if err != nil || uid <= 0 {
		return 0, fmt.Errorf("a plugin image must run as a numeric, non-root USER, not %q", user)
	}
	return uid, nil
}

// checkData checks the image has a /data dir owned by uid, where the
// plugin's host data dir is mounted.
func (in *Installer) checkData(ctx context.Context, containerID string, uid int) error {
	body, _, err := in.docker.CopyFromContainer(ctx, containerID, dataDir)
	if err != nil {
		return fmt.Errorf("a plugin image needs a %s dir owned by its USER: %w", dataDir, err)
	}
	defer body.Close()
	hdr, err := tar.NewReader(body).Next()
	if err != nil {
		return fmt.Errorf("reading the image's %s dir: %w", dataDir, err)
	}
	if hdr.Typeflag != tar.TypeDir || hdr.Uid != uid {
		return fmt.Errorf("the image's %s must be a dir owned by uid %d", dataDir, uid)
	}
	return nil
}

// extract copies the container's /loop-plugin dir into a new staging dir
// under root. Only plain files and dirs are taken: a link could point
// anywhere on the host once extracted.
func (in *Installer) extract(ctx context.Context, containerID string) (string, error) {
	body, _, err := in.docker.CopyFromContainer(ctx, containerID, pluginDir)
	if err != nil {
		return "", fmt.Errorf("copying %s out of the image: %w", pluginDir, err)
	}
	defer body.Close()
	staging, err := "", os.MkdirAll(in.root, 0o755)
	if err == nil {
		staging, err = os.MkdirTemp(in.root, ".extract-")
	}
	if err != nil {
		return "", fmt.Errorf("creating the plugins dir: %w", err)
	}
	if err := untar(body, staging); err != nil {
		_ = os.RemoveAll(staging)
		return "", fmt.Errorf("extracting %s: %w", pluginDir, err)
	}
	return staging, nil
}

// untar writes the tar stream r, whose entries sit under one top dir (the
// one copied), into dir.
func untar(r io.Reader, dir string) error {
	tr := tar.NewReader(r)
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		_, rel, _ := strings.Cut(hdr.Name, "/")
		if strings.Contains(rel, "..") {
			return fmt.Errorf("%s leaves %s", hdr.Name, pluginDir)
		}
		rel = path.Clean("/" + rel)[1:]
		if rel == "" {
			continue
		}
		target := filepath.Join(dir, filepath.FromSlash(rel))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if total += hdr.Size; total > maxExtract {
				return fmt.Errorf("it is over %d MB", maxExtract>>20)
			}
			if err := writeFile(target, tr); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s is not a plain file or dir", rel)
		}
	}
}

func writeFile(target string, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	return errors.Join(err, f.Close())
}

// validate parses the extracted manifest and checks what it names is there
// and that this Loop meets its loop constraint.
func (in *Installer) validate(dir string) (*plugin.Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, path.Base(plugin.ManifestPath)))
	if err != nil {
		return nil, fmt.Errorf("a plugin image needs %s: %w", plugin.ManifestPath, err)
	}
	m, err := plugin.ParseManifest(data)
	if err != nil {
		return nil, err
	}
	if m.UI != nil {
		for _, p := range m.UI.Panels {
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p.Entry))); err != nil {
				return nil, fmt.Errorf("panel %q: entry %s is not in the image", p.ID, p.Entry)
			}
		}
		for _, c := range m.UI.Cards {
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(c.Entry))); err != nil {
				return nil, fmt.Errorf("card %q: entry %s is not in the image", c.ID, c.Entry)
			}
		}
		for _, sw := range m.UI.ComposerSwitches {
			fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(sw.Icon)))
			if err != nil {
				return nil, fmt.Errorf("composer switch %q: icon %s is not in the image", sw.ID, sw.Icon)
			}
			if fi.Size() > plugin.ComposerSwitchIconMax {
				return nil, fmt.Errorf("composer switch %q: icon %s is over %d KB", sw.ID, sw.Icon, plugin.ComposerSwitchIconMax>>10)
			}
		}
	}
	if m.Loop != "" {
		v, err := semver.NewVersion(in.loopVersion)
		// ParseManifest checked the constraint parses.
		if c, _ := semver.NewConstraint(m.Loop); err == nil && !c.Check(v) {
			return nil, fmt.Errorf("plugin %s needs Loop %s, this is %s", m.ID, m.Loop, in.loopVersion)
		}
	}
	return m, nil
}

// place moves a staging dir to the plugin's dir for image, replacing an
// earlier extraction of the same image.
func (in *Installer) place(staging, pluginID, image string) (string, error) {
	dir := in.imageDir(pluginID, image)
	err := os.MkdirAll(filepath.Dir(dir), 0o755)
	if err == nil {
		err = os.RemoveAll(dir)
	}
	if err == nil {
		err = os.Rename(staging, dir)
	}
	if err != nil {
		_ = os.RemoveAll(staging)
		return "", fmt.Errorf("placing the extracted plugin: %w", err)
	}
	return dir, nil
}

// imageDir is where an image of a plugin is extracted, named by the hex of
// the image it runs (a dev install's image id, or the pinned repo's digest)
// so an upgrade doesn't overwrite the dir a running plugin serves from.
func (in *Installer) imageDir(pluginID, image string) string {
	hex := image[strings.LastIndex(image, ":")+1:]
	return filepath.Join(in.root, pluginID, "images", hex)
}

// Dir is where the image st runs was extracted at install: its
// /loop-plugin dir.
func (in *Installer) Dir(st *db.PluginState) string {
	return in.imageDir(st.PluginID, st.Image)
}

// Manifest reads the manifest of the image st runs, as extracted at install.
func (in *Installer) Manifest(st *db.PluginState) (*plugin.Manifest, error) {
	data, err := os.ReadFile(filepath.Join(in.Dir(st), path.Base(plugin.ManifestPath)))
	if err != nil {
		return nil, fmt.Errorf("reading the plugin's manifest: %w", err)
	}
	return plugin.ParseManifest(data)
}

// Install records c as installed with the capabilities the owner approved,
// which must be every one it asks for, and drops the plugin's other
// extracted images.
func (in *Installer) Install(ctx context.Context, c *Candidate, approved []plugin.Capability) error {
	var caps []string
	for _, cap := range c.Manifest.Capabilities {
		if !slices.Contains(approved, cap) {
			return fmt.Errorf("%w: %s", ErrNotApproved, cap)
		}
		caps = append(caps, string(cap))
	}
	if err := in.store.UpsertPluginState(ctx, &db.PluginState{
		PluginID: c.Manifest.ID, Image: c.Image, Digest: c.Digest, Version: c.Manifest.Version,
		API: c.Manifest.API, ApprovedCaps: caps, Dev: c.Dev, Status: db.PluginStatusInstalled,
	}); err != nil {
		return fmt.Errorf("recording the plugin: %w", err)
	}
	images := filepath.Join(in.root, c.Manifest.ID, "images")
	entries, _ := os.ReadDir(images)
	for _, e := range entries {
		if p := filepath.Join(images, e.Name()); p != c.Dir {
			_ = os.RemoveAll(p)
		}
	}
	return nil
}

// Discard drops the extraction of a candidate the owner didn't approve,
// unless it is the image the plugin already runs.
func (in *Installer) Discard(c *Candidate) {
	if c.Previous == nil || c.Previous.Image != c.Image {
		_ = os.RemoveAll(c.Dir)
	}
}

// Remove forgets an installed plugin and deletes its dir, keeping its data
// and gate audit dirs unless purge is set. Its containers are the runtime's
// to remove, first.
func (in *Installer) Remove(ctx context.Context, pluginID string, purge bool) error {
	if err := in.store.DeletePluginState(ctx, pluginID); err != nil {
		return fmt.Errorf("deleting the plugin: %w", err)
	}
	dir := filepath.Join(in.root, pluginID)
	if purge {
		if err := in.removeAll(dir); err != nil {
			return fmt.Errorf("deleting the plugin dir: %w", err)
		}
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("reading the plugin dir: %w", err)
	}
	for _, e := range entries {
		if e.Name() == "data" || e.Name() == "audit" {
			continue
		}
		if err := in.removeAll(filepath.Join(dir, e.Name())); err != nil {
			return fmt.Errorf("deleting the extracted plugin: %w", err)
		}
	}
	return nil
}
