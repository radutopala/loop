// trust_spec_test.go checks the project config trust boundary as a spec.
//
// The property: once a project config's trusted fields (see trustedFields)
// differ from the version the owner last trusted, the merged config's
// host-reaching values (trustView) are exactly the ones that version gives,
// whatever the file says now: changed, case-folded, duplicated, stripped or
// deleted. A project the owner never trusted gives the global config's
// values, as with no project config at all. Only a file whose trusted fields
// are the trusted version's, or that has none and was never trusted,
// applies as written.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/types"
)

const (
	specDir  = "/proj"
	specFile = "/proj/.loop/config.json"
)

// newMemTrustStore returns a TrustStore whose trust file lives in files.
func newMemTrustStore(files map[string][]byte) *TrustStore {
	return &TrustStore{
		userConfigDir: func() (string, error) { return "/cfg", nil },
		readFile: func(p string) ([]byte, error) {
			if b, ok := files[p]; ok {
				return b, nil
			}
			return nil, os.ErrNotExist
		},
		readIn: func(_, p string) ([]byte, error) {
			if b, ok := files[p]; ok {
				return b, nil
			}
			return nil, os.ErrNotExist
		},
		writeFile: func(p string, b []byte, _ os.FileMode) error {
			files[p] = b
			return nil
		},
		mkdirAll: func(string, os.FileMode) error { return nil },
		rename: func(from, to string) error {
			files[to] = files[from]
			delete(files, from)
			return nil
		},
		now: func() time.Time { return time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC) },
	}
}

// specMain is a global config that sets every value a project's trusted
// fields reach, so a project that changes one shows in trustView. Its gate
// rules have no overridable deny: a trusted project's rules go above those
// and an untrusted one's below, which TestTrustedProjectRulesPrecedeOverridableDenies
// covers; here the trusted and the untrusted version of the same rules merge
// alike.
func specMain() *Config {
	on := true
	return &Config{
		Mounts:    []string{"/g:/g", GHConfigMount, GlabConfigTarget + ":" + GlabConfigTarget},
		ExtraDirs: []string{"/global-extra"},
		CopyFiles: []string{".global"},
		Envs:      map[string]string{"G": "1"},
		Permissions: types.Permissions{
			Owners: types.RoleGrant{Users: []string{"owner"}},
		},
		Gates: GatesConfig{
			Agentgate: AgentgateConfig{
				Enabled:      true,
				PathRules:    []types.PathRule{{Pattern: "/g/**", Decision: types.DecisionDeny}},
				CommandRules: []types.CommandRule{{Commands: []string{"curl"}, Decision: types.DecisionApprove}},
				FileRules:    []types.FileRule{{Paths: []string{"**/.aws/**"}, Decision: types.DecisionDeny}},
			},
			DockerProxy: DockerProxyConfig{
				Enabled:   true,
				HTTPRules: []types.HTTPServiceRule{{Methods: []string{"POST"}, Paths: []string{"/g"}, Decision: types.DecisionDeny}},
				BodyRules: []types.BodyRule{{AppliesTo: "/g", Decision: types.DecisionDeny}},
			},
		},
		BashShortcuts:    []BashShortcut{{Name: "g", Command: "true"}},
		AnthropicBaseURL: "https://gateway.example.com",
		Browser: BrowserConfig{
			Mode:         "docker",
			HostCDPPort:  9222,
			Extensions:   []string{"/global/ext"},
			CookieImport: CookieImportConfig{Source: "chrome", Domains: []string{"example.com"}, Auto: true},
		},
		Memory: MemoryConfig{Paths: []string{"/global/memory"}},
		GitHub: GitHubConfig{MountGHConfig: true},
		GitLab: GitLabConfig{GlabConfigDir: "/global/glab", MountGlabConfig: true},
		Plugins: PluginsConfig{
			Enabled:    true,
			GateBinary: "/global/loop",
			DailyRuns:  5,
			ByID: map[string]PluginConfig{"p": {
				Enabled:   &on,
				Schedules: map[string]PluginSchedule{"a": {Type: "cron", Schedule: "0 * * * *"}},
			}},
		},
	}
}

