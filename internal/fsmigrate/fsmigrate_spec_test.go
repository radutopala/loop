// fsmigrate_spec_test.go checks every filesystem migration as a spec.
//
// The property, for each entry of migrations, found by walking the list as
// Run does: on a random tree, applying the migration twice leaves the tree
// it left after once, with the same error or none (idempotent); and it
// stays inside the roots it was handed. Nothing outside them is created,
// changed or removed, and every file operation it makes names a path under
// the root of the System it went through and, when it succeeds, reaches a
// file under that root once symlinks are followed.
//
// "Inside" is how Run's caller hands the roots over: ~/.loop as LoopDir,
// reached through Sys, and each project checkout in ProjectDirs, reached
// through ProjectSys(dir), which serve wires to osutil.RootFS. So a
// migration may use Sys only for paths under LoopDir and a project's
// System only for paths under that project, and a project's files only
// through its own System. A project's .loop is the agent's to write, so the
// generated projects hold symlinks out of the project: as the .loop dir, as
// config.json, as the config.json.tmp written beside it, dangling ones, and
// links that stay inside. ~/.loop is the owner's and reached through the
// unconfined Sys by design, so a link the owner put there is the owner's
// to follow; the generated homes hold no link out except under a channel
// work dir, which the agent writes and no migration may walk into. The
// Adopt callbacks act on the owner's trust file, outside both roots by
// design; the spec hands in recorders and checks they are only ever given
// the project dirs.
//
// Trees come from a seeded generator on a real temp dir: present, absent
// and malformed config.json files (not HJSON, not an object, a dir, keys
// twice), configs mixing every shape a migration rewrites (old seeded
// shortcuts and workflows, legacy proxy keys, the dropped dev channels key
// and comments naming it) with user-edited ones, and container/ dirs with
// canonical, edited, missing and backed-up files.
//
// Trusted base: the real file system, snapshotWalk (Lstat, Readlink, the
// walk), resolvePath (the reference symlink resolver), and osutil.RootFS,
// whose own spec is internal/osutil/within_spec_test.go.
package fsmigrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	containerimage "github.com/radutopala/loop/internal/container/image"
	"github.com/radutopala/loop/internal/osutil"
)

// specSeeds is how many generated trees each migration runs on.
const specSeeds = 64

// outMarker starts every file outside the roots.
const outMarker = "OUT:"

// migrationClass classifies one migration for the spec. exempt is empty
// when the spec checks both properties in full, and otherwise says what it
// can't check and why.
type migrationClass struct {
	description string
	exempt      string
}

const (
	exemptBootstrap = "never applied: Run starts at version 1"
	exemptAdopt     = "its effect is the Adopt callback's, on the owner's trust file outside both roots; " +
		"the spec checks it hands the callback only the project dirs and touches no file itself, " +
		"and the callback's own idempotence (a dir already trusted keeps its version) is config.TrustStore's"
)

