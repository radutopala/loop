package config

import "github.com/radutopala/loop/internal/types"

// DefaultGatePathRules returns the baseline unix-socket connect(2) rules.
// Returns a fresh slice on each call so callers can append without mutating canonical defaults.
//
// The direct-daemon path is hard-denied; the proxied path is silently allowed
// because every HTTP request through it is re-gated by the docker proxy's own
// rules — a prompt at the socket-connect layer would just add one extra
// up-front "agent wants to use docker" dialog per session without catching
// anything the proxy doesn't already see.
func DefaultGatePathRules() []types.PathRule {
	return []types.PathRule{
		{
			Pattern:  "/var/run/docker.sock.host",
			Decision: types.DecisionDeny,
			Message:  "direct daemon socket access blocked; use /var/run/docker.sock (proxied)",
		},
		{
			Pattern:  "/var/run/docker.sock",
			Decision: types.DecisionAllow,
		},
	}
}

// DefaultGateCommandRules returns the baseline execve(2) rules.
//
// Order matters: rules are evaluated top-to-bottom and the first match wins.
// The /tmp/ allow for rm is intentionally placed BEFORE the rm-rf deny so
// test cleanup (e.g. `rm -rf /tmp/testgit`) doesn't trip the absolute-path
// deny. The allow regex requires every non-flag argument to start with
// /tmp/, so `rm -rf /tmp/a /etc/b` still falls through to the deny.
func DefaultGateCommandRules() []types.CommandRule {
	return []types.CommandRule{
		{
			Commands:     []string{"rm"},
			ArgsPatterns: []string{`^(-[a-zA-Z]+\s+)*/tmp/\S+(\s+/tmp/\S+)*$`},
			Decision:     types.DecisionAllow,
			Message:      "rm under /tmp",
		},
		{
			Commands:     []string{"rm"},
			ArgsPatterns: []string{".*-[a-zA-Z]*r[fF]?.* /.*"},
			Decision:     types.DecisionDeny,
			Message:      "rm -rf on absolute path",
		},
	}
}