// trustPluginView is the part of a plugin's config that needs trust.
type trustPluginView struct {
	Enabled   *bool                     `json:"enabled"`
	Schedules map[string]PluginSchedule `json:"schedules"`
}

// trustView is what of a merged config reaches past the agent's container
// through a project config's trusted fields, plus the plugins block's own
// keys, which a project can't set at all. No omitempty: an empty list that
// replaces a global one isn't an unset one.
type trustView struct {
	Mounts           []string                   `json:"mounts"`
	ExtraDirs        []string                   `json:"extra_dirs"`
	CopyFiles        []string                   `json:"copy_files"`
	Envs             map[string]string          `json:"envs"`
	Permissions      types.Permissions          `json:"permissions"`
	Gates            GatesConfig                `json:"gates"`
	BashShortcuts    []BashShortcut             `json:"bash_shortcuts"`
	Workflows        []WorkflowDef              `json:"workflows"`
	AnthropicBaseURL string                     `json:"anthropic_base_url"`
	BrowserMode      string                     `json:"browser_mode"`
	HostCDPPort      int                        `json:"host_cdp_port"`
	Extensions       []string                   `json:"extensions"`
	CookieImport     CookieImportConfig         `json:"cookie_import"`
	MemoryPaths      []string                   `json:"memory_paths"`
	MountGHConfig    bool                       `json:"mount_gh_config"`
	GitLab           GitLabConfig               `json:"gitlab"`
	PluginsEnabled   bool                       `json:"plugins_enabled"`
	GateBinary       string                     `json:"gate_binary"`
	DailyRuns        int                        `json:"daily_runs"`
	Plugins          map[string]trustPluginView `json:"plugins"`
}

// viewOf returns cfg's trustView as JSON, which compares the way the
// consumers read it: a gate rule's omitted empty list is the same as none.
func viewOf(cfg *Config) string {
	v := trustView{
		Mounts:           cfg.Mounts,
		ExtraDirs:        cfg.ExtraDirs,
		CopyFiles:        cfg.CopyFiles,
		Envs:             cfg.Envs,
		Permissions:      cfg.Permissions,
		Gates:            cfg.Gates,
		BashShortcuts:    cfg.BashShortcuts,
		Workflows:        cfg.Workflows,
		AnthropicBaseURL: cfg.AnthropicBaseURL,
		BrowserMode:      cfg.Browser.Mode,
		HostCDPPort:      cfg.Browser.HostCDPPort,
		Extensions:       cfg.Browser.Extensions,
		CookieImport:     cfg.Browser.CookieImport,
		MemoryPaths:      cfg.Memory.Paths,
		MountGHConfig:    cfg.GitHub.MountGHConfig,
		GitLab:           cfg.GitLab,
		PluginsEnabled:   cfg.Plugins.Enabled,
		GateBinary:       cfg.Plugins.GateBinary,
		DailyRuns:        cfg.Plugins.DailyRuns,
		Plugins:          map[string]trustPluginView{},
	}
	for id, c := range cfg.Plugins.ByID {
		if c.Enabled != nil || len(c.Schedules) > 0 {
			v.Plugins[id] = trustPluginView{Enabled: c.Enabled, Schedules: c.Schedules}
		}
	}
	b, _ := json.Marshal(v) // plain data always encodes
	return string(b)
}

// specEnv is one project dir with its trust file, both in memory.
type specEnv struct {
	files map[string][]byte
	store *TrustStore
}

func newSpecEnv() *specEnv {
	files := map[string][]byte{}
	return &specEnv{files: files, store: newMemTrustStore(files)}
}

// write puts content in the project config, or removes the file for nil.
func (e *specEnv) write(content []byte) {
	if content == nil {
		delete(e.files, specFile)
		return
	}
	e.files[specFile] = content
}

// load merges the project config onto specMain and returns its trustView.
func (e *specEnv) load() (string, error) {
	l := &Loader{readFile: e.store.readFile, readIn: e.store.readIn, trust: e.store}
	merged, err := l.loadProjectConfig(specDir, specMain())
	if err != nil {
		return "", err
	}
	return viewOf(merged), nil
}