// migrationClasses is every entry of migrations, by version. A new
// migration fails TestMigrationClassification until it's added here.
var migrationClasses = []migrationClass{
	{description: "bootstrap", exempt: exemptBootstrap},
	{description: "refresh embedded container/ files"},
	{description: "refresh container/ files for Debian base switch"},
	{description: "refresh container/ files: HOST_UID/HOST_GID pinning + chrome alpine revert"},
	{description: "seed builtin code review prompt shortcut"},
	{description: "seed review-loop and review-fix-loop workflows"},
	{description: "patch review-fix-loop verify step to commit leftover changes"},
	{description: "patch review-fix-loop verify step: replace unsafe git add -A with git add -u"},
	{description: "patch review-fix-loop body children with explicit depends_on"},
	{description: "patch builtin code review shortcut to the review-panel-derived prompt"},
	{description: "seed builtin simplify prompt shortcut"},
	{description: "refresh container/ files: Node 24 via nodesource (Debian's Node 20 is EOL)"},
	{description: "patch review-loop/review-fix-loop to env-based review command + pr input"},
	{description: "patch builtin code review shortcut back to slash-command form"},
	{description: "patch builtin simplify shortcut back to slash-command form"},
	{description: "refresh container/ files: skip redundant chown -R over cache volumes"},
	{description: "refresh container/ files: golang 1.27 base image"},
	{description: "refresh container/ files: git safe.directory for VirtioFS uid flap"},
	{description: "refresh container/ files: git-lfs in the agent image"},
	{description: "refresh container/ files: chrome entrypoint without --disable-extensions"},
	{description: "refresh container/ files: Docker CLI + buildx/compose plugins from Docker's apt repo"},
	{description: "refresh container/ files: Chrome sidecar loads image CAs into its NSS store"},
	{description: "rename no_proxy_hosts to no_proxy in config.json"},
	{description: "refresh container/ files: Chrome sidecar graphics and D-Bus"},
	{description: "refresh container/ files: agent stays out of the root group under the docker proxy"},
	{description: "refresh container/ files: npm 12 in the agent image"},
	{description: "refresh container/ files: no syscallwrap alias in the agent bashrc"},
	{description: "trust existing project configs", exempt: exemptAdopt},
	{description: "patch review-loop with a final dedup node"},
	{description: "move http_proxy, https_proxy and no_proxy into a proxies block in config.json"},
	{description: "refresh container/ files: AWS CLI v2 in the agent image"},
	{description: "refresh container/ files: AWS CLI v2 from the amazon/aws-cli image"},
	{description: "refresh container/ files: LOOP_-prefixed container env"},
	{description: "drop review-loop's final dedup node"},
	{description: "refresh container/ files: pre-commit beside Debian's python3-packaging"},
	{description: "patch review-loop/review-fix-loop to loop review:run"},
	{description: "drop claude_dangerously_load_development_channels from config.json"},
	{description: "patch review-loop/review-fix-loop to verify the comments once, at the end"},
	{description: "trust existing project workflows", exempt: exemptAdopt},
	{description: "trust existing project container images", exempt: exemptAdopt},
	{description: "rename the ci config block to pr"},
}

// --- generated trees ---

// specTree is one generated input: a home (LoopDir), project checkouts,
// and a dir outside them all that the links out lead to.
type specTree struct {
	home     string
	projects []string
	outside  string
}

// pick returns one of opts.
func pick[T any](r *rand.Rand, opts ...T) T { return opts[r.IntN(len(opts))] }

// specQuote is s as a JSON string.
func specQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// specJSON is v as JSON.
func specJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// genDeps is a depends_on member in each shape the deps patcher tells
// apart, or none.
func genDeps(r *rand.Rand, dep string) string {
	return pick(r, "", `,"depends_on":null`, `,"depends_on":[]`, `,"depends_on":["`+dep+`"]`, `,"depends_on":"x"`)
}

// genReviewScript is a review node script: every seeded form, or the user's.
func genReviewScript(r *rand.Rand) string {
	return pick(r, reviewRunScriptOld, reviewRunScriptSpaced, reviewRunScript, reviewRunScriptNoVerify, "make review")
}

