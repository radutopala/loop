package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/dictation"
)

type DictationSuite struct {
	suite.Suite
	cfg *config.Config
}

func TestDictationSuite(t *testing.T) { suite.Run(t, new(DictationSuite)) }

func (s *DictationSuite) SetupTest() {
	s.cfg = &config.Config{LoopDir: s.T().TempDir()}
	s.cfg.Dictation = config.DictationConfig{Model: config.DefaultDictationModel, Language: "auto", IdleStopSec: 60}
}

func (s *DictationSuite) newService(reload func() (*config.Config, error)) *dictation.Service {
	svc, server := newDictation(context.Background(), s.cfg, reload, http.DefaultTransport, slog.New(slog.DiscardHandler))
	require.NotNil(s.T(), server)
	require.False(s.T(), server.Running())
	return svc
}

// The model follows the reloaded config, or the startup one when the reload
// fails.
func (s *DictationSuite) TestModelFollowsConfig() {
	reloaded := &config.Config{Dictation: config.DictationConfig{Model: "large-v3-turbo"}}
	require.Equal(s.T(), "large-v3-turbo", s.newService(func() (*config.Config, error) { return reloaded, nil }).Status().Model)

	failing := func() (*config.Config, error) { return nil, errors.New("bad config") }
	st := s.newService(failing).Status()
	require.Equal(s.T(), config.DefaultDictationModel, st.Model)
	require.False(s.T(), st.ModelReady, "nothing downloaded to the temp loop dir")
}

func (s *DictationSuite) TestArgs() {
	args := &dictationArgs{svc: s.newService(func() (*config.Config, error) {
		return &config.Config{Dictation: config.DictationConfig{Threads: 4}}, nil
	})}
	got := args.args(9)
	require.Contains(s.T(), got, "--port")
	require.Equal(s.T(), []string{"-t", "4"}, got[len(got)-2:])
}
