package container

import (
	"context"
	"errors"

	containertypes "github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func (s *ClientSuite) TestListContainerInfos() {
	ctx := context.Background()

	// Agent/shell containers (including stopped).
	s.api.On("ContainerList", ctx, mock.MatchedBy(func(opts containertypes.ListOptions) bool {
		return opts.All && opts.Filters.Get("label")[0] == "app="+ContainerLabel
	})).Return([]containertypes.Summary{
		{
			ID:      "agent-1",
			Names:   []string{"/loop-abc"},
			Labels:  map[string]string{ChannelLabelKey: "ch-1", ContainerTypeKey: "agent", InstanceLabelKey: "abc123"},
			State:   "running",
			Created: 1700000000,
		},
		{
			ID:      "shell-1",
			Names:   []string{"/loop-shell-def"},
			Labels:  map[string]string{ChannelLabelKey: "ch-1", ContainerTypeKey: "shell"},
			State:   "exited",
			Created: 1700000100,
		},
		{
			ID:     "old-agent",
			Names:  []string{"/loop-old"},
			Labels: map[string]string{ChannelLabelKey: "ch-2"}, // no loop-type label (pre-upgrade)
			State:  "running",
		},
		{
			ID:     "no-channel",
			Labels: map[string]string{}, // no channel label — should be skipped
		},
	}, nil)

	// Chrome containers (including stopped).
	s.api.On("ContainerList", ctx, mock.MatchedBy(func(opts containertypes.ListOptions) bool {
		return opts.All && opts.Filters.Get("label")[0] == "loop-chrome"
	})).Return([]containertypes.Summary{
		{
			ID:      "chrome-1",
			Names:   []string{"/loop-chrome-ch-3"},
			Labels:  map[string]string{"loop-chrome": "ch-3"},
			State:   "exited",
			Created: 1700000200,
		},
		{
			ID:     "chrome-no-channel",
			Labels: map[string]string{"loop-chrome": ""}, // empty channel — should be skipped
		},
	}, nil)

	infos, err := s.client.ListContainerInfos(ctx)
	require.NoError(s.T(), err)
	require.Len(s.T(), infos, 4) // 3 agent/shell (1 skipped) + 1 chrome

	// Verify agent container (running).
	require.Equal(s.T(), "agent-1", infos[0].ContainerID)
	require.Equal(s.T(), "ch-1", infos[0].ChannelID)
	require.Equal(s.T(), ContainerTypeAgent, infos[0].Type)
	require.Equal(s.T(), ContainerStatusRunning, infos[0].Status)
	require.Equal(s.T(), "loop-abc", infos[0].ContainerName)
	require.Equal(s.T(), "abc123", infos[0].InstanceID)

	// Verify shell container (exited → stopped).
	require.Equal(s.T(), "shell-1", infos[1].ContainerID)
	require.Equal(s.T(), ContainerTypeShell, infos[1].Type)
	require.Equal(s.T(), ContainerStatusStopped, infos[1].Status)

	// Verify old agent defaults to "agent" type (running).
	require.Equal(s.T(), "old-agent", infos[2].ContainerID)
	require.Equal(s.T(), ContainerTypeAgent, infos[2].Type)
	require.Equal(s.T(), ContainerStatusRunning, infos[2].Status)

	// Verify chrome container (exited → stopped).
	require.Equal(s.T(), "chrome-1", infos[3].ContainerID)
	require.Equal(s.T(), "ch-3", infos[3].ChannelID)
	require.Equal(s.T(), ContainerTypeChrome, infos[3].Type)
	require.Equal(s.T(), ContainerStatusStopped, infos[3].Status)
	require.Equal(s.T(), "loop-chrome-ch-3", infos[3].ContainerName)

	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestListContainerInfosAgentError() {
	ctx := context.Background()

	s.api.On("ContainerList", ctx, mock.MatchedBy(func(opts containertypes.ListOptions) bool {
		return opts.All && opts.Filters.Get("label")[0] == "app="+ContainerLabel
	})).Return([]containertypes.Summary(nil), errors.New("docker down"))

	infos, err := s.client.ListContainerInfos(ctx)
	require.Error(s.T(), err)
	require.Nil(s.T(), infos)
	require.Contains(s.T(), err.Error(), "listing agent containers")
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestListContainerInfosChromeError() {
	ctx := context.Background()

	s.api.On("ContainerList", ctx, mock.MatchedBy(func(opts containertypes.ListOptions) bool {
		return opts.All && opts.Filters.Get("label")[0] == "app="+ContainerLabel
	})).Return([]containertypes.Summary{}, nil)

	s.api.On("ContainerList", ctx, mock.MatchedBy(func(opts containertypes.ListOptions) bool {
		return opts.All && opts.Filters.Get("label")[0] == "loop-chrome"
	})).Return([]containertypes.Summary(nil), errors.New("chrome error"))

	infos, err := s.client.ListContainerInfos(ctx)
	require.Error(s.T(), err)
	require.Nil(s.T(), infos)
	require.Contains(s.T(), err.Error(), "listing chrome containers")
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestListContainerInfosEmpty() {
	ctx := context.Background()

	s.api.On("ContainerList", ctx, mock.Anything).Return([]containertypes.Summary{}, nil)

	infos, err := s.client.ListContainerInfos(ctx)
	require.NoError(s.T(), err)
	require.Empty(s.T(), infos)
	s.api.AssertExpectations(s.T())
}