// errText is err's message, or "" for nil.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// checkTrustSpec checks the property for a project config the owner trusted
// as approved and that's current now. It reports whether both parsed, so
// the property was checked at all.
func checkTrustSpec(t *testing.T, approved, current []byte) bool {
	t.Helper()
	pcCur, err := parseProjectConfig(current)
	if err != nil {
		return false
	}
	pcApproved, err := parseProjectConfig(approved)
	if err != nil {
		return false
	}
	canonCur := trustedFieldsOf(pcCur).canonical()
	canonApproved := trustedFieldsOf(pcApproved).canonical()
	mainView := viewOf(specMain())

	// Never trusted: the global values, as with no project config. Nothing
	// of the file's trusted fields is used, so even a bad mount can't fail
	// the load.
	e := newSpecEnv()
	e.write(current)
	got, err := e.load()
	require.NoError(t, err)
	require.Equal(t, mainView, got, "never trusted")
	st, err := e.store.Status(specDir)
	require.NoError(t, err)
	require.Equal(t, string(canonCur) == "{}", st.Trusted)
	require.Empty(t, e.store.MemoryPaths(specDir))

	// Trusted as approved: what approved gives, whatever the file is now.
	e = newSpecEnv()
	e.write(approved)
	require.NoError(t, e.store.Trust(specDir, ""))
	want, wantErr := e.load()
	wantMemory := e.store.MemoryPaths(specDir)
	for _, now := range [][]byte{current, nil} {
		e.write(now)
		got, err := e.load()
		require.Equal(t, errText(wantErr), errText(err))
		require.Equal(t, want, got, "trusted %s, now %q", canonApproved, now)
		st, err := e.store.Status(specDir)
		require.NoError(t, err)
		canonNow := canonCur
		if now == nil {
			canonNow = []byte("{}")
		}
		require.Equal(t, string(canonApproved) == string(canonNow), st.Trusted)
		if now != nil {
			require.Equal(t, wantMemory, e.store.MemoryPaths(specDir))
		}
	}
	return true
}

// FuzzProjectConfigTrust checks the property for project config texts:
// approved is the one the owner trusted, current the file now. Seeds are in
// testdata/fuzz/FuzzProjectConfigTrust.
func FuzzProjectConfigTrust(f *testing.F) {
	f.Fuzz(func(t *testing.T, approved, current string) {
		checkTrustSpec(t, []byte(approved), []byte(current))
	})
}

// fieldClass classifies one project config field: it needs trust, or it's
// safe to apply untrusted, for reason.
type fieldClass struct {
	// path is the field's JSON path; * stands for a plugin id.
	path    string
	trusted bool
	// sample is a JSON value that sets the field. A trusted one differs
	// from specMain's, so it shows in trustView once trusted.
	sample string
	reason string
}