// genWorkflow is one entry of a workflows array.
func genWorkflow(r *rand.Rand) string {
	inputs := pick(r, ``, `"inputs":{"max_iterations":{"default":"1","description":"n"}},`,
		`"inputs":{"max_iterations":{"default":"1","description":"n"},"pr":{"default":"","description":"x"}},`,
		`"inputs":"x",`)
	review := `{"id":"review","type":"bash","script":` + specQuote(genReviewScript(r)) + `}`
	switch r.IntN(8) {
	case 0:
		return `{"name":"review-loop",` + inputs + `"nodes":[{"id":"loop","type":"loop","body":[` + review + `]}]}`
	case 1:
		return `{"name":"review-loop",` + inputs + `"nodes":[{"id":"loop","type":"loop","body":[` + review + `]},` +
			`{"id":"dedup","type":"bash","script":"` + reviewDedupScript + `","depends_on":["loop"]}]}`
	case 2:
		verify := pick(r, reviewFixVerifyScriptOld, reviewFixVerifyScriptBuggyAddAll, reviewFixVerifyScript, "make check")
		return `{"name":"review-fix-loop",` + inputs + `"nodes":[{"id":"loop","type":"loop","body":[` + review +
			`,{"id":"fix","type":"prompt","prompt":"fix"` + genDeps(r, "review") + `}` +
			`,{"id":"verify","type":"bash","script":` + specQuote(verify) + genDeps(r, "fix") + `}]}]}`
	case 3:
		return specJSON(builtinReviewLoopDef())
	case 4:
		return specJSON(builtinReviewFixLoopDef())
	case 5:
		return pick(r, `"scalar"`, `{"name":"review-fix-loop","nodes":"x"}`, `{"name":"review-loop"}`,
			`{"name":"review-fix-loop","nodes":[{"type":"loop","body":"x"}]}`)
	default:
		return `{"name":"other","nodes":[]}`
	}
}

// genShortcut is one entry of a prompt_shortcuts array.
func genShortcut(r *rand.Rand) string {
	name := pick(r, builtinCodeReviewShortcutName, builtinSimplifyShortcutName, "mine")
	prompt := pick(r, oldBuiltinCodeReviewShortcutPrompt, skillToolBuiltinCodeReviewShortcutPrompt,
		skillToolBuiltinSimplifyShortcutPrompt, builtinCodeReviewShortcutPrompt, "my own prompt")
	desc := pick(r, oldBuiltinCodeReviewShortcutDescription, builtinSimplifyShortcutDescription, "mine")
	if r.IntN(6) == 0 {
		return pick(r, `"scalar"`, `{"name":`+specQuote(name)+`}`, `{"name":`+specQuote(name)+`,"prompt":{}}`)
	}
	return `{"name":` + specQuote(name) + `,"description":` + specQuote(desc) + `,"prompt":` + specQuote(prompt) + `}`
}

// genArray is a JSON array of up to three generated entries.
func genArray(r *rand.Rand, gen func(*rand.Rand) string) string {
	var elems []string
	for range r.IntN(4) {
		elems = append(elems, gen(r))
	}
	return "[" + strings.Join(elems, ",") + "]"
}

// memberKinds is how many kinds of member genMember makes.
const memberKinds = 17

// genMember is a top-level config member of the given kind, with the
// comments a migration has to keep or drop.
func genMember(r *rand.Rand, kind int) string {
	switch kind {
	case 0:
		return `"no_proxy_hosts": ` + pick(r, `["a.example.com"]`, `"a.example.com"`, `[]`)
	case 1:
		return `"no_proxy": ` + pick(r, `["b.example.com"]`, `"b.example.com"`)
	case 2:
		return `"http_proxy": "http://proxy.example.com:3128"`
	case 3:
		return "// the proxy for https\n  \"https_proxy\": \"http://proxy.example.com:3128\""
	case 4:
		return `"proxies": ` + pick(r, `{}`, `{"no_proxy": ["c.example.com"]}`, `{"http_proxy": "http://other.example.com"}`, `"x"`)
	case 5:
		return `"` + devChannelsKey + `": ` + pick(r, `true`, `false`) + pick(r, ``, ` // dev channels`)
	case 6:
		return `// "` + devChannelsKey + `": true,` + "\n  \"platforms\": [\"local\"]"
	case 7, 8:
		return `"prompt_shortcuts": ` + pick(r, genArray(r, genShortcut), `"scalar"`)
	case 9, 10, 11:
		return `"workflows": ` + pick(r, genArray(r, genWorkflow), genArray(r, genWorkflow), `"scalar"`)
	case 12:
		return "/* a block\n     comment */ \"mode\": \"docker\""
	case 13:
		return `"ci": ` + pick(r, `{"auto_fix": false}`, `{}`, `"x"`) + pick(r, ``, ` // ci`)
	case 14:
		return `"pr": ` + pick(r, `{"poll_sec": 30}`, `{}`)
	case 15:
		return "//\"ci\": {\n  //  \"auto_fix\": true\n  //},\n  \"platforms\": [\"local\"]"
	default:
		return `"platforms": ["local"]`
	}
}

