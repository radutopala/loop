package config

import (
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/types"
)

func gateMainCfg() *Config {
	return &Config{
		Gates: GatesConfig{
			RateLimits: types.RateLimits{Pending: 30, PerMinute: 60, Total: 500},
			Agentgate: AgentgateConfig{
				Enabled:         true,
				DefaultDecision: types.DecisionAllow,
				PathRules:       DefaultGatePathRules(),
				CommandRules:    DefaultGateCommandRules(),
				FileRules:       DefaultGateFileRules(),
			},
			DockerProxy: DockerProxyConfig{
				Enabled:         true,
				DefaultDecision: types.DecisionAllow,
				HTTPRules:       DefaultDockerProxyHTTPRules(),
				BodyRules:       DefaultDockerProxyBodyRules(),
			},
		},
	}
}

func (s *ConfigSuite) TestProjectAgentgateDisables() {
	s.setupProjectReadFile(`{"gates": {"agentgate": {"enabled": false}}}`)

	merged, err := s.loader.loadProjectConfig("/project", gateMainCfg())
	require.NoError(s.T(), err)
	require.False(s.T(), merged.Gates.Agentgate.Enabled)
	require.False(s.T(), merged.Gates.DockerProxy.Enabled, "docker proxy should transitively disable")
}

func (s *ConfigSuite) TestProjectAgentgateCannotReenable() {
	main := gateMainCfg()
	main.Gates.Agentgate.Enabled = false
	main.Gates.DockerProxy.Enabled = false
	s.setupProjectReadFile(`{"gates": {"agentgate": {"enabled": true}}}`)

	merged, err := s.loader.loadProjectConfig("/project", main)
	require.NoError(s.T(), err)
	require.False(s.T(), merged.Gates.Agentgate.Enabled,
		"project cannot re-enable a globally-disabled agentgate (kill-switch)")
}

func (s *ConfigSuite) TestProjectAgentgateRulesPrepend() {
	s.setupProjectReadFile(`{
		"gates": {
			"agentgate": {
				"command_rules": [
					{ "commands": ["npm"], "args_patterns": ["^publish"], "decision": "deny", "message": "no publish" }
				]
			}
		}
	}`)

	main := gateMainCfg()
	globalCount := len(main.Gates.Agentgate.CommandRules)

	merged, err := s.loader.loadProjectConfig("/project", main)
	require.NoError(s.T(), err)
	require.Len(s.T(), merged.Gates.Agentgate.CommandRules, globalCount+1)
	require.Equal(s.T(), []string{"npm"}, merged.Gates.Agentgate.CommandRules[0].Commands,
		"project rules must be prepended so first-match-wins applies to them first")
	require.Equal(s.T(), types.DecisionDeny, merged.Gates.Agentgate.CommandRules[0].Decision)
}

func (s *ConfigSuite) TestProjectAgentgateDefaultDecisionIgnored() {
	s.setupProjectReadFile(`{"gates": {"agentgate": {"default_decision": "approve"}}}`)

	merged, err := s.loader.loadProjectConfig("/project", gateMainCfg())
	require.NoError(s.T(), err)
	require.Equal(s.T(), types.DecisionAllow, merged.Gates.Agentgate.DefaultDecision,
		"project default_decision must be ignored (global wins)")
}

func (s *ConfigSuite) TestProjectGatesRateLimitsIgnored() {
	s.setupProjectReadFile(`{
		"gates": { "rate_limits": { "pending": 1000, "per_minute": 9999, "total": 99999 } }
	}`)

	merged, err := s.loader.loadProjectConfig("/project", gateMainCfg())
	require.NoError(s.T(), err)
	require.Equal(s.T(), 30, merged.Gates.RateLimits.Pending, "project rate_limits must be ignored")
	require.Equal(s.T(), 60, merged.Gates.RateLimits.PerMinute)
	require.Equal(s.T(), 500, merged.Gates.RateLimits.Total)
}