// projectFieldClasses is every projectConfig field, by JSON path down to
// the blocks that mix trusted and untrusted fields. A new field fails
// TestFieldClassification until it's added here, with a reason when it
// applies untrusted.
var projectFieldClasses = []fieldClass{
	{path: "mounts", trusted: true, sample: `["/:/host"]`},
	{path: "inherit_mounts", trusted: true, sample: `false`},
	{path: "copy_files", trusted: true, sample: `["/etc/passwd"]`},
	{path: "envs", trusted: true, sample: `{"G": "2"}`},
	{path: "extra_dirs", trusted: true, sample: `["/"]`},
	{path: "permissions", trusted: true, sample: `{"owners": {"users": ["agent"]}}`},
	{path: "gates", trusted: true, sample: `{"agentgate": {"file_rules": [{"paths": ["**/.aws/**"], "decision": "allow"}]}}`},
	{path: "bash_shortcuts", trusted: true, sample: `[{"name": "x", "command": "id"}]`},
	{path: "workflows", trusted: true, sample: `[{"name": "w", "nodes": [{"id": "b", "type": "bash", "command": "id"}]}]`},
	{path: "anthropic_base_url", trusted: true, sample: `"https://evil.example.com"`},
	{path: "browser.mode", trusted: true, sample: `"host"`},
	{path: "browser.host_cdp_port", trusted: true, sample: `9333`},
	{path: "browser.extensions", trusted: true, sample: `[]`},
	{path: "browser.cookie_import", trusted: true, sample: `{"source": "firefox"}`},
	{path: "memory.paths", trusted: true, sample: `["/"]`},
	{path: "github.mount_gh_config", trusted: true, sample: `false`},
	{path: "gitlab.glab_config_dir", trusted: true, sample: `"/elsewhere"`},
	{path: "gitlab.mount_glab_config", trusted: true, sample: `false`},
	{path: "plugins.*.enabled", trusted: true, sample: `false`},
	{path: "plugins.*.schedules", trusted: true, sample: `{"a": {"type": "interval", "schedule": "1m"}}`},

	{path: "proxies.http_proxy", sample: `"http://proxy:3128"`, reason: "the agent container's HTTP_PROXY, which the agent can set for itself"},
	{path: "proxies.https_proxy", sample: `"http://proxy:3128"`, reason: "the agent container's HTTPS_PROXY, which the agent can set for itself"},
	{path: "proxies.no_proxy", sample: `["svc"]`, reason: "added to the agent container's NO_PROXY"},
	{path: "mcp.servers", sample: `{"s": {"command": "x"}}`, reason: "MCP servers are commands the agent's Claude starts inside its container"},
	{path: "claude_model", sample: `"opus"`, reason: "the agent's model"},
	{path: "claude_effort", sample: `"high"`, reason: "the agent's effort"},
	{path: "claude_bin_path", sample: `"/usr/bin/claude"`, reason: "a path inside the agent container"},
	{path: "claude_batch_disallowed_tools", sample: `["Bash"]`, reason: "Claude's tools inside the container"},
	{path: "claude_retry.max_attempts", sample: `3`, reason: "retries of the agent's own runs"},
	{path: "claude_retry.backoff_base_sec", sample: `3`, reason: "retries of the agent's own runs"},
	{path: "claude_retry.backoff_max_sec", sample: `3`, reason: "retries of the agent's own runs"},
	{path: "claude_retry.session_limit_auto_continue", sample: `true`, reason: "retries of the agent's own runs"},
	{path: "claude_code_oauth_token", sample: `"own"`, reason: "the project's own credentials; they replace the global ones, never reveal them"},
	{path: "anthropic_api_key", sample: `"own"`, reason: "the project's own credentials; they replace the global ones, never reveal them"},
	{path: "container_image", sample: `"img"`, reason: "the agent's own image, run with the same mounts, gates and privileges"},
	{path: "container_image_autobuild", sample: `true`, reason: "builds the agent's own image from the project"},
	{path: "container_memory_mb", sample: `1`, reason: "the agent container's own limits"},
	{path: "container_cpus", sample: `1`, reason: "the agent container's own limits"},
	{path: "keep_mcp_configs", sample: `true`, reason: "keeps the channel's MCP config files in its own dir"},
	{path: "browser.enabled", sample: `true`, reason: "the Docker-mode Chrome sidecar, a container like the agent's"},
	{path: "browser.chrome_image", sample: `"img"`, reason: "the Docker-mode Chrome sidecar's image"},
	{path: "browser.persist_profile", sample: `true`, reason: "the sidecar's per-channel profile volume"},
	{path: "browser.memory_mb", sample: `1`, reason: "the sidecar's own limits"},
	{path: "browser.cpus", sample: `1`, reason: "the sidecar's own limits"},
	{path: "task_templates", sample: `[{"name": "t", "prompt": "p"}]`, reason: "prompts for the agent; prompt_path stays under {loopDir}/templates"},
	{path: "workflow_concurrency.max_concurrent_runs", sample: `1`, reason: "limits"},
	{path: "workflow_concurrency.max_concurrent_nodes", sample: `1`, reason: "limits"},
	{path: "prompt_shortcuts", sample: `[{"name": "s", "prompt": "p"}]`, reason: "prompts for the agent; prompt_path stays under {loopDir}/shortcuts"},
	{path: "chat_components", sample: `[{"name": "c", "path": "../../x"}]`, reason: "template files under {loopDir}/components, which ChatComponent.Dir stays inside"},
	{path: "memory.enabled", sample: `true`, reason: "not read by the project merge"},
	{path: "memory.max_chunk_chars", sample: `1`, reason: "chunking of what's indexed"},
	{path: "memory.reindex_interval_sec", sample: `1`, reason: "not read by the project merge"},
	{path: "memory.embeddings", sample: `{"provider": "ollama", "ollama_url": "http://evil"}`, reason: "the daemon's embedder reads the global config only"},
	{path: "quality.max_files", sample: `1`, reason: "code quality scan of the project's own dir"},
	{path: "quality.exclude_paths", sample: `["x"]`, reason: "code quality scan of the project's own dir"},
	{path: "quality.rules", sample: `{"r": {"enabled": false}}`, reason: "code quality scan of the project's own dir"},
	{path: "quality.complexity", sample: `{"loc_t": 1}`, reason: "code quality thresholds"},
	{path: "quality.clones", sample: `{"min_loc": 1}`, reason: "code quality thresholds"},
	{path: "github.gh_user", sample: `"other"`, reason: "picks one of the gh accounts the owner logged in on the host for PR lookups; documented as applying untrusted"},
	{path: "review.enabled", sample: `true`, reason: "the review panel's agent run"},
	{path: "review.prompt", sample: `"p"`, reason: "the review panel's agent prompt"},
	{path: "review.prompt_path", sample: `"p.md"`, reason: "stays under {loopDir}/review"},
	{path: "review.verify", sample: `true`, reason: "the review panel's agent run"},
	{path: "review.verify_prompt", sample: `"p"`, reason: "the review panel's agent prompt"},
	{path: "review.max_iterations", sample: `1`, reason: "the review panel's agent run"},
	{path: "review.update_base", sample: `true`, reason: "updates the project's own checkout"},
	{path: "playground_share.enabled", sample: `true`, reason: "sharing reads the global config only"},
	{path: "learn.enabled", sample: `true`, reason: "the learn agent run"},
	{path: "learn.min_turns", sample: `1`, reason: "the learn agent run"},
	{path: "learn.model", sample: `"m"`, reason: "the learn agent run"},
	{path: "learn.effort", sample: `"e"`, reason: "the learn agent run"},
	{path: "learn.prompt", sample: `"p"`, reason: "the learn agent prompt"},
	{path: "explain.enabled", sample: `true`, reason: "the explain agent run"},
	{path: "explain.model", sample: `"m"`, reason: "the explain agent run"},
	{path: "explain.effort", sample: `"e"`, reason: "the explain agent run"},
	{path: "explain.prompt", sample: `"p"`, reason: "the explain agent prompt"},
	{path: "plugins.enabled", sample: `false`, reason: "the plugins block's own keys are global only: the merge ignores a project's"},
	{path: "plugins.gate_binary", sample: `"/tmp/evil"`, reason: "the plugins block's own keys are global only: the merge ignores a project's"},
	{path: "plugins.daily_runs", sample: `1000`, reason: "the plugins block's own keys are global only: the merge ignores a project's"},
	{path: "plugins.*.settings", sample: `{"k": "v"}`, reason: "plugin settings its manifest declares; the plugin runs with the capabilities the owner approved"},
}

