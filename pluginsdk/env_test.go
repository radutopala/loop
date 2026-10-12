package pluginsdk

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type EnvSuite struct {
	suite.Suite
}

func TestEnvSuite(t *testing.T) {
	suite.Run(t, new(EnvSuite))
}

func (s *EnvSuite) TestLoadEnv() {
	vars := map[string]string{
		"LOOP_PLUGIN_ID":          "cogtest",
		"LOOP_CHANNEL_ID":         "ch1",
		"LOOP_CHANNEL_DIR":        "/src",
		"LOOP_PLUGIN_PORT":        "8080",
		"LOOP_API_URL":            "http://loop:8222",
		"LOOP_PLUGIN_TOKEN_FILE":  "/run/loop/plugin-token",
		"LOOP_PLUGIN_SECRET_FILE": "/run/loop/secret",
		"LOOP_DATA_DIR":           "/var/data",
	}
	env := LoadEnv(func(k string) string { return vars[k] })
	require.Equal(s.T(), Env{
		PluginID: "cogtest", ChannelID: "ch1", ChannelDir: "/src", Port: "8080", APIURL: "http://loop:8222",
		TokenFile: "/run/loop/plugin-token", SecretFile: "/run/loop/secret", DataDir: "/var/data",
	}, env)
	require.Equal(s.T(), &Client{BaseURL: "http://loop:8222", TokenFile: "/run/loop/plugin-token", HTTP: &http.Client{}, Timeout: 20 * time.Second}, env.Client())

	require.Equal(s.T(), "/data", LoadEnv(func(string) string { return "" }).DataDir)
}

func (s *EnvSuite) TestSecret() {
	dir := s.T().TempDir()
	secret := filepath.Join(dir, "secret")
	require.NoError(s.T(), os.WriteFile(secret, []byte(" sekret\n"), 0o600))
	got, err := Env{SecretFile: secret}.Secret()
	require.NoError(s.T(), err)
	require.Equal(s.T(), "sekret", got)

	_, err = Env{SecretFile: filepath.Join(dir, "none")}.Secret()
	require.ErrorContains(s.T(), err, "reading the secret")
}

func (s *EnvSuite) TestServe() {
	ln, err := Env{Port: "0"}.Listen()
	require.NoError(s.T(), err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ln, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d", port))
	require.NoError(s.T(), err)
	_ = resp.Body.Close()
	require.Equal(s.T(), http.StatusTeapot, resp.StatusCode)

	cancel()
	require.NoError(s.T(), <-done)
}

func (s *EnvSuite) TestServeFails() {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(s.T(), err)
	require.NoError(s.T(), ln.Close())
	err = Serve(context.Background(), ln, http.NotFoundHandler())
	require.ErrorIs(s.T(), err, net.ErrClosed)

	_, err = Env{Port: "x"}.Listen()
	require.Error(s.T(), err)
}
