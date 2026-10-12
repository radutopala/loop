package install

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin"
)

type mockDocker struct {
	mock.Mock
}

func (m *mockDocker) ImagePull(ctx context.Context, refStr string, options image.PullOptions) (io.ReadCloser, error) {
	args := m.Called(ctx, refStr, options)
	v, _ := args.Get(0).(io.ReadCloser)
	return v, args.Error(1)
}

func (m *mockDocker) ImageInspect(ctx context.Context, imageID string, _ ...client.ImageInspectOption) (image.InspectResponse, error) {
	args := m.Called(ctx, imageID)
	return args.Get(0).(image.InspectResponse), args.Error(1)
}

func (m *mockDocker) ContainerCreate(ctx context.Context, config *containertypes.Config, hostConfig *containertypes.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (containertypes.CreateResponse, error) {
	args := m.Called(ctx, config, hostConfig, networkingConfig, platform, containerName)
	return args.Get(0).(containertypes.CreateResponse), args.Error(1)
}

func (m *mockDocker) CopyFromContainer(ctx context.Context, containerID, srcPath string) (io.ReadCloser, containertypes.PathStat, error) {
	args := m.Called(ctx, containerID, srcPath)
	v, _ := args.Get(0).(io.ReadCloser)
	return v, containertypes.PathStat{}, args.Error(1)
}

func (m *mockDocker) ContainerRemove(ctx context.Context, id string, options containertypes.RemoveOptions) error {
	return m.Called(ctx, id, options).Error(0)
}

type mockStore struct {
	mock.Mock
}

func (m *mockStore) GetPluginState(ctx context.Context, pluginID string) (*db.PluginState, error) {
	args := m.Called(ctx, pluginID)
	v, _ := args.Get(0).(*db.PluginState)
	return v, args.Error(1)
}

func (m *mockStore) UpsertPluginState(ctx context.Context, st *db.PluginState) error {
	return m.Called(ctx, st).Error(0)
}

func (m *mockStore) DeletePluginState(ctx context.Context, pluginID string) error {
	return m.Called(ctx, pluginID).Error(0)
}

// entry is a tar entry: a dir when body is nil and link is empty.
type entry struct {
	name string
	body *string
	link string
	uid  int
	// size, when set, overrides the size the header claims.
	size int64
}

func str(s string) *string { return &s }

// tarOf builds a tar stream of entries. A claimed size larger than the body
// leaves the stream truncated.
func tarOf(entries ...entry) io.ReadCloser {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Uid: e.uid, Mode: 0o755, Typeflag: tar.TypeDir}
		switch {
		case e.link != "":
			hdr.Typeflag, hdr.Linkname = tar.TypeSymlink, e.link
		case e.body != nil:
			hdr.Typeflag, hdr.Mode, hdr.Size = tar.TypeReg, 0o644, int64(len(*e.body))
			if e.size != 0 {
				hdr.Size = e.size
			}
		}
		if err := tw.WriteHeader(hdr); err != nil {
			panic(err)
		}
		if e.body != nil {
			_, _ = tw.Write([]byte(*e.body))
		}
		if e.size != 0 {
			return io.NopCloser(&buf) // truncated: no padding, no trailer
		}
	}
	_ = tw.Close()
	return io.NopCloser(&buf)
}

