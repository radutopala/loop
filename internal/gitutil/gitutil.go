// Package gitutil builds git invocations that are safe to run on the host
// inside repositories an agent container can write to. The work dir is a bind
// mount, so an agent controls .git/config, .git/hooks and .gitattributes; a
// plain `git status` there would happily run whatever core.fsmonitor, a hook
// or a clean filter names. Every git process the daemon starts in such a repo
// goes through Command (or, for tools that spawn git themselves, Environ) so
// repo-controlled config can never make host git execute a program.
package gitutil

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
)

// hardenedConfig is applied at command scope, which outranks every config
// file, so a repo can't switch any of it back on.
var hardenedConfig = [][2]string{
	{"core.fsmonitor", "false"},
	{"core.hooksPath", "/dev/null"},
	{"core.pager", "cat"},
	{"core.askPass", ""},
	{"core.alternateRefsCommand", ""},
	{"diff.external", ""},
	{"log.showSignature", "false"},
	{"protocol.ext.allow", "never"},
}

// hardenedEnv is set on every invocation, replacing any inherited value.
var hardenedEnv = []string{
	"GIT_CONFIG_NOSYSTEM=1",
	"GIT_TERMINAL_PROMPT=0",
	"GIT_OPTIONAL_LOCKS=0",
	"GIT_PAGER=cat",
}

// Command returns a git command for args run in dir, carrying the hardened
// environment from Environ. Diff-producing subcommands (diff, show, log -p)
// must still pass --no-ext-diff --no-textconv: diff drivers are named by
// .gitattributes, so they can't be neutralized by config up front.
func Command(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = Environ(ctx, dir)
	return cmd
}

// Environ returns os.Environ() hardened for git processes run in dir: the
// config in hardenedConfig plus an emptied clean/smudge/process command for
// every filter driver dir's repo config touches, delivered through
// GIT_CONFIG_COUNT so it also reaches git processes spawned by other tools
// (gh). An empty filter command is a no-op, which disarms a repo-defined
// filter without having to know which paths .gitattributes routes through it.
func Environ(ctx context.Context, dir string) []string {
	return buildEnviron(os.Environ(), filterDrivers(ctx, dir))
}

// buildEnviron strips inherited variables that would override or smuggle in
// git config and appends the hardened set, neutralizing each named filter.
func buildEnviron(environ, filters []string) []string {
	env := slices.DeleteFunc(slices.Clone(environ), inheritedGitOverride)
	env = append(env, hardenedEnv...)
	cfg := slices.Clone(hardenedConfig)
	for _, name := range filters {
		cfg = append(cfg,
			[2]string{"filter." + name + ".clean", ""},
			[2]string{"filter." + name + ".smudge", ""},
			[2]string{"filter." + name + ".process", ""},
			[2]string{"filter." + name + ".required", "false"},
		)
	}
	env = append(env, "GIT_CONFIG_COUNT="+strconv.Itoa(len(cfg)))
	for i, kv := range cfg {
		env = append(env,
			fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, kv[0]),
			fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, kv[1]),
		)
	}
	return env
}

// inheritedGitOverride reports whether an environment entry is one this
// package replaces or must not let through: variables it sets itself, any
// command-scope config (which would outrank or renumber ours), and the
// external-diff and exec-path overrides.
func inheritedGitOverride(kv string) bool {
	name, _, _ := strings.Cut(kv, "=")
	switch name {
	case "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS", "GIT_EXTERNAL_DIFF", "GIT_EXEC_PATH",
		"GIT_CONFIG_NOSYSTEM", "GIT_TERMINAL_PROMPT", "GIT_OPTIONAL_LOCKS", "GIT_PAGER":
		return true
	}
	return strings.HasPrefix(name, "GIT_CONFIG_KEY_") || strings.HasPrefix(name, "GIT_CONFIG_VALUE_")
}

// filterDrivers lists the filter driver names the repo at dir touches in its
// own config (local or worktree scope, includes followed). Drivers set up
// only in the user's global config — git-lfs, typically — are the host's own
// and keep working; a repo that so much as sets one key of a driver gets the
// whole driver disarmed. `git config` only reads config files — it runs no
// hooks, filters or fsmonitor — so it is safe to ask before hardening. A
// non-repo dir, or one without filters, yields none.
func filterDrivers(ctx context.Context, dir string) []string {
	cmd := exec.CommandContext(ctx, "git", "config", "--null", "--show-scope", "--name-only", "--get-regexp", `^filter\.`)
	cmd.Dir = dir
	cmd.Env = buildEnviron(os.Environ(), nil)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseFilterNames(string(out))
}

// parseFilterNames extracts the unique driver names from `--show-scope
// --name-only --null` output — NUL-terminated scope, key pairs — skipping
// keys from the host's own (global, system) config. The name is everything
// between the first and last dot of `filter.<name>.<key>`, so names
// containing dots survive intact.
func parseFilterNames(out string) []string {
	var names []string
	fields := strings.Split(out, "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		scope, key := fields[i], fields[i+1]
		rest, ok := strings.CutPrefix(key, "filter.")
		dot := strings.LastIndexByte(rest, '.')
		if scope == "global" || scope == "system" || !ok || dot <= 0 {
			continue
		}
		if name := rest[:dot]; !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}
