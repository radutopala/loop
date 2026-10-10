package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/events"
)

// MockImageManager implements the ImageManager interface for testing.
type MockImageManager struct {
	mock.Mock
}

func (m *MockImageManager) Status() container.ImageBuildStatus {
	args := m.Called()
	return args.Get(0).(container.ImageBuildStatus)
}

func (m *MockImageManager) Versions() container.ImageVersions {
	args := m.Called()
	return args.Get(0).(container.ImageVersions)
}

func (m *MockImageManager) UpdateAvailable() *events.ImageUpdateAvailableData {
	args := m.Called()
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*events.ImageUpdateAvailableData)
}

func (m *MockImageManager) RemoveImage(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

func (m *MockImageManager) Rebuild(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

func (m *MockImageManager) Reclaimable(ctx context.Context, volumeSizes bool) (container.Reclaimable, error) {
	args := m.Called(ctx, volumeSizes)
	return args.Get(0).(container.Reclaimable), args.Error(1)
}

func (m *MockImageManager) ReclaimSpace(ctx context.Context, opts container.ReclaimOptions) (container.ReclaimResult, error) {
	args := m.Called(ctx, opts)
	return args.Get(0).(container.ReclaimResult), args.Error(1)
}

// --- GET /api/image/status ---

func (s *ServerSuite) TestImageStatusSuccess() {
	mockImgMgr := new(MockImageManager)
	s.srv.imageManager = mockImgMgr

	expectedStatus := container.ImageBuildStatus{
		State: "idle",
	}
	expectedVersions := container.ImageVersions{
		LoopVersion:   "1.2.3",
		ClaudeVersion: "4.0.0",
	}

	mockImgMgr.On("Status").Return(expectedStatus)
	mockImgMgr.On("Versions").Return(expectedVersions)
	mockImgMgr.On("UpdateAvailable").Return((*events.ImageUpdateAvailableData)(nil))

	s.mux.HandleFunc("GET /api/image/status", s.srv.handleImageStatus)
	rec := s.testRequest("GET", "/api/image/status", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp imageStatusResponse
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), expectedStatus.State, resp.Status.State)
	require.Equal(s.T(), expectedVersions.LoopVersion, resp.Versions.LoopVersion)
	require.Equal(s.T(), expectedVersions.ClaudeVersion, resp.Versions.ClaudeVersion)

	mockImgMgr.AssertExpectations(s.T())
}

func (s *ServerSuite) TestImageStatusBuildingState() {
	mockImgMgr := new(MockImageManager)
	s.srv.imageManager = mockImgMgr

	expectedStatus := container.ImageBuildStatus{
		State: "building",
		Phase: "building",
	}
	expectedVersions := container.ImageVersions{}

	mockImgMgr.On("Status").Return(expectedStatus)
	mockImgMgr.On("Versions").Return(expectedVersions)
	mockImgMgr.On("UpdateAvailable").Return((*events.ImageUpdateAvailableData)(nil))

	s.mux.HandleFunc("GET /api/image/status", s.srv.handleImageStatus)
	rec := s.testRequest("GET", "/api/image/status", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp imageStatusResponse
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "building", resp.Status.State)
	require.Equal(s.T(), "building", resp.Status.Phase)

	mockImgMgr.AssertExpectations(s.T())
}

func (s *ServerSuite) TestImageStatusNotConfigured() {
	// imageManager is nil by default in SetupTest — do not set it.
	s.mux.HandleFunc("GET /api/image/status", s.srv.handleImageStatus)
	rec := s.testRequest("GET", "/api/image/status", "")

	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "image management not configured")
}

// --- POST /api/image/rebuild ---

func (s *ServerSuite) TestImageRebuildSuccess() {
	mockImgMgr := new(MockImageManager)
	s.srv.imageManager = mockImgMgr

	mockImgMgr.On("Rebuild", mock.Anything).Return(nil)

	s.mux.HandleFunc("POST /api/image/rebuild", s.srv.handleImageRebuild)
	rec := s.testRequest("POST", "/api/image/rebuild", "")

	require.Equal(s.T(), http.StatusAccepted, rec.Code)
	mockImgMgr.AssertExpectations(s.T())
}

func (s *ServerSuite) TestImageRebuildConflict() {
	mockImgMgr := new(MockImageManager)
	s.srv.imageManager = mockImgMgr

	mockImgMgr.On("Rebuild", mock.Anything).Return(errors.New("build already in progress"))

	s.mux.HandleFunc("POST /api/image/rebuild", s.srv.handleImageRebuild)
	rec := s.testRequest("POST", "/api/image/rebuild", "")

	require.Equal(s.T(), http.StatusConflict, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "build already in progress")
	mockImgMgr.AssertExpectations(s.T())
}

func (s *ServerSuite) TestImageRebuildNotConfigured() {
	// imageManager is nil by default in SetupTest — do not set it.
	s.mux.HandleFunc("POST /api/image/rebuild", s.srv.handleImageRebuild)
	rec := s.testRequest("POST", "/api/image/rebuild", "")

	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "image management not configured")
}

// --- DELETE /api/image ---

