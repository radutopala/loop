// trust_spec_test.go checks project config trust as far as the container
// the daemon creates for an agent.
//
// The property: for any project config an agent could write, under any
// trust state, what the runner hands Docker and the host for an agent run
// (the ContainerCreate config: image, cmd, env, binds, masks, volumes,
// caps, labels; the files it copies into the container; the gate and
// docker proxy policy files and the MCP config it writes) is what the
// trusted base gives. The trusted base is the project config the owner last
// trusted, with the file's own untrusted fields: a change an agent makes to
// a trusted field (see trustedFields in internal/config) changes nothing of
// it until the owner trusts the change. A project the owner never trusted
// has no trusted fields: it runs on the global config's.
//
// The trusted fields come from internal/config/testdata/trusted_fields.json,
// which internal/config's TestTrustedFieldsFile keeps in step with its field
// classification, so a new trusted field is checked here too.
package container

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/types"
)

// specRecorder records what a run did, in order, one line per call.
type specRecorder struct {
	lines []string
}

func (r *specRecorder) add(kind string, v any) {
	b, _ := json.Marshal(v) // plain data always encodes
	r.lines = append(r.lines, kind+" "+string(b))
}

// specClient is a DockerClient that records the containers the runner
// creates and what it copies into them, and runs each as a successful
// turn. The runner calls nothing else on it in an agent run; anything else
// panics on the nil embedded interface.
type specClient struct {
	DockerClient
	rec *specRecorder
}

func (c *specClient) ContainerCreate(_ context.Context, cfg *ContainerConfig, name string) (string, error) {
	created := *cfg
	// The project's envs come from a map: their order is random, and each
	// key is set once, so a stable sort by key keeps which of a key's
	// values Docker sees last.
	created.Env = slices.Clone(cfg.Env)
	sort.SliceStable(created.Env, func(i, j int) bool {
		ki, _, _ := strings.Cut(created.Env[i], "=")
		kj, _, _ := strings.Cut(created.Env[j], "=")
		return ki < kj
	})
	c.rec.add("create", map[string]any{"name": name, "config": created})
	return "cid", nil
}

func (c *specClient) CopyToContainer(_ context.Context, _, dst string, content io.Reader) error {
	b, _ := io.ReadAll(content)
	c.rec.add("copy", map[string]string{"dst": dst, "tar": string(b)})
	return nil
}

func (c *specClient) ContainerStart(context.Context, string) error { return nil }

func (c *specClient) ContainerWait(context.Context, string) (<-chan WaitResponse, <-chan error) {
	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{}
	return waitCh, make(chan error)
}

func (c *specClient) ContainerLogs(context.Context, string) (io.Reader, error) {
	return strings.NewReader(`{"type":"result","result":"ok","session_id":"s1"}`), nil
}

// specDirInfo is the os.FileInfo of a dir.
type specDirInfo struct{ os.FileInfo }

func (specDirInfo) IsDir() bool { return true }

// specSystem is a host where every path exists, as a dir, and reads as its
// own name. It records what the runner writes and creates.
type specSystem struct {
	rec *specRecorder
}

func (s *specSystem) Stat(string) (os.FileInfo, error) { return specDirInfo{}, nil }
func (s *specSystem) ReadFile(name string) ([]byte, error) {
	return []byte("content of " + name), nil
}
func (s *specSystem) WriteFile(name string, data []byte, _ os.FileMode) error {
	s.rec.add("write", map[string]string{"path": name, "data": string(data)})
	return nil
}
func (s *specSystem) Remove(name string) error {
	s.rec.add("remove", name)
	return nil
}
func (s *specSystem) MkdirAll(path string, _ os.FileMode) error {
	s.rec.add("mkdir", path)
	return nil
}
func (s *specSystem) MkdirAllIn(_, name string, _ os.FileMode) error {
	s.rec.add("mkdir", name)
	return nil
}
func (s *specSystem) WriteFileIn(_, name string, data []byte, perm os.FileMode) error {
	return s.WriteFile(name, data, perm)
}
func (s *specSystem) Readlink(string) (string, error)     { return "", os.ErrNotExist }
func (s *specSystem) EvalSymlinks(string) (string, error) { return "", os.ErrNotExist }
func (s *specSystem) UserHomeDir() (string, error)        { return "/home/u", nil }
func (s *specSystem) Getenv(key string) string {
	if key == "USER" {
		return "u"
	}
	return ""
}
func (s *specSystem) Getuid() int                                         { return 1000 }
func (s *specSystem) Getgid() int                                         { return 1000 }
func (s *specSystem) ExecCommandOutput(string, ...string) ([]byte, error) { return nil, nil }