// DefaultGateFileRules returns the baseline file-op rules (9 rules, denies
// first). The workspace allow (the real host bind-mount path for the channel)
// is NOT in this list — it's injected per container by
// container.writeGatePolicyFile, which places it right before the first Allow
// so the denies above still fire inside the workspace.
func DefaultGateFileRules() []types.FileRule {
	return []types.FileRule{
		{
			// /proc/*/environ was previously denied but generated chronic
			// noise (Go test binaries, tooling, and runtime probes inside the
			// container open it routinely) and the threat model didn't hold
			// up: the gate parent's env carries no exploitable secret — the
			// notify fd is passed via SCM_RIGHTS, not authenticated by a
			// token an env reader could lift. /proc/*/mem and /proc/kcore
			// stay denied: they expose whole-process / kernel memory, which
			// is a real exfiltration channel.
			Paths:      []string{"/proc/*/mem", "/proc/kcore"},
			Operations: []string{"read"},
			Decision:   types.DecisionDeny,
			Message:    "kernel/process-memory read blocked",
		},
		{
			Paths: []string{
				"/etc/shadow",
				"/etc/gshadow",
				"/etc/sudoers",
				"/etc/sudoers.d/**",
				"/etc/ssh/ssh_host_*_key",
				"/etc/ssh/ssh_host_*_key.pub",
			},
			Operations: []string{"read", "write", "create", "delete", "chmod", "chown"},
			Decision:   types.DecisionDeny,
			Message:    "root-credential file",
		},
		{
			Paths: []string{
				"**/.ssh/**",
				"**/.aws/**",
				"**/.gcp/**",
				"**/.config/gcloud/**",
				"**/.kube/**",
				"**/.netrc",
				"**/.pgpass",
			},
			Operations: []string{"read", "write", "create", "delete", "chmod"},
			Decision:   types.DecisionDeny,
			Message:    "credentials path blocked",
		},
		{
			// ~/.docker/config.json and ~/.npmrc are credential files but tools
			// (docker CLI, npm) read them on every invocation for registry
			// config — a read-deny surfaces as EPERM noise and breaks routine
			// commands. Writes are what an agent could use to plant malicious
			// creds, so keep those denied; reads are allowed on the assumption
			// that the host file holds only registry/proxy config (operators
			// who keep auth tokens here should not bind-mount their host home
			// into agent containers).
			//
			// Scope to actual user-home layouts (same enumeration as the
			// shell-rcfile rule below). A `**/.npmrc` glob caught the npm
			// package's bundled .npmrc template that nodeenv extracts into
			// ~/.cache/pre-commit/.../node_modules/npm/.npmrc — EPERM there
			// broke pre-commit's nodeenv install, and even cleanup (rm/unlink)
			// was blocked because the same rule denies delete on the same
			// filename. Filename-anywhere is too broad for cred protection.
			Paths: []string{
				"/root/.docker/config.json",
				"/root/.npmrc",
				"/home/*/.docker/config.json",
				"/home/*/.npmrc",
				"/Users/*/.docker/config.json",
				"/Users/*/.npmrc",
			},
			Operations: []string{"write", "create", "delete", "chmod"},
			Decision:   types.DecisionDeny,
			Message:    "registry credentials file is read-only to the agent",
		},
		{
			// Narrow-scope: protect claude *settings* only. The ~/.claude
			// tree also holds harness-internal ephemeral state the agent
			// legitimately writes on every Bash tool call (session-env,
			// todos, shell-snapshots, projects auto-memory, plugins/, …);
			// blanket denying **/.claude/** broke Bash with EPERM on mkdir.
			// CLAUDE.md and mcp*.json are intentionally NOT denied — agents
			// legitimately update their own memory files and per-project MCP
			// configs as part of normal work; the only truly off-limits
			// surface is the harness's own permission/setting files.
			Paths: []string{
				"**/.claude/settings.json",
				"**/.claude/settings.local.json",
			},
			Operations: []string{"write", "create", "delete", "chmod"},
			Decision:   types.DecisionDeny,
			Message:    "claude settings file is read-only to the agent",
		},
		{
			// Scoped to real user-home rcfiles, not the filename anywhere.
			// `~/.bashrc` inside the agent container is bind-mounted read-only
			// from `~/.loop/.bashrc` on the host, so the attack surface is
			// writes to the actual login-shell rcfile. A `**/.bashrc` glob
			// tripped legitimate test fixtures that write a `.bashrc` into a
			// t.TempDir() to exercise onboard.
			//
			// Home-dir layouts we cover:
			//   - /root/            (container running as root)
			//   - /home/<user>/     (container with its own user namespace)
			//   - /Users/<user>/    (macOS host-home bind-mounted through —
			//                        Docker Desktop container with HOME set to
			//                        the host user's real `/Users/<user>` path)
			Paths: []string{
				"/root/.bashrc",
				"/root/.bash_profile",
				"/root/.zshrc",
				"/root/.zprofile",
				"/root/.profile",
				"/root/.bash_login",
				"/root/.inputrc",
				"/home/*/.bashrc",
				"/home/*/.bash_profile",
				"/home/*/.zshrc",
				"/home/*/.zprofile",
				"/home/*/.profile",
				"/home/*/.bash_login",
				"/home/*/.inputrc",
				"/Users/*/.bashrc",
				"/Users/*/.bash_profile",
				"/Users/*/.zshrc",
				"/Users/*/.zprofile",
				"/Users/*/.profile",
				"/Users/*/.bash_login",
				"/Users/*/.inputrc",
			},
			Operations: []string{"write", "create", "delete", "chmod"},
			Decision:   types.DecisionDeny,
			Message:    "shell rcfile write blocked",
		},
		{
			Paths:      []string{"/etc/**", "/usr/**", "/bin/**", "/sbin/**", "/lib/**", "/lib64/**", "/boot/**"},
			Operations: []string{"write", "create", "delete", "chmod", "chown"},
			Decision:   types.DecisionDeny,
			Message:    "system path write blocked",
		},
		{
			// The workspace allow (workDir/**, parentDirPath/**) is injected at
			// policy-serialization time by writeGatePolicyFile — it's dynamic
			// per container. This rule only covers the OS tmp dirs.
			Paths:      []string{"/tmp/**", "/var/tmp/**"},
			Operations: []string{"read", "write", "create", "delete", "stat", "list", "chmod", "chown", "link"},
			Decision:   types.DecisionAllow,
			Message:    "tmp fast-path",
		},
		{
			Paths: []string{
				"/proc/**",
				"/sys/**",
				"/dev/null",
				"/dev/zero",
				"/dev/urandom",
				"/dev/random",
				"/dev/tty",
				"/dev/pts/**",
			},
			Operations: []string{"read", "stat", "list"},
			Decision:   types.DecisionAllow,
			Message:    "system reads fast-path",
		},
	}
}

