package container

import (
	"context"
	"errors"

	"github.com/docker/docker/api/types/network"
	"github.com/stretchr/testify/require"
)

// --- NetworkEnsure tests ---

func (s *ClientSuite) TestNetworkEnsure() {
	ctx := context.Background()

	s.api.On("NetworkCreate", ctx, "loop-net", network.CreateOptions{
		Driver: "bridge",
	}).Return(network.CreateResponse{ID: "net-abc"}, nil)

	err := s.client.NetworkEnsure(ctx, "loop-net")
	require.NoError(s.T(), err)
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestNetworkEnsureAlreadyExists() {
	ctx := context.Background()

	s.api.On("NetworkCreate", ctx, "loop-net", network.CreateOptions{
		Driver: "bridge",
	}).Return(network.CreateResponse{}, errors.New("network loop-net already exists"))

	err := s.client.NetworkEnsure(ctx, "loop-net")
	require.NoError(s.T(), err)
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestNetworkEnsureError() {
	ctx := context.Background()

	s.api.On("NetworkCreate", ctx, "loop-net", network.CreateOptions{
		Driver: "bridge",
	}).Return(network.CreateResponse{}, errors.New("permission denied"))

	err := s.client.NetworkEnsure(ctx, "loop-net")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "permission denied")
	s.api.AssertExpectations(s.T())
}

// --- NetworkRemove tests ---

func (s *ClientSuite) TestNetworkRemove() {
	ctx := context.Background()

	s.api.On("NetworkRemove", ctx, "loop-net").Return(nil)

	err := s.client.NetworkRemove(ctx, "loop-net")
	require.NoError(s.T(), err)
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestNetworkRemoveError() {
	ctx := context.Background()

	s.api.On("NetworkRemove", ctx, "loop-net").Return(errors.New("network not found"))

	err := s.client.NetworkRemove(ctx, "loop-net")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "network not found")
	s.api.AssertExpectations(s.T())
}
