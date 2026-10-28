// merge_spec_test.go checks the project config merge rules as a spec.
//
// The property: every project config field has a rule for how a layer's
// value goes onto the config below it (the global config, then the
// project's, then a worktree's), and a layer that sets one field changes
// the merged config by that rule alone: the field's own merged value, and
// nothing else. The trusted base is mergeField.layer, a few lines per rule,
// and the samples, which set each field to a value the layer below doesn't
// have. Trust doesn't take part: the loader here applies every field as
// written (trust_spec_test.go covers what's held back).
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	mergeProjectDir   = "/proj"
	mergeWorktreeDir  = "/wt"
	mergeProjectFile  = "/proj/.loop/config.json"
	mergeWorktreeFile = "/wt/.loop/config.json"
)

// mergeKind is how a layer's value for a field goes onto the one below.
type mergeKind string

const (
	// mergeReplace: the layer's value replaces the one below.
	mergeReplace mergeKind = "replace"
	// mergeAppend: the layer's entries go after the ones below.
	mergeAppend mergeKind = "append"
	// mergeOverlay: the layer's keys replace the same keys below; the
	// other keys below stay.
	mergeOverlay mergeKind = "overlay"
	// mergeNamed: a layer's entry replaces the one below with the same
	// name, in its place; new names go after.
	mergeNamed mergeKind = "by name"
	// mergeIgnored: the merge doesn't read the field; the value below stays.
	mergeIgnored mergeKind = "ignored"
	// mergeCustom: a rule of its own, in apply.
	mergeCustom mergeKind = "custom"
)

// mergeLayer is the project or worktree layer being merged.
type mergeLayer struct {
	dir      string
	worktree bool
}

// mergeField is one project config field's merge rule.
type mergeField struct {
	path string
	kind mergeKind
	// field points at the merged value the path sets.
	field func(c *Config) any
	// global is field's value below the project, as JSON. Fields that
	// share a merged value set it once.
	global string
	// project and worktree are the values each layer sets, as JSON; an
	// ignored field's worktree sets the project's.
	project, worktree string
	// conv turns a JSON value into field's value when their types differ.
	conv func(raw string) any
	// apply layers raw onto c, for mergeCustom.
	apply func(c *Config, raw string, l mergeLayer)
	// reason says why an ignored or custom field merges as it does.
	reason string
}

// value is raw as the merged value of type typ.
func (f mergeField) value(raw string, typ reflect.Type) reflect.Value {
	if f.conv != nil {
		return reflect.ValueOf(f.conv(raw))
	}
	v := reflect.New(typ)
	if err := json.Unmarshal([]byte(raw), v.Interface()); err != nil {
		panic(f.path + ": " + err.Error())
	}
	return v.Elem()
}

// layer applies raw, the layer l's value for f, to c by f's rule.
func (f mergeField) layer(c *Config, raw string, l mergeLayer) {
	switch f.kind {
	case mergeIgnored:
		return
	case mergeCustom:
		f.apply(c, raw, l)
		return
	}
	dst := reflect.ValueOf(f.field(c)).Elem()
	v := f.value(raw, dst.Type())
	switch f.kind {
	case mergeReplace:
		dst.Set(v)
	case mergeAppend:
		out := reflect.MakeSlice(dst.Type(), 0, dst.Len()+v.Len())
		dst.Set(reflect.AppendSlice(reflect.AppendSlice(out, dst), v))
	case mergeOverlay:
		out := reflect.MakeMap(dst.Type())
		for _, m := range []reflect.Value{dst, v} {
			for it := m.MapRange(); it.Next(); {
				out.SetMapIndex(it.Key(), it.Value())
			}
		}
		dst.Set(out)
	case mergeNamed:
		dst.Set(byKey(dst, v, func(e reflect.Value) string { return e.FieldByName("Name").String() }))
	}
}

// byKey returns base with each overlay entry replacing the base entry with
// the same key in its place, and the new keys after.
func byKey(base, overlay reflect.Value, key func(reflect.Value) string) reflect.Value {
	out := reflect.AppendSlice(reflect.MakeSlice(base.Type(), 0, base.Len()+overlay.Len()), base)
	for i := range overlay.Len() {
		e := overlay.Index(i)
		at := -1
		for j := range out.Len() {
			if key(out.Index(j)) == key(e) {
				at = j
				break
			}
		}
		if at < 0 {
			out = reflect.Append(out, e)
			continue
		}
		out.Index(at).Set(e)
	}
	return out
}

func decodeJSON[T any](raw string) T {
	var v T
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		panic(err)
	}
	return v
}

// specMountTarget is a mount's container path, without a trailing slash.
func specMountTarget(m string) string {
	parts := strings.Split(m, ":")
	return strings.TrimSuffix(parts[1], "/")
}

// layerMounts layers a project's mounts: a relative host path is the
// layer dir's (a name without a slash is a named volume), and a mount replaces the one below at the same container
// path.
func layerMounts(c *Config, raw string, l mergeLayer) {
	ms := decodeJSON[[]string](raw)
	for i, m := range ms {
		host, rest, _ := strings.Cut(m, ":")
		if strings.Contains(host, "/") && !filepath.IsAbs(host) && !strings.HasPrefix(host, "~") {
			ms[i] = filepath.Join(l.dir, host) + ":" + rest
		}
	}
	c.Mounts = byKey(reflect.ValueOf(c.Mounts), reflect.ValueOf(ms), func(e reflect.Value) string {
		return specMountTarget(e.String())
	}).Interface().([]string)
}