// specContainerMain is a global config that sets everything a project's
// trusted fields reach in the container, so a project that changes one
// shows: mounts (the gh and glab config ones included), extra dirs, copied
// files, envs, the gateway, and both gates with rules of every kind.
func specContainerMain() *config.Config {
	return &config.Config{
		ClaudeBinPath:     "claude",
		ContainerImage:    "loop-agent:latest",
		ContainerMemoryMB: 512,
		ContainerCPUs:     1,
		APIAddr:           ":8222",
		LoopDir:           "/loop",
		// The glab config mount comes from gitlab.mount_glab_config alone,
		// so gitlab.glab_config_dir moves it.
		Mounts:           []string{"/g:/g", "~/.config/gh:~/.config/gh"},
		ExtraDirs:        []string{"/global-extra"},
		CopyFiles:        []string{"~/.global"},
		Envs:             map[string]string{"G": "1"},
		AnthropicBaseURL: "https://gateway.example.com",
		GitHub:           config.GitHubConfig{MountGHConfig: true},
		GitLab:           config.GitLabConfig{GlabConfigDir: "/global/glab", MountGlabConfig: true},
		Browser:          config.BrowserConfig{Enabled: true, Mode: "docker", HostCDPPort: 9222, Extensions: []string{"/global/ext"}},
		Memory:           config.MemoryConfig{Enabled: true, Paths: []string{"/global/memory"}},
		Gates: config.GatesConfig{
			Agentgate: config.AgentgateConfig{
				Enabled:      true,
				PathRules:    []types.PathRule{{Pattern: "/g/**", Decision: types.DecisionDeny}},
				CommandRules: []types.CommandRule{{Commands: []string{"curl"}, Decision: types.DecisionApprove}},
				FileRules:    []types.FileRule{{Paths: []string{"**/.aws/**"}, Decision: types.DecisionDeny}},
			},
			DockerProxy: config.DockerProxyConfig{
				Enabled:   true,
				HTTPRules: []types.HTTPServiceRule{{Methods: []string{"POST"}, Paths: []string{"/g"}, Decision: types.DecisionDeny}},
				BodyRules: []types.BodyRule{{AppliesTo: "/g", Decision: types.DecisionDeny}},
			},
		},
	}
}

// loaderFor returns the project config loader the runner gets, given the
// owner's trust store. config.NewProjectLoader is the daemon's; a loader
// that ignores trust is the spec's can-fail check.
type loaderFor func(trust *config.TrustStore) *config.Loader

// specLayout is where the project config an agent writes sits.
type specLayout struct {
	name string
	// worktree runs the agent in a worktree of the project.
	worktree bool
	// inWorktree puts the config in the worktree's own .loop, not the
	// project's.
	inWorktree bool
}

var specLayouts = []specLayout{
	{name: "project"},
	{name: "worktree"},
	{name: "worktree's own config", worktree: true, inWorktree: true},
}

// specProject is a project dir, its worktree and the owner's trust store,
// on disk under one temp dir.
type specProject struct {
	t        *testing.T
	layout   specLayout
	dir      string
	worktree string
	trustDir string
	store    *config.TrustStore
	loader   *config.Loader
}

