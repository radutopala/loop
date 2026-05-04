package config

import (
	"slices"
	"strings"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/types"
)

// ----- Gates (agentgate + docker_proxy) config tests -----

func (s *ConfigSuite) TestLoadGatesDefaults() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return s.minimalJSON(), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.True(s.T(), cfg.Gates.Agentgate.Enabled)
	require.Equal(s.T(), types.DecisionAllow, cfg.Gates.Agentgate.DefaultDecision)
	require.Len(s.T(), cfg.Gates.Agentgate.PathRules, 2)
	require.Equal(s.T(), "/var/run/docker.sock.host", cfg.Gates.Agentgate.PathRules[0].Pattern)
	require.Equal(s.T(), types.DecisionDeny, cfg.Gates.Agentgate.PathRules[0].Decision)
	require.Equal(s.T(), "/var/run/docker.sock", cfg.Gates.Agentgate.PathRules[1].Pattern)
	require.Equal(s.T(), types.DecisionAllow, cfg.Gates.Agentgate.PathRules[1].Decision)
	require.Len(s.T(), cfg.Gates.Agentgate.CommandRules, 2)
	require.Len(s.T(), cfg.Gates.Agentgate.FileRules, 9)
	require.Equal(s.T(), 30, cfg.Gates.RateLimits.Pending)
	require.Equal(s.T(), 60, cfg.Gates.RateLimits.PerMinute)
	require.Equal(s.T(), 500, cfg.Gates.RateLimits.Total)
	require.Equal(s.T(), 30, cfg.Gates.Audit.RetentionDays)
	require.True(s.T(), cfg.Gates.DockerProxy.Enabled)
	require.Equal(s.T(), types.DecisionAllow, cfg.Gates.DockerProxy.DefaultDecision)
	require.NotEmpty(s.T(), cfg.Gates.DockerProxy.HTTPRules)
	require.NotEmpty(s.T(), cfg.Gates.DockerProxy.BodyRules)
}

func (s *ConfigSuite) TestLoadAgentgateEnabledExplicitFalse() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
			"discord_token": "t",
			"discord_app_id": "a",
			"gates": { "agentgate": { "enabled": false } }
		}`), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.False(s.T(), cfg.Gates.Agentgate.Enabled)
	require.False(s.T(), cfg.Gates.DockerProxy.Enabled, "docker proxy should transitively disable when agentgate is off")
}

func (s *ConfigSuite) TestLoadAgentgateCustomRulesAppended() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
			"discord_token": "t",
			"discord_app_id": "a",
			"gates": {
				"agentgate": {
					"command_rules": [
						{ "commands": ["curl"], "args_patterns": ["evil\\.com"], "decision": "deny", "message": "block evil.com" }
					]
				}
			}
		}`), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Len(s.T(), cfg.Gates.Agentgate.CommandRules, 1)
	require.Equal(s.T(), []string{"curl"}, cfg.Gates.Agentgate.CommandRules[0].Commands)
	require.Equal(s.T(), types.DecisionDeny, cfg.Gates.Agentgate.CommandRules[0].Decision)
	require.Len(s.T(), cfg.Gates.Agentgate.PathRules, 2)
	require.Len(s.T(), cfg.Gates.Agentgate.FileRules, 9)
}