// configMount is the rule for a credentials mount switch: the value
// replaces the one below, and turning it off drops the mounts at or under
// target too.
func configMount(field func(c *Config) *bool, target string) func(c *Config, raw string, l mergeLayer) {
	return func(c *Config, raw string, _ mergeLayer) {
		on := decodeJSON[bool](raw)
		*field(c) = on
		if on {
			return
		}
		var kept []string
		for _, m := range c.Mounts {
			if t := specMountTarget(m); t != target && !strings.HasPrefix(t, target+"/") {
				kept = append(kept, m)
			}
		}
		c.Mounts = kept
	}
}

// permissionsLeaf is the rule for a permissions field: a layer's
// permissions replace the ones below whole, so the fields it doesn't set
// are empty.
func permissionsLeaf(leaf func(p *types.Permissions) *[]string) func(c *Config, raw string, l mergeLayer) {
	return func(c *Config, raw string, _ mergeLayer) {
		c.Permissions = types.Permissions{}
		*leaf(&c.Permissions) = decodeJSON[[]string](raw)
	}
}

// embeddingsLeaf is the rule for an embeddings field: a layer's embeddings
// block replaces the one below whole, with the Ollama URL defaulted.
func embeddingsLeaf(leaf func(e *EmbeddingsConfig) *string) func(c *Config, raw string, l mergeLayer) {
	return func(c *Config, raw string, _ mergeLayer) {
		c.Memory.Embeddings = EmbeddingsConfig{OllamaURL: "http://localhost:11434"}
		*leaf(&c.Memory.Embeddings) = decodeJSON[string](raw)
	}
}

// gateRules is the rule for a gate's rules: the deny rules below stay
// first, then the layer's rules, then the rest of the rules below. (A
// trusted project's rules also go above the overridable built-in denies,
// which trust_spec_test.go covers.)
func gateRules(field func(c *Config) any) func(c *Config, raw string, l mergeLayer) {
	return func(c *Config, raw string, _ mergeLayer) {
		dst := reflect.ValueOf(field(c)).Elem()
		v := reflect.New(dst.Type())
		if err := json.Unmarshal([]byte(raw), v.Interface()); err != nil {
			panic(err)
		}
		deny := func(r reflect.Value) bool { return r.FieldByName("Decision").String() == string(types.DecisionDeny) }
		out := reflect.MakeSlice(dst.Type(), 0, dst.Len()+v.Elem().Len())
		for i := range dst.Len() {
			if deny(dst.Index(i)) {
				out = reflect.Append(out, dst.Index(i))
			}
		}
		out = reflect.AppendSlice(out, v.Elem())
		for i := range dst.Len() {
			if !deny(dst.Index(i)) {
				out = reflect.Append(out, dst.Index(i))
			}
		}
		dst.Set(out)
	}
}

// pluginLeaf is the rule for a plugin's field: the layer sets it on that
// plugin, the one classJSON calls "p", and the other plugins stay.
func pluginLeaf(set func(pl *PluginConfig, raw string)) func(c *Config, raw string, l mergeLayer) {
	return func(c *Config, raw string, _ mergeLayer) {
		byID := map[string]PluginConfig{}
		for id, pl := range c.Plugins.ByID {
			byID[id] = pl
		}
		pl := byID["p"]
		set(&pl, raw)
		byID["p"] = pl
		c.Plugins.ByID = byID
	}
}

