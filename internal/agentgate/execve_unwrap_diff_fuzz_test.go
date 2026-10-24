//go:build linux

package agentgate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Spec: the command the execve gate judges is the one the wrapper actually
// execs. When unwrapCommand names a command other than the wrapper itself,
// the real wrapper chain must run exactly that command (same program, same
// arguments), or run nothing at all. A gate that keeps the wrapper as the
// command makes no claim: the wrapper's own exec traps again.
//
// Each fuzz input picks a wrapper and a string of tokens from a vocabulary
// of the wrappers' options (flags, option values attached and separate,
// clustered short flags, long-option prefixes, "--", "-", NAME=VALUE
// assignments, env -S strings) and nested wrappers. The argv runs through
// the real wrapper binaries, with probes first on PATH: a stub for every
// word the vocabulary can turn into a command, which records the program
// and arguments it ran with, and a probe for every wrapper that records its
// own argv and then execs the real wrapper. The ld.so loader can only load
// an ELF, so its program is the real env, whose own step isn't recorded.
//
// Trusted base: the installed wrapper binaries (coreutils, util-linux, the
// glibc loader) are the reference; /bin/sh runs the probes faithfully; the
// kernel passes a script the path it was exec'd by as $0, so a record names
// the program file (env -a / --argv0 only rename argv[0], and the gate
// judges the program it names). sudo isn't exercised: it needs credentials,
// and its grammar is covered by the unwrapCases table. Options acting on a
// running process (taskset -p, chrt -p, ionice -p/-P/-u) are left out of
// the vocabulary; the gate keeps those wrappers as the command anyway.
func FuzzUnwrapCommandMatchesExec(f *testing.F) {
	w := newUnwrapWorld(f)
	for _, seed := range unwrapSeeds() {
		f.Add(encodeUnwrapSeed(f, seed...))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		argv, skip := w.argv(data)
		if skip != "" {
			t.Skip(skip)
		}
		ran := w.run(t, argv)
		require.Empty(t, w.counterexample(argv, ran, unwrapCommand))
	})
}

// The spec must be able to fail: a gate that takes a wrapper's first
// non-option argument as its command, not knowing which options take a
// value or which wrappers take an operand, judges a command that never runs.
func (s *ExecveSuite) TestUnwrapExecSpecCanFail() {
	w := newUnwrapWorld(s.T())
	naive := func(argv []string) []string {
		for len(argv) > 1 && wrapperFor(filepath.Base(argv[0])) != nil {
			i := 1
			for i < len(argv) && strings.HasPrefix(argv[i], "-") {
				i++
			}
			if i == len(argv) {
				break
			}
			argv = argv[i:]
		}
		return argv
	}
	found := 0
	for _, seed := range unwrapSeeds() {
		argv, skip := w.argv(encodeUnwrapSeed(s.T(), seed...))
		if skip != "" {
			continue
		}
		if w.counterexample(argv, w.run(s.T(), argv), naive) != "" {
			found++
		}
	}
	if found == 0 && len(w.real) == 0 {
		s.T().Skip("no wrapper is installed")
	}
	s.Require().Positive(found)
}

func (s *ExecveSuite) TestEncodeUnwrapSeedRoundTrips() {
	w := newUnwrapWorld(s.T())
	for _, seed := range unwrapSeeds() {
		argv, skip := w.argv(encodeUnwrapSeed(s.T(), seed...))
		if skip != "" {
			continue
		}
		want := resolveUnwrapArgs(seed[1:], w.dir, w.loader)
		for i, a := range want {
			if a == "@env" {
				want[i] = w.real["env"]
			}
		}
		s.Require().Equal(want, argv[1:], "seed %q", seed)
	}
}

// unwrapLoaderName names the loader among the outer wrappers.
const unwrapLoaderName = "ld"

// unwrapOuters is the wrapper an input's first byte picks.
func unwrapOuters() []string {
	return []string{"env", "nice", "timeout", "nohup", "ionice", "chrt", "taskset", "stdbuf", "setsid", "unshare", unwrapLoaderName}
}

// unwrapVocab is the tokens a wrapper's arguments are drawn from. "@/w" is
// the probe for wrapper w, run by path so the step is recorded even once env
// has dropped PATH. Inputs index into it, so only append: reordering
// changes what the committed seeds under testdata/fuzz decode to.
func unwrapVocab() []string {
	return []string{
		// commands, separators, values
		"@/payloadcmd", "payloadcmd", "a", "--", "-", "5", "0", "1", "3", "L", "X", "KILL", "INT", "/tmp", "foo",
		"FOO=1", "=x", "-x", "--nope", "--help",
		// env
		"-i", "-u", "-uX", "--unset=X", "--un", "-C", "-a", "--argv0", "-v", "--debug", "-vu", "-S", "-vS",
		"--split-string=payloadcmd a", "-0", "--i", "--ignore-environment=1", "--block-signal", "--block-signal=INT",
		"payloadcmd a", "-u X payloadcmd", "-i payloadcmd", "a\tb", "-S payloadcmd",
		// nice
		"-n", "-n5", "-5", "--5", "-+5", "--adjustment=5", "--adj", "-1x",
		// timeout
		"-s", "-sKILL", "--signal=KILL", "--s=KILL", "-k", "-k1", "--kill-after", "-fpv", "-pk1", "--foreground", "--v",
		// ionice
		"-c", "-c3", "--class=3", "-t", "--cl",
		// chrt
		"-o", "--other", "--oth", "-b", "-R", "-T", "-o0",
		// taskset
		"--cpu", "-ac", "-c0",
		// stdbuf
		"-oL", "-o", "--output=L", "--out", "-i0", "-eL",
		// setsid
		"-w", "--fork", "--wait",
		// unshare
		"-r", "--wd=/tmp", "--kill-child", "-U", "--map-u", "-mfoo",
		// nested wrappers
		"@/env", "@/nice", "@/timeout", "@/nohup", "@/ionice", "@/chrt", "@/taskset", "@/stdbuf", "@/setsid", "@/unshare",
	}
}