const (
	ref     = "ghcr.io/acme/echo:1.0.0"
	digest  = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	imageID = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

const manifest = `{
	// comments are fine
	"id": "echo", "name": "Echo", "version": "1.0.0", "api": "1",
	"capabilities": ["ui.panel", "files.read"],
	"ui": {"panels": [{"id": "main", "label": "Echo", "entry": "ui/main/index.html"}]},
}`

// base is the fixture the installer and service suites share.
type base struct {
	suite.Suite
	ctx    context.Context
	docker *mockDocker
	store  *mockStore
	root   string
	in     *Installer
}

type InstallerSuite struct {
	base
}

func TestInstallerSuite(t *testing.T) {
	suite.Run(t, new(InstallerSuite))
}

func (s *base) SetupTest() {
	s.ctx = context.Background()
	s.docker = new(mockDocker)
	s.store = new(mockStore)
	s.root = filepath.Join(s.T().TempDir(), "plugins")
	s.in = NewInstaller(s.docker, s.store, s.root, "v2026.10.3")
}

func (s *base) TearDownTest() {
	s.docker.AssertExpectations(s.T())
	s.store.AssertExpectations(s.T())
}

func (s *base) info() image.InspectResponse {
	return image.InspectResponse{
		ID:          imageID,
		RepoDigests: []string{"not a ref!", "ghcr.io/acme/other@" + digest, "ghcr.io/acme/echo@" + digest},
		Config:      &dockerspec.DockerOCIImageConfig{ImageConfig: ocispec.ImageConfig{User: "1000:1000", Labels: map[string]string{plugin.ImageLabel: "1"}}},
	}
}

func pluginTar(m string) io.ReadCloser {
	return tarOf(
		entry{name: "loop-plugin/"},
		entry{name: "loop-plugin/plugin.json", body: str(m)},
		entry{name: "loop-plugin/ui/main/index.html", body: str("<html>")},
	)
}

func (s *base) expectPull() {
	s.docker.On("ImagePull", s.ctx, ref, image.PullOptions{}).Return(io.NopCloser(strings.NewReader(`{"status":"Pulling"}{"status":"Done"}`)), nil).Once()
}

func (s *base) expectCreated() {
	s.docker.On("ContainerCreate", s.ctx, &containertypes.Config{Image: imageID, Cmd: []string{"true"}}, (*containertypes.HostConfig)(nil), (*network.NetworkingConfig)(nil), (*ocispec.Platform)(nil), "").
		Return(containertypes.CreateResponse{ID: "x1"}, nil).Once()
	s.docker.On("ContainerRemove", mock.Anything, "x1", containertypes.RemoveOptions{Force: true}).Return(nil).Once()
}

func (s *base) expectData() {
	s.docker.On("CopyFromContainer", s.ctx, "x1", "/data").Return(tarOf(entry{name: "data/", uid: 1000}), nil).Once()
}

func (s *InstallerSuite) TestInspect() {
	s.expectPull()
	s.docker.On("ImageInspect", s.ctx, ref).Return(s.info(), nil).Once()
	s.expectCreated()
	s.expectData()
	s.docker.On("CopyFromContainer", s.ctx, "x1", "/loop-plugin").Return(pluginTar(manifest), nil).Once()
	prev := &db.PluginState{PluginID: "echo", ApprovedCaps: []string{"ui.panel"}}
	s.store.On("GetPluginState", s.ctx, "echo").Return(prev, nil).Once()

	c, err := s.in.Inspect(s.ctx, ref, false)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ghcr.io/acme/echo@"+digest, c.Image)
	require.Equal(s.T(), digest, c.Digest)
	require.False(s.T(), c.Dev)
	require.Equal(s.T(), "echo", c.Manifest.ID)
	require.Equal(s.T(), prev, c.Previous)
	require.Equal(s.T(), filepath.Join(s.root, "echo", "images", strings.TrimPrefix(digest, "sha256:")), c.Dir)
	require.FileExists(s.T(), filepath.Join(c.Dir, "plugin.json"))
	require.FileExists(s.T(), filepath.Join(c.Dir, "ui", "main", "index.html"))
	require.Equal(s.T(), []plugin.Capability{plugin.CapFilesRead}, c.NewCapabilities())
	entries, err := os.ReadDir(s.root)
	require.NoError(s.T(), err)
	require.Len(s.T(), entries, 1, "no staging dir is left behind")
}

func (s *InstallerSuite) TestInspectDev() {
	info := s.info()
	info.RepoDigests = nil
	s.docker.On("ImageInspect", s.ctx, "echo:dev").Return(info, nil).Once()
	s.expectCreated()
	s.expectData()
	s.docker.On("CopyFromContainer", s.ctx, "x1", "/loop-plugin").Return(pluginTar(manifest), nil).Once()
	s.store.On("GetPluginState", s.ctx, "echo").Return(nil, nil).Once()

	c, err := s.in.Inspect(s.ctx, "echo:dev", true)
	require.NoError(s.T(), err)
	require.Equal(s.T(), imageID, c.Image)
	require.Empty(s.T(), c.Digest)
	require.True(s.T(), c.Dev)
	require.Equal(s.T(), []plugin.Capability{plugin.CapUIPanel, plugin.CapFilesRead}, c.NewCapabilities())

	// Extracting the same image again replaces the earlier extraction.
	s.docker.On("ImageInspect", s.ctx, "echo:dev").Return(info, nil).Once()
	s.expectCreated()
	s.expectData()
	s.docker.On("CopyFromContainer", s.ctx, "x1", "/loop-plugin").Return(pluginTar(manifest), nil).Once()
	s.store.On("GetPluginState", s.ctx, "echo").Return(nil, nil).Once()
	again, err := s.in.Inspect(s.ctx, "echo:dev", true)
	require.NoError(s.T(), err)
	require.Equal(s.T(), c.Dir, again.Dir)
}

func (s *InstallerSuite) TestInspectFailures() {
	boom := errors.New("boom")
	pulled := func() { s.expectPull() }
	inspected := func(mod func(*image.InspectResponse)) func() {
		return func() {
			pulled()
			info := s.info()
			if mod != nil {
				mod(&info)
			}
			s.docker.On("ImageInspect", s.ctx, ref).Return(info, nil).Once()
		}
	}
	created := func() {
		inspected(nil)()
		s.expectCreated()
	}
	withData := func() {
		created()
		s.expectData()
	}
	extracting := func(tr io.ReadCloser) func() {
		return func() {
			withData()
			s.docker.On("CopyFromContainer", s.ctx, "x1", "/loop-plugin").Return(tr, nil).Once()
		}
	}
	tests := []struct {
		name    string
		setup   func()
		wantErr string
	}{
		{"pull", func() {
			s.docker.On("ImagePull", s.ctx, ref, image.PullOptions{}).Return(nil, boom).Once()
		}, "pulling ghcr.io/acme/echo:1.0.0: boom"},
		{"pull reports an error", func() {
			s.docker.On("ImagePull", s.ctx, ref, image.PullOptions{}).Return(io.NopCloser(strings.NewReader(`{"status":"x"}{"error":"denied"}`)), nil).Once()
		}, "pulling ghcr.io/acme/echo:1.0.0: denied"},
		{"pull stream garbled", func() {
			s.docker.On("ImagePull", s.ctx, ref, image.PullOptions{}).Return(io.NopCloser(strings.NewReader(`{"status"`)), nil).Once()
		}, "pulling ghcr.io/acme/echo:1.0.0: unexpected EOF"},
		{"inspect", func() {
			pulled()
			s.docker.On("ImageInspect", s.ctx, ref).Return(image.InspectResponse{}, boom).Once()
		}, "inspecting the plugin image: boom"},
		{"no config", inspected(func(i *image.InspectResponse) { i.Config = nil }), "is not a Loop plugin"},
		{"no label", inspected(func(i *image.InspectResponse) { i.Config.Labels = nil }), "is not a Loop plugin"},
		{"root user", inspected(func(i *image.InspectResponse) { i.Config.User = "0" }), `numeric, non-root USER, not "0"`},
		{"no user", inspected(func(i *image.InspectResponse) { i.Config.User = "" }), `non-root USER, not ""`},
		{"named user", inspected(func(i *image.InspectResponse) { i.Config.User = "app" }), `non-root USER, not "app"`},
		{"no digest", inspected(func(i *image.InspectResponse) { i.RepoDigests = i.RepoDigests[:2] }), "has no digest for ghcr.io/acme/echo"},
		{"create", func() {
			inspected(nil)()
			s.docker.On("ContainerCreate", s.ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything, "").Return(containertypes.CreateResponse{}, boom).Once()
		}, "creating a container to extract the plugin: boom"},
		{"no data dir", func() {
			created()
			s.docker.On("CopyFromContainer", s.ctx, "x1", "/data").Return(nil, boom).Once()
		}, "needs a /data dir owned by its USER: boom"},
		{"empty data tar", func() {
			created()
			s.docker.On("CopyFromContainer", s.ctx, "x1", "/data").Return(io.NopCloser(strings.NewReader("")), nil).Once()
		}, "reading the image's /data dir: EOF"},
		{"data owned by root", func() {
			created()
			s.docker.On("CopyFromContainer", s.ctx, "x1", "/data").Return(tarOf(entry{name: "data/"}), nil).Once()
		}, "/data must be a dir owned by uid 1000"},
		{"data is a file", func() {
			created()
			s.docker.On("CopyFromContainer", s.ctx, "x1", "/data").Return(tarOf(entry{name: "data", body: str(""), uid: 1000}), nil).Once()
		}, "/data must be a dir owned by uid 1000"},
		{"no plugin dir", func() {
			withData()
			s.docker.On("CopyFromContainer", s.ctx, "x1", "/loop-plugin").Return(nil, boom).Once()
		}, "copying /loop-plugin out of the image: boom"},
		{"root is a file", func() {
			withData()
			s.docker.On("CopyFromContainer", s.ctx, "x1", "/loop-plugin").Return(pluginTar(manifest), nil).Once()
			require.NoError(s.T(), os.WriteFile(s.root, nil, 0o644))
		}, "creating the plugins dir"},
		{"garbled tar", extracting(io.NopCloser(strings.NewReader(strings.Repeat("x", 1024)))), "extracting /loop-plugin"},
		{"symlink", extracting(tarOf(entry{name: "loop-plugin/plugin.json", link: "/etc/passwd"})), "plugin.json is not a plain file or dir"},
		{"too big", extracting(tarOf(entry{name: "loop-plugin/big", body: str(""), size: maxExtract + 1})), "it is over 50 MB"},
		{"truncated file", extracting(tarOf(entry{name: "loop-plugin/plugin.json", body: str("{"), size: 10})), "unexpected EOF"},
		{"dir over a file", extracting(tarOf(entry{name: "loop-plugin/a", body: str("")}, entry{name: "loop-plugin/a/"})), "extracting /loop-plugin"},
		{"file under a file", extracting(tarOf(entry{name: "loop-plugin/a", body: str("")}, entry{name: "loop-plugin/a/b", body: str("")})), "extracting /loop-plugin"},
		{"file over a dir", extracting(tarOf(entry{name: "loop-plugin/a/"}, entry{name: "loop-plugin/a", body: str("")})), "extracting /loop-plugin"},
		{"no manifest", extracting(tarOf(entry{name: "loop-plugin/"})), "needs /loop-plugin/plugin.json"},
		{"bad manifest", extracting(pluginTar(`{"id": "X"}`)), "invalid plugin manifest"},
		{"missing panel entry", extracting(tarOf(entry{name: "loop-plugin/plugin.json", body: str(manifest)})), `panel "main": entry ui/main/index.html is not in the image`},
		{"missing card entry", extracting(pluginTar(strings.NewReplacer(`"ui.panel",`, `"ui.panel", "ui.notification.card",`, `"panels"`, `"cards": [{"id": "score", "entry": "ui/score.html"}], "panels"`).Replace(manifest))), `card "score": entry ui/score.html is not in the image`},
		{"needs a newer loop", extracting(pluginTar(strings.Replace(manifest, `"api": "1",`, `"api": "1", "loop": ">=2027.1.0",`, 1))), "plugin echo needs Loop >=2027.1.0, this is v2026.10.3"},
		{"place", func() {
			extracting(pluginTar(manifest))()
			require.NoError(s.T(), os.MkdirAll(s.root, 0o755))
			require.NoError(s.T(), os.WriteFile(filepath.Join(s.root, "echo"), nil, 0o644))
		}, "placing the extracted plugin"},
		{"read state", func() {
			extracting(pluginTar(manifest))()
			s.store.On("GetPluginState", s.ctx, "echo").Return(nil, boom).Once()
		}, "reading the installed plugin: boom"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			tc.setup()
			_, err := s.in.Inspect(s.ctx, ref, false)
			require.ErrorContains(s.T(), err, tc.wantErr)
			entries, _ := os.ReadDir(s.root)
			for _, e := range entries {
				require.False(s.T(), strings.HasPrefix(e.Name(), ".extract-"), "staging dir %s left behind", e.Name())
			}
			s.TearDownTest()
		})
	}
}

