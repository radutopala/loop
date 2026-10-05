package config

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/types"
)

const trustPath = "/cfg/loop/project-trust.json"

type TrustSuite struct {
	suite.Suite
	files map[string][]byte
	store *TrustStore
}

func TestTrustSuite(t *testing.T) {
	suite.Run(t, new(TrustSuite))
}

func (s *TrustSuite) SetupTest() {
	s.files = map[string][]byte{}
	s.store = &TrustStore{
		userConfigDir: func() (string, error) { return "/cfg", nil },
		readFile: func(p string) ([]byte, error) {
			if b, ok := s.files[p]; ok {
				return b, nil
			}
			return nil, os.ErrNotExist
		},
		writeFile: func(p string, b []byte, _ os.FileMode) error {
			s.files[p] = b
			return nil
		},
		mkdirAll: func(string, os.FileMode) error { return nil },
		rename: func(from, to string) error {
			s.files[to] = s.files[from]
			delete(s.files, from)
			return nil
		},
		now: func() time.Time { return time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC) },
	}
}

func (s *TrustSuite) project(content string) {
	s.files["/proj/.loop/config.json"] = []byte(content)
}

func (s *TrustSuite) loader() *Loader {
	return &Loader{readFile: s.store.readFile, trust: s.store}
}

func (s *TrustSuite) TestNothingToTrust() {
	st, err := s.store.Status("/proj")
	require.NoError(s.T(), err)
	require.True(s.T(), st.Trusted, "no project config")
	require.Equal(s.T(), "{}", st.Current)

	s.project(`{"claude_model": "opus", "memory": {"enabled": true}, "browser": {"memory_mb": 512}}`)
	st, err = s.store.Status("/proj")
	require.NoError(s.T(), err)
	require.True(s.T(), st.Trusted, "only fields that stay in the container")
	require.Empty(s.T(), st.Approved)
	merged, err := s.loader().loadProjectConfig("/proj", &Config{})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "opus", merged.ClaudeModel)
}

func (s *TrustSuite) TestTrustFlow() {
	s.project(`{"mounts": ["/a:/a"], "envs": {"B": "2", "A": 1}}`)

	st, err := s.store.Status("/proj")
	require.NoError(s.T(), err)
	require.False(s.T(), st.Trusted)
	require.Empty(s.T(), st.Approved)
	require.Contains(s.T(), st.Current, `"/a:/a"`)
	require.True(s.T(), strings.HasPrefix(st.Diff, "--- /dev/null\n+++ .loop/config.json (now)\n@@ -0,0 +1,"), st.Diff)
	require.Contains(s.T(), st.Diff, "\n+    \"/a:/a\"\n")
	require.NotContains(s.T(), st.Diff, "\n-")

	require.ErrorIs(s.T(), s.store.Trust("/proj", "stale"), ErrTrustChanged)
	require.NotContains(s.T(), s.files, trustPath)

	require.NoError(s.T(), s.store.Trust("/proj/", st.Hash))
	st2, err := s.store.Status("/proj")
	require.NoError(s.T(), err)
	require.True(s.T(), st2.Trusted)
	require.Equal(s.T(), st.Current, st2.Approved)
	require.Empty(s.T(), st2.Diff)
	require.Contains(s.T(), string(s.files[trustPath]), `"trusted_at": "2026-09-30T00:00:00Z"`)

	// Formatting, comments and key order don't matter.
	s.project("{\n  // a comment\n  \"envs\": {\"A\": 1, \"B\": \"2\"},\n  \"mounts\": [\"/a:/a\"],\n}")
	st3, err := s.store.Status("/proj")
	require.NoError(s.T(), err)
	require.True(s.T(), st3.Trusted)
	require.Equal(s.T(), st.Hash, st3.Hash)

	// A change needs trust again, and shows what was approved.
	s.project(`{"mounts": ["/a:/a", "/:/host"], "envs": {"A": 1, "B": "2"}}`)
	st4, err := s.store.Status("/proj")
	require.NoError(s.T(), err)
	require.False(s.T(), st4.Trusted)
	require.Equal(s.T(), st.Current, st4.Approved)
	require.NotEqual(s.T(), st.Hash, st4.Hash)
	require.Equal(s.T(), "--- .loop/config.json (last trusted)\n+++ .loop/config.json (now)\n@@ -1,6 +1,7 @@\n {\n   \"mounts\": [\n-    \"/a:/a\"\n+    \"/a:/a\",\n+    \"/:/host\"\n   ],\n   \"envs\": {\n     \"A\": 1,\n", st4.Diff)
}

