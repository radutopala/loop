//go:build integration

package plugin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	dockerclient "github.com/docker/docker/client"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/apiauth"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin/install"
	"github.com/radutopala/loop/internal/plugin/proxy"
	"github.com/radutopala/loop/internal/plugin/runtime"
)

// echoImage is the fixture plugin, built from
// test/component/testdata/plugins/echo by make test-integration-plugin.
const echoImage = "loop-plugin-dev/echo:latest"

type IntegrationSuite struct {
	suite.Suite
	ctx     context.Context
	docker  *dockerclient.Client
	store   *db.SQLiteStore
	runtime *runtime.Manager
	service *install.Service
}

func TestIntegrationSuite(t *testing.T) {
	suite.Run(t, new(IntegrationSuite))
}

func (s *IntegrationSuite) SetupTest() {
	s.ctx = context.Background()
	var err error
	s.docker, err = dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	require.NoError(s.T(), err)
	s.store, err = db.NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)

	root := filepath.Join(s.T().TempDir(), "plugins")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s.runtime = runtime.NewManager(s.docker, s.store, apiauth.NewRegistry(nil), runtime.Config{
		Root:        root,
		APIURL:      "http://host.docker.internal:8222",
		IdleTimeout: time.Minute,
		StartWait:   30 * time.Second,
	}, logger)
	installer := install.NewInstaller(s.docker, s.store, root, "dev")
	s.service = install.NewService(installer, s.runtime, s.store, proxy.NewClient(http.DefaultClient))
}

func (s *IntegrationSuite) TearDownTest() {
	ctx := context.WithoutCancel(s.ctx)
	_ = s.service.Remove(ctx, "echo", true)
	s.runtime.StopAll(ctx)
	_ = s.store.Close()
	_ = s.docker.Close()
}

// call sends body to path on the plugin at ep with secret and returns the
// status and decoded answer.
func (s *IntegrationSuite) call(ep runtime.Endpoint, secret, method, path string, body any) (int, map[string]any) {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(s.T(), err)
		r = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(s.ctx, method, ep.URL+path, r)
	require.NoError(s.T(), err)
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(s.T(), err)
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (s *IntegrationSuite) TestInstallRunRemove() {
	c, err := s.service.Install(s.ctx, echoImage, true, "", nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "echo", c.Manifest.ID)

	ch := runtime.Channel{ID: "ch1", DirPath: s.T().TempDir()}
	ep, err := s.runtime.Ensure(s.ctx, runtime.Plugin{Manifest: c.Manifest, Image: c.Image}, ch)
	require.NoError(s.T(), err)

	status, health := s.call(ep, ep.Secret, http.MethodGet, "/healthz", nil)
	require.Equal(s.T(), http.StatusOK, status)
	require.Equal(s.T(), "1", health["api"])

	status, _ = s.call(ep, "wrong", http.MethodGet, "/healthz", nil)
	require.Equal(s.T(), http.StatusUnauthorized, status)

	status, out := s.call(ep, ep.Secret, http.MethodPost, "/tools/echo", map[string]any{"input": map[string]string{"text": "hi"}})
	require.Equal(s.T(), http.StatusOK, status)
	require.Equal(s.T(), map[string]any{"content": "hi", "is_error": false}, out)

	// A second Ensure reuses the running container.
	again, err := s.runtime.Ensure(s.ctx, runtime.Plugin{Manifest: c.Manifest, Image: c.Image}, ch)
	require.NoError(s.T(), err)
	require.Equal(s.T(), ep, again)

	// A manual action goes through the service to the running container.
	require.NoError(s.T(), s.service.RunAction(s.ctx, "echo", "ping", ch, proxy.ActionRequest{Trigger: proxy.TriggerManual}))
	require.ErrorIs(s.T(), s.service.RunAction(s.ctx, "echo", "nope", ch, proxy.ActionRequest{Trigger: proxy.TriggerManual}), install.ErrUnknownAction)

	require.NoError(s.T(), s.service.Remove(s.ctx, "echo", true))
	_, err = s.docker.ContainerInspect(s.ctx, runtime.ContainerName("echo", ch.ID))
	require.True(s.T(), dockerclient.IsErrNotFound(err), "container still exists: %v", err)
	// --purge removes the channel's data volume too.
	_, err = s.docker.VolumeInspect(s.ctx, runtime.ContainerName("echo", ch.ID))
	require.True(s.T(), dockerclient.IsErrNotFound(err), "volume still exists: %v", err)
}
