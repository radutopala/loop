package api

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/config"
)

type ConfigResolverSuite struct {
	suite.Suite
}

func TestConfigResolverSuite(t *testing.T) {
	suite.Run(t, new(ConfigResolverSuite))
}

// TestWithConfigLoaders: the server resolves each layer with the loaders
// it was given.
func (s *ConfigResolverSuite) TestWithConfigLoaders() {
	srv := &Server{}
	WithConfigLoaders(
		func() (*config.Config, error) { return &config.Config{ClaudeModel: "global"}, nil },
		func(dir string, _ *config.Config) (*config.Config, error) {
			return &config.Config{ClaudeModel: "project " + dir}, nil
		},
		func(dir, parent string, _ *config.Config) (*config.Config, error) {
			return &config.Config{ClaudeModel: "worktree " + dir + " of " + parent}, nil
		},
	)(srv)

	tests := []struct {
		name, dir, parent, want string
	}{
		{"global", "", "", "global"},
		{"project", "/p", "", "project /p"},
		{"worktree", "/w", "/p", "worktree /w of /p"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, srv.configs.merged(tc.dir, tc.parent).ClaudeModel)
		})
	}
}