func newSpecProject(t *testing.T, layout specLayout, newLoader loaderFor) *specProject {
	root := t.TempDir()
	p := &specProject{
		t:        t,
		layout:   layout,
		dir:      filepath.Join(root, "proj"),
		worktree: filepath.Join(root, "proj", ".worktrees", "wt"),
		trustDir: filepath.Join(root, "trust"),
	}
	require.NoError(t, os.MkdirAll(filepath.Join(p.worktree, ".loop"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(p.dir, ".loop"), 0o755))
	p.store = config.NewTrustStoreIn(func() (string, error) { return p.trustDir, nil })
	p.loader = newLoader(p.store)
	return p
}

// configDir is the dir whose .loop/config.json the agent writes.
func (p *specProject) configDir() string {
	if p.layout.inWorktree {
		return p.worktree
	}
	return p.dir
}

// write puts content in the project config, or removes it for nil.
func (p *specProject) write(content []byte) {
	path := filepath.Join(p.configDir(), ".loop", "config.json")
	if content == nil {
		require.NoError(p.t, os.Remove(path))
		return
	}
	require.NoError(p.t, os.WriteFile(path, content, 0o644))
}

// trust records the project config as it is as trusted.
func (p *specProject) trust() {
	require.NoError(p.t, p.store.Trust(p.configDir(), ""))
}

// run runs one agent turn the way the daemon does and returns what the
// runner did, in order.
func (p *specProject) run() string {
	rec := &specRecorder{}
	r := NewDockerRunner(&specClient{rec: rec}, specContainerMain(), nil)
	r.sys = &specSystem{rec: rec}
	r.loadProjectConfig = p.loader.LoadProject
	r.loadWorktreeProjectConfig = p.loader.LoadWorktreeProject
	r.instanceID = "spec"
	r.osRandRead = func(b []byte) (int, error) {
		for i := range b {
			b[i] = 0xab
		}
		return len(b), nil
	}
	r.osTimeLocalName = func() string { return "UTC" }
	r.transcriptMissing = func(string, string) bool { return false }
	r.SetDockerProxyDeps("/run/loop", "")
	r.SetProtectedDirs(DefaultProtectedDirs("/run/loop", func() (string, error) { return p.trustDir, nil }))

	req := &agent.AgentRequest{ChannelID: "ch-1", DirPath: p.dir, AuthorID: "u1", Prompt: "hi"}
	if p.layout.worktree {
		req.DirPath, req.ParentDirPath = p.worktree, p.dir
	}
	_, err := r.Run(context.Background(), req)
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	rec.add("run", errText)
	return strings.Join(rec.lines, "\n")
}

// containerTrustRuns runs an agent on base, trusted by the owner or never
// trusted, then on current (nil: the file deleted), and returns both
// runs. The property holds when they're equal.
func containerTrustRuns(t *testing.T, newLoader loaderFor, layout specLayout, base []byte, trusted bool, current []byte) (want, got string) {
	t.Helper()
	p := newSpecProject(t, layout, newLoader)
	p.write(base)
	if trusted {
		p.trust()
	}
	want = p.run()
	p.write(current)
	got = p.run()
	return want, got
}

// runDiff returns the lines of two runs that differ, "-" for want's and
// "+" for got's, or nil when the property holds: the runs are equal, or got
// was refused before anything reached the host or a container, because the
// project config doesn't parse. An agent can always break its own runs.
func runDiff(want, got string) []string {
	if want == got || strings.HasPrefix(got, `run "loading project config: parsing project config file: `) {
		return nil
	}
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	var out []string
	for i := range max(len(w), len(g)) {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			out = append(out, "- "+wl, "+ "+gl)
		}
	}
	return out
}

// specField is a trusted field of a project config.
type specField struct {
	Path   string          `json:"path"`
	Sample json.RawMessage `json:"sample"`
}

// keys is the field's JSON path; a plugin id (*) is "p".
func (f specField) keys() []string {
	keys := strings.Split(f.Path, ".")
	for i, k := range keys {
		if k == "*" {
			keys[i] = "p"
		}
	}
	return keys
}

// loadSpecFields reads the trusted fields internal/config classifies.
func loadSpecFields(t testing.TB) []specField {
	data, err := os.ReadFile(filepath.Join("..", "config", "testdata", "trusted_fields.json"))
	require.NoError(t, err)
	var fields []specField
	require.NoError(t, json.Unmarshal(data, &fields))
	require.NotEmpty(t, fields)
	return fields
}

// specUntrusted is the untrusted part of the project configs the spec
// writes: it applies as written in every run, so the runs differ only by
// what the trusted fields do.
const specUntrusted = `{"claude_model": "claude-opus-5-5", "proxies": {"no_proxy": ["svc"]}, "mcp": {"servers": {"s": {"command": "x"}}}}`

// specConfig is a project config as nested JSON objects.
type specConfig map[string]any