// genConfig is a config.json: usually an HJSON object of generated members
// (a key may come twice), sometimes not one at all.
func genConfig(r *rand.Rand) string {
	if r.IntN(8) == 0 {
		return pick(r, ``, `{`, `[]`, `null`, `not json`, `{"workflows": [}`)
	}
	// Each kind comes in at random, in a random order, and may come twice:
	// HJSON, like encoding/json, takes a key twice, the last one winning.
	var members []string
	for _, kind := range r.Perm(memberKinds) {
		if r.IntN(3) != 0 {
			continue
		}
		members = append(members, genMember(r, kind))
		if r.IntN(4) == 0 {
			members = append(members, genMember(r, kind))
		}
	}
	trailing := pick(r, "", ",")
	if len(members) == 0 {
		trailing = ""
	}
	return pick(r, "", "// my config\n") + "{\n  " + strings.Join(members, ",\n  ") + trailing + "\n}\n"
}

// specWriter builds a tree, failing the test on a setup error.
type specWriter struct{ t *testing.T }

func (w specWriter) file(path, content string) {
	require.NoError(w.t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(w.t, os.WriteFile(path, []byte(content), 0o644))
}

func (w specWriter) dir(path string) { require.NoError(w.t, os.MkdirAll(path, 0o755)) }

func (w specWriter) link(target, path string) {
	require.NoError(w.t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(w.t, os.Symlink(target, path))
}

// genHome fills LoopDir: a config.json, a container/ dir and a channel
// work dir with a link out.
func genHome(r *rand.Rand, w specWriter, home, outside string) {
	w.dir(home)
	cfg := filepath.Join(home, "config.json")
	switch r.IntN(10) {
	case 0, 1:
	case 2:
		w.dir(cfg)
	default:
		w.file(cfg, genConfig(r))
	}
	containerDir := filepath.Join(home, "container")
	switch r.IntN(6) {
	case 0, 1:
	case 2:
		w.file(containerDir, "not a dir")
	default:
		for _, name := range append(slices.Clone(versionedContainerFiles), "setup.sh") {
			path := filepath.Join(containerDir, name)
			switch r.IntN(4) {
			case 0:
			case 1:
				w.file(path, string(containerimage.MustRead(name)))
			case 2:
				w.file(path, "# edited "+name+"\n")
			default:
				w.file(path+".bkp", "# an older backup\n")
			}
		}
	}
	if r.IntN(2) == 0 {
		w.link(outside, filepath.Join(home, "ch1", "work", "out"))
	}
}

// genProject fills a project checkout's .loop with what the agent may have
// written there: configs, malformed ones, and symlinks in and out.
func genProject(r *rand.Rand, w specWriter, dir, outside string) {
	w.dir(dir)
	loop := filepath.Join(dir, ".loop")
	switch r.IntN(8) {
	case 0:
		return
	case 1:
		w.link(filepath.Join(outside, "loopdir"), loop)
		return
	case 2:
		w.link("../../"+filepath.Base(outside)+"/loopdir", loop)
		return
	}
	w.dir(loop)
	cfg := filepath.Join(loop, "config.json")
	switch r.IntN(10) {
	case 0:
	case 1:
		w.link(filepath.Join(outside, "config.json"), cfg)
	case 2:
		w.link(filepath.Join(outside, "missing.json"), cfg)
	case 3:
		w.file(filepath.Join(dir, "real.json"), genConfig(r))
		w.link("../real.json", cfg)
	case 4:
		w.dir(cfg)
	default:
		w.file(cfg, genConfig(r))
	}
	switch r.IntN(6) {
	case 0:
		w.link(filepath.Join(outside, "config.json"), cfg+".tmp")
	case 1:
		w.link(filepath.Join(outside, "new.json"), cfg+".tmp")
	case 2:
		w.link("../stray.json", cfg+".tmp")
	case 3:
		w.file(cfg+".tmp", "stale")
	}
}

// genTree builds the tree for seed under base. The same seed gives the
// same tree.
func genTree(t *testing.T, base string, seed uint64) specTree {
	r := rand.New(rand.NewPCG(seed, 0x5eed))
	w := specWriter{t}
	tr := specTree{home: filepath.Join(base, "home", ".loop"), outside: filepath.Join(base, "outside")}

	// Everything outside holds a config a migration would rewrite, so a
	// write that reaches it shows.
	out := outMarker + "\n" + `{"` + devChannelsKey + `": true, "no_proxy_hosts": ["x"], "http_proxy": "http://p"}`
	w.file(filepath.Join(tr.outside, "config.json"), out)
	w.file(filepath.Join(tr.outside, "loopdir", "config.json"), out)
	w.file(filepath.Join(tr.outside, "loopdir", "config.json.tmp"), out)

	genHome(r, w, tr.home, tr.outside)
	for i := range r.IntN(4) {
		dir := filepath.Join(base, fmt.Sprintf("p%d", i))
		genProject(r, w, dir, tr.outside)
		tr.projects = append(tr.projects, dir)
	}
	if r.IntN(6) == 0 {
		tr.projects = append(tr.projects, filepath.Join(base, "gone"))
	}
	return tr
}

// --- observing a run ---

// snapshotWalk records every entry under root, without following links:
// its kind, mode and content, or a link's target. A missing root is empty.
func snapshotWalk(root string) (map[string]string, error) {
	snap := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && path == root {
				return nil
			}
			return err
		}
		rel, _ := filepath.Rel(root, path)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			snap[rel] = "link " + target
		case d.IsDir():
			snap[rel] = fmt.Sprintf("dir %v", info.Mode().Perm())
		default:
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			snap[rel] = fmt.Sprintf("file %v %q", info.Mode().Perm(), data)
		}
		return nil
	})
	return snap, err
}

