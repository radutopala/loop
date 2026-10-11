package container

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	containertypes "github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/require"
)

// --- latestClaudeVersion tests ---

func (s *ClientSuite) TestLatestClaudeVersionSuccess() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("  1.2.3  "))
	}))
	defer ts.Close()

	s.client.claudeVersionURL = ts.URL

	got := s.client.defaultLatestClaudeVersion()
	require.Equal(s.T(), "1.2.3", got)
}

func (s *ClientSuite) TestLatestClaudeVersionHTTPError() {
	s.client.claudeVersionURL = "http://127.0.0.1:0" // connection refused

	got := s.client.defaultLatestClaudeVersion()
	require.True(s.T(), strings.HasPrefix(got, "unknown-"))
}

func (s *ClientSuite) TestLatestClaudeVersionNon200() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	s.client.claudeVersionURL = ts.URL

	got := s.client.defaultLatestClaudeVersion()
	require.True(s.T(), strings.HasPrefix(got, "unknown-"))
}

func (s *ClientSuite) TestLatestClaudeVersionInvalidURL() {
	s.client.claudeVersionURL = "://bad-url"

	got := s.client.defaultLatestClaudeVersion()
	require.True(s.T(), strings.HasPrefix(got, "unknown-"))
}

// --- ContainerInspect tests ---

func (s *ClientSuite) TestContainerInspect() {
	ctx := context.Background()

	expected := containertypes.InspectResponse{
		ContainerJSONBase: &containertypes.ContainerJSONBase{
			ID:   "cid-1",
			Name: "/my-container",
		},
	}
	s.api.On("ContainerInspect", ctx, "cid-1").Return(expected, nil)

	resp, err := s.client.ContainerInspect(ctx, "cid-1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "cid-1", resp.ID)
	require.Equal(s.T(), "/my-container", resp.Name)
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestContainerInspectError() {
	ctx := context.Background()

	s.api.On("ContainerInspect", ctx, "cid-missing").
		Return(containertypes.InspectResponse{}, errors.New("no such container"))

	_, err := s.client.ContainerInspect(ctx, "cid-missing")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "no such container")
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestContainerRunning() {
	ctx := context.Background()
	tests := []struct {
		name    string
		resp    containertypes.InspectResponse
		err     error
		running bool
		wantErr string
	}{
		{name: "running", resp: containertypes.InspectResponse{ContainerJSONBase: &containertypes.ContainerJSONBase{State: &containertypes.State{Running: true}}}, running: true},
		{name: "stopped", resp: containertypes.InspectResponse{ContainerJSONBase: &containertypes.ContainerJSONBase{State: &containertypes.State{}}}},
		{name: "no state", resp: containertypes.InspectResponse{ContainerJSONBase: &containertypes.ContainerJSONBase{}}},
		{name: "removed", err: fmt.Errorf("No such container: cid-1: %w", cerrdefs.ErrNotFound)},
		{name: "docker down", err: errors.New("cannot connect"), wantErr: "cannot connect"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.api.On("ContainerInspect", ctx, "cid-1").Return(tt.resp, tt.err).Once()
			running, err := s.client.ContainerRunning(ctx, "cid-1")
			if tt.wantErr != "" {
				require.EqualError(s.T(), err, tt.wantErr)
			} else {
				require.NoError(s.T(), err)
			}
			require.Equal(s.T(), tt.running, running)
			s.api.AssertExpectations(s.T())
		})
	}
}
