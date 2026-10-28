package config

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type PRConfigSuite struct {
	suite.Suite
}

func TestPRConfigSuite(t *testing.T) {
	suite.Run(t, new(PRConfigSuite))
}

func (s *PRConfigSuite) TestLoad() {
	off := false
	defaults := DefaultPRConfig()
	tests := []struct {
		name string
		json string
		want PRConfig
	}{
		{"unset", ``, defaults},
		{"empty block", `, "pr": {}`, defaults},
		{"every field", `, "pr": {"auto_fix": false, "max_attempts": 5, "merge_method": "rebase", "poll_sec": 10, "idle_poll_sec": 60}`,
			PRConfig{AutoFix: off, MaxAttempts: 5, MergeMethod: "rebase", PollSec: 10, IdlePollSec: 60}},
		{"invalid values keep the defaults", `, "pr": {"max_attempts": -1, "merge_method": "octopus", "poll_sec": 0, "idle_poll_sec": -5}`, defaults},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			l := &Loader{
				userHomeDir: func() (string, error) { return "/home/testuser", nil },
				readFile: func(string) ([]byte, error) {
					return []byte(`{"platforms": ["discord"], "discord_token": "t", "discord_app_id": "a"` + tc.json + `}`), nil
				},
			}
			cfg, err := l.load()
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.want, cfg.PR)
		})
	}
}

func (s *PRConfigSuite) TestOverlayNil() {
	c := PRConfig{AutoFix: true, MaxAttempts: 2}
	require.Equal(s.T(), c, c.overlay(nil))
}

func (s *PRConfigSuite) TestTrustedOnlyWhenSet() {
	pc, err := parseProjectConfig([]byte(`{"pr": {}}`))
	require.NoError(s.T(), err)
	require.Nil(s.T(), trustedFieldsOf(pc).PR)

	pc, err = parseProjectConfig([]byte(`{"pr": {"poll_sec": 5}}`))
	require.NoError(s.T(), err)
	require.Equal(s.T(), &jsonPRConfig{PollSec: 5}, trustedFieldsOf(pc).PR)
}
