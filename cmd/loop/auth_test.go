package main

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/apiauth"
	"github.com/radutopala/loop/internal/config"
)

func (s *MainSuite) ownerTokenFile() *apiauth.TokenFile {
	dir, err := s.app.userConfigDir()
	require.NoError(s.T(), err)
	return apiauth.NewTokenFile(filepath.Join(dir, "loop", "api-token"))
}

func (s *MainSuite) useAPI(h http.HandlerFunc) {
	srv := httptest.NewServer(h)
	s.T().Cleanup(srv.Close)
	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{APIAddr: srv.Listener.Addr().String()}, nil
	}
}

func (s *MainSuite) TestRotateAPIToken() {
	s.Run("by the daemon", func() {
		old, err := s.ownerTokenFile().LoadOrCreate()
		require.NoError(s.T(), err)
		var gotAuth string
		s.useAPI(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(s.T(), "/api/auth/rotate", r.URL.Path)
			gotAuth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusNoContent)
		})
		s.app.apiClient = &http.Client{Transport: &apiauth.Transport{Token: func() string { return old }}}
		var out bytes.Buffer
		cmd := s.app.newAPIRotateTokenCmd()
		cmd.SetOut(&out)
		cmd.SetArgs([]string{})
		require.NoError(s.T(), cmd.Execute())
		require.Equal(s.T(), "Bearer "+old, gotAuth)
		require.Equal(s.T(), "API token rotated\n", out.String())
	})
	s.Run("daemon refuses", func() {
		s.useAPI(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "nope", http.StatusUnauthorized)
		})
		err := s.app.rotateAPIToken(&bytes.Buffer{})
		require.ErrorContains(s.T(), err, "401 Unauthorized: nope")
	})
	s.Run("daemon down", func() {
		s.app.configLoad = func() (*config.Config, error) { return &config.Config{APIAddr: "127.0.0.1:1"}, nil }
		old, err := s.ownerTokenFile().LoadOrCreate()
		require.NoError(s.T(), err)
		var out bytes.Buffer
		require.NoError(s.T(), s.app.rotateAPIToken(&out))
		require.Contains(s.T(), out.String(), "the daemon isn't running")
		now, err := s.ownerTokenFile().Load()
		require.NoError(s.T(), err)
		require.NotEqual(s.T(), old, now)
	})
	s.Run("daemon down, token file unwritable", func() {
		s.app.configLoad = func() (*config.Config, error) { return &config.Config{APIAddr: "127.0.0.1:1"}, nil }
		dir := s.T().TempDir()
		require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "loop"), nil, 0o600))
		s.app.userConfigDir = func() (string, error) { return dir, nil }
		require.ErrorContains(s.T(), s.app.rotateAPIToken(&bytes.Buffer{}), "rotating the API token")
	})
	s.Run("daemon down, no config dir", func() {
		s.app.configLoad = func() (*config.Config, error) { return &config.Config{APIAddr: "127.0.0.1:1"}, nil }
		s.app.userConfigDir = func() (string, error) { return "", errors.New("no home") }
		require.ErrorContains(s.T(), s.app.rotateAPIToken(&bytes.Buffer{}), "no home")
	})
}

func (s *MainSuite) TestAppURL() {
	s.Run("no token yet", func() {
		cmd := s.app.newAppURLCmd()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetArgs([]string{})
		require.ErrorContains(s.T(), cmd.Execute(), "start the daemon")
	})
	s.Run("prints the url", func() {
		tok, err := s.ownerTokenFile().LoadOrCreate()
		require.NoError(s.T(), err)
		var out bytes.Buffer
		cmd := s.app.newAppURLCmd()
		cmd.SetOut(&out)
		cmd.SetArgs([]string{"--base", "http://localhost:9000/"})
		require.NoError(s.T(), cmd.Execute())
		require.Equal(s.T(), "http://localhost:9000/#loop_token="+tok+"\n", out.String())
	})
	s.Run("no config dir", func() {
		s.app.userConfigDir = func() (string, error) { return "", errors.New("no home") }
		require.ErrorContains(s.T(), s.app.printAppURL(&bytes.Buffer{}, "x"), "no home")
	})
}
