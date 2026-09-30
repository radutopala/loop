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
// System config stays on: it takes root to write, so it's the host's, and on
// macOS it's where git keeps the keychain credential helper.
var hardenedEnv = []string{
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
// config in hardenedConfig plus overrides disarming every command the repo's
// own config names (see repoOverrides), delivered through GIT_CONFIG_COUNT so
// it also reaches git processes spawned by other tools (gh).
func Environ(ctx context.Context, dir string) []string {
	return buildEnviron(os.Environ(), repoOverrides(readConfig(ctx, dir)))
}

// buildEnviron strips inherited variables that would override or smuggle in
// git config and appends the hardened set, then overrides.
func buildEnviron(environ []string, overrides [][2]string) []string {
	env := slices.DeleteFunc(slices.Clone(environ), inheritedGitOverride)
	env = append(env, hardenedEnv...)
	cfg := append(slices.Clone(hardenedConfig), overrides...)
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
		"GIT_TERMINAL_PROMPT", "GIT_OPTIONAL_LOCKS", "GIT_PAGER":
		return true
	}
	return strings.HasPrefix(name, "GIT_CONFIG_KEY_") || strings.HasPrefix(name, "GIT_CONFIG_VALUE_")
}

// configEntry is one key of the config git reads in a repo.
type configEntry struct {
	scope, key, value string
}

// fromHost reports whether the entry comes from the host's own config (global
// or system) rather than the repo's (local, worktree, or a file either
// includes).
func (e configEntry) fromHost() bool {
	return e.scope == "global" || e.scope == "system"
}

// commandKeys matches the config keys that name a program git runs and that
// repoOverrides disarms. git lowercases section and key names but keeps the
// subsection (a remote name, a credential URL) as written.
const commandKeys = `^(filter\..+|credential\.(.+\.)?helper|core\.sshcommand|core\.gitproxy|remote\..+\.(uploadpack|receivepack))$`

// readConfig lists the entries of dir's config matching commandKeys, in the
// order git reads them. `git config` only reads config files — it runs no
// hooks, filters or fsmonitor — so it is safe to ask before hardening. A
// non-repo dir, or one without such keys, yields none.
func readConfig(ctx context.Context, dir string) []configEntry {
	cmd := exec.CommandContext(ctx, "git", "config", "--null", "--show-scope", "--get-regexp", commandKeys)
	cmd.Dir = dir
	cmd.Env = buildEnviron(os.Environ(), nil)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseConfig(string(out))
}

// parseConfig splits `--null --show-scope` output: NUL-terminated scope, then
// NUL-terminated "key\nvalue" (a key set without a value has no newline).
func parseConfig(out string) []configEntry {
	var entries []configEntry
	fields := strings.Split(out, "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		key, value, _ := strings.Cut(fields[i+1], "\n")
		entries = append(entries, configEntry{scope: fields[i], key: key, value: value})
	}
	return entries
}

// repoOverrides returns command-scope config that disarms every program the
// repo's own config names, keeping what the host's config sets:
//   - A filter driver the repo touches gets empty commands, a no-op, so it's
//     disarmed without knowing which paths .gitattributes routes through it.
//     Drivers only the host sets up (git-lfs, typically) keep working.
//   - Credential helpers form one list across credential.helper and
//     credential.<url>.helper, and an empty value clears it. If the repo adds
//     any, every helper key is cleared, then the host's helpers are added back
//     in the order git read them.
//   - core.sshCommand takes the last value, so the host's (or plain ssh) goes
//     last.
//   - core.gitProxy, remote.<name>.uploadpack and receivepack take the first
//     value, which command scope can't outrank. So a repo that sets one loses
//     the one transport that runs it here: git:// for the proxy, local paths
//     for upload-pack and receive-pack (over ssh those run on the server).
func repoOverrides(entries []configEntry) [][2]string {
	var filters, cfg [][2]string
	var helperKeys []string
	var hostHelpers [][2]string
	repoHelper := false
	hostSSH := "ssh"
	for _, e := range entries {
		switch {
		case strings.HasPrefix(e.key, "filter."):
			rest := strings.TrimPrefix(e.key, "filter.")
			dot := strings.LastIndexByte(rest, '.')
			if e.fromHost() || dot <= 0 {
				continue
			}
			name := rest[:dot]
			if !slices.Contains(filters, [2]string{"filter." + name + ".clean", ""}) {
				filters = append(filters,
					[2]string{"filter." + name + ".clean", ""},
					[2]string{"filter." + name + ".smudge", ""},
					[2]string{"filter." + name + ".process", ""},
					[2]string{"filter." + name + ".required", "false"},
				)
			}
		case strings.HasPrefix(e.key, "credential."):
			if !slices.Contains(helperKeys, e.key) {
				helperKeys = append(helperKeys, e.key)
			}
			if e.fromHost() {
				hostHelpers = append(hostHelpers, [2]string{e.key, e.value})
			} else {
				repoHelper = true
			}
		case e.key == "core.sshcommand":
			if e.fromHost() {
				hostSSH = e.value
			}
		}
	}
	cfg = append(cfg, filters...)
	if repoHelper {
		for _, k := range helperKeys {
			cfg = append(cfg, [2]string{k, ""})
		}
		cfg = append(cfg, hostHelpers...)
	}
	for _, e := range entries {
		if e.fromHost() {
			continue
		}
		switch {
		case e.key == "core.sshcommand":
			cfg = appendOnce(cfg, [2]string{"core.sshCommand", hostSSH})
		case e.key == "core.gitproxy":
			cfg = appendOnce(cfg, [2]string{"protocol.git.allow", "never"})
		case strings.HasPrefix(e.key, "remote."):
			cfg = appendOnce(cfg, [2]string{"protocol.file.allow", "never"})
		}
	}
	return cfg
}

// appendOnce appends kv to cfg unless it's already there.
func appendOnce(cfg [][2]string, kv [2]string) [][2]string {
	if slices.Contains(cfg, kv) {
		return cfg
	}
	return append(cfg, kv)
}