func (s *InstallerSuite) TestPinnedBadRef() {
	_, _, err := pinned("Not A Ref", nil)
	require.ErrorContains(s.T(), err, `parsing image "Not A Ref"`)
}

func (s *InstallerSuite) TestUntarSkipsTraversal() {
	dir := s.T().TempDir()
	require.NoError(s.T(), untar(tarOf(entry{name: "loop-plugin/../../evil", body: str("x")}), dir))
	require.FileExists(s.T(), filepath.Join(dir, "evil"))
}

func (s *InstallerSuite) TestValidateLoopVersion() {
	m := strings.Replace(manifest, `"api": "1",`, `"api": "1", "loop": ">=2026.10.0",`, 1)
	for _, version := range []string{"v2026.10.3", "dev"} {
		dir := s.T().TempDir()
		require.NoError(s.T(), untar(pluginTar(m), dir))
		s.in.loopVersion = version
		_, err := s.in.validate(dir)
		require.NoError(s.T(), err, version)
	}
}

// candidate returns an extracted candidate of the echo plugin.
func (s *base) candidate(hex string) *Candidate {
	dir := filepath.Join(s.root, "echo", "images", hex)
	require.NoError(s.T(), os.MkdirAll(dir, 0o755))
	m, err := plugin.ParseManifest([]byte(manifest))
	require.NoError(s.T(), err)
	return &Candidate{Manifest: m, Image: "ghcr.io/acme/echo@" + digest, Digest: digest, Dir: dir}
}