func (s *TrustSuite) TestMergeUsesTrustedFields() {
	main := &Config{Mounts: []string{"/g:/g"}, ExtraDirs: []string{"/global-extra"}, Envs: map[string]string{"G": "1"}}

	s.project(`{"mounts": ["/a:/a"], "extra_dirs": ["/x"], "claude_model": "opus"}`)
	merged, err := s.loader().loadProjectConfig("/proj", main)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"/g:/g"}, merged.Mounts, "never trusted: no project mounts")
	require.Equal(s.T(), []string{"/global-extra"}, merged.ExtraDirs)
	require.Equal(s.T(), "opus", merged.ClaudeModel, "untrusted fields still apply")

	require.NoError(s.T(), s.store.Trust("/proj", ""))
	merged, err = s.loader().loadProjectConfig("/proj", main)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"/g:/g", "/a:/a"}, merged.Mounts)
	require.Equal(s.T(), []string{"/x"}, merged.ExtraDirs)

	// An agent edits them: the approved ones stay in effect.
	s.project(`{"mounts": ["/:/host"], "extra_dirs": ["/"], "claude_model": "haiku"}`)
	merged, err = s.loader().loadProjectConfig("/proj", main)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"/g:/g", "/a:/a"}, merged.Mounts)
	require.Equal(s.T(), []string{"/x"}, merged.ExtraDirs)
	require.Equal(s.T(), "haiku", merged.ClaudeModel)
}

func (s *TrustSuite) TestMergeBrowserAndMemory() {
	main := &Config{}
	main.Browser.Mode = "container"
	main.Memory.Paths = []string{"/g"}

	s.project(`{"browser": {"mode": "host", "memory_mb": 256, "cookie_import": {"source": "chrome"}}, "memory": {"paths": ["/etc"], "max_chunk_chars": 99}}`)
	merged, err := s.loader().loadProjectConfig("/proj", main)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "container", merged.Browser.Mode)
	require.Empty(s.T(), merged.Browser.CookieImport.Source)
	require.Equal(s.T(), int64(256), merged.Browser.MemoryMB, "the rest of the block applies")
	require.Equal(s.T(), []string{"/g"}, merged.Memory.Paths)
	require.Equal(s.T(), 99, merged.Memory.MaxChunkChars)

	require.NoError(s.T(), s.store.Trust("/proj", ""))
	merged, err = s.loader().loadProjectConfig("/proj", main)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "host", merged.Browser.Mode)
	require.Equal(s.T(), "chrome", merged.Browser.CookieImport.Source)
	require.Equal(s.T(), []string{"/g", "/etc"}, merged.Memory.Paths)

	// Approved blocks apply even when the file drops them.
	s.project(`{"mounts": ["/new:/new"]}`)
	merged, err = s.loader().loadProjectConfig("/proj", main)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "host", merged.Browser.Mode)
	require.Equal(s.T(), []string{"/g", "/etc"}, merged.Memory.Paths)
	require.Empty(s.T(), merged.Mounts)
}

func (s *TrustSuite) TestMergeUnreadableTrustFileTrustsNothing() {
	s.project(`{"mounts": ["/a:/a"]}`)
	s.files[trustPath] = []byte("not json")
	merged, err := s.loader().loadProjectConfig("/proj", &Config{})
	require.NoError(s.T(), err)
	require.Empty(s.T(), merged.Mounts)

	_, err = s.store.Status("/proj")
	require.ErrorContains(s.T(), err, "parsing /cfg/loop/project-trust.json")
}

