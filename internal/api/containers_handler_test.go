package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/container"
)

// mockContainerManager implements ContainerManager for testing.
type mockContainerManager struct {
	mock.Mock
	containers []*container.ContainerInfo
	byChannel  []*container.ContainerInfo
	runningIDs map[string]struct{}
}

func (m *mockContainerManager) List() []*container.ContainerInfo {
	return m.containers
}

func (m *mockContainerManager) ListByChannel(string) []*container.ContainerInfo {
	return m.byChannel
}

func (m *mockContainerManager) RunningChannelIDs(context.Context) map[string]struct{} {
	return m.runningIDs
}

func (m *mockContainerManager) RemoveContainer(ctx context.Context, containerID string) error {
	return m.Called(ctx, containerID).Error(0)
}

func (m *mockContainerManager) ScheduleRemove(containerID string, delay time.Duration) {
	m.Called(containerID, delay)
}

func (m *mockContainerManager) FindOrCreateShell(ctx context.Context, channelID, dirPath string) (string, error) {
	args := m.Called(ctx, channelID, dirPath)
	return args.String(0), args.Error(1)
}

func (s *ServerSuite) TestListContainersNotConfigured() {
	// containerRegistry is nil by default.
	rec := s.testRequest("GET", "/api/containers", "")
	require.Equal(s.T(), http.StatusServiceUnavailable, rec.Code)
}

func (s *ServerSuite) TestListContainersEmpty() {
	reg := container.NewRegistry(nil)
	s.srv.SetContainerRegistry(reg)

	rec := s.testRequest("GET", "/api/containers", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var result []*container.ContainerInfo
	err := json.Unmarshal(rec.Body.Bytes(), &result)
	require.NoError(s.T(), err)
	require.Empty(s.T(), result)
}

func (s *ServerSuite) TestListContainers() {
	reg := container.NewRegistry(nil)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reg.SetTimeNow(func() time.Time { return now })
	s.srv.containerRegistry = reg

	reg.Register(&container.ContainerInfo{
		ContainerID:   "c1",
		ChannelID:     "ch-1",
		Type:          container.ContainerTypeAgent,
		ContainerName: "loop-test-abc",
	})
	reg.Register(&container.ContainerInfo{
		ContainerID: "c2",
		ChannelID:   "ch-1",
		Type:        container.ContainerTypeShell,
	})

	rec := s.testRequest("GET", "/api/containers", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var result []*container.ContainerInfo
	err := json.Unmarshal(rec.Body.Bytes(), &result)
	require.NoError(s.T(), err)
	require.Len(s.T(), result, 2)
}