func (s *ConfigSuite) TestProjectGatesAllowRulePrepends() {
	tests := []struct {
		name  string
		json  string
		check func(*Config)
	}{
		{
			name: "command rule",
			json: `{"gates": {"agentgate": {"command_rules": [{"commands":["rm"], "args_patterns":[".*"], "decision":"allow"}]}}}`,
			check: func(c *Config) {
				require.Equal(s.T(), types.DecisionAllow, c.Gates.Agentgate.CommandRules[0].Decision)
				require.Equal(s.T(), []string{"rm"}, c.Gates.Agentgate.CommandRules[0].Commands)
			},
		},
		{
			name: "file rule",
			json: `{"gates": {"agentgate": {"file_rules": [{"paths":["/etc/**"], "operations":["write"], "decision":"allow"}]}}}`,
			check: func(c *Config) {
				require.Equal(s.T(), types.DecisionAllow, c.Gates.Agentgate.FileRules[0].Decision)
				require.Equal(s.T(), []string{"/etc/**"}, c.Gates.Agentgate.FileRules[0].Paths)
			},
		},
		{
			name: "path rule",
			json: `{"gates": {"agentgate": {"path_rules": [{"pattern":"/var/run/docker.sock", "decision":"allow"}]}}}`,
			check: func(c *Config) {
				require.Equal(s.T(), types.DecisionAllow, c.Gates.Agentgate.PathRules[0].Decision)
				require.Equal(s.T(), "/var/run/docker.sock", c.Gates.Agentgate.PathRules[0].Pattern)
			},
		},
		{
			name: "docker proxy http rule",
			json: `{"gates": {"docker_proxy": {"http_rules": [{"methods":["POST"], "paths":["^/x$"], "decision":"allow"}]}}}`,
			check: func(c *Config) {
				require.Equal(s.T(), types.DecisionAllow, c.Gates.DockerProxy.HTTPRules[0].Decision)
				require.Equal(s.T(), []string{"POST"}, c.Gates.DockerProxy.HTTPRules[0].Methods)
			},
		},
	}

	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.setupProjectReadFile(tc.json)
			merged, err := s.loader.loadProjectConfig("/project", gateMainCfg())
			require.NoError(s.T(), err)
			tc.check(merged)
		})
	}
}

func (s *ConfigSuite) TestProjectDockerProxyRulesPrepend() {
	s.setupProjectReadFile(`{
		"gates": {
			"docker_proxy": {
				"http_rules": [
					{ "methods": ["POST"], "paths": ["^/custom$"], "decision": "deny" }
				]
			}
		}
	}`)

	main := gateMainCfg()
	globalCount := len(main.Gates.DockerProxy.HTTPRules)

	merged, err := s.loader.loadProjectConfig("/project", main)
	require.NoError(s.T(), err)
	require.Len(s.T(), merged.Gates.DockerProxy.HTTPRules, globalCount+1)
	require.Equal(s.T(), []string{"POST"}, merged.Gates.DockerProxy.HTTPRules[0].Methods)
	require.Equal(s.T(), types.DecisionDeny, merged.Gates.DockerProxy.HTTPRules[0].Decision)
}

func (s *ConfigSuite) TestProjectAgentgatePathRulesPrepend() {
	s.setupProjectReadFile(`{
		"gates": {
			"agentgate": {
				"path_rules": [
					{ "pattern": "/custom/socket", "decision": "deny", "message": "internal" }
				]
			}
		}
	}`)

	main := gateMainCfg()
	globalCount := len(main.Gates.Agentgate.PathRules)

	merged, err := s.loader.loadProjectConfig("/project", main)
	require.NoError(s.T(), err)
	require.Len(s.T(), merged.Gates.Agentgate.PathRules, globalCount+1)
	require.Equal(s.T(), "/custom/socket", merged.Gates.Agentgate.PathRules[0].Pattern)
	require.Equal(s.T(), types.DecisionDeny, merged.Gates.Agentgate.PathRules[0].Decision)
}