func parseSpecConfig(t testing.TB, data string) specConfig {
	var c specConfig
	require.NoError(t, json.Unmarshal([]byte(data), &c))
	return c
}

func (c specConfig) bytes() []byte {
	b, _ := json.Marshal(c) // plain data always encodes
	return b
}

// with returns c with keys set to v.
func (c specConfig) with(keys []string, v any) specConfig {
	out := specConfig{}
	for k, x := range c {
		out[k] = x
	}
	if len(keys) == 1 {
		out[keys[0]] = v
		return out
	}
	inner, _ := out[keys[0]].(map[string]any)
	out[keys[0]] = map[string]any(specConfig(inner).with(keys[1:], v))
	return out
}

// without returns c with keys removed.
func (c specConfig) without(keys []string) specConfig {
	out := specConfig{}
	for k, x := range c {
		out[k] = x
	}
	if len(keys) == 1 {
		delete(out, keys[0])
		return out
	}
	if inner, ok := out[keys[0]].(map[string]any); ok {
		out[keys[0]] = map[string]any(specConfig(inner).without(keys[1:]))
	}
	return out
}

// sampleOf returns f's sample as a JSON value.
func sampleOf(t testing.TB, f specField) any {
	var v any
	require.NoError(t, json.Unmarshal(f.Sample, &v))
	return v
}

// zeroOf returns the empty value of v's kind, and false for true.
func zeroOf(v any) any {
	switch x := v.(type) {
	case []any:
		return []any{}
	case map[string]any:
		return map[string]any{}
	case string:
		return ""
	case float64:
		return 0
	case bool:
		return !x
	}
	return nil
}

// specCase is one run of the property: base, trusted or not, then current.
type specCase struct {
	name    string
	base    []byte
	trusted bool
	current []byte
}

// containerSamples are values of trusted fields, besides the classified
// sample, that reach the container where the sample doesn't: the runner
// drops a bind of /, an ancestor of the dirs it protects.
var containerSamples = map[string]string{
	"mounts": `["/srv/data:/data"]`,
}

// fieldCases are the property's cases for field f: f added to the
// untrusted base, never trusted and trusted without it, and, from a
// trusted base that has every trusted field set (full), f removed, set to
// null and emptied.
func fieldCases(t testing.TB, f specField, full specConfig) []specCase {
	untrusted := parseSpecConfig(t, specUntrusted)
	keys := f.keys()
	sample := sampleOf(t, f)
	samples := []any{sample}
	if extra, ok := containerSamples[f.Path]; ok {
		samples = append(samples, sampleOf(t, specField{Sample: json.RawMessage(extra)}))
	}
	var cases []specCase
	for _, v := range samples {
		added := untrusted.with(keys, v).bytes()
		cases = append(cases,
			specCase{name: "added " + string(mustJSON(v)) + ", never trusted", base: untrusted.bytes(), current: added},
			specCase{name: "added " + string(mustJSON(v)) + " to a trusted config", base: untrusted.bytes(), trusted: true, current: added},
		)
	}
	return append(cases,
		specCase{name: "removed", base: full.bytes(), trusted: true, current: full.without(keys).bytes()},
		specCase{name: "set to null", base: full.bytes(), trusted: true, current: full.with(keys, nil).bytes()},
		specCase{name: "emptied", base: full.bytes(), trusted: true, current: full.with(keys, zeroOf(sample)).bytes()},
	)
}

// mustJSON encodes plain data.
func mustJSON(v any) []byte {
	b, _ := json.Marshal(v) // plain data always encodes
	return b
}

// fullSpecConfig is the untrusted base with every trusted field set to its
// sample.
func fullSpecConfig(t testing.TB, fields []specField) specConfig {
	full := parseSpecConfig(t, specUntrusted)
	for _, f := range fields {
		full = full.with(f.keys(), sampleOf(t, f))
	}
	return full
}

// trustOf is the daemon's loader: project configs gated by the owner's
// trust.
func trustOf(trust *config.TrustStore) *config.Loader { return config.NewProjectLoader(trust) }

// ignoringTrust is a loader that applies every project config as written.
func ignoringTrust(*config.TrustStore) *config.Loader { return config.NewProjectLoader(nil) }

type ContainerTrustSpecSuite struct {
	suite.Suite
}