// overlayMap returns base with over's keys on top, as a new map.
func overlayMap[V any](base, over map[string]V) map[string]V {
	out := map[string]V{}
	for _, m := range []map[string]V{base, over} {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

func seconds(raw string) any { return time.Duration(decodeJSON[int](raw)) * time.Second }

const ignoredGatesReason = "the global config stays the baseline: a project adds gate rules, nothing else"

// mergeFields classifies every project config field; TestFieldClassification
// fails for a new field until it's added here.
var mergeFields = []mergeField{
	{path: "mounts", kind: mergeCustom, field: func(c *Config) any { return &c.Mounts },
		global:  `["/g:/m", "~/.config/gh:~/.config/gh", "~/.config/glab-cli/hosts:~/.config/glab-cli/hosts", "/keep:/keep"]`,
		project: `["./rel:/m", "/abs:/new", "vol:/vol"]`, worktree: `["wrel/x:/new/", "/w:/w"]`,
		apply: layerMounts, reason: "added by container path, a mount replacing the one below at the same path; a relative host path is the layer dir's"},
	{path: "inherit_mounts", kind: mergeCustom, project: `false`, worktree: `true`,
		apply: func(c *Config, raw string, _ mergeLayer) {
			if !decodeJSON[bool](raw) {
				c.Mounts = []string{}
			}
		}, reason: "false makes the layer's mounts replace the ones below"},
	{path: "proxies.http_proxy", kind: mergeReplace, field: func(c *Config) any { return &c.Proxies.HTTPProxy }, global: `"http://g"`, project: `"http://p"`, worktree: `"http://w"`},
	{path: "proxies.https_proxy", kind: mergeReplace, field: func(c *Config) any { return &c.Proxies.HTTPSProxy }, global: `"http://g"`, project: `"http://p"`, worktree: `"http://w"`},
	{path: "proxies.no_proxy", kind: mergeAppend, field: func(c *Config) any { return &c.Proxies.NoProxy }, global: `["g"]`, project: `["p"]`, worktree: `["w"]`},
	{path: "copy_files", kind: mergeReplace, field: func(c *Config) any { return &c.CopyFiles }, global: `[".g"]`, project: `[".p"]`, worktree: `[".w"]`},
	{path: "envs", kind: mergeOverlay, field: func(c *Config) any { return &c.Envs }, global: `{"G": "g", "E": "g"}`, project: `{"E": "p", "P": "p"}`, worktree: `{"P": "w", "W": "w"}`},
	{path: "mcp.servers", kind: mergeOverlay, field: func(c *Config) any { return &c.MCPServers }, global: `{"g": {"command": "g"}, "s": {"command": "g"}}`, project: `{"s": {"command": "p"}}`, worktree: `{"w": {"command": "w"}}`},
	{path: "claude_model", kind: mergeReplace, field: func(c *Config) any { return &c.ClaudeModel }, global: `"g"`, project: `"p"`, worktree: `"w"`},
	{path: "claude_effort", kind: mergeReplace, field: func(c *Config) any { return &c.ClaudeEffort }, global: `"g"`, project: `"p"`, worktree: `"w"`},
	{path: "claude_bin_path", kind: mergeReplace, field: func(c *Config) any { return &c.ClaudeBinPath }, global: `"/g"`, project: `"/p"`, worktree: `"/w"`},
	{path: "claude_batch_disallowed_tools", kind: mergeReplace, field: func(c *Config) any { return &c.ClaudeBatchDisallowedTools }, global: `["G"]`, project: `["P"]`, worktree: `["W"]`},
	{path: "claude_retry.max_attempts", kind: mergeReplace, field: func(c *Config) any { return &c.AgentRetry.MaxAttempts }, global: `1`, project: `2`, worktree: `3`},
	{path: "claude_retry.backoff_base_sec", kind: mergeReplace, field: func(c *Config) any { return &c.AgentRetry.BackoffBase }, global: `1`, project: `2`, worktree: `3`, conv: seconds},
	{path: "claude_retry.backoff_max_sec", kind: mergeReplace, field: func(c *Config) any { return &c.AgentRetry.BackoffMax }, global: `1`, project: `2`, worktree: `3`, conv: seconds},
	{path: "claude_retry.session_limit_auto_continue", kind: mergeReplace, field: func(c *Config) any { return &c.AgentRetry.SessionLimitAutoContinue }, global: `true`, project: `false`, worktree: `true`},
	{path: "claude_code_oauth_token", kind: mergeCustom, field: func(c *Config) any { return &c.ClaudeCodeOAuthToken }, global: `"g-token"`, project: `"p-token"`, worktree: `"w-token"`,
		apply: func(c *Config, raw string, _ mergeLayer) {
			c.ClaudeCodeOAuthToken, c.AnthropicAPIKey = decodeJSON[string](raw), ""
		}, reason: "replaces the one below and clears the API key: one credential applies"},
	{path: "anthropic_api_key", kind: mergeCustom, field: func(c *Config) any { return &c.AnthropicAPIKey }, global: `"g-key"`, project: `"p-key"`, worktree: `"w-key"`,
		apply: func(c *Config, raw string, _ mergeLayer) {
			c.AnthropicAPIKey, c.ClaudeCodeOAuthToken = decodeJSON[string](raw), ""
		}, reason: "replaces the one below and clears the OAuth token: one credential applies"},
	{path: "anthropic_base_url", kind: mergeReplace, field: func(c *Config) any { return &c.AnthropicBaseURL }, global: `"https://g.example.com"`, project: `"https://p.example.com"`, worktree: `"https://w.example.com"`},
	{path: "container_image", kind: mergeReplace, field: func(c *Config) any { return &c.ContainerImage }, global: `"g"`, project: `"p"`, worktree: `"w"`},
	{path: "container_image_autobuild", kind: mergeReplace, field: func(c *Config) any { return &c.ContainerImageAutobuild }, global: `true`, project: `false`, worktree: `true`},
	{path: "container_memory_mb", kind: mergeReplace, field: func(c *Config) any { return &c.ContainerMemoryMB }, global: `1`, project: `2`, worktree: `3`},
	{path: "container_cpus", kind: mergeReplace, field: func(c *Config) any { return &c.ContainerCPUs }, global: `1`, project: `2.5`, worktree: `3`},
	{path: "keep_mcp_configs", kind: mergeReplace, field: func(c *Config) any { return &c.KeepMCPConfigs }, global: `true`, project: `false`, worktree: `true`},
	{path: "browser.enabled", kind: mergeReplace, field: func(c *Config) any { return &c.Browser.Enabled }, global: `true`, project: `false`, worktree: `true`},
	{path: "browser.chrome_image", kind: mergeReplace, field: func(c *Config) any { return &c.Browser.ChromeImage }, global: `"g"`, project: `"p"`, worktree: `"w"`},
	{path: "browser.mode", kind: mergeReplace, field: func(c *Config) any { return &c.Browser.Mode }, global: `"docker"`, project: `"host"`, worktree: `"docker"`},
	{path: "browser.host_cdp_port", kind: mergeReplace, field: func(c *Config) any { return &c.Browser.HostCDPPort }, global: `1`, project: `2`, worktree: `3`},
	{path: "browser.persist_profile", kind: mergeReplace, field: func(c *Config) any { return &c.Browser.PersistProfile }, global: `true`, project: `false`, worktree: `true`},
	{path: "browser.extensions", kind: mergeReplace, field: func(c *Config) any { return &c.Browser.Extensions }, global: `["/g"]`, project: `[]`, worktree: `["/w"]`},
	{path: "browser.memory_mb", kind: mergeReplace, field: func(c *Config) any { return &c.Browser.MemoryMB }, global: `1`, project: `2`, worktree: `3`},
	{path: "browser.cpus", kind: mergeReplace, field: func(c *Config) any { return &c.Browser.CPUs }, global: `1`, project: `2.5`, worktree: `3`},
	{path: "browser.cookie_import.source", kind: mergeReplace, field: func(c *Config) any { return &c.Browser.CookieImport.Source }, global: `"chrome"`, project: `"firefox"`, worktree: `"safari"`},
	{path: "browser.cookie_import.domains", kind: mergeReplace, field: func(c *Config) any { return &c.Browser.CookieImport.Domains }, global: `["g.example.com"]`, project: `[]`, worktree: `["w.example.com"]`},
	{path: "browser.cookie_import.auto", kind: mergeReplace, field: func(c *Config) any { return &c.Browser.CookieImport.Auto }, global: `true`, project: `false`, worktree: `true`},
	{path: "task_templates", kind: mergeNamed, field: func(c *Config) any { return &c.TaskTemplates }, global: `[{"name": "t", "prompt": "g"}, {"name": "g", "prompt": "g"}]`, project: `[{"name": "t", "prompt": "p"}, {"name": "p", "prompt": "p"}]`, worktree: `[{"name": "g", "prompt": "w"}]`},
	{path: "workflows", kind: mergeNamed, field: func(c *Config) any { return &c.Workflows }, global: `[{"name": "f", "description": "g"}, {"name": "g"}]`, project: `[{"name": "f", "description": "p"}, {"name": "p"}]`, worktree: `[{"name": "w"}]`},
	{path: "workflow_concurrency.max_concurrent_runs", kind: mergeReplace, field: func(c *Config) any { return &c.WorkflowConcurrency.MaxConcurrentRuns }, global: `1`, project: `2`, worktree: `3`},
	{path: "workflow_concurrency.max_concurrent_nodes", kind: mergeReplace, field: func(c *Config) any { return &c.WorkflowConcurrency.MaxConcurrentNodes }, global: `1`, project: `2`, worktree: `3`},
	{path: "prompt_shortcuts", kind: mergeNamed, field: func(c *Config) any { return &c.PromptShortcuts }, global: `[{"name": "s", "prompt": "g"}, {"name": "g", "prompt": "g"}]`, project: `[{"name": "s", "prompt": "p"}]`, worktree: `[{"name": "w", "prompt": "w"}]`},
	{path: "bash_shortcuts", kind: mergeNamed, field: func(c *Config) any { return &c.BashShortcuts }, global: `[{"name": "s", "command": "g"}, {"name": "g", "command": "g"}]`, project: `[{"name": "s", "command": "p"}]`, worktree: `[{"name": "s", "command": "w"}]`},
	{path: "chat_components", kind: mergeNamed, field: func(c *Config) any { return &c.ChatComponents }, global: `[{"name": "c", "path": "g"}, {"name": "g", "path": "g"}]`, project: `[{"name": "c", "path": "p"}]`, worktree: `[{"name": "w", "path": "w"}]`},
	{path: "memory.enabled", kind: mergeIgnored, project: `false`, reason: "the daemon's memory indexer reads the global config only"},
	{path: "memory.paths", kind: mergeAppend, field: func(c *Config) any { return &c.Memory.Paths }, global: `["/g"]`, project: `["/p"]`, worktree: `["/w"]`},
	{path: "memory.max_chunk_chars", kind: mergeReplace, field: func(c *Config) any { return &c.Memory.MaxChunkChars }, global: `1`, project: `2`, worktree: `3`},
	{path: "memory.reindex_interval_sec", kind: mergeIgnored, project: `1`, reason: "the daemon's memory indexer reads the global config only"},
	{path: "memory.embeddings.provider", kind: mergeCustom, field: func(c *Config) any { return &c.Memory.Embeddings }, global: `{"provider": "g", "model": "g", "ollama_url": "http://g"}`, project: `"p"`, worktree: `"w"`,
		apply: embeddingsLeaf(func(e *EmbeddingsConfig) *string { return &e.Provider }), reason: "the layer's embeddings block replaces the one below whole"},
	{path: "memory.embeddings.model", kind: mergeCustom, project: `"p"`, worktree: `"w"`,
		apply: embeddingsLeaf(func(e *EmbeddingsConfig) *string { return &e.Model }), reason: "the layer's embeddings block replaces the one below whole"},
	{path: "memory.embeddings.ollama_url", kind: mergeCustom, project: `"http://p"`, worktree: `"http://w"`,
		apply: embeddingsLeaf(func(e *EmbeddingsConfig) *string { return &e.OllamaURL }), reason: "the layer's embeddings block replaces the one below whole"},
	{path: "quality.max_files", kind: mergeReplace, field: func(c *Config) any { return &c.Quality.MaxFiles }, global: `1`, project: `2`, worktree: `3`},
	{path: "quality.exclude_paths", kind: mergeReplace, field: func(c *Config) any { return &c.Quality.ExcludePaths }, global: `["g"]`, project: `[]`, worktree: `["w"]`},
	{path: "quality.rules", kind: mergeCustom, field: func(c *Config) any { return &c.Quality.Rules },
		global:  `{"r": {"enabled": false, "threshold": 1}, "g": {"enabled": true, "threshold": 5}}`,
		project: `{"r": {"threshold": 2}, "n": {}}`, worktree: `{"n": {"enabled": false}, "r": {"enabled": true}}`,
		apply: func(c *Config, raw string, _ mergeLayer) {
			rules := overlayMap(c.Quality.Rules, nil)
			for name, jr := range decodeJSON[map[string]jsonQualityRuleConfig](raw) {
				r, existed := rules[name]
				switch {
				case jr.Enabled != nil:
					r.Enabled = *jr.Enabled
				case !existed:
					r.Enabled = true
				}
				if jr.Threshold > 0 {
					r.Threshold = jr.Threshold
				}
				rules[name] = r
			}
			c.Quality.Rules = rules
		}, reason: "overlaid by rule name, field by field: a new rule is on unless it says otherwise"},
	{path: "quality.complexity.cyclomatic_t", kind: mergeReplace, field: func(c *Config) any { return &c.Quality.Complexity.CyclomaticT }, global: `1`, project: `2`, worktree: `3`},
	{path: "quality.complexity.cognitive_t", kind: mergeReplace, field: func(c *Config) any { return &c.Quality.Complexity.CognitiveT }, global: `1`, project: `2`, worktree: `3`},
	{path: "quality.complexity.nesting_t", kind: mergeReplace, field: func(c *Config) any { return &c.Quality.Complexity.NestingT }, global: `1`, project: `2`, worktree: `3`},
	{path: "quality.complexity.params_t", kind: mergeReplace, field: func(c *Config) any { return &c.Quality.Complexity.ParamsT }, global: `1`, project: `2`, worktree: `3`},
	{path: "quality.complexity.loc_t", kind: mergeReplace, field: func(c *Config) any { return &c.Quality.Complexity.LOCT }, global: `1`, project: `2`, worktree: `3`},
	{path: "quality.clones.min_loc", kind: mergeReplace, field: func(c *Config) any { return &c.Quality.Clones.MinLOC }, global: `1`, project: `2`, worktree: `3`},
	{path: "quality.clones.max_distance", kind: mergeReplace, field: func(c *Config) any { return &c.Quality.Clones.MaxDistance }, global: `1`, project: `2`, worktree: `3`},
	{path: "permissions.owners.users", kind: mergeCustom, field: func(c *Config) any { return &c.Permissions },
		global:  `{"owners": {"users": ["g"], "roles": ["g"]}, "members": {"users": ["g"], "roles": ["g"]}}`,
		project: `["p"]`, worktree: `["w"]`,
		apply: permissionsLeaf(func(p *types.Permissions) *[]string { return &p.Owners.Users }), reason: "the layer's permissions replace the ones below whole"},
	{path: "permissions.owners.roles", kind: mergeCustom, project: `["p"]`, worktree: `["w"]`,
		apply: permissionsLeaf(func(p *types.Permissions) *[]string { return &p.Owners.Roles }), reason: "the layer's permissions replace the ones below whole"},
	{path: "permissions.members.users", kind: mergeCustom, project: `["p"]`, worktree: `["w"]`,
		apply: permissionsLeaf(func(p *types.Permissions) *[]string { return &p.Members.Users }), reason: "the layer's permissions replace the ones below whole"},
	{path: "permissions.members.roles", kind: mergeCustom, project: `["p"]`, worktree: `["w"]`,
		apply: permissionsLeaf(func(p *types.Permissions) *[]string { return &p.Members.Roles }), reason: "the layer's permissions replace the ones below whole"},
	{path: "extra_dirs", kind: mergeCustom, field: func(c *Config) any { return &c.ExtraDirs }, global: `["/g"]`, project: `["/p"]`, worktree: `["/proj", "/w", "/p"]`,
		apply: func(c *Config, raw string, l mergeLayer) {
			dirs := decodeJSON[[]string](raw)
			if !l.worktree {
				c.ExtraDirs = dirs
				return
			}
			for _, d := range dirs {
				if !slices.Contains(c.ExtraDirs, d) {
					c.ExtraDirs = append(c.ExtraDirs, d)
				}
			}
		}, reason: "a project's replace the global ones; a worktree's, seeded with the project dir, are added to the project's"},
	{path: "gates.rate_limits.pending", kind: mergeIgnored, project: `1`, reason: ignoredGatesReason},
	{path: "gates.rate_limits.per_minute", kind: mergeIgnored, project: `1`, reason: ignoredGatesReason},
	{path: "gates.rate_limits.total", kind: mergeIgnored, project: `1`, reason: ignoredGatesReason},
	{path: "gates.audit.retention_days", kind: mergeIgnored, project: `1`, reason: ignoredGatesReason},
	{path: "gates.audit.verbose", kind: mergeIgnored, project: `true`, reason: ignoredGatesReason},
	{path: "gates.agentgate.enabled", kind: mergeIgnored, project: `false`, reason: "the project config is in the agent's workspace: it can't turn a gate off"},
	{path: "gates.agentgate.default_decision", kind: mergeIgnored, project: `"allow"`, reason: ignoredGatesReason},
	{path: "gates.agentgate.path_rules", kind: mergeCustom, field: func(c *Config) any { return &c.Gates.Agentgate.PathRules },
		global:  `[{"pattern": "/a/**", "decision": "allow"}, {"pattern": "/d/**", "decision": "deny"}]`,
		project: `[{"pattern": "/p/**", "decision": "allow"}]`, worktree: `[{"pattern": "/w/**", "decision": "deny"}]`,
		apply: gateRules(func(c *Config) any { return &c.Gates.Agentgate.PathRules }), reason: "between the denies below and the rest"},
	{path: "gates.agentgate.command_rules", kind: mergeCustom, field: func(c *Config) any { return &c.Gates.Agentgate.CommandRules },
		global:  `[{"commands": ["a"], "decision": "approve"}, {"commands": ["d"], "decision": "deny"}]`,
		project: `[{"commands": ["p"], "decision": "allow"}]`, worktree: `[{"commands": ["w"], "decision": "deny"}]`,
		apply: gateRules(func(c *Config) any { return &c.Gates.Agentgate.CommandRules }), reason: "between the denies below and the rest"},
	{path: "gates.agentgate.file_rules", kind: mergeCustom, field: func(c *Config) any { return &c.Gates.Agentgate.FileRules },
		global:  `[{"paths": ["a"], "decision": "allow"}, {"paths": ["d"], "decision": "deny"}]`,
		project: `[{"paths": ["p"], "decision": "allow"}]`, worktree: `[{"paths": ["w"], "decision": "approve"}]`,
		apply: gateRules(func(c *Config) any { return &c.Gates.Agentgate.FileRules }), reason: "between the denies below and the rest"},
	{path: "gates.docker_proxy.enabled", kind: mergeIgnored, project: `false`, reason: "the project config is in the agent's workspace: it can't turn a gate off"},
	{path: "gates.docker_proxy.default_decision", kind: mergeIgnored, project: `"allow"`, reason: ignoredGatesReason},
	{path: "gates.docker_proxy.http_rules", kind: mergeCustom, field: func(c *Config) any { return &c.Gates.DockerProxy.HTTPRules },
		global:  `[{"methods": ["GET"], "paths": ["/a"], "decision": "allow"}, {"methods": ["POST"], "paths": ["/d"], "decision": "deny"}]`,
		project: `[{"methods": ["POST"], "paths": ["/p"], "decision": "allow"}]`, worktree: `[{"methods": ["PUT"], "paths": ["/w"], "decision": "deny"}]`,
		apply: gateRules(func(c *Config) any { return &c.Gates.DockerProxy.HTTPRules }), reason: "between the denies below and the rest"},
	{path: "gates.docker_proxy.body_rules", kind: mergeCustom, field: func(c *Config) any { return &c.Gates.DockerProxy.BodyRules },
		global:  `[{"applies_to": "/a", "decision": "allow"}, {"applies_to": "/d", "decision": "deny"}]`,
		project: `[{"applies_to": "/p", "decision": "allow"}]`, worktree: `[{"applies_to": "/w", "decision": "deny"}]`,
		apply: gateRules(func(c *Config) any { return &c.Gates.DockerProxy.BodyRules }), reason: "between the denies below and the rest"},
	{path: "github.gh_user", kind: mergeReplace, field: func(c *Config) any { return &c.GitHub.GHUser }, global: `"g"`, project: `"p"`, worktree: `"w"`},
	{path: "github.mount_gh_config", kind: mergeCustom, field: func(c *Config) any { return &c.GitHub.MountGHConfig }, global: `true`, project: `false`, worktree: `true`,
		apply: configMount(func(c *Config) *bool { return &c.GitHub.MountGHConfig }, "~/.config/gh"), reason: "replaces the one below; off also drops the gh config mount"},
	{path: "gitlab.glab_config_dir", kind: mergeReplace, field: func(c *Config) any { return &c.GitLab.GlabConfigDir }, global: `"/g"`, project: `"/p"`, worktree: `"/w"`},
	{path: "gitlab.mount_glab_config", kind: mergeCustom, field: func(c *Config) any { return &c.GitLab.MountGlabConfig }, global: `true`, project: `false`, worktree: `true`,
		apply: configMount(func(c *Config) *bool { return &c.GitLab.MountGlabConfig }, GlabConfigTarget), reason: "replaces the one below; off also drops the mounts under glab's config dir"},
	{path: "review.enabled", kind: mergeReplace, field: func(c *Config) any { return &c.Review.Enabled }, global: `true`, project: `false`, worktree: `true`},
	{path: "review.prompt", kind: mergeReplace, field: func(c *Config) any { return &c.Review.Prompt }, global: `"g"`, project: `"p"`, worktree: `"w"`},
	{path: "review.prompt_path", kind: mergeReplace, field: func(c *Config) any { return &c.Review.PromptPath }, global: `"g.md"`, project: `"p.md"`, worktree: `"w.md"`},
	{path: "review.verify", kind: mergeReplace, field: func(c *Config) any { return &c.Review.Verify }, global: `true`, project: `false`, worktree: `true`},
	{path: "review.verify_prompt", kind: mergeReplace, field: func(c *Config) any { return &c.Review.VerifyPrompt }, global: `"g"`, project: `"p"`, worktree: `"w"`},
	{path: "review.max_iterations", kind: mergeReplace, field: func(c *Config) any { return &c.Review.MaxIterations }, global: `1`, project: `2`, worktree: `3`},
	{path: "review.update_base", kind: mergeReplace, field: func(c *Config) any { return &c.Review.UpdateBase }, global: `true`, project: `false`, worktree: `true`},
	{path: "playground_share.enabled", kind: mergeReplace, field: func(c *Config) any { return &c.PlaygroundShare.Enabled }, global: `true`, project: `false`, worktree: `true`},
	{path: "ci.auto_fix", kind: mergeReplace, field: func(c *Config) any { return &c.CI.AutoFix }, global: `true`, project: `false`, worktree: `true`},
	{path: "ci.max_attempts", kind: mergeReplace, field: func(c *Config) any { return &c.CI.MaxAttempts }, global: `1`, project: `2`, worktree: `3`},
	{path: "ci.merge_method", kind: mergeReplace, field: func(c *Config) any { return &c.CI.MergeMethod }, global: `"merge"`, project: `"squash"`, worktree: `"rebase"`},
	{path: "ci.poll_sec", kind: mergeReplace, field: func(c *Config) any { return &c.CI.PollSec }, global: `1`, project: `2`, worktree: `3`},
	{path: "learn.enabled", kind: mergeReplace, field: func(c *Config) any { return &c.Learn.Enabled }, global: `true`, project: `false`, worktree: `true`},
	{path: "learn.min_turns", kind: mergeReplace, field: func(c *Config) any { return &c.Learn.MinTurns }, global: `1`, project: `2`, worktree: `3`},
	{path: "learn.model", kind: mergeReplace, field: func(c *Config) any { return &c.Learn.Model }, global: `"g"`, project: `"p"`, worktree: `"w"`},
	{path: "learn.effort", kind: mergeReplace, field: func(c *Config) any { return &c.Learn.Effort }, global: `"g"`, project: `"p"`, worktree: `"w"`},
	{path: "learn.prompt", kind: mergeReplace, field: func(c *Config) any { return &c.Learn.Prompt }, global: `"g"`, project: `"p"`, worktree: `"w"`},
	{path: "explain.enabled", kind: mergeReplace, field: func(c *Config) any { return &c.Explain.Enabled }, global: `true`, project: `false`, worktree: `true`},
	{path: "explain.model", kind: mergeReplace, field: func(c *Config) any { return &c.Explain.Model }, global: `"g"`, project: `"p"`, worktree: `"w"`},
	{path: "explain.effort", kind: mergeReplace, field: func(c *Config) any { return &c.Explain.Effort }, global: `"g"`, project: `"p"`, worktree: `"w"`},
	{path: "explain.prompt", kind: mergeReplace, field: func(c *Config) any { return &c.Explain.Prompt }, global: `"g"`, project: `"p"`, worktree: `"w"`},
	{path: "plugins.*.enabled", kind: mergeCustom, field: func(c *Config) any { return &c.Plugins.ByID },
		global:  `{"p": {"enabled": true, "settings": {"k": "g", "g": "g"}, "schedules": {"s": {"schedule": "1m"}, "g": {"schedule": "1h"}}}, "q": {"enabled": true}}`,
		project: `false`, worktree: `true`,
		apply: pluginLeaf(func(pl *PluginConfig, raw string) {
			on := decodeJSON[bool](raw)
			pl.Enabled = &on
		}), reason: "set on that plugin; the other plugins stay"},
	{path: "plugins.*.settings", kind: mergeCustom, project: `{"k": "p", "p": "p"}`, worktree: `{"p": "w"}`,
		apply: pluginLeaf(func(pl *PluginConfig, raw string) {
			pl.Settings = overlayMap(pl.Settings, decodeJSON[map[string]any](raw))
		}), reason: "overlaid by key on that plugin's settings"},
	{path: "plugins.*.schedules", kind: mergeCustom, project: `{"s": {"schedule": "5m"}}`, worktree: `{"w": {"schedule": "1d"}}`,
		apply: pluginLeaf(func(pl *PluginConfig, raw string) {
			pl.Schedules = overlayMap(pl.Schedules, decodeJSON[map[string]PluginSchedule](raw))
		}), reason: "overlaid by name on that plugin's schedules"},
	{path: "plugins.enabled", kind: mergeIgnored, project: `false`, reason: "the plugins block's own keys are global only"},
	{path: "plugins.gate_binary", kind: mergeIgnored, project: `"/p"`, reason: "the plugins block's own keys are global only"},
	{path: "plugins.daily_runs", kind: mergeIgnored, project: `1`, reason: "the plugins block's own keys are global only"},
}

// mergeBase is a global config with each field's global value.
func mergeBase() *Config {
	c := &Config{}
	for _, f := range mergeFields {
		if f.global != "" {
			dst := reflect.ValueOf(f.field(c)).Elem()
			dst.Set(f.value(f.global, dst.Type()))
		}
	}
	return c
}

// withSpareCapacity gives each of c's slices room to grow, as a decoded
// global config's can have, and returns c.
func withSpareCapacity(c *Config) *Config {
	var grow func(v reflect.Value)
	grow = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Struct:
			for i := range v.NumField() {
				if v.Type().Field(i).IsExported() {
					grow(v.Field(i))
				}
			}
		case reflect.Slice:
			if v.Len() > 0 {
				v.Set(reflect.AppendSlice(reflect.MakeSlice(v.Type(), 0, v.Len()+8), v))
			}
		}
	}
	grow(reflect.ValueOf(c).Elem())
	return c
}