// snapshotDiff names the entries that differ between a and b, at most a
// few.
func snapshotDiff(a, b map[string]string) []string {
	var diff []string
	for k := range a {
		if b[k] != a[k] {
			diff = append(diff, k)
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			diff = append(diff, k)
		}
	}
	slices.Sort(diff)
	diff = slices.Compact(diff)
	if len(diff) > 5 {
		diff = diff[:5]
	}
	for i, k := range diff {
		before, after := around(a[k], b[k])
		diff[i] = fmt.Sprintf("%s: %s -> %s", k, before, after)
	}
	return diff
}

// around cuts a and b down to where they start to differ, so a long file
// shows its change.
func around(a, b string) (string, string) {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	start := max(0, i-60)
	cut := func(s string) string {
		end := min(len(s), start+160)
		if start >= end {
			return "<missing>"
		}
		return s[start:end]
	}
	return cut(a), cut(b)
}

// resolvePath is where p leads once every link along it is followed,
// including a last component that is one; a missing component ends the
// walk. At most 40 links are followed.
func resolvePath(p string) string {
	n := 0
	return resolveN(filepath.Clean(p), &n)
}

func resolveN(p string, n *int) string {
	if parent := filepath.Dir(p); parent != p {
		p = filepath.Join(resolveN(parent, n), filepath.Base(p))
	}
	if *n >= 40 {
		return p
	}
	target, err := os.Readlink(p)
	if err != nil {
		return p
	}
	*n++
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(p), target)
	}
	return resolveN(filepath.Clean(target), n)
}

