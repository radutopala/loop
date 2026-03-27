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

func (m *MockImageManager) RemoveImage(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

func (m *MockImageManager) Rebuild(ctx context.Context) error {
	return m.Called(ctx).Error(0)
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
