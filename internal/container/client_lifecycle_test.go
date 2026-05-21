package container

import (
	"context"
	"errors"

	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- RemoveImageAndContainers tests ---

func (s *ClientSuite) TestRemoveImageAndContainers_HappyPath() {
	ctx := context.Background()

	// Mock ContainerList with ancestor filter and All: true.
	s.api.On("ContainerList", ctx, mock.MatchedBy(func(opts containertypes.ListOptions) bool {
		return opts.All && opts.Filters.Get("ancestor")[0] == "loop-agent:latest"
	})).Return([]containertypes.Summary{
		{ID: "container-aaa111"},
		{ID: "container-bbb222"},
	}, nil)

	// Mock ContainerRemove for each container.
	s.api.On("ContainerRemove", ctx, "container-aaa111", containertypes.RemoveOptions{Force: true, RemoveVolumes: true}).Return(nil)
	s.api.On("ContainerRemove", ctx, "container-bbb222", containertypes.RemoveOptions{Force: true, RemoveVolumes: true}).Return(nil)

	// Mock ImageList for reference lookup.
	s.api.On("ImageList", ctx, mock.MatchedBy(func(opts image.ListOptions) bool {
		return opts.Filters.Get("reference")[0] == "loop-agent:latest"
	})).Return([]image.Summary{
		{ID: "sha256:img-aaa111bbb222"},
	}, nil)

	// Mock ImageRemove.
	s.api.On("ImageRemove", ctx, "sha256:img-aaa111bbb222", image.RemoveOptions{Force: true}).
		Return([]image.DeleteResponse{{Deleted: "sha256:img-aaa111bbb222"}}, nil)

	err := s.client.RemoveImageAndContainers(ctx, "loop-agent:latest")
	require.NoError(s.T(), err)
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestRemoveImageAndContainers_NoContainers() {
	ctx := context.Background()

	// No containers found.
	s.api.On("ContainerList", ctx, mock.MatchedBy(func(opts containertypes.ListOptions) bool {
		return opts.All && opts.Filters.Get("ancestor")[0] == "loop-agent:latest"
	})).Return([]containertypes.Summary{}, nil)

	// Image found and removed.
	s.api.On("ImageList", ctx, mock.MatchedBy(func(opts image.ListOptions) bool {
		return opts.Filters.Get("reference")[0] == "loop-agent:latest"
	})).Return([]image.Summary{
		{ID: "sha256:img-aaa111bbb222"},
	}, nil)

	s.api.On("ImageRemove", ctx, "sha256:img-aaa111bbb222", image.RemoveOptions{Force: true}).
		Return([]image.DeleteResponse{{Deleted: "sha256:img-aaa111bbb222"}}, nil)

	err := s.client.RemoveImageAndContainers(ctx, "loop-agent:latest")
	require.NoError(s.T(), err)
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestRemoveImageAndContainers_ContainerListError() {
	ctx := context.Background()

	s.api.On("ContainerList", ctx, mock.MatchedBy(func(opts containertypes.ListOptions) bool {
		return opts.All
	})).Return([]containertypes.Summary(nil), errors.New("daemon error"))

	err := s.client.RemoveImageAndContainers(ctx, "loop-agent:latest")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "listing containers for image")
	require.Contains(s.T(), err.Error(), "daemon error")
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestRemoveImageAndContainers_ContainerRemoveError() {
	ctx := context.Background()

	s.api.On("ContainerList", ctx, mock.MatchedBy(func(opts containertypes.ListOptions) bool {
		return opts.All && opts.Filters.Get("ancestor")[0] == "loop-agent:latest"
	})).Return([]containertypes.Summary{
		{ID: "container-aaa111bbb222"},
	}, nil)

	s.api.On("ContainerRemove", ctx, "container-aaa111bbb222", containertypes.RemoveOptions{Force: true, RemoveVolumes: true}).
		Return(errors.New("rm failed"))

	err := s.client.RemoveImageAndContainers(ctx, "loop-agent:latest")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "removing container")
	require.Contains(s.T(), err.Error(), "rm failed")
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestRemoveImageAndContainers_ImageListError() {
	ctx := context.Background()

	// Containers removed successfully.
	s.api.On("ContainerList", ctx, mock.MatchedBy(func(opts containertypes.ListOptions) bool {
		return opts.All
	})).Return([]containertypes.Summary{}, nil)

	// ImageList (called by c.ImageList) fails.
	s.api.On("ImageList", ctx, mock.MatchedBy(func(opts image.ListOptions) bool {
		return opts.Filters.Get("reference")[0] == "loop-agent:latest"
	})).Return([]image.Summary(nil), errors.New("image list failed"))

	err := s.client.RemoveImageAndContainers(ctx, "loop-agent:latest")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "listing image")
	require.Contains(s.T(), err.Error(), "image list failed")
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestRemoveImageAndContainers_ImageRemoveError() {
	ctx := context.Background()

	// No containers.
	s.api.On("ContainerList", ctx, mock.MatchedBy(func(opts containertypes.ListOptions) bool {
		return opts.All
	})).Return([]containertypes.Summary{}, nil)

	// Image found.
	s.api.On("ImageList", ctx, mock.MatchedBy(func(opts image.ListOptions) bool {
		return opts.Filters.Get("reference")[0] == "loop-agent:latest"
	})).Return([]image.Summary{
		{ID: "sha256:img-aaa111bbb222"},
	}, nil)

	// ImageRemove fails.
	s.api.On("ImageRemove", ctx, "sha256:img-aaa111bbb222", image.RemoveOptions{Force: true}).
		Return([]image.DeleteResponse(nil), errors.New("image in use"))

	err := s.client.RemoveImageAndContainers(ctx, "loop-agent:latest")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "removing image")
	require.Contains(s.T(), err.Error(), "image in use")
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestRemoveImageAndContainers_NoImageFound() {
	ctx := context.Background()

	// No containers.
	s.api.On("ContainerList", ctx, mock.MatchedBy(func(opts containertypes.ListOptions) bool {
		return opts.All
	})).Return([]containertypes.Summary{}, nil)

	// No images found.
	s.api.On("ImageList", ctx, mock.MatchedBy(func(opts image.ListOptions) bool {
		return opts.Filters.Get("reference")[0] == "loop-agent:latest"
	})).Return([]image.Summary{}, nil)

	err := s.client.RemoveImageAndContainers(ctx, "loop-agent:latest")
	require.NoError(s.T(), err)
	s.api.AssertExpectations(s.T())
}

// --- ImageInspectLabels tests ---

func (s *ClientSuite) TestImageInspectLabels_HappyPath() {
	ctx := context.Background()

	// Mock ImageList (called by c.ImageList).
	s.api.On("ImageList", ctx, mock.MatchedBy(func(opts image.ListOptions) bool {
		return opts.Filters.Get("reference")[0] == "loop-agent:latest"
	})).Return([]image.Summary{
		{ID: "sha256:img-aaa111"},
	}, nil)

	// Mock ImageInspectWithRaw.
	expectedLabels := map[string]string{
		"version":    "1.2.3",
		"build-date": "2026-03-27",
	}
	s.api.On("ImageInspectWithRaw", ctx, "sha256:img-aaa111").Return(image.InspectResponse{
		Config: &dockerspec.DockerOCIImageConfig{
			ImageConfig: ocispec.ImageConfig{
				Labels: expectedLabels,
			},
		},
	}, []byte("{}"), nil)

	labels, err := s.client.ImageInspectLabels(ctx, "loop-agent:latest")
	require.NoError(s.T(), err)
	require.Equal(s.T(), expectedLabels, labels)
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestImageInspectLabels_NoImagesFound() {
	ctx := context.Background()

	// ImageList returns empty.
	s.api.On("ImageList", ctx, mock.MatchedBy(func(opts image.ListOptions) bool {
		return opts.Filters.Get("reference")[0] == "loop-agent:latest"
	})).Return([]image.Summary{}, nil)

	labels, err := s.client.ImageInspectLabels(ctx, "loop-agent:latest")
	require.NoError(s.T(), err)
	require.Nil(s.T(), labels)
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestImageInspectLabels_ImageListError() {
	ctx := context.Background()

	s.api.On("ImageList", ctx, mock.Anything).Return([]image.Summary(nil), errors.New("list error"))

	labels, err := s.client.ImageInspectLabels(ctx, "loop-agent:latest")
	require.Error(s.T(), err)
	require.Nil(s.T(), labels)
	require.Contains(s.T(), err.Error(), "list error")
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestImageInspectLabels_InspectError() {
	ctx := context.Background()

	// ImageList returns one image.
	s.api.On("ImageList", ctx, mock.MatchedBy(func(opts image.ListOptions) bool {
		return opts.Filters.Get("reference")[0] == "loop-agent:latest"
	})).Return([]image.Summary{
		{ID: "sha256:img-aaa111"},
	}, nil)

	// ImageInspectWithRaw fails.
	s.api.On("ImageInspectWithRaw", ctx, "sha256:img-aaa111").
		Return(image.InspectResponse{}, []byte(nil), errors.New("inspect failed"))

	labels, err := s.client.ImageInspectLabels(ctx, "loop-agent:latest")
	require.Error(s.T(), err)
	require.Nil(s.T(), labels)
	require.Contains(s.T(), err.Error(), "inspect failed")
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestImageInspectLabels_ConfigNil() {
	ctx := context.Background()

	// ImageList returns one image.
	s.api.On("ImageList", ctx, mock.MatchedBy(func(opts image.ListOptions) bool {
		return opts.Filters.Get("reference")[0] == "loop-agent:latest"
	})).Return([]image.Summary{
		{ID: "sha256:img-aaa111"},
	}, nil)

	// ImageInspectWithRaw returns nil Config.
	s.api.On("ImageInspectWithRaw", ctx, "sha256:img-aaa111").
		Return(image.InspectResponse{Config: nil}, []byte("{}"), nil)

	labels, err := s.client.ImageInspectLabels(ctx, "loop-agent:latest")
	require.NoError(s.T(), err)
	require.Nil(s.T(), labels)
	s.api.AssertExpectations(s.T())
}