// mergeLoad merges the project config in files onto main, and with
// worktree, the worktree's on top.
type mergeLoad func(files map[string][]byte, main *Config, worktree bool) (*Config, error)

// realMergeLoad is mergeLoad by the loader, which applies every field as
// written.
func realMergeLoad(files map[string][]byte, main *Config, worktree bool) (*Config, error) {
	l := &Loader{readIn: func(_, p string) ([]byte, error) {
		if b, ok := files[p]; ok {
			return b, nil
		}
		return nil, os.ErrNotExist
	}}
	if worktree {
		return l.loadWorktreeProjectConfig(mergeWorktreeDir, mergeProjectDir, main)
	}
	return l.loadProjectConfig(mergeProjectDir, main)
}

// mergeCase is what a merge should give and what load gave.
type mergeCase struct {
	name      string
	want, got *Config
	err       error
}

// mergeCases loads f's project sample onto the global config, and then the
// worktree sample on top, with load. A third case loads another project
// sample onto the same global config and looks at the first result again:
// a merge that appended into the room left in a global config's slice
// would have changed it.
func mergeCases(f mergeField, load mergeLoad) []mergeCase {
	if f.worktree == "" {
		f.worktree = f.project
	}
	main := withSpareCapacity(mergeBase())
	project := map[string][]byte{mergeProjectFile: []byte(classJSON(f.path, f.project))}
	wantProject := mergeBase()
	f.layer(wantProject, f.project, mergeLayer{dir: mergeProjectDir})
	gotProject, err := load(project, main, false)
	cases := []mergeCase{{name: "project", want: wantProject, got: gotProject, err: err}}

	worktree := overlayMap(project, map[string][]byte{mergeWorktreeFile: []byte(classJSON(f.path, f.worktree))})
	want := mergeBase()
	f.layer(want, f.project, mergeLayer{dir: mergeProjectDir})
	f.layer(want, f.worktree, mergeLayer{dir: mergeWorktreeDir, worktree: true})
	got, err := load(worktree, main, true)
	cases = append(cases, mergeCase{name: "worktree", want: want, got: got, err: err})

	_, err = load(map[string][]byte{mergeProjectFile: []byte(classJSON(f.path, f.worktree))}, main, false)
	cases = append(cases, mergeCase{name: "project, after another load", want: wantProject, got: gotProject, err: err})
	return cases
}