// resolveParent is resolvePath for an operation on the entry itself, as
// Rename and Remove act on a link rather than its target.
func resolveParent(p string) string {
	p = filepath.Clean(p)
	return filepath.Join(resolvePath(filepath.Dir(p)), filepath.Base(p))
}

// under reports whether p is root or below it.
func under(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// specLog collects what a checked run did wrong.
type specLog struct {
	mu       sync.Mutex
	problems []string
}

func (l *specLog) add(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.problems = append(l.problems, fmt.Sprintf(format, args...))
}

// guardSys is the System a migration is handed for one root. Each call
// must name a path under root and, when it succeeds, have reached one: the
// path is resolved before the call, as the call itself would resolve it.
type guardSys struct {
	inner System
	root  string
	log   *specLog
}

func (g guardSys) check(op, path string, resolved string, err error) {
	if !under(g.root, filepath.Clean(path)) {
		g.log.add("%s %s: named outside its root %s", op, path, g.root)
	}
	if err == nil && !under(resolvePath(g.root), resolved) {
		g.log.add("%s %s: reached %s, outside its root %s", op, path, resolved, g.root)
	}
}

func (g guardSys) Stat(name string) (os.FileInfo, error) {
	r := resolvePath(name)
	fi, err := g.inner.Stat(name)
	g.check("stat", name, r, err)
	return fi, err
}

func (g guardSys) ReadFile(name string) ([]byte, error) {
	r := resolvePath(name)
	data, err := g.inner.ReadFile(name)
	g.check("read", name, r, err)
	return data, err
}

func (g guardSys) WriteFile(name string, data []byte, perm os.FileMode) error {
	r := resolvePath(name)
	err := g.inner.WriteFile(name, data, perm)
	g.check("write", name, r, err)
	return err
}

func (g guardSys) MkdirAll(path string, perm os.FileMode) error {
	r := resolvePath(path)
	err := g.inner.MkdirAll(path, perm)
	g.check("mkdir", path, r, err)
	return err
}

func (g guardSys) Remove(name string) error {
	r := resolveParent(name)
	err := g.inner.Remove(name)
	g.check("remove", name, r, err)
	return err
}

func (g guardSys) Rename(oldpath, newpath string) error {
	ro, rn := resolveParent(oldpath), resolveParent(newpath)
	err := g.inner.Rename(oldpath, newpath)
	g.check("rename from", oldpath, ro, err)
	g.check("rename to", newpath, rn, err)
	return err
}

// specCtx is the Ctx Run's caller would build for tr, with every System
// guarded and the Adopt callbacks recording the dirs they're handed.
func specCtx(tr specTree, log *specLog) *Ctx {
	adopt := func(what string) func(string) error {
		return func(dir string) error {
			if !slices.Contains(tr.projects, dir) {
				log.add("adopt %s: handed %s, not a project dir", what, dir)
			}
			return nil
		}
	}
	return &Ctx{
		Sys:         guardSys{inner: osutil.RealSystem{}, root: tr.home, log: log},
		LoopDir:     tr.home,
		Version:     "spec",
		ProjectDirs: slices.Clone(tr.projects),
		ProjectSys: func(dir string) System {
			return guardSys{inner: osutil.RootFS{Root: dir}, root: dir, log: log}
		},
		AdoptProjectConfig:         adopt("config"),
		AdoptProjectWorkflows:      adopt("workflows"),
		AdoptProjectContainerImage: adopt("container image"),
		// The trust file is outside both roots; config.TrustStore covers the rename.
		RenameTrustedField: func(string, string) error { return nil },
	}
}

func specErrText(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

// errPanicked marks the error applyRecovering makes of a panic.
var errPanicked = errors.New("panicked")

// applyRecovering applies m, turning a panic into an error so the
// counterexample names its seed. A panic is a counterexample of its own:
// Run's caller is the daemon's startup, which it would take down.
func applyRecovering(m Migration, c *Ctx) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%w: %v", errPanicked, p)
		}
	}()
	return m.Apply(context.Background(), c)
}

