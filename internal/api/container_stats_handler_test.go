package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/container"
)

// MockContainerStatsFetcher mocks api.ContainerStatsFetcher.
type MockContainerStatsFetcher struct {
	mock.Mock
}

func (m *MockContainerStatsFetcher) ContainerStats(ctx context.Context, containerID string) (*container.ContainerStatsSummary, error) {
	args := m.Called(ctx, containerID)
	if v := args.Get(0); v != nil {
		return v.(*container.ContainerStatsSummary), args.Error(1)
	}
	return nil, args.Error(1)
}

func (s *ServerSuite) TestContainerStatsHandler() {
	reg := new(mockContainerManager)
	fetcher := new(MockContainerStatsFetcher)
	s.srv.SetContainerRegistry(reg)
	s.srv.SetContainerStatsFetcher(fetcher)

	reg.byChannel = []*container.ContainerInfo{
		{ContainerID: "agent-ctr", ChannelID: "ch-1", Type: container.ContainerTypeAgent, Status: container.ContainerStatusRunning},
		{ContainerID: "shell-ctr", ChannelID: "ch-1", Type: container.ContainerTypeShell, Status: container.ContainerStatusRunning},
		{ContainerID: "old-ctr", ChannelID: "ch-1", Type: container.ContainerTypeAgent, Status: container.ContainerStatusStopped},
	}
	fetcher.On("ContainerStats", mock.Anything, "agent-ctr").Return(&container.ContainerStatsSummary{CPUPercent: 12.5, MemUsage: 800, MemLimit: 4096}, nil)
	// A fetch error (teardown race) skips the entry instead of failing the request.
	fetcher.On("ContainerStats", mock.Anything, "shell-ctr").Return(nil, errors.New("gone"))

	rec := s.testRequest("GET", "/api/channels/ch-1/container-stats", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var entries []containerStatsEntry
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&entries))
	require.Len(s.T(), entries, 1)
	require.Equal(s.T(), "agent-ctr", entries[0].ContainerID)
	require.Equal(s.T(), "agent", entries[0].Type)
	require.InDelta(s.T(), 12.5, entries[0].CPUPercent, 0.001)
	require.Equal(s.T(), uint64(800), entries[0].MemUsage)
	fetcher.AssertExpectations(s.T())
}

func (s *ServerSuite) TestContainerStatsHandlerWithoutDepsIsEmpty() {
	rec := s.testRequest("GET", "/api/channels/ch-1/container-stats", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.JSONEq(s.T(), "[]", rec.Body.String())
}