func (s *TrustSuite) TestKeep() {
	trusted := func() bool {
		st, err := s.store.Status("/proj")
		require.NoError(s.T(), err)
		return st.Trusted
	}

	// Trusted before (no file): the owner's write stays trusted.
	written := []byte(`{"mounts": ["/a:/a"]}`)
	s.project(string(written))
	require.NoError(s.T(), s.store.Keep("/proj", nil, written))
	require.True(s.T(), trusted())

	// A trusted config the owner edits stays trusted.
	edited := []byte(`{"mounts": ["/a:/a", "/b:/b"]}`)
	s.project(string(edited))
	require.NoError(s.T(), s.store.Keep("/proj", written, edited))
	require.True(s.T(), trusted())

	// An agent's change waits for review, and an owner edit doesn't approve it.
	agent := []byte(`{"mounts": ["/:/host"]}`)
	owner := []byte(`{"mounts": ["/:/host"], "claude_model": "opus"}`)
	s.project(string(owner))
	require.NoError(s.T(), s.store.Keep("/proj", agent, owner))
	require.False(s.T(), trusted())

	// What the owner wrote is what's trusted, not what's on disk after.
	s.project(`{"mounts": ["/:/host"]}`)
	require.NoError(s.T(), s.store.Keep("/proj", edited, []byte(`{"mounts": ["/c:/c"]}`)))
	require.False(s.T(), trusted())
	s.project(`{"mounts": ["/c:/c"]}`)
	require.True(s.T(), trusted())

	// An unreadable config before counts as untrusted.
	s.project(`{"mounts": ["/d:/d"]}`)
	require.NoError(s.T(), s.store.Keep("/proj", []byte(`{not hjson`), []byte(`{"mounts": ["/d:/d"]}`)))
	require.False(s.T(), trusted())

	// Nothing that needs trust before: the owner's write is trusted.
	require.NoError(s.T(), s.store.Keep("/proj", []byte(`{"claude_model": "opus"}`), []byte(`{"mounts": ["/d:/d"]}`)))
	require.True(s.T(), trusted())
}

func (s *TrustSuite) TestAdopt() {
	trusted := func() bool {
		st, err := s.store.Status("/proj")
		require.NoError(s.T(), err)
		return st.Trusted
	}

	tests := []struct {
		name     string
		approved string // trusted before Adopt; "" for none
		config   string // "" for no file
		trusted  bool
		kept     string // what's trusted after, when not the config
		recorded bool   // false when Adopt must leave the trust file alone
	}{
		{name: "no file", trusted: true},
		{name: "nothing that needs trust", config: `{"claude_model": "opus"}`, trusted: true},
		{name: "never trusted", config: `{"mounts": ["/a:/a"]}`, trusted: true, recorded: true},
		{name: "already trusted as is", approved: `{"mounts": ["/a:/a"]}`, config: `{"mounts": ["/a:/a"]}`, trusted: true, recorded: true},
		{name: "a change since trust still waits", approved: `{"mounts": ["/a:/a"]}`, config: `{"mounts": ["/:/host"]}`, kept: "/a:/a", recorded: true},
		{name: "unreadable config is left for review", config: `{"mounts": "not a list"}`, recorded: false},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			if tt.approved != "" {
				s.project(tt.approved)
				require.NoError(s.T(), s.store.Trust("/proj", ""))
			}
			delete(s.files, "/proj/.loop/config.json")
			if tt.config != "" {
				s.project(tt.config)
			}
			require.NoError(s.T(), s.store.Adopt("/proj"))
			if !tt.recorded {
				require.NotContains(s.T(), s.files, trustPath)
				return
			}
			require.Equal(s.T(), tt.trusted, trusted())
			if tt.kept != "" {
				st, err := s.store.Status("/proj")
				require.NoError(s.T(), err)
				require.Contains(s.T(), st.Approved, tt.kept)
			}
		})
	}
}

