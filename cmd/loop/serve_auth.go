package main

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/radutopala/loop/internal/api"
	"github.com/radutopala/loop/internal/apiauth"
	"github.com/radutopala/loop/internal/db"
)

// apiAuth is what serve needs to authenticate API callers.
type apiAuth struct {
	deps   api.AuthDeps
	agents *apiauth.Registry
	// tokenDir holds the owner token. No channel dir or container mount may
	// be it or contain it.
	tokenDir string
}

// newAPIAuth loads (or creates) the owner token and restores the agent
// tokens of containers that are still around; alive reports those.
func (a *app) newAPIAuth(ctx context.Context, store db.Store, alive func(containerID string) bool, logger *slog.Logger) (*apiAuth, error) {
	path, err := apiauth.OwnerTokenPath(a.userConfigDir)
	if err != nil {
		return nil, err
	}
	owner := apiauth.NewTokenFile(path)
	tok, err := owner.LoadOrCreate()
	if err != nil {
		return nil, fmt.Errorf("loading the API token: %w", err)
	}
	caps, err := a.newSigner()
	if err != nil {
		return nil, fmt.Errorf("creating the content link key: %w", err)
	}
	tokenStore, _ := store.(apiauth.Store)
	agents := apiauth.NewRegistry(tokenStore)
	if err := agents.Load(ctx, alive); err != nil {
		// Running agents lose API access until their next container.
		logger.Warn("restoring agent API tokens failed", "error", err)
	}
	return &apiAuth{
		deps: api.AuthDeps{
			OwnerToken:       tok,
			RotateOwnerToken: owner.Rotate,
			Agents:           agents,
			Caps:             caps,
		},
		agents:   agents,
		tokenDir: filepath.Dir(path),
	}, nil
}

// agentTokens issues each agent container its API token; the runner revokes
// it when the container goes.
type agentTokens struct {
	agents *apiauth.Registry
	logger *slog.Logger
}

func (t agentTokens) Issue(containerID, channelID, dirPath string) (string, error) {
	return t.agents.Issue(containerID, channelID, dirPath)
}

func (t agentTokens) Revoke(containerID string) {
	if err := t.agents.Revoke(containerID); err != nil {
		// The token is out of memory already; the stale row is pruned at the
		// next start.
		t.logger.Warn("revoking agent API token failed", "container", containerID, "error", err)
	}
}