func TestContainerTrustSpecSuite(t *testing.T) {
	suite.Run(t, new(ContainerTrustSpecSuite))
}

// TestTrustedFieldsDontReachContainer checks the property for every trusted
// field, in every layout.
func (s *ContainerTrustSpecSuite) TestTrustedFieldsDontReachContainer() {
	fields := loadSpecFields(s.T())
	full := fullSpecConfig(s.T(), fields)
	for _, layout := range specLayouts {
		for _, f := range fields {
			for _, c := range fieldCases(s.T(), f, full) {
				s.Run(layout.name+"/"+f.Path+"/"+c.name, func() {
					want, got := containerTrustRuns(s.T(), trustOf, layout, c.base, c.trusted, c.current)
					require.Empty(s.T(), runDiff(want, got), "now %s", c.current)
				})
			}
		}
		untrusted := parseSpecConfig(s.T(), specUntrusted)
		// Deleting the file takes its untrusted fields too, so that case
		// starts from the trusted fields alone.
		trustedOnly := full
		for k := range untrusted {
			trustedOnly = trustedOnly.without([]string{k})
		}
		zeroed := full
		for _, f := range fields {
			zeroed = zeroed.with(f.keys(), zeroOf(sampleOf(s.T(), f)))
		}
		for _, c := range []specCase{
			{name: "file deleted", base: trustedOnly.bytes(), trusted: true},
			{name: "every trusted field removed", base: full.bytes(), trusted: true, current: untrusted.bytes()},
			{name: "every trusted field emptied", base: full.bytes(), trusted: true, current: zeroed.bytes()},
			{name: "every trusted field added, never trusted", base: untrusted.bytes(), current: full.bytes()},
		} {
			s.Run(layout.name+"/"+c.name, func() {
				want, got := containerTrustRuns(s.T(), trustOf, layout, c.base, c.trusted, c.current)
				require.Empty(s.T(), runDiff(want, got), "now %s", c.current)
			})
		}
	}
}

// containerTrustedFields are the trusted fields that reach the agent's
// container, through its image, a mount, an env, a copied file or a policy
// file.
var containerTrustedFields = []string{
	"container_image",
	"mounts",
	"inherit_mounts",
	"copy_files",
	"envs",
	"extra_dirs",
	"gates",
	"anthropic_base_url",
	"github.mount_gh_config",
	"gitlab.glab_config_dir",
	"gitlab.mount_glab_config",
}

// TestSpecCanFail: with a loader that ignores trust, the same cases see
// every trusted field that reaches the container change it, so the spec
// would catch trust going missing on the way to the container.
func (s *ContainerTrustSpecSuite) TestSpecCanFail() {
	fields := loadSpecFields(s.T())
	full := fullSpecConfig(s.T(), fields)
	reached := map[string]bool{}
	for _, f := range fields {
		for _, c := range fieldCases(s.T(), f, full) {
			want, got := containerTrustRuns(s.T(), ignoringTrust, specLayouts[0], c.base, c.trusted, c.current)
			if want != got {
				reached[f.Path] = true
			}
		}
	}
	var got []string
	for path := range reached {
		got = append(got, path)
	}
	require.ElementsMatch(s.T(), containerTrustedFields, got)
}

// FuzzContainerTrust checks the property for a trusted base config and one
// trusted field (by index into the trusted fields) set to value.
func FuzzContainerTrust(f *testing.F) {
	fields := loadSpecFields(f)
	full := fullSpecConfig(f, fields)
	for i, fl := range fields {
		f.Add(string(full.bytes()), uint(i), `"/etc:/etc"`)
		f.Add(specUntrusted, uint(i), string(fl.Sample))
	}
	f.Fuzz(func(t *testing.T, base string, field uint, value string) {
		var c specConfig
		var v any
		if json.Unmarshal([]byte(base), &c) != nil || c == nil || json.Unmarshal([]byte(value), &v) != nil {
			return
		}
		fl := fields[field%uint(len(fields))]
		current := c.with(fl.keys(), v).bytes()
		for _, layout := range specLayouts {
			want, got := containerTrustRuns(t, trustOf, layout, c.bytes(), true, current)
			require.Empty(t, runDiff(want, got), "%s: now %s", layout.name, current)
		}
	})
}
