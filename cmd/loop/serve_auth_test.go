package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/apiauth"
	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/testutil"
)

// tokenStore is a MockStore that also stores API tokens.
type tokenStore struct {
	*testutil.MockStore
}

func (t tokenStore) InsertAPIToken(ctx context.Context, tok *db.APIToken) error {
	return t.Called(ctx, tok).Error(0)
}

func (t tokenStore) DeleteAPITokens(ctx context.Context, containerID string) error {
	return t.Called(ctx, containerID).Error(0)
}

func (t tokenStore) ListAPITokens(ctx context.Context) ([]*db.APIToken, error) {
	args := t.Called(ctx)
	v, _ := args.Get(0).([]*db.APIToken)
	return v, args.Error(1)
}

// runServe runs serve until it's ready, then stops it.
func (s *MainSuite) runServe() {
	errCh := make(chan error, 1)
	go func() { errCh <- s.app.serve() }()
	s.waitForServeReady(errCh)
	p, err := os.FindProcess(os.Getpid())
	require.NoError(s.T(), err)
	require.NoError(s.T(), p.Signal(syscall.SIGINT))
	select {
	case err := <-errCh:
		require.NoError(s.T(), err)
	case <-time.After(10 * time.Second):
		s.T().Fatal("serve() did not return in time")
	}
}

func (s *MainSuite) TestServeRestoresAgentTokens() {
	tests := []struct {
		name     string
		infos    []*container.ContainerInfo
		listErr  error
		wantKept []string
	}{
		{"prunes containers that are gone", []*container.ContainerInfo{{ContainerID: "live", Status: container.ContainerStatusRunning}}, nil, []string{"live"}},
		{"keeps all when docker can't be listed", nil, errors.New("docker unavailable"), []string{"gone", "live"}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			m := s.setupServeMocks()
			m.setupHappyBot()
			m.dockerClient.ExpectedCalls = filterExpected(m.dockerClient.ExpectedCalls, "ListContainerInfos")
			m.dockerClient.On("ListContainerInfos", mock.Anything).Return(tc.infos, tc.listErr)
			m.dockerClient.On("ContainerRemove", mock.Anything, mock.Anything).Return(nil).Maybe()
			dbPath := filepath.Join(s.T().TempDir(), "loop.db")
			m.cfg.LoopDir = s.T().TempDir()
			m.store = nil
			s.app.newSQLiteStore = func(path string) (db.Store, error) { return db.NewSQLiteStore(path) }
			m.cfg.DBPath = dbPath
			seed, err := db.NewSQLiteStore(dbPath)
			require.NoError(s.T(), err)
			for _, id := range []string{"gone", "live"} {
				require.NoError(s.T(), seed.InsertAPIToken(context.Background(), &db.APIToken{Hash: "h-" + id, ContainerID: id, ChannelID: "ch"}))
			}
			require.NoError(s.T(), seed.Close())

			s.runServe()

			store, err := db.NewSQLiteStore(dbPath)
			require.NoError(s.T(), err)
			defer store.Close()
			toks, err := store.ListAPITokens(context.Background())
			require.NoError(s.T(), err)
			var kept []string
			for _, t := range toks {
				kept = append(kept, t.ContainerID)
			}
			require.ElementsMatch(s.T(), tc.wantKept, kept)
		})
	}
}

func (s *MainSuite) TestServeAPIAuthError() {
	m := s.setupServeMocks()
	m.setupHappyBot()
	s.app.userConfigDir = func() (string, error) { return "", errors.New("no home") }
	require.ErrorContains(s.T(), s.app.serve(), "no home")
}

func (s *MainSuite) TestNewAPIAuth() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	all := func(string) bool { return true }

	s.Run("creates the owner token", func() {
		dir := s.T().TempDir()
		s.app.userConfigDir = func() (string, error) { return dir, nil }
		auth, err := s.app.newAPIAuth(context.Background(), new(testutil.MockStore), all, logger)
		require.NoError(s.T(), err)
		require.Equal(s.T(), filepath.Join(dir, "loop"), auth.tokenDir)
		saved, err := apiauth.NewTokenFile(filepath.Join(dir, "loop", "api-token")).Load()
		require.NoError(s.T(), err)
		require.Equal(s.T(), saved, auth.deps.OwnerToken)
		require.NotNil(s.T(), auth.deps.Caps)
		require.Same(s.T(), auth.agents, auth.deps.Agents)

		rotated, err := auth.deps.RotateOwnerToken()
		require.NoError(s.T(), err)
		require.NotEqual(s.T(), saved, rotated)
	})
	s.Run("token file error", func() {
		dir := s.T().TempDir()
		require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "loop"), nil, 0o600))
		s.app.userConfigDir = func() (string, error) { return dir, nil }
		_, err := s.app.newAPIAuth(context.Background(), new(testutil.MockStore), all, logger)
		require.ErrorContains(s.T(), err, "loading the API token")
	})
	s.Run("signer error", func() {
		dir := s.T().TempDir()
		s.app.userConfigDir = func() (string, error) { return dir, nil }
		s.app.newSigner = func() (*apiauth.Signer, error) { return nil, errors.New("no entropy") }
		_, err := s.app.newAPIAuth(context.Background(), new(testutil.MockStore), all, logger)
		require.ErrorContains(s.T(), err, "no entropy")
		s.app.newSigner = apiauth.NewSigner
	})
	s.Run("restoring agent tokens fails", func() {
		dir := s.T().TempDir()
		s.app.userConfigDir = func() (string, error) { return dir, nil }
		store := tokenStore{new(testutil.MockStore)}
		store.On("ListAPITokens", mock.Anything).Return(nil, errors.New("db down"))
		auth, err := s.app.newAPIAuth(context.Background(), store, all, logger)
		require.NoError(s.T(), err, "the daemon still starts")
		require.NotNil(s.T(), auth.agents)
		store.AssertExpectations(s.T())
	})
}

func (s *MainSuite) TestAgentTokens() {
	store := tokenStore{new(testutil.MockStore)}
	store.On("InsertAPIToken", mock.Anything, mock.Anything).Return(nil)
	store.On("DeleteAPITokens", mock.Anything, "c1").Return(nil).Once()
	store.On("DeleteAPITokens", mock.Anything, "c2").Return(errors.New("db down")).Once()
	var logs bytes.Buffer
	reg := apiauth.NewRegistry(store)
	issuer := agentTokens{agents: reg, logger: slog.New(slog.NewTextHandler(&logs, nil))}

	tok, err := issuer.Issue("c1", "ch1", "/proj")
	require.NoError(s.T(), err)
	p, ok := reg.Lookup(tok)
	require.True(s.T(), ok)
	require.Equal(s.T(), "ch1", p.ChannelID)

	issuer.Revoke("c1")
	_, ok = reg.Lookup(tok)
	require.False(s.T(), ok)
	require.Empty(s.T(), logs.String())

	issuer.Revoke("c2")
	require.Contains(s.T(), logs.String(), "revoking agent API token failed")
	store.AssertExpectations(s.T())
}