// classJSON returns a project config that sets path to sample.
func classJSON(path, sample string) string {
	keys := strings.Split(path, ".")
	out := sample
	for _, k := range slices.Backward(keys) {
		if k == "*" {
			k = "p"
		}
		out = `{"` + k + `": ` + out + `}`
	}
	return out
}

// jsonFieldPaths walks typ's JSON fields under prefix, down to the paths
// classes has, and returns them. A plugins block's plugin ids are under *.
func jsonFieldPaths(typ reflect.Type, prefix string, classes map[string]bool) []string {
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	var out []string
	if typ == reflect.TypeFor[PluginsConfig]() {
		out = jsonFieldPaths(reflect.TypeFor[PluginConfig](), prefix+"*.", classes)
	}
	for f := range typ.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		path := prefix + name
		ft := f.Type
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if !classes[path] && ft.Kind() == reflect.Struct {
			out = append(out, jsonFieldPaths(ft, path+".", classes)...)
			continue
		}
		out = append(out, path)
	}
	return out
}

type TrustSpecSuite struct {
	suite.Suite
}

func TestTrustSpecSuite(t *testing.T) {
	suite.Run(t, new(TrustSpecSuite))
}

// TestFieldClassification: every project config field is classified, and
// the classes are the code's: a trusted field takes trust and is held back
// until it's given, then shows in trustView; an untrusted one needs none.
func (s *TrustSpecSuite) TestFieldClassification() {
	classes := map[string]bool{}
	for _, c := range projectFieldClasses {
		require.False(s.T(), classes[c.path], "%s classified twice", c.path)
		classes[c.path] = true
		require.True(s.T(), c.trusted || c.reason != "", "%s applies untrusted without a reason", c.path)
	}
	paths := jsonFieldPaths(reflect.TypeFor[projectConfig](), "", classes)
	want := make([]string, 0, len(projectFieldClasses))
	for _, c := range projectFieldClasses {
		want = append(want, c.path)
	}
	require.ElementsMatch(s.T(), want, paths, "projectConfig fields and projectFieldClasses differ: classify the new field")

	mainView := viewOf(specMain())
	for _, c := range projectFieldClasses {
		s.Run(c.path, func() {
			content := []byte(classJSON(c.path, c.sample))
			pc, err := parseProjectConfig(content)
			require.NoError(s.T(), err)
			canon := string(trustedFieldsOf(pc).canonical())
			if !c.trusted {
				require.Equal(s.T(), "{}", canon, "applies untrusted")
				return
			}
			require.NotEqual(s.T(), "{}", canon, "needs trust")

			e := newSpecEnv()
			e.write(content)
			got, err := e.load()
			require.NoError(s.T(), err)
			require.Equal(s.T(), mainView, got, "held back until trusted")
			require.NoError(s.T(), e.store.Trust(specDir, ""))
			got, err = e.load()
			require.NoError(s.T(), err)
			require.NotEqual(s.T(), mainView, got, "applies once trusted")
		})
	}
}