func (s *TrustSuite) TestErrors() {
	boom := errors.New("boom")

	s.project(`{"mounts": ["/a:/a"]}`)
	s.store.writeFile = func(string, []byte, os.FileMode) error { return boom }
	require.ErrorContains(s.T(), s.store.Trust("/proj", ""), "writing /cfg/loop/project-trust.json.tmp")
	s.SetupTest()

	s.project(`{"mounts": ["/a:/a"]}`)
	s.store.rename = func(string, string) error { return boom }
	require.ErrorContains(s.T(), s.store.Trust("/proj", ""), "replacing /cfg/loop/project-trust.json")
	s.SetupTest()

	s.project(`{"mounts": ["/a:/a"]}`)
	s.store.mkdirAll = func(string, os.FileMode) error { return boom }
	require.ErrorContains(s.T(), s.store.Trust("/proj", ""), "creating /cfg/loop")
	s.SetupTest()

	s.project(`{"mounts": ["/a:/a"]}`)
	s.files[trustPath] = []byte("not json")
	require.ErrorContains(s.T(), s.store.Trust("/proj", ""), "parsing")
	s.SetupTest()

	s.project(`{"mounts": "not a list"}`)
	require.ErrorContains(s.T(), s.store.Trust("/proj", ""), "parsing project config file")
	_, err := s.store.Status("/proj")
	require.ErrorContains(s.T(), err, "parsing project config file")
	s.SetupTest()

	s.store.readFile = func(string) ([]byte, error) { return nil, boom }
	_, err = s.store.Status("/proj")
	require.ErrorContains(s.T(), err, "reading project config file")
	require.ErrorContains(s.T(), s.store.Trust("/proj", ""), "reading project config file")
	s.SetupTest()

	s.store.readFile = func(p string) ([]byte, error) {
		if p == trustPath {
			return nil, boom
		}
		return []byte(`{"mounts": ["/a:/a"]}`), nil
	}
	_, err = s.store.Status("/proj")
	require.ErrorContains(s.T(), err, "reading /cfg/loop/project-trust.json")
	s.SetupTest()

	s.store.userConfigDir = func() (string, error) { return "", boom }
	s.project(`{"mounts": ["/a:/a"]}`)
	_, err = s.store.Status("/proj")
	require.ErrorContains(s.T(), err, "locating the config dir")
	s.SetupTest()

	// Readable trust file, but not after the read (no config dir).
	s.project(`{"mounts": ["/a:/a"]}`)
	calls := 0
	s.store.userConfigDir = func() (string, error) {
		calls++
		if calls > 1 {
			return "", boom
		}
		return "/cfg", nil
	}
	require.ErrorContains(s.T(), s.store.Trust("/proj", ""), "locating the config dir")
	s.SetupTest()

	s.project(`{"mounts": ["/a:/a"]}`)
	s.files[trustPath] = []byte(`{"/proj": {"fields": "not an object"}}`)
	_, err = s.store.Status("/proj")
	require.ErrorContains(s.T(), err, "parsing trusted config of /proj")
	s.SetupTest()

	// Keep: what the owner wrote doesn't parse, or the trust file doesn't.
	require.ErrorContains(s.T(), s.store.Keep("/proj", nil, []byte(`{not hjson`)), "parsing project config file")
	s.files[trustPath] = []byte("not json")
	require.ErrorContains(s.T(), s.store.Keep("/proj", []byte(`{"mounts": ["/a:/a"]}`), nil), "parsing /cfg/loop/project-trust.json")
	s.SetupTest()

	// Adopt: the trust file doesn't parse, or can't be written.
	s.project(`{"mounts": ["/a:/a"]}`)
	s.files[trustPath] = []byte("not json")
	require.ErrorContains(s.T(), s.store.Adopt("/proj"), "parsing /cfg/loop/project-trust.json")
	s.SetupTest()
	s.project(`{"mounts": ["/a:/a"]}`)
	s.store.writeFile = func(string, []byte, os.FileMode) error { return boom }
	require.ErrorContains(s.T(), s.store.Adopt("/proj"), "writing /cfg/loop/project-trust.json.tmp")
}

func (s *TrustSuite) TestMemoryPaths() {
	require.Nil(s.T(), s.store.MemoryPaths("/proj"), "no project config")
	s.project(`{"memory": {"paths": ["/docs"]}}`)
	require.Nil(s.T(), s.store.MemoryPaths("/proj"), "not trusted yet")
	require.NoError(s.T(), s.store.Trust("/proj", ""))
	require.Equal(s.T(), []string{"/docs"}, s.store.MemoryPaths("/proj"))
	s.project(`{not valid`)
	require.Nil(s.T(), s.store.MemoryPaths("/proj"))
}

