package container

import (
	"bytes"
	"context"
	"errors"
	"io"

	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func (s *ClientSuite) TestImageList() {
	ctx := context.Background()

	s.api.On("ImageList", ctx, mock.MatchedBy(func(opts image.ListOptions) bool {
		return opts.Filters.Get("reference")[0] == "my-image:latest"
	})).Return([]image.Summary{
		{ID: "sha256:aaa"},
		{ID: "sha256:bbb"},
	}, nil)

	ids, err := s.client.ImageList(ctx, "my-image:latest")
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"sha256:aaa", "sha256:bbb"}, ids)
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestImageListEmpty() {
	ctx := context.Background()

	s.api.On("ImageList", ctx, mock.Anything).Return([]image.Summary{}, nil)

	ids, err := s.client.ImageList(ctx, "nonexistent:latest")
	require.NoError(s.T(), err)
	require.Empty(s.T(), ids)
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestImageListError() {
	ctx := context.Background()

	s.api.On("ImageList", ctx, mock.Anything).Return([]image.Summary(nil), errors.New("list failed"))

	ids, err := s.client.ImageList(ctx, "my-image:latest")
	require.Error(s.T(), err)
	require.Nil(s.T(), ids)
	require.Contains(s.T(), err.Error(), "list failed")
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestImagePull() {
	ctx := context.Background()

	s.api.On("ImagePull", ctx, "my-image:latest", image.PullOptions{}).
		Return(io.NopCloser(bytes.NewReader([]byte("pulling..."))), nil)

	err := s.client.ImagePull(ctx, "my-image:latest")
	require.NoError(s.T(), err)
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestImagePullError() {
	ctx := context.Background()

	s.api.On("ImagePull", ctx, "my-image:latest", image.PullOptions{}).
		Return(nil, errors.New("pull failed"))

	err := s.client.ImagePull(ctx, "my-image:latest")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "pull failed")
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestImagePullDrainError() {
	ctx := context.Background()

	s.api.On("ImagePull", ctx, "my-image:latest", image.PullOptions{}).
		Return(io.NopCloser(&errReader{err: errors.New("read error")}), nil)

	err := s.client.ImagePull(ctx, "my-image:latest")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "read error")
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestContainerList() {
	ctx := context.Background()

	s.api.On("ContainerList", ctx, mock.MatchedBy(func(opts containertypes.ListOptions) bool {
		return !opts.All && opts.Filters.Get("label")[0] == "app=loop-agent"
	})).Return([]containertypes.Summary{
		{ID: "cid-1"},
		{ID: "cid-2"},
	}, nil)

	ids, err := s.client.ContainerList(ctx, "app", "loop-agent")
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"cid-1", "cid-2"}, ids)
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestContainerListEmpty() {
	ctx := context.Background()

	s.api.On("ContainerList", ctx, mock.Anything).Return([]containertypes.Summary{}, nil)

	ids, err := s.client.ContainerList(ctx, "app", "loop-agent")
	require.NoError(s.T(), err)
	require.Empty(s.T(), ids)
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestContainerListError() {
	ctx := context.Background()

	s.api.On("ContainerList", ctx, mock.Anything).Return([]containertypes.Summary(nil), errors.New("list failed"))

	ids, err := s.client.ContainerList(ctx, "app", "loop-agent")
	require.Error(s.T(), err)
	require.Nil(s.T(), ids)
	require.Contains(s.T(), err.Error(), "list failed")
	s.api.AssertExpectations(s.T())
}