func (s *InstallerSuite) TestInstall() {
	old := s.candidate("old")
	c := s.candidate("new")
	s.store.On("UpsertPluginState", s.ctx, &db.PluginState{
		PluginID: "echo", Image: c.Image, Digest: digest, Version: "1.0.0", API: "1",
		ApprovedCaps: []string{"ui.panel", "files.read"}, Status: db.PluginStatusInstalled,
	}).Return(nil).Once()
	require.NoError(s.T(), s.in.Install(s.ctx, c, []plugin.Capability{plugin.CapFilesRead, plugin.CapUIPanel}))
	require.DirExists(s.T(), c.Dir)
	require.NoDirExists(s.T(), old.Dir, "an upgrade drops the old extraction")
}

func (s *InstallerSuite) TestInstallFailures() {
	c := s.candidate("new")
	require.EqualError(s.T(), s.in.Install(s.ctx, c, []plugin.Capability{plugin.CapUIPanel}), "capability not approved: files.read")

	s.store.On("UpsertPluginState", s.ctx, mock.Anything).Return(errors.New("boom")).Once()
	require.EqualError(s.T(), s.in.Install(s.ctx, c, plugin.Capabilities), "recording the plugin: boom")
}

func (s *InstallerSuite) TestDiscard() {
	c := s.candidate("new")
	c.Previous = &db.PluginState{Image: c.Image}
	s.in.Discard(c)
	require.DirExists(s.T(), c.Dir, "the image the plugin already runs stays")

	c.Previous.Image = "ghcr.io/acme/echo@sha256:old"
	s.in.Discard(c)
	require.NoDirExists(s.T(), c.Dir)

	c = s.candidate("new")
	s.in.Discard(c)
	require.NoDirExists(s.T(), c.Dir)
}