func (s *ConfigSuite) TestProjectAgentgateFileRulesPrepend() {
	s.setupProjectReadFile(`{
		"gates": {
			"agentgate": {
				"file_rules": [
					{ "paths": ["./secret-vault/**"], "operations": ["read"], "decision": "deny" }
				]
			}
		}
	}`)

	main := gateMainCfg()
	globalCount := len(main.Gates.Agentgate.FileRules)

	merged, err := s.loader.loadProjectConfig("/project", main)
	require.NoError(s.T(), err)
	require.Len(s.T(), merged.Gates.Agentgate.FileRules, globalCount+1)
	require.Equal(s.T(), []string{"./secret-vault/**"}, merged.Gates.Agentgate.FileRules[0].Paths)
	require.Equal(s.T(), types.DecisionDeny, merged.Gates.Agentgate.FileRules[0].Decision)
}

func (s *ConfigSuite) TestProjectDockerProxyDisables() {
	s.setupProjectReadFile(`{"gates": {"docker_proxy": {"enabled": false}}}`)

	merged, err := s.loader.loadProjectConfig("/project", gateMainCfg())
	require.NoError(s.T(), err)
	require.True(s.T(), merged.Gates.Agentgate.Enabled, "agentgate should stay enabled")
	require.False(s.T(), merged.Gates.DockerProxy.Enabled)
}

func (s *ConfigSuite) TestProjectDockerProxyBodyRulesPrepend() {
	s.setupProjectReadFile(`{
		"gates": {
			"docker_proxy": {
				"body_rules": [
					{
						"applies_to": "POST ^/containers/create$",
						"json_checks": [ {"path": "Image", "op": "equals", "values": ["evil"]} ],
						"decision": "deny"
					}
				]
			}
		}
	}`)

	main := gateMainCfg()
	globalCount := len(main.Gates.DockerProxy.BodyRules)

	merged, err := s.loader.loadProjectConfig("/project", main)
	require.NoError(s.T(), err)
	require.Len(s.T(), merged.Gates.DockerProxy.BodyRules, globalCount+1)
	require.Equal(s.T(), "POST ^/containers/create$", merged.Gates.DockerProxy.BodyRules[0].AppliesTo)
	require.Equal(s.T(), types.DecisionDeny, merged.Gates.DockerProxy.BodyRules[0].Decision)
}

func (s *ConfigSuite) TestProjectDockerProxyBodyRuleAllowPrepends() {
	s.setupProjectReadFile(`{
		"gates": {
			"docker_proxy": {
				"body_rules": [
					{
						"applies_to": "POST ^/containers/create$",
						"json_checks": [ {"path": "HostConfig.Binds[*]", "op": "source_path_in", "values": ["^/var/run/docker\\.sock$"]} ],
						"decision": "allow"
					}
				]
			}
		}
	}`)

	main := gateMainCfg()
	globalCount := len(main.Gates.DockerProxy.BodyRules)

	merged, err := s.loader.loadProjectConfig("/project", main)
	require.NoError(s.T(), err)
	require.Len(s.T(), merged.Gates.DockerProxy.BodyRules, globalCount+1)
	require.Equal(s.T(), types.DecisionAllow, merged.Gates.DockerProxy.BodyRules[0].Decision)
	require.Equal(s.T(), "POST ^/containers/create$", merged.Gates.DockerProxy.BodyRules[0].AppliesTo)
}

func (s *ConfigSuite) TestProjectAgentgateCommandRuleApprovePrepends() {
	s.setupProjectReadFile(`{
		"gates": {
			"agentgate": {
				"command_rules": [
					{ "commands": ["git"], "args_patterns": ["^commit(\\s|$)"], "decision": "approve", "message": "git commit (approval required)" }
				]
			}
		}
	}`)

	main := gateMainCfg()
	globalCount := len(main.Gates.Agentgate.CommandRules)

	merged, err := s.loader.loadProjectConfig("/project", main)
	require.NoError(s.T(), err)
	require.Len(s.T(), merged.Gates.Agentgate.CommandRules, globalCount+1)
	require.Equal(s.T(), types.DecisionApprove, merged.Gates.Agentgate.CommandRules[0].Decision)
	require.Equal(s.T(), []string{"git"}, merged.Gates.Agentgate.CommandRules[0].Commands)
	require.Equal(s.T(), []string{`^commit(\s|$)`}, merged.Gates.Agentgate.CommandRules[0].ArgsPatterns)
}