// unwrapLoaderVocab is the loader's arguments. "@env" is the real env, the
// program the loader runs; the tokens after it come from unwrapVocab.
func unwrapLoaderVocab() []string {
	return []string{
		"@env", "--library-path", "/tmp", "--argv0", "foo", "--inhibit-cache", "--", "--list", "--lib",
		"--library-path=/tmp", "--inhibit-rpath", "--preload", "-x", "payloadcmd", "--nope",
	}
}

// unwrapSeeds are the committed shapes: each wrapper's options in their
// spellings, and chains.
func unwrapSeeds() [][]string {
	p := "payloadcmd"
	return [][]string{
		{"env", "FOO=1", "=x", p, "a"},
		{"env", "-u", "X", "-C", "/tmp", "-a", "foo", p, "a"},
		{"env", "-i", "@/payloadcmd", "a"},
		{"env", "-", "@/" + p},
		{"env", "--un", "X", "--block-signal", "INT", p},
		{"env", "-vS", "-u X payloadcmd", "a"},
		{"env", "--split-string=payloadcmd a", "a"},
		{"env", "-S", "a\tb", p},
		{"env", "FOO=1", "-u", "X", p},
		{"env", "--i", p},
		{"nice", "-n", "5", p, "a"},
		{"nice", "-5", "-n5", "--", p},
		{"nice", "--adj", "5", "-+5", p},
		{"nice", "-1x", p},
		{"timeout", "-sKILL", "-k1", "5", p, "-s"},
		{"timeout", "-pk1", "--", "5", "--", p},
		{"timeout", "--s=KILL", "--v", "5", p},
		{"nohup", "--", p, "--help"},
		{"ionice", "-c", "3", "-t", p},
		{"ionice", "--class=3", "--cl", "3", p},
		{"chrt", "-o", "0", "--", p},
		{"chrt", "--oth", "--", "0", p, "a"},
		{"chrt", "-o", p, "a"},
		{"taskset", "1", p, "-c"},
		{"taskset", "-ac", "0", "--", p},
		{"stdbuf", "-oL", "-i0", p},
		{"stdbuf", "--out", "L", "--", p},
		{"stdbuf", p},
		{"setsid", "-w", "--fork", p},
		{"setsid", "--wait", "--", p},
		{"unshare", "--kill-child", "-U", p},
		{"unshare", "--map-u", "0", p},
		{"timeout", "5", "@/nice", "-n", "3", "@/env", "-u", "X", p, "a"},
		{"env", "-i", "@/stdbuf", "-oL", "@/setsid", "-w", "@/" + p},
		{"nice", "@/timeout", "--help"},
		{"ld", "--library-path", "/tmp", "--argv0", "foo", "@env", p, "a"},
		{"ld", "--inhibit-cache", "--", "@env", "-i", "@/" + p},
		{"ld", "--library-path=/tmp", "@env", p},
		{"ld", "-x"},
	}
}

// encodeUnwrapSeed turns a wrapper name and its tokens into the fuzz input
// that picks them.
func encodeUnwrapSeed(tb testing.TB, tokens ...string) []byte {
	outer := slices.Index(unwrapOuters(), tokens[0])
	require.GreaterOrEqual(tb, outer, 0, "unknown wrapper %q", tokens[0])
	out := []byte{byte(outer)}
	vocab := unwrapVocab()
	if tokens[0] == unwrapLoaderName {
		vocab = unwrapLoaderVocab()
	}
	for _, tok := range tokens[1:] {
		i := slices.Index(vocab, tok)
		require.GreaterOrEqual(tb, i, 0, "token %q isn't in the vocabulary", tok)
		out = append(out, byte(i))
		if tok == "@env" {
			vocab = unwrapVocab()
		}
	}
	return out
}

// unwrapWorld is the probe directory and the installed binaries the spec
// runs against.
type unwrapWorld struct {
	dir, log string
	loader   string
	// real maps each installed wrapper to its binary.
	real map[string]string
	path string
}