func (s *InstallerSuite) TestRemove() {
	c := s.candidate("new")
	dir := filepath.Join(s.root, "echo")
	for _, d := range []string{"data/ch1", "audit/ch1", "run/ch1"} {
		require.NoError(s.T(), os.MkdirAll(filepath.Join(dir, d), 0o755))
	}

	// Without purge, the plugin's data and audit log stay.
	s.store.On("DeletePluginState", s.ctx, "echo").Return(nil).Once()
	require.NoError(s.T(), s.in.Remove(s.ctx, "echo", false))
	require.NoDirExists(s.T(), c.Dir)
	require.NoDirExists(s.T(), filepath.Join(dir, "run"))
	require.DirExists(s.T(), filepath.Join(dir, "data", "ch1"))
	require.DirExists(s.T(), filepath.Join(dir, "audit", "ch1"))

	// With purge, they go too.
	s.store.On("DeletePluginState", s.ctx, "echo").Return(nil).Once()
	require.NoError(s.T(), s.in.Remove(s.ctx, "echo", true))
	require.NoDirExists(s.T(), dir)

	// A plugin that never extracted anything has nothing to delete.
	s.store.On("DeletePluginState", s.ctx, "echo").Return(nil).Once()
	require.NoError(s.T(), s.in.Remove(s.ctx, "echo", false))

	s.store.On("DeletePluginState", s.ctx, "echo").Return(errors.New("boom")).Once()
	require.EqualError(s.T(), s.in.Remove(s.ctx, "echo", false), "deleting the plugin: boom")
}