func (s *ServerSuite) TestImageRemoveSuccess() {
	mockImgMgr := new(MockImageManager)
	s.srv.imageManager = mockImgMgr

	mockImgMgr.On("RemoveImage", mock.Anything).Return(nil)

	s.mux.HandleFunc("DELETE /api/image", s.srv.handleImageRemove)
	rec := s.testRequest("DELETE", "/api/image", "")

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	mockImgMgr.AssertExpectations(s.T())
}

func (s *ServerSuite) TestImageRemoveError() {
	mockImgMgr := new(MockImageManager)
	s.srv.imageManager = mockImgMgr

	mockImgMgr.On("RemoveImage", mock.Anything).Return(errors.New("removal failed"))

	s.mux.HandleFunc("DELETE /api/image", s.srv.handleImageRemove)
	rec := s.testRequest("DELETE", "/api/image", "")

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "removal failed")
	mockImgMgr.AssertExpectations(s.T())
}

func (s *ServerSuite) TestImageRemoveNotConfigured() {
	// imageManager is nil by default in SetupTest — do not set it.
	s.mux.HandleFunc("DELETE /api/image", s.srv.handleImageRemove)
	rec := s.testRequest("DELETE", "/api/image", "")

	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "image management not configured")
}

// --- GET /api/image/reclaimable ---

func (s *ServerSuite) TestImageReclaimable() {
	tests := []struct {
		name     string
		mgr      bool
		query    string
		sized    bool
		estimate container.Reclaimable
		err      error
		wantCode int
		wantBody string
	}{
		{name: "volumes sized", mgr: true, query: "?volume_sizes=true", sized: true, estimate: container.Reclaimable{VolumesSized: true, AnonymousVolumes: 5}, wantCode: http.StatusOK},
		{
			name: "estimate", mgr: true, wantCode: http.StatusOK,
			estimate: container.Reclaimable{BuildCache: 4096, UnusedImages: 2048, UnusedImageTags: []string{"old:1"}, OrphanVolumes: 10, OrphanVolumeList: []string{"loop-chrome-profile-gone"}},
			wantBody: `"unused_image_tags":["old:1"]`,
		},
		{name: "daemon error", mgr: true, err: errors.New("daemon down"), wantCode: http.StatusInternalServerError, wantBody: "daemon down"},
		{name: "not configured", wantCode: http.StatusNotImplemented, wantBody: "image management not configured"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			mgr := new(MockImageManager)
			if tt.mgr {
				s.srv.imageManager = mgr
				mgr.On("Reclaimable", mock.Anything, tt.sized).Return(tt.estimate, tt.err)
			}
			s.mux.HandleFunc("GET /api/image/reclaimable", s.srv.handleImageReclaimable)
			rec := s.testRequest("GET", "/api/image/reclaimable"+tt.query, "")

			require.Equal(s.T(), tt.wantCode, rec.Code)
			require.Contains(s.T(), rec.Body.String(), tt.wantBody)
			if tt.wantCode == http.StatusOK {
				var resp container.Reclaimable
				require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
				require.Equal(s.T(), tt.estimate, resp)
			}
			mgr.AssertExpectations(s.T())
		})
	}
}

// --- POST /api/image/reclaim ---

func (s *ServerSuite) TestImageReclaim() {
	result := container.ReclaimResult{BuildCacheReclaimed: 4096, ImagesReclaimed: 8192, UnusedImagesReclaimed: 1, VolumesReclaimed: 2, TotalReclaimed: 12291, OrphanVolumesRemoved: 3}
	tests := []struct {
		name     string
		mgr      bool
		body     string
		opts     *container.ReclaimOptions // nil: ReclaimSpace isn't called
		err      error
		wantCode int
		wantBody string
	}{
		{name: "no body", mgr: true, opts: &container.ReclaimOptions{}, wantCode: http.StatusOK},
		{name: "unused images opted in", mgr: true, body: `{"unused_images":true}`, opts: &container.ReclaimOptions{UnusedImages: true}, wantCode: http.StatusOK},
		{name: "invalid body", mgr: true, body: `{`, wantCode: http.StatusBadRequest, wantBody: "invalid request body"},
		{name: "daemon error", mgr: true, opts: &container.ReclaimOptions{}, err: errors.New("daemon down"), wantCode: http.StatusInternalServerError, wantBody: "daemon down"},
		{name: "not configured", wantCode: http.StatusNotImplemented, wantBody: "image management not configured"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			mgr := new(MockImageManager)
			if tt.mgr {
				s.srv.imageManager = mgr
			}
			if tt.opts != nil {
				mgr.On("ReclaimSpace", mock.Anything, *tt.opts).Return(result, tt.err)
			}
			s.mux.HandleFunc("POST /api/image/reclaim", s.srv.handleImageReclaim)
			rec := s.testRequest("POST", "/api/image/reclaim", tt.body)

			require.Equal(s.T(), tt.wantCode, rec.Code)
			require.Contains(s.T(), rec.Body.String(), tt.wantBody)
			if tt.wantCode == http.StatusOK {
				var resp container.ReclaimResult
				require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
				require.Equal(s.T(), result, resp)
			}
			mgr.AssertExpectations(s.T())
		})
	}
}