// trustedFieldsFile lists the trusted classes of projectFieldClasses, path
// and sample, for the container package's trust spec, which drives a
// container's create config with them and can't import this package's
// tests. TestTrustedFieldsFile keeps it in step;
// LOOP_UPDATE_TRUSTED_FIELDS=1 rewrites it.
const trustedFieldsFile = "testdata/trusted_fields.json"

// trustedFieldSample is one entry of trustedFieldsFile.
type trustedFieldSample struct {
	Path   string          `json:"path"`
	Sample json.RawMessage `json:"sample"`
}

// TestTrustedFieldsFile: trustedFieldsFile lists every trusted field, so a
// newly classified one reaches the container spec too.
func (s *TrustSpecSuite) TestTrustedFieldsFile() {
	var want []trustedFieldSample
	for _, c := range projectFieldClasses {
		if c.trusted {
			want = append(want, trustedFieldSample{Path: c.path, Sample: json.RawMessage(c.sample)})
		}
	}
	data, err := json.MarshalIndent(want, "", "  ")
	require.NoError(s.T(), err)
	data = append(data, '\n')
	if os.Getenv("LOOP_UPDATE_TRUSTED_FIELDS") == "1" {
		require.NoError(s.T(), os.WriteFile(trustedFieldsFile, data, 0o644))
	}
	got, err := os.ReadFile(trustedFieldsFile)
	require.NoError(s.T(), err)
	require.Equal(s.T(), string(data), string(got), "%s is out of date: rerun with LOOP_UPDATE_TRUSTED_FIELDS=1", trustedFieldsFile)
}