type MergeSpecSuite struct {
	suite.Suite
}

func TestMergeSpecSuite(t *testing.T) {
	suite.Run(t, new(MergeSpecSuite))
}

// TestFieldClassification: every project config field has a merge rule,
// with what the rule needs.
func (s *MergeSpecSuite) TestFieldClassification() {
	classes := map[string]bool{}
	want := make([]string, 0, len(mergeFields))
	for _, f := range mergeFields {
		require.False(s.T(), classes[f.path], "%s classified twice", f.path)
		classes[f.path] = true
		want = append(want, f.path)
		require.NotEmpty(s.T(), f.project, f.path)
		switch f.kind {
		case mergeIgnored:
			require.NotEmpty(s.T(), f.reason, "%s is ignored without a reason", f.path)
		case mergeCustom:
			require.NotNil(s.T(), f.apply, f.path)
			require.NotEmpty(s.T(), f.reason, "%s has a rule of its own without a reason", f.path)
			require.NotEqual(s.T(), f.project, f.worktree, "%s: the worktree sets the project's value", f.path)
		default:
			require.NotNil(s.T(), f.field, f.path)
			require.NotEmpty(s.T(), f.global, f.path)
			require.NotEqual(s.T(), f.global, f.project, "%s: the project sets the global value", f.path)
			require.NotEqual(s.T(), f.project, f.worktree, "%s: the worktree sets the project's value", f.path)
		}
	}
	paths := jsonFieldPaths(reflect.TypeFor[projectConfig](), "", map[string]bool{})
	require.ElementsMatch(s.T(), want, paths, "projectConfig fields and mergeFields differ: give the new field a merge rule")
}