// checkMigration applies m twice to the tree for seed, built in a fresh
// dir under parent, and returns what it did wrong, nothing when it holds.
func checkMigration(t *testing.T, parent string, m Migration, seed uint64) []string {
	base, err := os.MkdirTemp(parent, "tree")
	require.NoError(t, err)
	// macOS temp dirs sit behind a /var link; the roots are handed resolved,
	// as a real home and checkout would be.
	base, err = filepath.EvalSymlinks(base)
	require.NoError(t, err)
	tr := genTree(t, base, seed)

	outBefore, err := snapshotWalk(tr.outside)
	require.NoError(t, err)

	log := &specLog{}
	c := specCtx(tr, log)
	err1 := applyRecovering(m, c)
	once, err := snapshotWalk(base)
	require.NoError(t, err)
	err2 := applyRecovering(m, c)
	twice, err := snapshotWalk(base)
	require.NoError(t, err)

	problems := log.problems
	if errors.Is(err1, errPanicked) {
		problems = append(problems, err1.Error())
	}
	if specErrText(err1) != specErrText(err2) {
		problems = append(problems, fmt.Sprintf("not idempotent: first run %q, second %q", specErrText(err1), specErrText(err2)))
	}
	if d := snapshotDiff(once, twice); len(d) > 0 {
		problems = append(problems, "not idempotent: the second run changed "+strings.Join(d, "; "))
	}
	outAfter, err := snapshotWalk(tr.outside)
	require.NoError(t, err)
	if d := snapshotDiff(outBefore, outAfter); len(d) > 0 {
		problems = append(problems, "changed outside the roots: "+strings.Join(d, "; "))
	}
	return problems
}

// checkMigrations runs checkMigration for every migration in ms but the
// bootstrap, on seeds trees each, and returns the first counterexample:
// the version, the seed and what went wrong.
func checkMigrations(t *testing.T, ms []Migration, seeds uint64) string {
	parent := t.TempDir()
	for v := 1; v < len(ms); v++ {
		for seed := range seeds {
			if p := checkMigration(t, parent, ms[v], seed); len(p) > 0 {
				return fmt.Sprintf("migration %d (%s), seed %d: %s", v, ms[v].Description, seed, strings.Join(p, "\n  "))
			}
		}
	}
	return ""
}

// --- the spec ---

type MigrationSpecSuite struct {
	suite.Suite
}

func TestMigrationSpecSuite(t *testing.T) {
	suite.Run(t, new(MigrationSpecSuite))
}

// TestMigrationsIdempotentAndInside is the spec over the shipped list.
func (s *MigrationSpecSuite) TestMigrationsIdempotentAndInside() {
	require.Empty(s.T(), checkMigrations(s.T(), migrations, specSeeds))
}

// TestMigrationClassification fails for a migration with no entry in
// migrationClasses, or one whose entry no longer lines up with it.
func (s *MigrationSpecSuite) TestMigrationClassification() {
	require.Len(s.T(), migrationClasses, len(migrations), "classify every migration in migrationClasses")
	for v, m := range migrations {
		require.Equal(s.T(), m.Description, migrationClasses[v].description, "migration %d", v)
		require.Equal(s.T(), m.Apply == nil, migrationClasses[v].exempt == exemptBootstrap, "migration %d", v)
	}
}