// DefaultDockerProxyHTTPRules returns the baseline per-method/per-path Docker API rules.
//
// Policy posture: DefaultDecision is Allow. The body rules on POST
// /containers/create and /update are the real container-escape guardrails —
// they hard-deny host bind mounts, privileged mode, host namespaces, dangerous
// caps, unconfined security opts, device access, and masked/readonly-paths
// tampering. Given that, the agent can run docker freely (make lint, builds,
// tests, `docker run`, attach, wait, …) without per-call prompts.
//
// This list only enumerates the exceptions to the default:
//
//   - Approve: lateral-movement ops the proxy can't tell apart by path —
//     exec/attach-start into an arbitrary container, and docker cp across
//     containers. The agent owns the containers it creates, but the proxy
//     has no way to distinguish "my lint container" from "user's local prod
//     postgres" given only a container id.
//
//   - Deny: swarm / nodes / secrets / configs / plugins APIs — these are
//     off-limits surfaces with no legitimate dev-loop use.
func DefaultDockerProxyHTTPRules() []types.HTTPServiceRule {
	return []types.HTTPServiceRule{
		{Methods: []string{"POST"}, Paths: []string{"^/containers/[^/]+/exec$", "^/exec/[^/]+/start$"}, Decision: types.DecisionApprove, Message: "exec into container"},
		{Methods: []string{"PUT", "GET", "HEAD"}, Paths: []string{"^/containers/[^/]+/archive$"}, Decision: types.DecisionApprove, Message: "docker cp into/out of container"},
		{Methods: []string{"*"}, Paths: []string{"^/swarm/", "^/nodes/", "^/secrets/", "^/configs/", "^/plugins/"}, Decision: types.DecisionDeny, Message: "swarm/secrets/plugins API off-limits"},
	}
}

// dangerousBindSourceRegexes lists host paths that must never appear as the
// source side of a bind mount the agent creates in another container —
// covering the legacy `HostConfig.Binds` (short -v syntax) and the long-form
// `HostConfig.Mounts` (used by docker-compose v2 and `docker run --mount`).
// Keep in sync between the two checks; the only difference is which JSONPath
// they read.
var dangerousBindSourceRegexes = []string{
	"^/$",
	"^/etc(/|$)",
	"^/root(/|$)",
	"^/home(/|$)",
	"^/boot(/|$)",
	"^/usr(/|$)",
	"^/lib(/|$)",
	"^/lib64(/|$)",
	"^/proc(/|$)",
	"^/sys(/|$)",
	"^/dev(/|$)",
	`^/var/run/docker\.sock$`,
	"^/run/loop/",
}

// DefaultDockerProxyBodyRules returns the baseline container-escape defense body rules.
func DefaultDockerProxyBodyRules() []types.BodyRule {
	return []types.BodyRule{
		{
			AppliesTo:    "POST ^/containers/create$",
			ContentTypes: []string{"application/json"},
			MaxBodyBytes: 1048576,
			JSONChecks: []types.JSONCheck{
				{
					Path:   "HostConfig.Binds[*]",
					Op:     "source_path_in",
					Values: append([]string(nil), dangerousBindSourceRegexes...),
				},
				{
					// Same regex set as Binds — compose v2 and `--mount` send
					// long-form mounts here. A previous version used
					// starts_with_any with a literal "/" entry, which silently
					// matched every absolute path and blanket-denied compose v2.
					Path:   "HostConfig.Mounts[*].Source",
					Op:     "source_path_in",
					Values: append([]string(nil), dangerousBindSourceRegexes...),
				},
				{Path: "HostConfig.Privileged", Op: "equals", Values: []string{"true"}},
				{Path: "HostConfig.PidMode", Op: "equals", Values: []string{"host"}},
				{Path: "HostConfig.NetworkMode", Op: "equals", Values: []string{"host"}},
				{Path: "HostConfig.IpcMode", Op: "equals", Values: []string{"host"}},
				{Path: "HostConfig.UsernsMode", Op: "equals", Values: []string{"host"}},
				{
					Path: "HostConfig.CapAdd[*]", Op: "contains_any",
					Values: []string{
						"SYS_ADMIN", "SYS_PTRACE", "SYS_MODULE", "DAC_READ_SEARCH",
						"DAC_OVERRIDE", "SYS_RAWIO", "SYS_BOOT", "NET_ADMIN",
					},
				},
				{
					Path: "HostConfig.SecurityOpt[*]", Op: "contains_any",
					Values: []string{
						"apparmor=unconfined", "seccomp=unconfined",
						"apparmor:unconfined", "seccomp:unconfined",
						"systempaths=unconfined",
					},
				},
				{Path: "HostConfig.Devices[*]", Op: "present"},
				{Path: "HostConfig.DeviceCgroupRules[*]", Op: "present"},
				{Path: "HostConfig.VolumesFrom[*]", Op: "present"},
				{Path: "HostConfig.MaskedPaths", Op: "empty_array"},
				{Path: "HostConfig.ReadonlyPaths", Op: "empty_array"},
			},
			Decision: types.DecisionDeny,
			Message:  "container-escape risk: bind-mount or flag rejected (see loop gate policy)",
		},
		{
			AppliesTo:    "POST ^/containers/[^/]+/update$",
			ContentTypes: []string{"application/json"},
			MaxBodyBytes: 1048576,
			JSONChecks: []types.JSONCheck{
				{Path: "Privileged", Op: "equals", Values: []string{"true"}},
				{Path: "CapAdd[*]", Op: "contains_any", Values: []string{"SYS_ADMIN", "SYS_PTRACE", "SYS_MODULE"}},
			},
			Decision: types.DecisionDeny,
			Message:  "container update would escalate capabilities",
		},
	}
}