// TestSneakTrustedFields tries ways to get a trusted field past the owner:
// key spellings encoding/json folds onto a field, duplicate keys, nested
// blocks, stripping or deleting what the owner trusted, and changes
// canonical() could miss. Each must leave the merged trustView what the
// trusted version (or, with none, the global config) gives.
func (s *TrustSpecSuite) TestSneakTrustedFields() {
	const hostMount = `["/:/host"]`
	ghOff := `{"github": {"mount_gh_config": false}}`
	tests := []struct {
		name     string
		approved string // "" when never trusted
		current  string
		missing  bool
		trusted  bool
	}{
		{name: "upper case key", current: `{"MOUNTS": ` + hostMount + `}`},
		{name: "mixed case nested keys", current: `{"GitLab": {"GLAB_Config_Dir": "/x"}, "Browser": {"MODE": "host", "Host_CDP_Port": 1}}`},
		{name: "long s folds to s", current: "{\"mount\u017f\": " + hostMount + "}"},
		{name: "kelvin sign folds to k", current: "{\"browser\": {\"coo\u212aie_import\": {\"source\": \"firefox\"}}}"},
		{name: "escaped key", current: `{"m\u006funts": ` + hostMount + `}`},
		{name: "comments and trailing commas", current: "{/* x */ \"mounts\": [\"/:/host\",], // y\n}"},
		{name: "duplicate key, later wins", current: `{"mounts": [], "mounts": ` + hostMount + `}`},
		{name: "duplicate map keys merge", approved: `{"envs": {"A": "1"}}`, current: `{"envs": {"A": "1"}, "envs": {"B": "2"}}`},
		{name: "case variant duplicate after trusted key", approved: `{"mounts": ["/a:/a"]}`, current: `{"mounts": ["/a:/a"], "Mounts": ` + hostMount + `}`},
		{name: "case variant duplicate before trusted key", approved: `{"mounts": ["/a:/a"]}`, current: `{"Mounts": ` + hostMount + `, "mounts": ["/a:/a"]}`, trusted: true},
		{name: "plugin id case variant", approved: `{"plugins": {"p": {"enabled": false}}}`, current: `{"plugins": {"p": {"enabled": false}, "P": {"enabled": true}}}`},
		{name: "plugin enabled case variant", current: `{"plugins": {"q": {"ENABLED": true}}}`},
		{name: "plugins own keys", current: `{"plugins": {"enabled": false, "gate_binary": "/tmp/evil", "daily_runs": 1000}}`, trusted: true},
		{name: "memory paths case variant", current: `{"memory": {"Paths": ["/"]}}`},
		{name: "empty permissions wipe global", current: `{"permissions": {}}`},
		{name: "null clears a trusted field", approved: `{"mounts": ["/a:/a"]}`, current: `{"mounts": null}`},
		{name: "stripping every trusted field", approved: ghOff, current: `{"claude_model": "opus"}`},
		{name: "stripping one trusted field", approved: `{"inherit_mounts": false, "envs": {"G": ""}}`, current: `{"envs": {"G": ""}}`},
		{name: "deleting the file", approved: ghOff, missing: true},
		{name: "trusted stays trusted", approved: ghOff, current: "{\"github\": {\"mount_gh_config\": false, \"gh_user\": \"x\"}, \"claude_model\": \"opus\"}", trusted: true},
		{name: "extensions emptied", approved: `{"browser": {"mode": "docker"}}`, current: `{"browser": {"mode": "docker", "extensions": []}}`},
		{name: "extensions emptied, trusted", approved: `{"browser": {"mode": "docker", "extensions": []}}`, current: `{"browser": {"mode": "docker", "extensions": ["/x"]}}`},
		{name: "extensions only, emptied", current: `{"browser": {"extensions": []}}`},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			e := newSpecEnv()
			want := viewOf(specMain())
			if tc.approved != "" {
				e.write([]byte(tc.approved))
				require.NoError(s.T(), e.store.Trust(specDir, ""))
				var err error
				want, err = e.load()
				require.NoError(s.T(), err)
			}
			var current []byte
			if !tc.missing {
				current = []byte(tc.current)
			}
			e.write(current)
			got, err := e.load()
			require.NoError(s.T(), err)
			require.Equal(s.T(), want, got)
			st, err := e.store.Status(specDir)
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.trusted, st.Trusted)
			if !tc.missing {
				approved := tc.approved
				if approved == "" {
					approved = "{}"
				}
				require.True(s.T(), checkTrustSpec(s.T(), []byte(approved), current), "the property holds too")
			}
		})
	}
}