func (s *TrustSuite) TestNewTrustStore() {
	st := NewTrustStore()
	require.NotNil(s.T(), st.userConfigDir)
	require.NotNil(s.T(), st.readFile)
	require.NotNil(s.T(), st.writeFile)
	require.NotNil(s.T(), st.mkdirAll)
	require.NotNil(s.T(), st.rename)
	require.NotNil(s.T(), st.now)
	require.NotNil(s.T(), newLoader().trust)
}

// TestNewProjectLoader: a nil trust store applies the trusted fields as
// written; a store holds them back until trusted.
func (s *TrustSuite) TestNewProjectLoader() {
	dir := s.T().TempDir()
	require.NoError(s.T(), os.MkdirAll(filepath.Join(dir, ".loop"), 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, ".loop", "config.json"), []byte(`{"extra_dirs": ["/x"]}`), 0o644))
	cfgDir := s.T().TempDir()
	tests := []struct {
		name  string
		trust *TrustStore
		want  []string
	}{
		{"no trust store", nil, []string{"/x"}},
		{"untrusted", NewTrustStoreIn(func() (string, error) { return cfgDir, nil }), nil},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			l := NewProjectLoader(tc.trust)
			merged, err := l.LoadProject(dir, &Config{})
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.want, merged.ExtraDirs)
			merged, err = l.LoadWorktreeProject(dir, "", &Config{})
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.want, merged.ExtraDirs)
		})
	}
}

// TestWorktreeParentDirNeedsNoTrust: a worktree config's seeded parent dir
// applies untrusted; anything else in extra_dirs waits for the owner.
func (s *TrustSuite) TestWorktreeParentDirNeedsNoTrust() {
	s.files["/proj/.worktrees/wt/.loop/config.json"] = []byte(`{"extra_dirs": ["/proj", "/evil"]}`)
	merged, err := s.loader().loadWorktreeProjectConfig("/proj/.worktrees/wt", "/proj", &Config{})
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"/proj"}, merged.ExtraDirs)

	s.files["/proj/.worktrees/wt/.loop/config.json"] = []byte(`{"extra_dirs": ["/evil"]}`)
	merged, err = s.loader().loadWorktreeProjectConfig("/proj/.worktrees/wt", "/proj", &Config{})
	require.NoError(s.T(), err)
	require.Empty(s.T(), merged.ExtraDirs)

	require.False(s.T(), seedsParentDir([]byte(`{not hjson`), "/proj"))
}

func (s *TrustSuite) TestParseProjectConfigLeavesInput() {
	data := []byte("{\n  // note\n  \"mounts\": [\"/a:/a\"],\n}\n")
	orig := string(data)
	_, err := parseProjectConfig(data)
	require.NoError(s.T(), err)
	require.Equal(s.T(), orig, string(data))
}

// ruleIndex returns the index of the first file rule naming path with the
// given decision, or -1.
func ruleIndex(rules []types.FileRule, path string, decision types.Decision) int {
	return slices.IndexFunc(rules, func(r types.FileRule) bool {
		return r.Decision == decision && slices.Contains(r.Paths, path)
	})
}