func (s *InstallerSuite) TestRemoveFails() {
	// An extracted file that can't be deleted stays.
	require.NoError(s.T(), os.MkdirAll(filepath.Join(s.root, "echo", "images"), 0o755))
	s.in.removeAll = func(string) error { return errors.New("busy") }
	s.store.On("DeletePluginState", s.ctx, "echo").Return(nil)
	require.EqualError(s.T(), s.in.Remove(s.ctx, "echo", false), "deleting the extracted plugin: busy")
	require.EqualError(s.T(), s.in.Remove(s.ctx, "echo", true), "deleting the plugin dir: busy")

	// A file where the plugins root should be can't be descended into.
	s.in.root = filepath.Join(s.T().TempDir(), "file")
	require.NoError(s.T(), os.WriteFile(s.in.root, nil, 0o644))
	require.ErrorContains(s.T(), s.in.Remove(s.ctx, "echo", false), "reading the plugin dir")
}

func (m *mockStore) ListPluginStates(ctx context.Context) ([]*db.PluginState, error) {
	args := m.Called(ctx)
	v, _ := args.Get(0).([]*db.PluginState)
	return v, args.Error(1)
}

func (m *mockStore) ListPluginContainers(ctx context.Context, pluginID string) ([]*db.PluginContainer, error) {
	args := m.Called(ctx, pluginID)
	v, _ := args.Get(0).([]*db.PluginContainer)
	return v, args.Error(1)
}

func (m *mockStore) ListChannels(ctx context.Context) ([]*db.Channel, error) {
	args := m.Called(ctx)
	v, _ := args.Get(0).([]*db.Channel)
	return v, args.Error(1)
}

func (m *mockStore) SeedPluginTask(ctx context.Context, task *db.ScheduledTask) (bool, error) {
	args := m.Called(ctx, task)
	return args.Bool(0), args.Error(1)
}

func (m *mockStore) SetPluginTasksPaused(ctx context.Context, pluginID, channelID string, paused bool) error {
	return m.Called(ctx, pluginID, channelID, paused).Error(0)
}

func (s *InstallerSuite) TestManifest() {
	hex := strings.TrimPrefix(digest, "sha256:")
	st := &db.PluginState{PluginID: "echo", Image: "ghcr.io/acme/echo@" + digest}
	_, err := s.in.Manifest(st)
	require.ErrorContains(s.T(), err, "reading the plugin's manifest")

	c := s.candidate(hex)
	require.NoError(s.T(), os.WriteFile(filepath.Join(c.Dir, "plugin.json"), []byte(manifest), 0o644))
	m, err := s.in.Manifest(st)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "echo", m.ID)

	// A dev install's image is its local id.
	dev := s.candidate(strings.TrimPrefix(imageID, "sha256:"))
	require.NoError(s.T(), os.WriteFile(filepath.Join(dev.Dir, "plugin.json"), []byte(manifest), 0o644))
	m, err = s.in.Manifest(&db.PluginState{PluginID: "echo", Image: imageID, Dev: true})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "1.0.0", m.Version)
}