// TestSpecCanFail injects broken migrations and requires the checker to
// find each one's counterexample.
func (s *MigrationSpecSuite) TestSpecCanFail() {
	projectConfigs := func(c *Ctx, write func(path string) error) error {
		for _, dir := range c.ProjectDirs {
			_ = write(filepath.Join(dir, ".loop", "config.json"))
		}
		return nil
	}
	cases := []struct {
		name  string
		apply func(context.Context, *Ctx) error
		want  string
	}{
		{
			name: "appends on every run",
			apply: func(_ context.Context, c *Ctx) error {
				path := filepath.Join(c.LoopDir, "config.json")
				data, _ := c.Sys.ReadFile(path)
				return c.Sys.WriteFile(path, append(data, "\n// migrated"...), 0o644)
			},
			want: "not idempotent",
		},
		{
			name: "reports a different error the second time",
			apply: func(_ context.Context, c *Ctx) error {
				marker := filepath.Join(c.LoopDir, "ran")
				if _, err := c.Sys.Stat(marker); err == nil {
					return fmt.Errorf("already ran")
				}
				return c.Sys.WriteFile(marker, nil, 0o644)
			},
			want: "not idempotent",
		},
		{
			name: "writes a project config through Sys",
			apply: func(_ context.Context, c *Ctx) error {
				return projectConfigs(c, func(p string) error { return c.Sys.WriteFile(p, []byte("{}"), 0o644) })
			},
			want: "named outside its root",
		},
		{
			name: "writes through a project's symlink out",
			apply: func(_ context.Context, c *Ctx) error {
				return projectConfigs(c, func(p string) error { return os.WriteFile(p, []byte("{}"), 0o644) })
			},
			want: "changed outside the roots",
		},
		{
			name: "follows the channel work dir's link out",
			apply: func(_ context.Context, c *Ctx) error {
				data, err := c.Sys.ReadFile(filepath.Join(c.LoopDir, "ch1", "work", "out", "config.json"))
				if err != nil {
					return nil
				}
				return c.Sys.WriteFile(filepath.Join(c.LoopDir, "copy"), data, 0o644)
			},
			want: "outside its root",
		},
		{
			name: "reaches out of a project by its System's own path",
			apply: func(_ context.Context, c *Ctx) error {
				for _, dir := range c.ProjectDirs {
					_, _ = c.ProjectSys(dir).ReadFile(filepath.Join(c.LoopDir, "config.json"))
				}
				return nil
			},
			want: "named outside its root",
		},
		{
			name: "hands Adopt a dir that isn't a project",
			apply: func(_ context.Context, c *Ctx) error {
				return c.AdoptProjectConfig(c.LoopDir)
			},
			want: "not a project dir",
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			got := checkMigrations(s.T(), []Migration{{Description: "bootstrap"}, {Description: tc.name, Apply: tc.apply}}, specSeeds)
			require.Contains(s.T(), got, tc.want)
		})
	}
}

// TestResolvePathFollowsChainsAndLoops checks the reference resolver on
// link chains, relative and absolute targets, missing entries and loops.
func (s *MigrationSpecSuite) TestResolvePathFollowsChainsAndLoops() {
	dir, err := filepath.EvalSymlinks(s.T().TempDir())
	require.NoError(s.T(), err)
	w := specWriter{s.T()}
	w.dir(filepath.Join(dir, "real"))
	w.link("real", filepath.Join(dir, "a"))
	w.link(filepath.Join(dir, "a"), filepath.Join(dir, "b"))
	w.link("loop2", filepath.Join(dir, "loop1"))
	w.link("loop1", filepath.Join(dir, "loop2"))

	cases := []struct{ in, want string }{
		{filepath.Join(dir, "b", "x"), filepath.Join(dir, "real", "x")},
		{filepath.Join(dir, "missing", "x"), filepath.Join(dir, "missing", "x")},
		{filepath.Join(dir, "a", "..", "real"), filepath.Join(dir, "real")},
	}
	for _, tc := range cases {
		require.Equal(s.T(), tc.want, resolvePath(tc.in), tc.in)
	}
	// A loop ends after the bound rather than hanging.
	require.NotEmpty(s.T(), resolvePath(filepath.Join(dir, "loop1")))
	require.Equal(s.T(), filepath.Join(dir, "real", "b"), resolveParent(filepath.Join(dir, "a", "b")))
}