func (s *ConfigSuite) TestLoadAgentgateDefaultDecisionOverride() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
			"discord_token": "t",
			"discord_app_id": "a",
			"gates": { "agentgate": { "default_decision": "approve" } }
		}`), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), types.DecisionApprove, cfg.Gates.Agentgate.DefaultDecision)
}

func (s *ConfigSuite) TestLoadAgentgateDefaultsNotShared() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return s.minimalJSON(), nil
	}

	cfg1, err := s.loader.load()
	require.NoError(s.T(), err)
	cfg1.Gates.Agentgate.FileRules[0].Message = "mutated"

	cfg2, err := s.loader.load()
	require.NoError(s.T(), err)
	require.NotEqual(s.T(), "mutated", cfg2.Gates.Agentgate.FileRules[0].Message,
		"DefaultGateFileRules() must return a fresh slice each call")
}

// TestDefaultGateFileRulesClaudeConfigNarrowScope guards against regressing
// back to a blanket `**/.claude/**` deny. That broad glob was too wide: Claude
// Code writes ephemeral session state (session-env/<uuid>, todos/,
// shell-snapshots/, …) under ~/.claude before every Bash tool call, and a
// blanket deny breaks Bash with EPERM on mkdir.
func (s *ConfigSuite) TestDefaultGateFileRulesClaudeConfigNarrowScope() {
	rules := DefaultGateFileRules()

	var claudeRule *types.FileRule
	for i := range rules {
		for _, p := range rules[i].Paths {
			if strings.Contains(p, ".claude/settings.json") {
				claudeRule = &rules[i]
				break
			}
		}
		if claudeRule != nil {
			break
		}
	}
	require.NotNil(s.T(), claudeRule, "claude/MCP config deny rule must exist")
	require.Equal(s.T(), types.DecisionDeny, claudeRule.Decision)

	// Must protect: claude settings files the agent should not mutate.
	for _, want := range []string{
		"**/.claude/settings.json",
		"**/.claude/settings.local.json",
	} {
		require.Contains(s.T(), claudeRule.Paths, want,
			"claude settings deny rule missing sensitive path %q", want)
	}

	// Must NOT contain the broad `**/.claude/**` pattern — that blanket glob
	// caught harness-internal ephemeral state and broke Bash with EPERM.
	// Must NOT block CLAUDE.md, mcp*.json, or plugins/** — agents legitimately
	// update memory files, per-project MCP configs, and plugins state as part
	// of normal work. Must NOT block .mcp.json globally — `**/.mcp.json` and
	// `/work/.mcp.json` trip legitimate test fixtures and onboarding flows
	// that write a fresh .mcp.json into scratch dirs.
	for _, p := range claudeRule.Paths {
		require.NotEqual(s.T(), "**/.claude/**", p,
			"broad **/.claude/** deny is a regression; scope to individual settings files")
		require.NotEqual(s.T(), "**/.claude/CLAUDE.md", p,
			"**/.claude/CLAUDE.md deny was removed; agents legitimately update their own memory files")
		require.NotEqual(s.T(), "**/.claude/mcp*.json", p,
			"**/.claude/mcp*.json deny was removed; agents legitimately update per-project MCP configs")
		require.NotEqual(s.T(), "**/.claude/plugins/**", p,
			"**/.claude/plugins/** deny was removed; agents legitimately update plugins state")
		require.NotEqual(s.T(), "**/.mcp.json", p,
			"global **/.mcp.json deny is over-broad")
		require.NotEqual(s.T(), "/work/.mcp.json", p,
			"/work/.mcp.json deny is over-broad; was tripping legitimate writes")
	}
}

// TestDefaultGateFileRulesShellRcfileNarrowScope guards against regressing
// back to a filename-only `**/.bashrc` (and siblings) deny. That glob matched
// any path ending in the rcfile name, including t.TempDir()/.bashrc fixtures
// exercised by cmd/loop's TestOnboardLocal* / TestOnboardGlobal* — writing a
// plain .bashrc into a scratch dir is not a persistence vector. Scope denies
// to actual login-shell homes instead.
func (s *ConfigSuite) TestDefaultGateFileRulesShellRcfileNarrowScope() {
	rules := DefaultGateFileRules()

	var rcRule *types.FileRule
	for i := range rules {
		if slices.Contains(rules[i].Paths, "/root/.bashrc") {
			rcRule = &rules[i]
			break
		}
	}
	require.NotNil(s.T(), rcRule, "shell rcfile deny rule must exist")
	require.Equal(s.T(), types.DecisionDeny, rcRule.Decision)

	// Must still cover every shell rcfile name under every home-dir layout
	// we support: /root (root user), /home/* (Linux per-user), /Users/*
	// (macOS host-home bind-mounted into Docker Desktop containers).
	for _, name := range []string{
		".bashrc", ".bash_profile", ".zshrc", ".zprofile",
		".profile", ".bash_login", ".inputrc",
	} {
		require.Contains(s.T(), rcRule.Paths, "/root/"+name,
			"rcfile deny missing /root/%s", name)
		require.Contains(s.T(), rcRule.Paths, "/home/*/"+name,
			"rcfile deny missing /home/*/%s", name)
		require.Contains(s.T(), rcRule.Paths, "/Users/*/"+name,
			"rcfile deny missing /Users/*/%s (macOS host-home bind-mount layout)", name)
	}

	// Must NOT use the filename-only `**/.rcname` pattern — that caught
	// test-fixture writes into scratch dirs (t.TempDir() + /.bashrc).
	for _, p := range rcRule.Paths {
		require.False(s.T(), strings.HasPrefix(p, "**/"),
			"rcfile deny path %q uses filename-anywhere glob; scope to /root/ or /home/*/ instead", p)
	}
}