// TestStrippedConfigStaysUntrusted: an owner edit of a project whose
// trusted fields were all taken out since they trusted it doesn't approve
// that, and Status shows what was taken out.
func (s *TrustSpecSuite) TestStrippedConfigStaysUntrusted() {
	e := newSpecEnv()
	e.write([]byte(`{"github": {"mount_gh_config": false}}`))
	require.NoError(s.T(), e.store.Trust(specDir, ""))

	e.write([]byte(`{}`))
	st, err := e.store.Status(specDir)
	require.NoError(s.T(), err)
	require.False(s.T(), st.Trusted)
	require.Equal(s.T(), "{}", st.Current)
	require.Contains(s.T(), st.Diff, "-    \"mount_gh_config\": false")

	require.NoError(s.T(), e.store.Keep(specDir, []byte(`{}`), []byte(`{"claude_model": "opus"}`)))
	st, err = e.store.Status(specDir)
	require.NoError(s.T(), err)
	require.False(s.T(), st.Trusted, "an owner edit elsewhere doesn't approve it")

	require.NoError(s.T(), e.store.Trust(specDir, st.Hash))
	got, err := e.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), viewOf(specMain()), got, "trusting the stripped config lets it apply")
}

// TestUnreadableTrustFile: a project with nothing that needs trust loads
// as the global config when the trust file can't be read.
func (s *TrustSpecSuite) TestUnreadableTrustFile() {
	e := newSpecEnv()
	e.store.readFile = func(p string) ([]byte, error) {
		if p == specFile {
			return []byte(`{"claude_model": "opus"}`), nil
		}
		return nil, errors.New("boom")
	}
	got, err := e.load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), viewOf(specMain()), got)
}

// TestDeletedWorktreeConfigKeepsTrusted: deleting a worktree's own config
// keeps the version the owner trusted, like a project's.
func (s *TrustSpecSuite) TestDeletedWorktreeConfigKeepsTrusted() {
	const wt = "/proj/.worktrees/wt"
	e := newSpecEnv()
	l := &Loader{readFile: e.store.readFile, readIn: e.store.readIn, trust: e.store}
	e.files[wt+"/.loop/config.json"] = []byte(`{"github": {"mount_gh_config": false}}`)
	require.NoError(s.T(), e.store.Trust(wt, ""))
	delete(e.files, wt+"/.loop/config.json")

	merged, err := l.loadWorktreeProjectConfig(wt, specDir, specMain())
	require.NoError(s.T(), err)
	require.False(s.T(), merged.GitHub.MountGHConfig)
	require.NotContains(s.T(), merged.Mounts, GHConfigMount)

	// With nothing trusted for it, the parent's config is used as is.
	main := specMain()
	merged, err = l.loadWorktreeProjectConfig("/proj/.worktrees/other", "", main)
	require.NoError(s.T(), err)
	require.Same(s.T(), main, merged)
}

// TestChatComponentDirStaysInComponents: a template path can't leave
// {loopDir}/components.
func (s *TrustSpecSuite) TestChatComponentDirStaysInComponents() {
	tests := []struct {
		name string
		c    ChatComponent
		want string
	}{
		{name: "name", c: ChatComponent{Name: "math"}, want: "/loop/components/math"},
		{name: "path", c: ChatComponent{Name: "math", Path: "paper/a"}, want: "/loop/components/paper/a"},
		{name: "parent dirs", c: ChatComponent{Name: "x", Path: "../../../home/me/.ssh"}, want: "/loop/components/home/me/.ssh"},
		{name: "inner parent dirs", c: ChatComponent{Name: "x", Path: "a/../../b"}, want: "/loop/components/b"},
		{name: "absolute", c: ChatComponent{Name: "x", Path: "/etc"}, want: "/loop/components/etc"},
		{name: "name with parent dirs", c: ChatComponent{Name: ".."}, want: "/loop/components"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, tc.c.Dir("/loop"))
		})
	}
}
