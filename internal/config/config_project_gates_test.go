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

// countDeny returns how many of rules deny: a project rule lands right
// after them.
func countDeny[T any](rules []T, decision func(T) types.Decision) int {
	n := 0
	for _, r := range rules {
		if decision(r) == types.DecisionDeny {
			n++
		}
	}
	return n
}

func (s *ConfigSuite) TestProjectCannotToggleGates() {
	tests := []struct {
		name          string
		json          string
		globalOn      bool
		wantAgentgate bool
		wantProxy     bool
	}{
		{"agentgate off", `{"gates": {"agentgate": {"enabled": false}}}`, true, true, true},
		{"docker proxy off", `{"gates": {"docker_proxy": {"enabled": false}}}`, true, true, true},
		{"agentgate on", `{"gates": {"agentgate": {"enabled": true}, "docker_proxy": {"enabled": true}}}`, false, false, false},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			main := gateMainCfg()
			main.Gates.Agentgate.Enabled = tc.globalOn
			main.Gates.DockerProxy.Enabled = tc.globalOn
			s.setupProjectReadFile(tc.json)

			merged, err := s.loader.loadProjectConfig("/project", main)
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.wantAgentgate, merged.Gates.Agentgate.Enabled)
			require.Equal(s.T(), tc.wantProxy, merged.Gates.DockerProxy.Enabled)
		})
	}
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

func (s *ConfigSuite) TestProjectGateRulesFollowGlobalDenies() {
	decPath := func(r types.PathRule) types.Decision { return r.Decision }
	decCmd := func(r types.CommandRule) types.Decision { return r.Decision }
	decFile := func(r types.FileRule) types.Decision { return r.Decision }
	decHTTP := func(r types.HTTPServiceRule) types.Decision { return r.Decision }
	decBody := func(r types.BodyRule) types.Decision { return r.Decision }

	// check asserts the merged list is the global denies, then the one
	// project rule, then the rest of the global rules, and returns the
	// project rule's index.
	type checkFn func(main, merged *Config) int
	tests := []struct {
		name  string
		json  string
		check checkFn
	}{
		{
			name: "command rule deny",
			json: `{"gates": {"agentgate": {"command_rules": [{"commands": ["npm"], "args_patterns": ["^publish"], "decision": "deny"}]}}}`,
			check: func(main, merged *Config) int {
				g, m := main.Gates.Agentgate.CommandRules, merged.Gates.Agentgate.CommandRules
				i := countDeny(g, decCmd)
				require.Len(s.T(), m, len(g)+1)
				require.Equal(s.T(), []string{"npm"}, m[i].Commands)
				return i
			},
		},
		{
			name: "command rule approve",
			json: `{"gates": {"agentgate": {"command_rules": [{"commands": ["git"], "args_patterns": ["^commit(\\s|$)"], "decision": "approve"}]}}}`,
			check: func(main, merged *Config) int {
				g, m := main.Gates.Agentgate.CommandRules, merged.Gates.Agentgate.CommandRules
				i := countDeny(g, decCmd)
				require.Equal(s.T(), types.DecisionApprove, m[i].Decision)
				require.Equal(s.T(), []string{`^commit(\s|$)`}, m[i].ArgsPatterns)
				return i
			},
		},
		{
			name: "file rule allow can't beat a global deny",
			json: `{"gates": {"agentgate": {"file_rules": [{"paths": ["/etc/**"], "operations": ["write"], "decision": "allow"}]}}}`,
			check: func(main, merged *Config) int {
				g, m := main.Gates.Agentgate.FileRules, merged.Gates.Agentgate.FileRules
				i := countDeny(g, decFile)
				require.Positive(s.T(), i)
				require.Equal(s.T(), []string{"/etc/**"}, m[i].Paths)
				require.Equal(s.T(), types.DecisionAllow, m[i].Decision)
				for _, r := range m[:i] {
					require.Equal(s.T(), types.DecisionDeny, r.Decision)
				}
				require.Equal(s.T(), "tmp fast-path", m[i+1].Message, "global allows follow the project rule")
				return i
			},
		},
		{
			name: "path rule",
			json: `{"gates": {"agentgate": {"path_rules": [{"pattern": "/var/run/docker.sock", "decision": "allow"}]}}}`,
			check: func(main, merged *Config) int {
				g, m := main.Gates.Agentgate.PathRules, merged.Gates.Agentgate.PathRules
				i := countDeny(g, decPath)
				require.Len(s.T(), m, len(g)+1)
				require.Equal(s.T(), "/var/run/docker.sock", m[i].Pattern)
				return i
			},
		},
		{
			name: "docker proxy http rule",
			json: `{"gates": {"docker_proxy": {"http_rules": [{"methods": ["POST"], "paths": ["^/x$"], "decision": "allow"}]}}}`,
			check: func(main, merged *Config) int {
				g, m := main.Gates.DockerProxy.HTTPRules, merged.Gates.DockerProxy.HTTPRules
				i := countDeny(g, decHTTP)
				require.Len(s.T(), m, len(g)+1)
				require.Equal(s.T(), []string{"^/x$"}, m[i].Paths)
				return i
			},
		},
		{
			name: "docker proxy body rule",
			json: `{"gates": {"docker_proxy": {"body_rules": [{"applies_to": "POST ^/containers/create$", "json_checks": [{"path": "Image", "op": "equals", "values": ["evil"]}], "decision": "allow"}]}}}`,
			check: func(main, merged *Config) int {
				g, m := main.Gates.DockerProxy.BodyRules, merged.Gates.DockerProxy.BodyRules
				i := countDeny(g, decBody)
				require.Len(s.T(), m, len(g)+1)
				require.Equal(s.T(), "POST ^/containers/create$", m[i].AppliesTo)
				require.Equal(s.T(), types.DecisionAllow, m[i].Decision)
				return i
			},
		},
	}

	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.setupProjectReadFile(tc.json)
			main := gateMainCfg()
			merged, err := s.loader.loadProjectConfig("/project", main)
			require.NoError(s.T(), err)
			tc.check(main, merged)
		})
	}
}

func (s *ConfigSuite) TestLayerRules() {
	dec := func(r types.FileRule) types.Decision { return r.Decision }
	rule := func(msg string, d types.Decision) types.FileRule { return types.FileRule{Message: msg, Decision: d} }
	global := []types.FileRule{
		rule("g-allow", types.DecisionAllow),
		rule("g-deny-1", types.DecisionDeny),
		rule("g-approve", types.DecisionApprove),
		rule("g-deny-2", types.DecisionDeny),
	}
	require.Equal(s.T(), global, layerRules(global, nil, dec), "no project rules keeps the global order")

	got := layerRules(global, []types.FileRule{rule("p-allow", types.DecisionAllow), rule("p-deny", types.DecisionDeny)}, dec)
	var msgs []string
	for _, r := range got {
		msgs = append(msgs, r.Message)
	}
	require.Equal(s.T(), []string{"g-deny-1", "g-deny-2", "p-allow", "p-deny", "g-allow", "g-approve"}, msgs)
}
