package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
)

// --- image:rebuild tests ---

func (s *MainSuite) TestNewImageRebuildCmd() {
	cmd := s.app.newImageRebuildCmd()
	require.Equal(s.T(), "image:rebuild", cmd.Use)
	require.Equal(s.T(), []string{"i:rebuild", "i:r"}, cmd.Aliases)
	require.NotNil(s.T(), cmd.RunE)
}

func (s *MainSuite) TestImageRebuildHappyPath() {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/image/rebuild":
			w.WriteHeader(http.StatusAccepted)
		case r.Method == "GET" && r.URL.Path == "/api/image/status":
			callCount++
			resp := imageStatusJSON{}
			resp.Status.State = "completed"
			resp.Versions.LoopVersion = "1.2.3"
			resp.Versions.ClaudeVersion = "4.0.0"
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
	defer srv.Close()

	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{APIAddr: srv.Listener.Addr().String()}, nil
	}

	cmd := s.app.newImageRebuildCmd()
	err := cmd.Execute()
	require.NoError(s.T(), err)
}

func (s *MainSuite) TestImageRebuildPolling() {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/image/rebuild":
			w.WriteHeader(http.StatusAccepted)
		case r.Method == "GET" && r.URL.Path == "/api/image/status":
			callCount++
			resp := imageStatusJSON{}
			if callCount <= 1 {
				resp.Status.State = "building"
				resp.Status.Phase = "building"
			} else {
				resp.Status.State = "completed"
				resp.Versions.LoopVersion = "1.0.0"
				resp.Versions.ClaudeVersion = "2.0.0"
			}
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
	defer srv.Close()

	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{APIAddr: srv.Listener.Addr().String()}, nil
	}

	cmd := s.app.newImageRebuildCmd()
	err := cmd.Execute()
	require.NoError(s.T(), err)
	require.GreaterOrEqual(s.T(), callCount, 2)
}

func (s *MainSuite) TestImageRebuildAPIError() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{APIAddr: srv.Listener.Addr().String()}, nil
	}

	cmd := s.app.newImageRebuildCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "rebuild failed")
}

func (s *MainSuite) TestImageRebuildConflict() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer srv.Close()

	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{APIAddr: srv.Listener.Addr().String()}, nil
	}

	cmd := s.app.newImageRebuildCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "already in progress")
}

func (s *MainSuite) TestImageRebuildBuildFailed() {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/image/rebuild":
			w.WriteHeader(http.StatusAccepted)
		case r.Method == "GET" && r.URL.Path == "/api/image/status":
			callCount++
			resp := imageStatusJSON{}
			resp.Status.State = "failed"
			resp.Status.Error = "build exploded"
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
	defer srv.Close()

	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{APIAddr: srv.Listener.Addr().String()}, nil
	}

	cmd := s.app.newImageRebuildCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "build exploded")
}

// --- image:status tests ---

func (s *MainSuite) TestNewImageStatusCmd() {
	cmd := s.app.newImageStatusCmd()
	require.Equal(s.T(), "image:status", cmd.Use)
	require.Equal(s.T(), []string{"i:status", "i:s"}, cmd.Aliases)
	require.NotNil(s.T(), cmd.RunE)
}

func (s *MainSuite) TestImageStatusSuccess() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := imageStatusJSON{}
		resp.Status.State = "idle"
		resp.Versions.LoopVersion = "1.2.3"
		resp.Versions.ClaudeVersion = "4.0.0"
		resp.Versions.BuiltAt = "2026-03-28T10:00:00Z"
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{APIAddr: srv.Listener.Addr().String()}, nil
	}

	cmd := s.app.newImageStatusCmd()
	err := cmd.Execute()
	require.NoError(s.T(), err)
}

func (s *MainSuite) TestImageStatusWithUpdate() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := imageStatusJSON{}
		resp.Status.State = "idle"
		resp.Versions.LoopVersion = "1.2.3"
		resp.Versions.ClaudeVersion = "4.0.0"
		resp.UpdateAvailable = &struct {
			CurrentVersion string `json:"current_version"`
			LatestVersion  string `json:"latest_version"`
			Component      string `json:"component"`
		}{"4.0.0", "5.0.0", "claude_code"}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{APIAddr: srv.Listener.Addr().String()}, nil
	}

	cmd := s.app.newImageStatusCmd()
	err := cmd.Execute()
	require.NoError(s.T(), err)
}

func (s *MainSuite) TestImageStatusAPIError() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not configured", http.StatusNotImplemented)
	}))
	defer srv.Close()

	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{APIAddr: srv.Listener.Addr().String()}, nil
	}

	cmd := s.app.newImageStatusCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "image status failed")
}

func (s *MainSuite) TestImageStatusBadJSON() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{APIAddr: srv.Listener.Addr().String()}, nil
	}

	cmd := s.app.newImageStatusCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parsing image status")
}

func (s *MainSuite) TestImageRebuildConnectionError() {
	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{APIAddr: "localhost:1"}, nil
	}

	cmd := s.app.newImageRebuildCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "calling rebuild API")
}

func (s *MainSuite) TestImageStatusConnectionError() {
	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{APIAddr: "localhost:1"}, nil
	}

	cmd := s.app.newImageStatusCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "calling image status API")
}

func (s *MainSuite) TestImageRebuildIdleStatus() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "POST":
			w.WriteHeader(http.StatusAccepted)
		default:
			resp := imageStatusJSON{}
			resp.Status.State = "idle"
			resp.Versions.LoopVersion = "1.0.0"
			resp.Versions.ClaudeVersion = "2.0.0"
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
	defer srv.Close()

	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{APIAddr: srv.Listener.Addr().String()}, nil
	}

	cmd := s.app.newImageRebuildCmd()
	err := cmd.Execute()
	require.NoError(s.T(), err)
}

func (s *MainSuite) TestImageRebuildPollError() {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "POST":
			w.WriteHeader(http.StatusAccepted)
		default:
			callCount++
			if callCount == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("server error"))
			}
		}
	}))
	defer srv.Close()

	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{APIAddr: srv.Listener.Addr().String()}, nil
	}

	cmd := s.app.newImageRebuildCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
}

// --- resolveAPIURL ---

func (s *MainSuite) TestResolveAPIURL_Default() {
	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{}, nil
	}
	require.Equal(s.T(), "http://localhost:8222", s.app.resolveAPIURL())
}

func (s *MainSuite) TestResolveAPIURL_CustomAddr() {
	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{APIAddr: ":9999"}, nil
	}
	require.Equal(s.T(), "http://localhost:9999", s.app.resolveAPIURL())
}

func (s *MainSuite) TestResolveAPIURL_FullAddr() {
	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{APIAddr: "192.168.1.1:8222"}, nil
	}
	require.Equal(s.T(), "http://192.168.1.1:8222", s.app.resolveAPIURL())
}

func (s *MainSuite) TestResolveAPIURL_ConfigError() {
	s.app.configLoad = func() (*config.Config, error) {
		return nil, fmt.Errorf("fail")
	}
	require.Equal(s.T(), "http://localhost:8222", s.app.resolveAPIURL())
}
