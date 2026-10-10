package container

import (
	"context"
	"errors"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// reclaimDiskUsage is a daemon holding one of each kind of thing reclaiming
// space looks at.
func reclaimDiskUsage() *DiskUsage {
	return &DiskUsage{
		BuildCache: 1000,
		Images: []DiskImage{
			{ID: "dangling", Size: 100, SharedSize: -1},
			{ID: "dangling-old-daemon", Tags: []string{"<none>:<none>"}, Size: 50, SharedSize: 10},
			{ID: "dangling-in-use", Containers: 1, Size: 9999},
			{ID: "unused", Tags: []string{"old:1", "old:latest"}, Size: 300, SharedSize: 100},
			{ID: "unused-2", Tags: []string{"other:2"}, Size: 20},
			{ID: "in-use", Tags: []string{"busy:1"}, Containers: 1, Size: 9999},
			{ID: "uncounted", Tags: []string{"unknown:1"}, Containers: -1, Size: 9999},
			{ID: "agent", Tags: []string{"loop-agent"}, Size: 9999},
			{ID: "chrome", Tags: []string{"loop-chrome:latest"}, Size: 9999},
			{ID: "kept", Tags: []string{"project:dev"}, Size: 9999},
			{ID: "child", Tags: []string{"child:1"}, Labels: map[string]string{ParentIDLabel: "sha256:base"}, Size: 9999},
		},
		Volumes: []DiskVolume{
			{Name: "anon", Labels: map[string]string{anonymousVolumeLabel: ""}, Size: 400},
			{Name: "anon-unsized", Labels: map[string]string{anonymousVolumeLabel: ""}, Size: -1},
			{Name: "anon-in-use", Labels: map[string]string{anonymousVolumeLabel: ""}, RefCount: 1, Size: 9999},
			{Name: "orphan-b", Size: 60},
			{Name: "orphan-a", Size: 40},
			{Name: "orphan-in-use", RefCount: 1, Size: 9999},
			{Name: "orphan-uncounted", RefCount: -1, Size: 9999},
			{Name: "named", Size: 9999},
		},
	}
}

func (s *LifecycleSuite) newReclaimManager() *ImageLifecycleManager {
	m := s.newManager(func() string { return "" })
	m.imageName = "loop-agent:latest"
	m.sidecarImage = "loop-chrome"
	m.SetReclaimScope(func(context.Context) (ReclaimScope, error) {
		return ReclaimScope{
			KeepImages:   []string{"project:dev", ""},
			OrphanVolume: func(name string) bool { return len(name) > 7 && name[:7] == "orphan-" },
		}, nil
	})
	return m
}

func (s *LifecycleSuite) TestReclaimable() {
	m := s.newReclaimManager()
	s.client.On("DiskUsage", mock.Anything, true).Return(reclaimDiskUsage(), nil)

	got, err := m.Reclaimable(context.Background(), true)
	require.NoError(s.T(), err)
	require.Equal(s.T(), Reclaimable{
		VolumesSized:     true,
		BuildCache:       1000,
		DanglingImages:   140,
		UnusedImages:     220,
		UnusedImageTags:  []string{"old:1", "old:latest", "other:2"},
		AnonymousVolumes: 400,
		OrphanVolumes:    100,
		OrphanVolumeList: []string{"orphan-a", "orphan-b"},
	}, got)
}

func (s *LifecycleSuite) TestReclaimableWithoutScope() {
	m := s.newManager(func() string { return "" })
	s.client.On("DiskUsage", mock.Anything, false).Return(&DiskUsage{
		Images:  []DiskImage{{ID: "agent", Tags: []string{s.imageName}, Size: 10}},
		Volumes: []DiskVolume{{Name: "orphan-a", Size: -1}, {Name: "anon", Labels: map[string]string{anonymousVolumeLabel: ""}, Size: -1}},
	}, nil)

	got, err := m.Reclaimable(context.Background(), false)
	require.NoError(s.T(), err)
	require.Equal(s.T(), Reclaimable{UnusedImageTags: []string{}, OrphanVolumeList: []string{}}, got)
}

func (s *LifecycleSuite) TestReclaimableErrors() {
	tests := []struct {
		name    string
		scope   error
		usage   error
		wantErr string
	}{
		{name: "scope", scope: errors.New("db closed"), wantErr: "db closed"},
		{name: "disk usage", usage: errors.New("daemon down"), wantErr: "daemon down"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			m := s.newManager(func() string { return "" })
			m.SetReclaimScope(func(context.Context) (ReclaimScope, error) { return ReclaimScope{}, tt.scope })
			s.client.On("DiskUsage", mock.Anything, mock.Anything).Return(nil, tt.usage).Maybe()

			_, err := m.Reclaimable(context.Background(), true)
			require.EqualError(s.T(), err, tt.wantErr)

			_, err = m.ReclaimSpace(context.Background(), ReclaimOptions{})
			require.EqualError(s.T(), err, tt.wantErr)
			s.client.AssertNotCalled(s.T(), "PruneBuildCache", mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

func (s *LifecycleSuite) TestReclaimSpace() {
	tests := []struct {
		name   string
		opts   ReclaimOptions
		failOn string // a step that fails
		want   ReclaimResult
		// wantErr is the error ReclaimSpace returns; a failed image or
		// volume removal isn't one.
		wantErr string
	}{
		{
			name: "default leaves unused images",
			want: ReclaimResult{BuildCacheReclaimed: 4096, ImagesReclaimed: 8192, VolumesReclaimed: 512, TotalReclaimed: 12800, OrphanVolumesRemoved: 2},
		},
		{
			name: "unused images opted in",
			opts: ReclaimOptions{UnusedImages: true},
			want: ReclaimResult{BuildCacheReclaimed: 4096, ImagesReclaimed: 8192, UnusedImagesReclaimed: 220, VolumesReclaimed: 512, TotalReclaimed: 13020, OrphanVolumesRemoved: 2},
		},
		{
			name:   "an image a container started using stays",
			opts:   ReclaimOptions{UnusedImages: true},
			failOn: "old:1",
			want:   ReclaimResult{BuildCacheReclaimed: 4096, ImagesReclaimed: 8192, UnusedImagesReclaimed: 20, VolumesReclaimed: 512, TotalReclaimed: 12820, OrphanVolumesRemoved: 2},
		},
		{
			name:   "a volume a container started using stays",
			failOn: "orphan-b",
			want:   ReclaimResult{BuildCacheReclaimed: 4096, ImagesReclaimed: 8192, VolumesReclaimed: 512, TotalReclaimed: 12800, OrphanVolumesRemoved: 1},
		},
		{
			name:    "build cache fails",
			failOn:  "PruneBuildCache",
			wantErr: "cache fail",
		},
		{
			name:    "dangling images fail, the cache freed is still reported",
			opts:    ReclaimOptions{UnusedImages: true},
			failOn:  "PruneDanglingImages",
			want:    ReclaimResult{BuildCacheReclaimed: 4096, UnusedImagesReclaimed: 220, TotalReclaimed: 4316},
			wantErr: "images fail",
		},
		{
			name:    "anonymous volumes fail",
			failOn:  "PruneAnonymousVolumes",
			want:    ReclaimResult{BuildCacheReclaimed: 4096, ImagesReclaimed: 8192, TotalReclaimed: 12288},
			wantErr: "volumes fail",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			m := s.newReclaimManager()
			fail := func(step string, err error) error {
				if tt.failOn == step {
					return err
				}
				return nil
			}
			s.client.On("DiskUsage", mock.Anything, false).Return(reclaimDiskUsage(), nil)
			s.client.On("PruneBuildCache", mock.Anything, time.Duration(0), true).Return(uint64(4096), fail("PruneBuildCache", errors.New("cache fail"))).Maybe()
			s.client.On("PruneDanglingImages", mock.Anything).Return(uint64(8192), fail("PruneDanglingImages", errors.New("images fail"))).Maybe()
			s.client.On("PruneAnonymousVolumes", mock.Anything).Return(uint64(512), fail("PruneAnonymousVolumes", errors.New("volumes fail"))).Maybe()
			for _, ref := range []string{"old:1", "old:latest", "other:2"} {
				s.client.On("RemoveImage", mock.Anything, ref).Return(fail(ref, errors.New("image in use"))).Maybe()
			}
			for _, name := range []string{"orphan-a", "orphan-b"} {
				s.client.On("RemoveVolume", mock.Anything, name).Return(fail(name, errors.New("volume in use"))).Maybe()
			}

			got, err := m.ReclaimSpace(context.Background(), tt.opts)
			if tt.wantErr != "" {
				require.EqualError(s.T(), err, tt.wantErr)
			} else {
				require.NoError(s.T(), err)
			}
			require.Equal(s.T(), tt.want, got)
			if !tt.opts.UnusedImages {
				s.client.AssertNotCalled(s.T(), "RemoveImage", mock.Anything, mock.Anything)
			}
			if tt.failOn == "old:1" {
				// The image's other tag stays once one fails.
				s.client.AssertNotCalled(s.T(), "RemoveImage", mock.Anything, "old:latest")
			}
		})
	}
}