func newUnwrapWorld(tb testing.TB) *unwrapWorld {
	dir := tb.TempDir()
	w := &unwrapWorld{
		dir:  dir,
		log:  filepath.Join(dir, "exec.log"),
		real: map[string]string{},
		path: dir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
	if loaders, _ := filepath.Glob("/lib*/ld-linux*.so*"); len(loaders) > 0 {
		w.loader = loaders[0]
	}
	record := "printf '%s\\0' \"$0\" \"$@\" '\x1e' >> '" + w.log + "'\n"
	for _, name := range unwrapOuters() {
		bin, err := exec.LookPath(name)
		if name == unwrapLoaderName || err != nil {
			continue
		}
		w.real[name] = bin
		script := "#!/bin/sh\n" + record + "exec '" + bin + "' \"$@\"\n"
		require.NoError(tb, os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755))
	}
	// Every word a token or an env -S field can make a command gets a stub.
	// None may name a real program, which env -i would find in /bin.
	words := map[string]bool{}
	for _, tok := range slices.Concat(unwrapVocab(), unwrapLoaderVocab()) {
		for _, word := range append(strings.Fields(tok), tok) {
			if !strings.ContainsRune(word, '/') && !strings.HasPrefix(word, "@") {
				words[word] = true
			}
		}
	}
	for word := range words {
		for _, sys := range []string{"/bin", "/usr/bin"} {
			_, err := os.Stat(filepath.Join(sys, word))
			require.Truef(tb, errors.Is(err, fs.ErrNotExist), "token %q names a real program in %s", word, sys)
		}
		require.NoError(tb, os.WriteFile(filepath.Join(dir, word), []byte("#!/bin/sh\n"+record), 0o755))
	}
	return w
}

// argv decodes a fuzz input. skip is non-empty when it needs a wrapper that
// isn't installed.
func (w *unwrapWorld) argv(data []byte) (argv []string, skip string) {
	if len(data) == 0 {
		return nil, "empty input"
	}
	outers := unwrapOuters()
	outer := outers[int(data[0])%len(outers)]
	vocab := unwrapVocab()
	if outer == unwrapLoaderName {
		if w.loader == "" || w.real["env"] == "" {
			return nil, "the loader or env isn't installed"
		}
		vocab = unwrapLoaderVocab()
		argv = []string{w.loader}
	} else {
		argv = []string{"@/" + outer}
	}
	for _, b := range data[1:min(len(data), 25)] {
		tok := vocab[int(b)%len(vocab)]
		if tok == "@env" {
			tok = w.real["env"]
			vocab = unwrapVocab()
		}
		argv = append(argv, tok)
	}
	for i, a := range argv {
		if name, ok := strings.CutPrefix(a, "@/"); ok {
			if _, wrapper := transparentWrappers[name]; wrapper && w.real[name] == "" {
				return nil, name + " isn't installed"
			}
			argv[i] = filepath.Join(w.dir, name)
		}
	}
	return argv, ""
}

// run executes argv and returns every recorded exec, in order.
func (w *unwrapWorld) run(tb testing.TB, argv []string) [][]string {
	require.NoError(tb, os.RemoveAll(w.log))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = w.dir
	cmd.Env = append(os.Environ(), "PATH="+w.path)
	// A pipe, not a file: Wait returns once every process holding it has
	// exited, so a command run by setsid --fork is recorded too.
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	_ = cmd.Run() // only what ran matters, not how the wrapper exited
	require.NoError(tb, ctx.Err(), "%q timed out", argv)

	raw, err := os.ReadFile(w.log)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	require.NoError(tb, err)
	var ran [][]string
	var rec []string
	for _, field := range strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00") {
		if field == "\x1e" {
			ran = append(ran, rec)
			rec = nil
			continue
		}
		rec = append(rec, field)
	}
	return ran
}

// counterexample checks one run step by step: each recorded exec is what
// the gate sees at that exec's trap. When unwrap names a command there, the
// next exec must be judged the same, or there must be none (the wrapper
// refused to run anything). A loader run starts with the loader's own step,
// which no probe records. "" means no counterexample.
func (w *unwrapWorld) counterexample(argv []string, ran [][]string, unwrap func([]string) []string) string {
	steps := ran
	if argv[0] == w.loader {
		steps = slices.Concat([][]string{argv}, ran)
	}
	for k, step := range steps {
		judged := unwrap(step)
		if slices.Equal(judged, step) || k+1 == len(steps) {
			// Judged as itself, the exec's command traps on its own; or
			// nothing ran after it.
			continue
		}
		if len(judged) > 0 && judged[0] == w.real["env"] {
			// The loader's env, whose own step isn't recorded.
			continue
		}
		if next := unwrap(steps[k+1]); !sameCommand(judged, next) {
			return fmt.Sprintf("gate judged %q as %q, but it ran %q, judged %q", step, judged, steps[k+1], next)
		}
	}
	return ""
}

// sameCommand reports whether two argvs run the same program with the same
// arguments; argv[0] is compared by basename, as a probe records the path
// it was found at.
func sameCommand(a, b []string) bool {
	return len(a) > 0 && len(b) > 0 && filepath.Base(a[0]) == filepath.Base(b[0]) && slices.Equal(a[1:], b[1:])
}