// TestDefaultGateFileRulesRegistryCredsReadAllowed guards the invariant that
// registry-auth files (~/.docker/config.json, ~/.npmrc) are write-denied but
// **readable**. docker CLI and npm read these on every invocation; a read-deny
// surfaces as EPERM and breaks routine commands. Writes are what plant creds,
// not reads.
//
// Also guards scope: the rule must enumerate /root/, /home/*/, /Users/*/ — a
// filename-anywhere `**/.npmrc` glob caught nodeenv's extraction of npm's
// bundled .npmrc template inside ~/.cache/pre-commit/, breaking pre-commit
// hooks. Same shape as the shell-rcfile rule.
func (s *ConfigSuite) TestDefaultGateFileRulesRegistryCredsReadAllowed() {
	rules := DefaultGateFileRules()

	var registryRule *types.FileRule
	var credsRule *types.FileRule
	for i := range rules {
		for _, p := range rules[i].Paths {
			if p == "/root/.docker/config.json" {
				registryRule = &rules[i]
			}
			if p == "**/.ssh/**" {
				credsRule = &rules[i]
			}
		}
	}

	require.NotNil(s.T(), registryRule, "registry credentials rule must exist")
	require.Equal(s.T(), types.DecisionDeny, registryRule.Decision)
	require.NotContains(s.T(), registryRule.Operations, "read",
		"registry creds must be readable — docker CLI / npm need them on every invocation")
	for _, want := range []string{"write", "create", "delete", "chmod"} {
		require.Contains(s.T(), registryRule.Operations, want,
			"registry creds write-class op %q must stay denied (cred-plant defense)", want)
	}

	// Must cover both filenames under every home-dir layout we support:
	// /root (root user), /home/* (Linux per-user), /Users/* (macOS host-home
	// bind-mounted into Docker Desktop containers).
	for _, name := range []string{".docker/config.json", ".npmrc"} {
		require.Contains(s.T(), registryRule.Paths, "/root/"+name,
			"registry-creds deny missing /root/%s", name)
		require.Contains(s.T(), registryRule.Paths, "/home/*/"+name,
			"registry-creds deny missing /home/*/%s", name)
		require.Contains(s.T(), registryRule.Paths, "/Users/*/"+name,
			"registry-creds deny missing /Users/*/%s (macOS host-home bind-mount layout)", name)
	}

	// Must NOT use a `**/.<name>` filename-anywhere glob — that caught
	// nodeenv's extracted npm/.npmrc and made pre-commit's cache un-cleanable.
	for _, p := range registryRule.Paths {
		require.False(s.T(), strings.HasPrefix(p, "**/"),
			"registry-creds deny path %q uses filename-anywhere glob; scope to home-dir layouts instead", p)
	}

	// Regression guard: the broader credentials rule must not re-list these
	// filenames either (would re-introduce the same read-deny / nodeenv noise).
	require.NotNil(s.T(), credsRule, "credentials deny rule must exist")
	for _, p := range []string{"**/.docker/config.json", "**/.npmrc"} {
		require.NotContains(s.T(), credsRule.Paths, p,
			"%s must not be in the blanket credentials deny rule", p)
	}
}