// TestTrustedProjectRulesPrecedeOverridableDenies: a trusted project's
// allow for ~/.kube goes before the built-in credentials deny, never before
// a deny that guards more, and the last approved rules of an edited config
// go back under every global deny.
func (s *TrustSuite) TestTrustedProjectRulesPrecedeOverridableDenies() {
	const kube = "**/.kube/**"
	main := gateMainCfg()
	defaults := main.Gates.Agentgate.FileRules
	creds := ruleIndex(defaults, kube, types.DecisionDeny)
	require.True(s.T(), defaults[creds].Overridable)

	s.project(`{"gates": {
		"agentgate": {
			"file_rules": [{"paths": ["**/.kube/**", "**/.aws/**"], "operations": ["read", "write", "create"], "decision": "allow"}],
			"path_rules": [{"pattern": "/run/x.sock", "decision": "allow"}],
			"command_rules": [{"commands": ["rm"], "args_patterns": ["^-rf /scratch/"], "decision": "allow"}]
		},
		"docker_proxy": {"http_rules": [{"methods": ["GET"], "paths": ["^/x$"], "decision": "allow"}]}
	}}`)
	merged, err := s.loader().loadProjectConfig("/proj", main)
	require.NoError(s.T(), err)
	require.Equal(s.T(), defaults, merged.Gates.Agentgate.FileRules, "never trusted: no project rules")

	require.NoError(s.T(), s.store.Trust("/proj", ""))
	merged, err = s.loader().loadProjectConfig("/proj", main)
	require.NoError(s.T(), err)
	rules := merged.Gates.Agentgate.FileRules
	allow := ruleIndex(rules, kube, types.DecisionAllow)
	require.Less(s.T(), allow, ruleIndex(rules, kube, types.DecisionDeny), "trusted: before the credentials deny")
	for _, r := range rules[:allow] {
		require.Equal(s.T(), types.DecisionDeny, r.Decision)
		require.False(s.T(), r.Overridable, "only the denies that stay first come before it")
	}
	require.Less(s.T(), ruleIndex(rules, "/etc/shadow", types.DecisionDeny), allow, "root credentials stay first")
	require.Equal(s.T(), "/var/run/docker.sock.host", merged.Gates.Agentgate.PathRules[0].Pattern, "no path deny is overridable")
	cmds := merged.Gates.Agentgate.CommandRules
	require.Less(s.T(),
		slices.IndexFunc(cmds, func(r types.CommandRule) bool {
			return r.Decision == types.DecisionAllow && slices.Contains(r.ArgsPatterns, "^-rf /scratch/")
		}),
		slices.IndexFunc(cmds, func(r types.CommandRule) bool { return r.Decision == types.DecisionDeny && r.Overridable }),
		"trusted: before the rm -rf deny")
	require.Equal(s.T(), countDeny(main.Gates.DockerProxy.HTTPRules, func(r types.HTTPServiceRule) types.Decision { return r.Decision }),
		slices.IndexFunc(merged.Gates.DockerProxy.HTTPRules, func(r types.HTTPServiceRule) bool { return slices.Contains(r.Paths, "^/x$") }),
		"no docker proxy deny is overridable")

	// An agent edits the config: the approved rules stay in effect, but
	// under every global deny until the owner trusts the change.
	s.project(`{"gates": {"agentgate": {"file_rules": [{"paths": ["/etc/**"], "operations": ["write"], "decision": "allow"}]}}}`)
	merged, err = s.loader().loadProjectConfig("/proj", main)
	require.NoError(s.T(), err)
	rules = merged.Gates.Agentgate.FileRules
	allow = ruleIndex(rules, kube, types.DecisionAllow)
	require.Equal(s.T(), countDeny(defaults, func(r types.FileRule) types.Decision { return r.Decision }), allow)
	require.Greater(s.T(), allow, ruleIndex(rules, kube, types.DecisionDeny))
	require.Equal(s.T(), -1, ruleIndex(rules, "/etc/**", types.DecisionAllow), "the unapproved change doesn't apply")
}

// TestTrustedWorktreeKeepsParentOverride: a worktree's own rules don't
// push a trusted parent's allow back under the built-in deny it overrides.
func (s *TrustSuite) TestTrustedWorktreeKeepsParentOverride() {
	const kube = "**/.kube/**"
	s.project(`{"gates": {"agentgate": {"file_rules": [{"paths": ["**/.kube/**"], "operations": ["read"], "decision": "allow"}]}}}`)
	require.NoError(s.T(), s.store.Trust("/proj", ""))
	const wt = "/proj/.worktrees/wt"
	s.files[wt+"/.loop/config.json"] = []byte(`{"gates": {"agentgate": {"file_rules": [{"paths": ["/w/**"], "operations": ["read"], "decision": "deny"}]}}}`)
	require.NoError(s.T(), s.store.Trust(wt, ""))

	merged, err := s.loader().loadWorktreeProjectConfig(wt, "/proj", gateMainCfg())
	require.NoError(s.T(), err)
	rules := merged.Gates.Agentgate.FileRules
	allow := ruleIndex(rules, kube, types.DecisionAllow)
	require.Less(s.T(), ruleIndex(rules, "/w/**", types.DecisionDeny), allow)
	require.Less(s.T(), allow, ruleIndex(rules, kube, types.DecisionDeny))
}