// TestMergeRules: a layer that sets one field changes the merged config by
// that field's rule, and nothing else.
func (s *MergeSpecSuite) TestMergeRules() {
	for _, f := range mergeFields {
		s.Run(f.path, func() {
			for _, c := range mergeCases(f, realMergeLoad) {
				require.NoError(s.T(), c.err, c.name)
				require.Equal(s.T(), c.want, c.got, "%s: %s by %q", c.name, f.path, f.kind)
			}
		})
	}
}

// TestMergeRulesCanFail: the check catches a merge that breaks a rule.
func (s *MergeSpecSuite) TestMergeRulesCanFail() {
	broken := func(fix func(merged, main *Config)) mergeLoad {
		return func(files map[string][]byte, main *Config, worktree bool) (*Config, error) {
			merged, err := realMergeLoad(files, main, worktree)
			if err != nil {
				return nil, err
			}
			fix(merged, main)
			return merged, nil
		}
	}
	tests := []struct {
		name string
		path string
		load mergeLoad
	}{
		{"a field changes another", "claude_model", broken(func(merged, _ *Config) { merged.ClaudeEffort = merged.ClaudeModel })},
		{"replace appends", "copy_files", broken(func(merged, main *Config) {
			merged.CopyFiles = append(slices.Clone(main.CopyFiles), merged.CopyFiles...)
		})},
		{"append replaces", "memory.paths", broken(func(merged, main *Config) {
			merged.Memory.Paths = merged.Memory.Paths[len(main.Memory.Paths):]
		})},
		{"a field is dropped", "review.prompt", broken(func(merged, main *Config) { merged.Review.Prompt = main.Review.Prompt })},
		{"the worktree skips the project", "proxies.no_proxy", func(files map[string][]byte, main *Config, worktree bool) (*Config, error) {
			l := &Loader{readIn: func(_, p string) ([]byte, error) {
				if b, ok := files[p]; ok {
					return b, nil
				}
				return nil, os.ErrNotExist
			}}
			if worktree {
				return l.loadProjectConfig(mergeWorktreeDir, main)
			}
			return l.loadProjectConfig(mergeProjectDir, main)
		}},
		{"the merge shares the global config's slice", "memory.paths", broken(func(merged, main *Config) {
			merged.Memory.Paths = main.Memory.Paths
		})},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			i := slices.IndexFunc(mergeFields, func(f mergeField) bool { return f.path == tc.path })
			require.GreaterOrEqual(s.T(), i, 0)
			caught := false
			for _, c := range mergeCases(mergeFields[i], tc.load) {
				caught = caught || c.err != nil || !reflect.DeepEqual(c.want, c.got)
			}
			require.True(s.T(), caught, "the broken merge passes")
		})
	}
}