// TestDefaultGatePathRulesDockerSockSilent guards that the proxied
// /var/run/docker.sock is Allow, not Approve — every HTTP request flowing
// through it is re-gated by the dockerproxy rules anyway, so a socket-level
// prompt just adds one extra "agent wants to use docker" dialog per session
// without catching anything the proxy doesn't.
func (s *ConfigSuite) TestDefaultGatePathRulesDockerSockSilent() {
	rules := DefaultGatePathRules()
	var proxiedSock *types.PathRule
	for i := range rules {
		if rules[i].Pattern == "/var/run/docker.sock" {
			proxiedSock = &rules[i]
		}
	}
	s.Require().NotNil(proxiedSock, "proxied docker.sock rule must exist")
	s.Require().Equal(types.DecisionAllow, proxiedSock.Decision)

	// The direct-daemon path must still be hard-denied.
	var hostSock *types.PathRule
	for i := range rules {
		if rules[i].Pattern == "/var/run/docker.sock.host" {
			hostSock = &rules[i]
		}
	}
	s.Require().NotNil(hostSock, "direct-daemon rule must exist")
	s.Require().Equal(types.DecisionDeny, hostSock.Decision)
}

// TestDefaultDockerProxyHTTPRulesMinimal guards the policy intent that the
// baseline rules enumerate only the exceptions to DefaultDecision=Allow:
// Approve for lateral-movement ops (exec, /exec/*/start, docker cp archive)
// and Deny for off-limits APIs (swarm/nodes/secrets/configs/plugins). Normal
// container lifecycle ops (create/start/stop/attach/wait/update/build/delete)
// and read endpoints (/_ping, /containers/json, …) are handled by the default
// Allow fall-through + body rules, so they must NOT appear as explicit rules
// here.
func (s *ConfigSuite) TestDefaultDockerProxyHTTPRulesMinimal() {
	rules := DefaultDockerProxyHTTPRules()
	s.Require().Len(rules, 3, "defaults must be minimal: exec Approve, archive Approve, swarm Deny")

	exec := rules[0]
	s.Require().Equal(types.DecisionApprove, exec.Decision)
	s.Require().Contains(exec.Paths, "^/containers/[^/]+/exec$")
	s.Require().Contains(exec.Paths, "^/exec/[^/]+/start$")

	archive := rules[1]
	s.Require().Equal(types.DecisionApprove, archive.Decision)
	s.Require().Contains(archive.Paths, "^/containers/[^/]+/archive$")
	// HEAD + GET + PUT all reach the docker cp approval — docker clients use
	// HEAD as a preflight before GETing an archive.
	s.Require().Contains(archive.Methods, "HEAD")

	deny := rules[2]
	s.Require().Equal(types.DecisionDeny, deny.Decision)
	for _, want := range []string{"^/swarm/", "^/nodes/", "^/secrets/", "^/configs/", "^/plugins/"} {
		s.Require().Contains(deny.Paths, want)
	}
}

func (s *ConfigSuite) TestLoadGatesRateLimitsOverride() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
			"discord_token": "t",
			"discord_app_id": "a",
			"gates": {
				"rate_limits": { "pending": 100, "per_minute": 200, "total": 9999 },
				"audit":       { "retention_days": 7 }
			}
		}`), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), 100, cfg.Gates.RateLimits.Pending)
	require.Equal(s.T(), 200, cfg.Gates.RateLimits.PerMinute)
	require.Equal(s.T(), 9999, cfg.Gates.RateLimits.Total)
	require.Equal(s.T(), 7, cfg.Gates.Audit.RetentionDays)
}

func (s *ConfigSuite) TestLoadDockerProxyExplicitEnabled() {
	s.loader.readFile = func(_ string) ([]byte, error) {
		return []byte(`{
			"platforms": ["discord"],
			"discord_token": "t",
			"discord_app_id": "a",
			"gates": {
				"agentgate":    { "enabled": false },
				"docker_proxy": { "enabled": true, "default_decision": "deny" }
			}
		}`), nil
	}

	cfg, err := s.loader.load()
	require.NoError(s.T(), err)
	require.False(s.T(), cfg.Gates.Agentgate.Enabled)
	require.True(s.T(), cfg.Gates.DockerProxy.Enabled,
		"explicit gates.docker_proxy.enabled=true should stick even when agentgate is off")
	require.Equal(s.T(), types.DecisionDeny, cfg.Gates.DockerProxy.DefaultDecision)
}

// ----- Project gates merge tests -----
