package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin"
)

type mockRuntime struct {
	mock.Mock
}

func (m *mockRuntime) StopPlugin(ctx context.Context, pluginID string) {
	m.Called(ctx, pluginID)
}

func (m *mockRuntime) Remove(ctx context.Context, pluginID, channelID string, purge bool) error {
	return m.Called(ctx, pluginID, channelID, purge).Error(0)
}

type ServiceSuite struct {
	base
	runtime *mockRuntime
	svc     *Service
}

func TestServiceSuite(t *testing.T) {
	suite.Run(t, new(ServiceSuite))
}

func (s *ServiceSuite) SetupTest() {
	s.base.SetupTest()
	s.runtime = new(mockRuntime)
	s.svc = NewService(s.in, s.runtime, s.store)
}

func (s *ServiceSuite) TearDownTest() {
	s.base.TearDownTest()
	s.runtime.AssertExpectations(s.T())
}

// expectInspect expects a full, successful inspection of ref, finding the
// plugin installed as prev.
func (s *ServiceSuite) expectInspect(prev *db.PluginState) {
	s.expectPull()
	s.docker.On("ImageInspect", s.ctx, ref).Return(s.info(), nil).Once()
	s.expectCreated()
	s.expectData()
	s.docker.On("CopyFromContainer", s.ctx, "x1", "/loop-plugin").Return(pluginTar(manifest), nil).Once()
	s.store.On("GetPluginState", s.ctx, "echo").Return(prev, nil).Once()
}

func (s *ServiceSuite) extracted() []os.DirEntry {
	entries, _ := os.ReadDir(filepath.Join(s.root, "echo", "images"))
	return entries
}

func (s *ServiceSuite) TestList() {
	states := []*db.PluginState{{PluginID: "echo"}}
	s.store.On("ListPluginStates", s.ctx).Return(states, nil).Once()
	got, err := s.svc.List(s.ctx)
	require.NoError(s.T(), err)
	require.Equal(s.T(), states, got)
}

func (s *ServiceSuite) TestInspect() {
	s.expectInspect(nil)
	c, err := s.svc.Inspect(s.ctx, ref, false)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "echo", c.Manifest.ID)
	require.Empty(s.T(), s.extracted(), "inspecting keeps nothing")

	s.expectPull()
	s.docker.On("ImageInspect", s.ctx, ref).Return(s.info(), errors.New("boom")).Once()
	_, err = s.svc.Inspect(s.ctx, ref, false)
	require.ErrorContains(s.T(), err, "boom")
}

func (s *ServiceSuite) TestInstall() {
	s.expectInspect(nil)
	s.store.On("UpsertPluginState", s.ctx, mock.Anything).Return(nil).Once()
	c, err := s.svc.Install(s.ctx, ref, false, digest, plugin.Capabilities)
	require.NoError(s.T(), err)
	require.Len(s.T(), s.extracted(), 1)
	require.DirExists(s.T(), c.Dir)
}

func (s *ServiceSuite) TestUpgradeStopsContainers() {
	s.expectInspect(&db.PluginState{PluginID: "echo", Image: "ghcr.io/acme/echo@sha256:old"})
	s.store.On("UpsertPluginState", s.ctx, mock.Anything).Return(nil).Once()
	s.runtime.On("StopPlugin", s.ctx, "echo").Once()
	_, err := s.svc.Install(s.ctx, ref, false, "", plugin.Capabilities)
	require.NoError(s.T(), err)
}

func (s *ServiceSuite) TestInstallFailures() {
	s.expectPull()
	s.docker.On("ImageInspect", s.ctx, ref).Return(s.info(), errors.New("boom")).Once()
	_, err := s.svc.Install(s.ctx, ref, false, "", plugin.Capabilities)
	require.ErrorContains(s.T(), err, "boom")

	s.expectInspect(nil)
	_, err = s.svc.Install(s.ctx, ref, false, "sha256:other", plugin.Capabilities)
	require.ErrorIs(s.T(), err, ErrDigestChanged)
	require.Empty(s.T(), s.extracted())

	s.expectInspect(nil)
	_, err = s.svc.Install(s.ctx, ref, false, digest, nil)
	require.ErrorIs(s.T(), err, ErrNotApproved)
	require.Empty(s.T(), s.extracted())
}

func (s *ServiceSuite) TestRemove() {
	s.store.On("GetPluginState", s.ctx, "echo").Return(&db.PluginState{PluginID: "echo"}, nil).Once()
	s.store.On("ListPluginContainers", s.ctx, "echo").Return([]*db.PluginContainer{{ChannelID: "ch1"}, {ChannelID: "ch2"}}, nil).Once()
	s.runtime.On("Remove", s.ctx, "echo", "ch1", true).Return(nil).Once()
	s.runtime.On("Remove", s.ctx, "echo", "ch2", true).Return(errors.New("busy")).Once()
	s.store.On("DeletePluginState", s.ctx, "echo").Return(nil).Once()
	require.EqualError(s.T(), s.svc.Remove(s.ctx, "echo", true), "busy")
}

func (s *ServiceSuite) TestRemoveFailures() {
	boom := errors.New("boom")
	s.store.On("GetPluginState", s.ctx, "echo").Return(nil, boom).Once()
	require.ErrorIs(s.T(), s.svc.Remove(s.ctx, "echo", false), boom)

	s.store.On("GetPluginState", s.ctx, "echo").Return(nil, nil).Once()
	require.ErrorIs(s.T(), s.svc.Remove(s.ctx, "echo", false), ErrNotInstalled)

	s.store.On("GetPluginState", s.ctx, "echo").Return(&db.PluginState{}, nil).Once()
	s.store.On("ListPluginContainers", s.ctx, "echo").Return(nil, boom).Once()
	require.EqualError(s.T(), s.svc.Remove(s.ctx, "echo", false), "listing the plugin's containers: boom")
}
