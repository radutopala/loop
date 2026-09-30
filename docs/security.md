---
title: Security Model
---
Loop runs coding agents with `--dangerously-skip-permissions` inside Docker containers, next to a daemon that runs on your machine as you. This page describes what an agent can reach, what it can't, and which boundary holds each line. Loop's source is public, so none of it relies on an agent not knowing how Loop works.

**Related docs:** [Security Gate](gates.md) | [HTTP API](api.md#authentication) | [Containers](containers.md) | [Configuration](configuration.md#project-config-trust) | [Terminal](terminal.md)

---

## Who's who

| Principal | Runs as | Trusted with |
|---|---|---|
| **Owner** | You: the desktop app, the `loop` CLI, host tools | Everything. Holds the owner API token |
| **Agent** | One agent container: claude and the tools it starts, including Loop's MCP server | Its own project. Holds an agent API token scoped to its container and channel |
| **Container root** | The gate processes in an agent container (seccomp parent, docker proxy) | The container's gate token, for approval prompts |

Anything an agent can write is treated as agent-controlled, whoever wrote it first: the workspace, the project's `.loop/` directory, git metadata, playground files, memory files.

## Boundaries

### The daemon API

Every API request carries a token (see [Authentication](api.md#authentication)). There are no cookies and no trust by origin, so a web page you visit can't drive the daemon, and neither can an agent that reaches it through `host.docker.internal`.

- The **owner token** sits in the OS user config dir, outside `~/.loop`, and that directory is never mounted into a container or accepted as a channel dir.
- The same goes for `~/.loop/run`, which holds each container's gate policies and audit logs. A mount that contains either directory is skipped if it's writable. If it's read-only, it's kept and the directory inside it is covered with an empty read-only tmpfs, so `~/.loop:~/.loop:ro` still works. Containers an agent starts can't mount either directory or any folder above it.
- **Agent tokens** are issued per container, written to `/run/loop/api-token` readable by the agent user only, kept out of the container's environment and `docker inspect`, and revoked with the container.
- An agent token works only on the routes in-container clients call, and only for its own project's channels and dirs. Config, terminals (host shells included), gates, images and token rotation are owner-only.
- URLs the browser loads directly (playground iframes, image and PDF previews, HTML preview `<base href>`) use short-lived signed content links rather than the token. Playground pages are served sandboxed, with an opaque origin.

### Gate tokens and approvals

Each container's gate token authenticates its approval prompts. It's written to `/run/loop/gate-token`, readable by root only; the agent runs as the unprivileged `agent` user, and the token isn't in any environment. Approvals are cached per kind, so an approved prompt of one kind can't be reused for another. The docker proxy denies an agent's operations on another channel's agent containers.

### Files and git on the host

The daemon reads and writes files in directories agents can write to, and runs `git` in their repos. It treats both as hostile:

- File, raw-file, playground, shortcut and template paths resolve symlinks and must stay inside their root. Writes refuse symlinked targets.
- Host-side `git` runs with repo-configured executables switched off: fsmonitor, hooks, pagers, external diff, textconv, filter drivers, the `ext::` transport. `gh` gets the same settings.
- The gate asks before an agent writes `.git/config` or `.git/hooks/` in the workspace, since your own git would run what's planted there.

### Project config

The project `.loop/config.json` is in the workspace, so an agent can edit it. Its security-sensitive fields (mounts, extra dirs, gates, envs, copied files, permissions, bash shortcuts, host browser access and memory paths) only take effect once you've trusted them; until then Loop uses the last version you trusted. The record of what you trusted sits next to the owner token, out of the agents' reach. The global gate baseline's denies always come before project rules, and a project can't switch off a gate the global config enables. See [Project config trust](configuration.md#project-config-trust).

### The desktop app

- Chat, memory, notes and file previews render markdown through a sanitizer: no scripts, event handlers or `javascript:` links.
- The app window only navigates to the app itself. Other `http(s)` links open in your browser; every other scheme is refused.
- The agent's browser (CDP) only navigates to `http`, `https` and `about:blank`.
- The Browser panel won't open a signed-in Loop link (`#loop_token=` with this daemon's owner token), since the agent can read the token from the page. Links to other Loop daemons, such as a test one, still open. In Host mode the agent drives your own Chrome, where Loop can't see what you open, so don't keep a Loop web UI tab there.

## Out of scope

- **Directories you mount read-write.** A path you add to `mounts` or `extra_dirs` is yours to share; the agent can change what's in it. Inside the agent's container, the gate still denies credential directories and writes to Claude settings files. A container the agent starts can mount them too, where the gate's file rules don't apply, so mount credential folders like `~/.aws` or `~/.config/gh` only if agents need them, and read-only if you can.
- **`workflow_bash_local`.** With it on, workflow bash nodes run on the host, project workflows included, and a project workflow is as agent-writable as the rest of the project config. Agents can't start runs then, but a run you start executes what the workflow says. Leave it off unless Docker isn't available.
- **A multi-user web deployment.** The token model assumes one owner on one machine. Host shells are desktop-only.
