package container

import (
	"context"
	"errors"
	"fmt"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/volume"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func (s *ClientSuite) TestDiskUsage() {
	ctx := context.Background()
	s.api.On("DiskUsage", ctx, types.DiskUsageOptions{Types: []types.DiskUsageObject{types.ImageObject, types.BuildCacheObject, types.VolumeObject}}).Return(types.DiskUsage{
		Images: []*image.Summary{
			nil,
			{ID: "img", RepoTags: []string{"a:1"}, Labels: map[string]string{"k": "v"}, Containers: 2, Size: 300, SharedSize: 100},
		},
		Volumes: []*volume.Volume{
			nil,
			{Name: "sized", Labels: map[string]string{anonymousVolumeLabel: ""}, UsageData: &volume.UsageData{RefCount: 1, Size: 50}},
			{Name: "unsized"},
		},
		BuildCache: []*build.CacheRecord{nil, {Size: 10}, {Size: 20, InUse: true}, {Size: 5}, {Size: -1}},
	}, nil)

	du, err := s.client.DiskUsage(ctx, true)
	require.NoError(s.T(), err)
	require.Equal(s.T(), &DiskUsage{
		Images: []DiskImage{{ID: "img", Tags: []string{"a:1"}, Labels: map[string]string{"k": "v"}, Containers: 2, Size: 300, SharedSize: 100}},
		Volumes: []DiskVolume{
			{Name: "sized", Labels: map[string]string{anonymousVolumeLabel: ""}, RefCount: 1, Size: 50},
			{Name: "unsized", RefCount: -1, Size: -1},
		},
		BuildCache: 15,
	}, du)
	require.True(s.T(), du.Volumes[0].Anonymous())
	require.False(s.T(), du.Volumes[1].Anonymous())
}

func (s *ClientSuite) TestDiskUsageUnsizedVolumes() {
	ctx := context.Background()
	s.api.On("DiskUsage", ctx, types.DiskUsageOptions{Types: []types.DiskUsageObject{types.ImageObject, types.BuildCacheObject}}).
		Return(types.DiskUsage{BuildCache: []*build.CacheRecord{{Size: 10}}}, nil)
	s.api.On("VolumeList", ctx, mock.MatchedBy(func(o volume.ListOptions) bool {
		// Only the volumes no container uses.
		return o.Filters.Len() == 1 && o.Filters.ExactMatch("dangling", "true")
	})).Return(volume.ListResponse{Volumes: []*volume.Volume{nil, {Name: "unused", Labels: map[string]string{"k": "v"}}}}, nil)

	du, err := s.client.DiskUsage(ctx, false)
	require.NoError(s.T(), err)
	require.Equal(s.T(), &DiskUsage{
		Volumes:    []DiskVolume{{Name: "unused", Labels: map[string]string{"k": "v"}, RefCount: 0, Size: -1}},
		BuildCache: 10,
	}, du)
}

func (s *ClientSuite) TestDiskUsageErrors() {
	tests := []struct {
		name    string
		usage   error
		list    error
		wantErr string
	}{
		{name: "disk usage", usage: errors.New("daemon down"), wantErr: "reading docker disk usage: daemon down"},
		{name: "volume list", list: errors.New("daemon down"), wantErr: "listing unused volumes: daemon down"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.api.On("DiskUsage", mock.Anything, mock.Anything).Return(types.DiskUsage{}, tt.usage)
			s.api.On("VolumeList", mock.Anything, mock.Anything).Return(volume.ListResponse{}, tt.list).Maybe()
			_, err := s.client.DiskUsage(context.Background(), false)
			require.EqualError(s.T(), err, tt.wantErr)
		})
	}
}

func (s *ClientSuite) TestDiskImageUniqueSize() {
	tests := []struct {
		name string
		img  DiskImage
		want uint64
	}{
		{name: "shared layers left out", img: DiskImage{Size: 300, SharedSize: 100}, want: 200},
		{name: "shared size not counted", img: DiskImage{Size: 300, SharedSize: -1}, want: 300},
		{name: "never negative", img: DiskImage{Size: -1}, want: 0},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			require.Equal(s.T(), tt.want, tt.img.UniqueSize())
		})
	}
}

func (s *ClientSuite) TestPruneAnonymousVolumes() {
	tests := []struct {
		name    string
		err     error
		want    uint64
		wantErr string
	}{
		{name: "pruned", want: 4096},
		{name: "daemon error", err: errors.New("daemon down"), wantErr: "pruning anonymous volumes: daemon down"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			ctx := context.Background()
			s.api.On("VolumesPrune", ctx, mock.MatchedBy(func(f filters.Args) bool {
				// Only anonymous volumes: never a named one.
				return f.Len() == 1 && f.ExactMatch("label", anonymousVolumeLabel)
			})).Return(volume.PruneReport{SpaceReclaimed: 4096}, tt.err)

			got, err := s.client.PruneAnonymousVolumes(ctx)
			if tt.wantErr != "" {
				require.EqualError(s.T(), err, tt.wantErr)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), tt.want, got)
		})
	}
}

func (s *ClientSuite) TestRemoveImage() {
	tests := []struct {
		name    string
		err     error
		wantErr string
	}{
		{name: "removed"},
		{name: "already gone with an earlier tag", err: fmt.Errorf("No such image: old:1: %w", cerrdefs.ErrNotFound)},
		{name: "in use", err: errors.New("conflict"), wantErr: "removing image old:1: conflict"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			ctx := context.Background()
			s.api.On("ImageRemove", ctx, "old:1", image.RemoveOptions{PruneChildren: true}).Return([]image.DeleteResponse(nil), tt.err)
			err := s.client.RemoveImage(ctx, "old:1")
			if tt.wantErr != "" {
				require.EqualError(s.T(), err, tt.wantErr)
				return
			}
			require.NoError(s.T(), err)
		})
	}
}

func (s *ClientSuite) TestRemoveVolume() {
	tests := []struct {
		name    string
		err     error
		wantErr string
	}{
		{name: "removed"},
		{name: "already gone", err: fmt.Errorf("no such volume: %w", cerrdefs.ErrNotFound)},
		{name: "in use", err: errors.New("volume is in use"), wantErr: "removing volume vol: volume is in use"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			ctx := context.Background()
			s.api.On("VolumeRemove", ctx, "vol", false).Return(tt.err)
			err := s.client.RemoveVolume(ctx, "vol")
			if tt.wantErr != "" {
				require.EqualError(s.T(), err, tt.wantErr)
				return
			}
			require.NoError(s.T(), err)
		})
	}
}
