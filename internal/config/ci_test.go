package config

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type CIConfigSuite struct {
	suite.Suite
}

func TestCIConfigSuite(t *testing.T) {
	suite.Run(t, new(CIConfigSuite))
}

func (s *CIConfigSuite) TestLoad() {
	off := false
	defaults := CIConfig{AutoFix: true, MaxAttempts: DefaultCIMaxAttempts, PollSec: DefaultCIPollSec}
	tests := []struct {
		name string
		json string
		want CIConfig
	}{
		{"unset", ``, defaults},
		{"empty block", `, "ci": {}`, defaults},
		{"every field", `, "ci": {"auto_fix": false, "max_attempts": 5, "merge_method": "rebase", "poll_sec": 10}`,
			CIConfig{AutoFix: off, MaxAttempts: 5, MergeMethod: "rebase", PollSec: 10}},
		{"invalid values keep the defaults", `, "ci": {"max_attempts": -1, "merge_method": "octopus", "poll_sec": 0}`, defaults},
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
			require.Equal(s.T(), tc.want, cfg.CI)
		})
	}
}

func (s *CIConfigSuite) TestOverlayNil() {
	c := CIConfig{AutoFix: true, MaxAttempts: 2}
	require.Equal(s.T(), c, c.overlay(nil))
}

func (s *CIConfigSuite) TestTrustedOnlyWhenSet() {
	pc, err := parseProjectConfig([]byte(`{"ci": {}}`))
	require.NoError(s.T(), err)
	require.Nil(s.T(), trustedFieldsOf(pc).CI)

	pc, err = parseProjectConfig([]byte(`{"ci": {"poll_sec": 5}}`))
	require.NoError(s.T(), err)
	require.Equal(s.T(), &jsonCIConfig{PollSec: 5}, trustedFieldsOf(pc).CI)
}
