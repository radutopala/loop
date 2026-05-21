---
title: Security Gate
---
The security gate is a seccomp `RET_USER_NOTIF` filter plus a Docker HTTP proxy that sit between the agent process inside each container and the kernel / Docker daemon. It exists because agent containers run `claude` with `--dangerously-skip-permissions`: Claude's in-process tool approvals are disabled, so every `Bash`, `Edit`, `Write`, and `docker` call reaches the syscall layer with no human in the loop. The gate is the sole source of human-in-the-loop approval for anything touching the host.

The gate is **enabled by default** for new installs and on upgrades. Operators disable it explicitly with `gates.agentgate.enabled: false`.

This page is a single-page overview. Each subsystem has its own reference:

- **Config surface:** [Configuration: Security Gate](configuration.md#security-gate)
- **WebSocket events:** [Events: `gate.approval_requested` / `gate.approval_resolved`](events.md#gateapproval_requested)
- **Resolve endpoint:** [API: `POST /api/gate/approvals/{id}`](api.md#post-apigateapprovalsid)
- **Desktop UI card:** [Chat: Gate Approval Card](chat.md#gate-approval-card)
- **Host plumbing & env:** [Containers: Security Gate](containers.md#security-gate)

---

## Threat model under `--dangerously-skip-permissions`

`--dangerously-skip-permissions` disables every Claude-side tool prompt. Without the gate, a prompt injection in fetched content runs commands in the container with no brake. The gate closes the gap at the kernel boundary, where Claude's userspace flag has no meaning.

| Claude tool | Underlying action | Gate coverage |
|---|---|---|
| `Bash` | `execve` on every subprocess | Covered — seccomp notify + `CommandRule` + wrapper unwrap |
| `Edit` / `Write` / `NotebookEdit` | `openat(O_WRONLY\|O_CREAT\|O_TRUNC)` + `renameat2` | Covered — seccomp notify on 9-syscall file-op set + `FileRule` |
| `Read` / `Glob` / `Grep` / `NotebookRead` | `openat(O_RDONLY)` + `statx` + `readdir` | Covered — same machinery, default-allow with a credential denylist |
| `WebFetch` / `WebSearch` | TCP+TLS inside Claude's process | **Not covered in v1** — operator mitigation is a no-internet Docker network plus HTTPS egress proxy |
| `TodoWrite`, memory tools | No syscalls | Not coverable by any syscall gate |
| MCP tool calls (`mcp__loop__*`, etc.) | stdio to MCP servers in `loop-server` on the host | Out of scope — enforcement belongs in `loop-server`'s MCP exposure policy |

### Parent-kill / orphan-fd attack

The seccomp notify fd is held by the `loop syscallwrap` parent inside the container. If the agent could kill the parent, the kernel would close the fd and subsequent notifications on the filter would drop through unhandled — effectively allow-all for the trap set. Two layers close this:

1. **Different uids.** The parent keeps the uid that entrypoint invoked it as (root). The child is spawned with `SysProcAttr.Credential={uid, gid}` looked up from `$HOST_USER`, and `syscall.Exec`s claude from that uid. Under POSIX `kill(2)`, a non-root process cannot signal a process owned by a different uid — `EPERM`. The agent cannot signal the parent at all.
2. **`PR_SET_PDEATHSIG(SIGKILL)`.** Set by the parent in `SysProcAttr.Pdeathsig` and re-set by the child in `prctl(PR_SET_PDEATHSIG, SIGKILL)` before the filter install (belt-and-braces — the parent-set value is cleared by Linux on uid change, so the child re-sets it after the credential switch). If the parent dies by any means (panic, OOM, external SIGKILL from pid 1), the kernel delivers SIGKILL to the child, which transitively kills claude and every descendant via the process tree. The container exits instead of continuing with an ungated filter.

These two together mean the only way to end up with claude running past a dead parent is a kernel bug in one of `kill(2)` / pdeathsig / seccomp inheritance.

---

## Architecture

Both enforcement layers live **inside the container**. The host publishes per-container policy JSON files under `~/.loop/run/<CID>/` and bind-mounts them read-only; no host-side unix sockets are bind-mounted any more. Approval clicks round-trip back over HTTP, authenticated by a per-container bearer token.

```
                   host                              agent container
 ┌───────────────────────────────────┐     ┌───────────────────────────────┐
 │ loop-server                       │     │ entrypoint.sh (root)          │
 │                                   │     │   ├─ loop dockerproxy &       │
 │  ~/.loop/run/<CID>/               │     │   │   listens /var/run/docker │
 │    ├─ proxy-policy.json ──────────┼─────┼──▶     .sock (tmpfs)          │
 │    └─ gate-policy.json ───────────┼──bind┼───▶ /etc/loop/{proxy,gate}-  │
 │                                   │  :ro │         policy.json          │
 │  /var/run/docker.sock ────────────┼─bind─┼───▶ /var/run/docker.sock.host│
 │                                   │ (ro) │         (daemon, root-only)  │
 │                                   │     │   └─ exec loop syscallwrap -- │
 │                                   │     │      │  (still root)          │
 │                                   │     │      ├─ re-exec /proc/self/exe│
 │                                   │     │      │   ExtraFiles=[child]   │
 │                                   │     │      │   Credential=agent uid │
 │                                   │     │      │   Pdeathsig=SIGKILL    │
 │                                   │     │      │                        │
 │                                   │     │      │   child (agent user):  │
 │                                   │     │      │    install filter,     │
 │                                   │     │      │    send notify fd via  │
 │                                   │     │   ◀──┤◀─── SCM_RIGHTS         │
 │                                   │     │      │    syscall.Exec claude │
 │                                   │     │      │                        │
 │                                   │     │      └─ parent runs           │
 │                                   │     │         agentgate.Server loop │
 │                                   │     │         on the notify fd      │
 │                                   │     │                               │
 │  POST /api/gate/container-approval│     │      ▲                        │
 │    Authorization: Bearer <token>  │◀────┼──────┤ httpapprover.Approver  │
 │    ByToken → (cid, Manager,       │ HTTP│      │ (shared by proxy+gate) │
 │               channelID)          │     │      │                        │
 │    → mgr.Request → bot prompt     │     │      │                        │
 │                                   │     │      └─ claude (filtered)     │
 └───────────────────────────────────┘     └───────────────────────────────┘
```

Two enforcement layers, same approval backend:

1. **`internal/agentgate/` + `internal/syscallwrap/`** — kernel-level syscall gate. `entrypoint.sh` execs `loop syscallwrap` as root; the parent re-execs `/proc/self/exe` with `LOOP_SYSCALLWRAP_MODE=child`, drops the child to the agent uid, and hands the seccomp notify fd back over a socketpair. The parent then runs `agentgate.Server` in-container on that fd.
2. **`internal/dockerproxy/`** — application-level Docker HTTP proxy. `loop dockerproxy` listens on `/var/run/docker.sock` (tmpfs inside the container) and reverse-proxies to `/var/run/docker.sock.host` (the real daemon, bind-mounted read-only). Every byte is reparsed as HTTP, matched against `HTTPServiceRule` and `BodyRule`.

Both layers authenticate HTTP callbacks with the same per-container bearer token (`LOOP_GATE_TOKEN`, 32 random bytes hex-encoded) and target the same endpoint: `POST /api/gate/container-approval`. `MultiManagerResolver.ByToken` does a constant-time lookup that yields the per-container `Manager` and the channel id to prompt on.

Both layers feed into the same `agentgate.Manager` for `approve` decisions, so approval prompts, caching, and rate limits are unified.

---

## Components

| Package | Role |
|---|---|
| `internal/agentgate/bpf.go` | Hand-assembled BPF: arch check (x86_64 + aarch64, kill-on-mismatch), 12 trap syscalls (`connect`, `execve`, `execveat`, `openat`, `openat2`, `renameat2`, `unlinkat`, `linkat`, `symlinkat`, `fchmodat`, `fchownat`, `mkdirat`), 3 deny syscalls (`io_uring_setup` / `_enter` / `_register` → `EPERM`) |
| `internal/agentgate/server.go` | Seccomp notify loop; dispatches by syscall nr to connect / execve / file handlers. Runs in-container inside the `loop syscallwrap` parent process |
| `internal/agentgate/connect.go` | Parses `AF_UNIX` sockaddr; abstract sockets rendered as `@name` |
| `internal/agentgate/execve.go` | Reads argv via `ProcessVMReadv`; transparent-wrapper unwrap (`env`, `sudo`, `nice`, `ionice`, `chrt`, `timeout`, `nohup`, `unshare`, `setsid`, `taskset`, `stdbuf`, `script`, `ld-linux*`); `execveat`+`AT_EMPTY_PATH` resolves fd and denies `memfd:` unconditionally |
| `internal/agentgate/file.go`, `file_syscalls.go` | Per-syscall arg-layout dispatcher; dirfd resolution via `/proc/<pid>/fd/<N>`; `AT_FDCWD` via `/proc/<pid>/cwd`; mandatory `filepath.EvalSymlinks`; FIFO decision cache (1024 entries) |
| `internal/agentgate/handshake.go` | `ReceiveHandshake` / `SendAck` — `[4-byte BE length][channel-id][SCM_RIGHTS fd]`. Shared code path for the in-container socketpair handshake |
| `internal/agentgate/policy.go` | `PathRule` / `CommandRule` / `FileRule` compilation: doublestar for paths, `regexp` for argv, `filepath.Match` for command basenames |
| `internal/agentgate/approval.go` | `Manager` with session cache (populated only on `session` / `deny-session`), `checkLimits` for pending / per-minute / total rate caps, `Resolve(reqID, decision, actorID)` entrypoint, `Shutdown()` that resolves every in-flight request with `deny`/actor=`shutdown` so [`gate.approval_resolved`](events.md#gateapproval_resolved) fans out on teardown, and `ListPending()` returning a snapshot of in-flight requests for rehydration |
| `internal/agentgate/multi_resolver.go` | `MultiManagerResolver`: click routing by `reqID`, plus per-container bearer-token routing (`AddWithToken` / `ByToken`) for the in-container HTTP approval hop. `ByToken` compares in constant time via `crypto/subtle`. `Remove(containerID)` invokes the Manager's `Shutdown()` before unregistering so stale containers can't leave the FE card/dock-bouncer stuck. `ListPending()` aggregates across all live Managers and is what backs [`GET /api/gate/approvals`](api.md#get-apigateapprovals) |
| `internal/syscallwrap/app.go`, `parent.go`, `child.go` | In-container gate wrapper — the body of the `loop syscallwrap` subcommand. `app.go` dispatches parent vs child on `LOOP_SYSCALLWRAP_MODE`. Parent: load policy JSON → socketpair → re-exec `/proc/self/exe` with `ExtraFiles=[child-end]`, `Credential={agent uid,gid}`, `Pdeathsig=SIGKILL` → receive SCM_RIGHTS handshake → build `agentgate.Server` → run until child exits. Child (agent user): `LockOSThread` → install filter via `SECCOMP_SET_MODE_FILTER \| NEW_LISTENER \| TSYNC` → send notify fd over fd 3 → read ack → `syscall.Exec` target |
| `internal/dockerproxy/app.go` | In-container docker-proxy binary — the body of the `loop dockerproxy` subcommand. Loads the policy JSON, builds an `httpapprover.Approver`, listens on `/var/run/docker.sock` (tmpfs), runs `dockerproxy.Server` until SIGTERM |
| `internal/httpapprover/approver.go` | Shared HTTP-backed `Approver` used by both the in-container docker proxy and the seccomp-gate parent. POSTs `{kind, target, message, cache_key}` to `{API_URL}/api/gate/container-approval` with `Authorization: Bearer <LOOP_GATE_TOKEN>`; fail-closed on any transport or non-200 error |
| `internal/container/image/entrypoint.sh` | Branches on `$LOOP_DOCKERPROXY_ENABLED=1` (starts `loop dockerproxy` as root before dropping privileges) and `$LOOP_GATE_ENABLED=1` (execs `loop syscallwrap -- "$@"` as root, which drops its child to `$AGENT_USER`). When neither is set, falls back to plain `gosu "$AGENT_USER" "$@"` |
| `internal/dockerproxy/server.go` | HTTP handler + reverse proxy to the upstream socket (`/var/run/docker.sock.host` in production); hijack on `POST /containers/*/attach` and `POST /exec/*/start`; `FlushInterval: 100ms` for streaming; strips API version prefix before rule match |
| `internal/dockerproxy/policy.go` | `HTTPServiceRule` compile + `MatchHTTP` (first-match + default); `CheckBody` |
| `internal/dockerproxy/bodyrule.go` | JSONPath-lite evaluator: `present`, `empty_array`, `equals`, `contains_any`, `starts_with_any`, `source_path_in` |
| `internal/orchestrator/gate_adapter.go` | Maps `agentgate.ApprovalRequest` to `bot.ApprovalPrompt`; same adapter for all three platforms |
| `internal/api/gate_handler.go` | Three handlers: `GET /api/gate/approvals` (snapshot of every Manager's pending requests — used by the renderer on WS reconnect to reconcile its local map and the electron dock-bouncer), `POST /api/gate/approvals/{id}` (bot-click resolve — 204 / 400 / 404), and `POST /api/gate/container-approval` (in-container call-in — bearer-token auth → `ByToken` → `mgr.Request` → `{decision, actor, reason}`, 200 / 401 / 503) |
| `internal/types/gate.go` | Shared rule types (`Decision`, `PathRule`, `CommandRule`, `FileRule`, `HTTPServiceRule`, `BodyRule`, `JSONCheck`, `RateLimits`, `AuditConfig`) — leaf package to avoid config↔agentgate cycles |
| `internal/config/defaults_gate.go` | Baseline rule factories: `DefaultGatePathRules` / `CommandRules` / `FileRules` / `DefaultDockerProxyHTTPRules` / `BodyRules` — each returns a fresh slice per call |

---

## Default policy

All defaults ship from code (`internal/config/defaults_gate.go`), not from an example JSON. The example templates at `internal/config/config.*.example.json` document the surface but are commented out so new installs pick up the baseline automatically.

Decision semantics:

- `deny` — hard reject, no prompt. Agent sees `EACCES` / 403.
- `approve` — emits an approval prompt; cached per `CacheKey` once the operator picks **Allow for session**.
- `allow` — silent pass-through.

The default posture is **default-allow with targeted exceptions**: the agent can work freely in its workspace, read standard system paths, and talk to the Docker daemon; only credential-adjacent writes, container-escape shapes, and specific lateral-movement operations block or prompt. Fall-through hits `default_decision`, which is `allow` for both `agentgate` and `docker_proxy`.

### Connect (`PathRule`, 2 rules, first-match-wins)

Covers `connect(2)` on `AF_UNIX` paths.

| # | Pattern | Decision | Why |
|---|---|---|---|
| 1 | `/var/run/docker.sock.host` | `deny` | The real host daemon socket, bind-mounted here so only the in-container Docker proxy can dial it. Agents must go through the proxied `/var/run/docker.sock` |
| 2 | `/var/run/docker.sock` | `allow` | Proxied socket — every HTTP request is re-gated at the Docker-HTTP layer below, so a prompt here would be duplicate noise |

### Exec (`CommandRule`, 1 static rule + 1 dynamic workspace rule, first-match-wins)

Seccomp catches `execve` / `execveat`; transparent wrappers (`env`, `sudo`, `nice`, `timeout`, …) are unwrapped so the rule matches the real target.

| # | Command | Args regex | Decision | Why |
|---|---|---|---|---|
| 0 | *(injected)* `rm` | every positional arg must lie under `{workDir}` (or `{parentDirPath}`) — flag-only prefix `^(-[a-zA-Z]+\s+)*` followed by an alternation of workspace path prefixes | `allow` | Workspace fast-path. Inserted per-container by `injectWorkspaceRmRfRule` (`internal/container/runner.go:1583`). Agents routinely `rm -rf build/` or `rm -rf dist/` inside their own tree; without this carve-out rule #1 would block legitimate cleanup. The pattern is **all-args-or-nothing** — a mixed `rm -rf /workspace/build /etc/passwd` falls through to rule #1 and is denied. Omitted when `workDir` is empty (ad-hoc one-shot runs). |
| 1 | `rm` | `-[a-zA-Z]*r[fF]?.* /.*` | `deny` | `rm -rf` on an absolute path outside the workspace — unconditional blast-radius block. The `/tmp/` allow is injected before this for the same reason as the workspace carve-out (test cleanup like `rm -rf /tmp/testgit`). |

Git write-side operations (`push`, `commit`, `reset --hard`, …) are intentionally not gated by default; add an approve rule if you want prompts.

**Example — gate `git commit` and `git push`.** Drop this into `~/.loop/config.json` for a global rule that applies to every project, or into `{project}/.loop/config.json` for just one repo — the schema is identical and project rules are prepended to global (first-match-wins).

```jsonc
{
  "gates": {
    "agentgate": {
      "command_rules": [
        {
          "commands": ["git"],
          "args_patterns": ["^(commit|push)(\\s|$)"],
          "decision": "approve",
          "message": "git commit/push (approval required)"
        }
      ]
    }
  }
}
```

The args regex runs against `strings.Join(argv[1:], " ")`, so `\s` covers the space before the next arg *and* embedded newlines in multi-line commit messages; the `$` alternation handles bare `git push` with no trailing args.

### File ops (`FileRule`, 9 rules, first-match-wins, plus one dynamic workspace rule)

Order matters: denies first, then the dynamically injected workspace rule, then the tmp / system-read fast-paths.

| # | Paths | Operations | Decision | Why |
|---|---|---|---|---|
| 1 | `/proc/*/mem`, `/proc/kcore` | read | `deny` | Kernel / process-memory exfiltration. `/proc/*/environ` is intentionally NOT denied — Go test binaries, runtime probes, and tooling open it routinely (chronic noise) and the gate parent's env carries no exploitable secret (the notify fd is passed via SCM_RIGHTS, not authenticated by an env-readable token) |
| 2 | `/etc/shadow`, `/etc/gshadow`, `/etc/sudoers`, `/etc/sudoers.d/**`, `/etc/ssh/ssh_host_*_key`, `.pub` variants | all ops | `deny` | Root credential files |
| 3 | `**/.ssh/**`, `**/.aws/**`, `**/.gcp/**`, `**/.config/gcloud/**`, `**/.kube/**`, `**/.netrc`, `**/.pgpass` | read, write, create, delete, chmod | `deny` | User credential directories — apply to any path, including inside the workspace |
| 4 | `.docker/config.json` and `.npmrc` under `/root/`, `/home/*/`, `/Users/*/` | write, create, delete, chmod | `deny` | Registry/proxy credential files — reads stay allowed because the docker CLI and npm read them on every invocation (missing file would surface as a confusing EPERM warning). Scoped to real home-dir layouts rather than `**/`: a filename-anywhere glob caught nodeenv's bundled `.npmrc` template inside `~/.cache/pre-commit/`, breaking pre-commit hook installs |
| 5 | `**/.claude/settings.json`, `settings.local.json` | write, create, delete, chmod | `deny` | Claude harness settings. The rule is narrow on purpose — `CLAUDE.md`, `mcp*.json`, `plugins/**`, and the rest of `~/.claude` are tree the agent legitimately writes (memory updates, per-project MCP configs, plugins state, ephemeral harness session/todos/snapshot dirs) |
| 6 | `/root/.bashrc` (and `.bash_profile`, `.zshrc`, `.zprofile`, `.profile`, `.bash_login`, `.inputrc`); same set under `/home/*/` and `/Users/*/` | write, create, delete, chmod | `deny` | Shell rcfile write — persistence vector. Scoped to real home-dir layouts (root, Linux `/home/<user>`, macOS host-home bind-mount `/Users/<user>`) so test fixtures writing a `.bashrc` inside a t.TempDir() don't trip |
| 7 | `/etc/**`, `/usr/**`, `/bin/**`, `/sbin/**`, `/lib/**`, `/lib64/**`, `/boot/**` | write, create, delete, chmod, chown | `deny` | System paths — writes would mutate the container image outside the workspace |
| 8 | *(injected)* `{workDir}/**`, `{parentDirPath}/**` | all ops | `allow` | Workspace fast-path. Inserted per-container by `writeGatePolicyFile` using the real host bind-mount path for that channel / thread. Positioned after all Deny rules so cred-path denies still win inside the workspace |
| 9 | `/tmp/**`, `/var/tmp/**` | all ops | `allow` | OS tmp fast-path |
| 10 | `/proc/**`, `/sys/**`, `/dev/null`, `/dev/zero`, `/dev/urandom`, `/dev/random`, `/dev/tty`, `/dev/pts/**` | read, stat, list | `allow` | System reads fast-path — reads are silent, writes to these paths fall through to `default_decision` |

Anything that doesn't match falls through to `gates.agentgate.default_decision` (`"allow"` by default).

### Docker HTTP (`HTTPServiceRule`, 3 rules + default `allow`)

The default posture is `allow`. The body rules below are the real container-escape guardrails — given that, the agent can run Docker freely (builds, tests, `docker run`, `docker logs`, attach, wait, …) without per-call prompts. This table only enumerates the exceptions:

| # | Methods | Paths | Decision | Why |
|---|---|---|---|---|
| 1 | `POST` | `^/containers/[^/]+/exec$`, `^/exec/[^/]+/start$` | `approve` | Lateral movement — exec / attach-start into an arbitrary container. The agent owns the containers it creates, but the proxy cannot distinguish "my lint container" from "the user's local postgres" given only a container id |
| 2 | `PUT`, `GET`, `HEAD` | `^/containers/[^/]+/archive$` | `approve` | `docker cp` into / out of a container — same lateral-movement concern as exec |
| 3 | `*` | `^/swarm/`, `^/nodes/`, `^/secrets/`, `^/configs/`, `^/plugins/` | `deny` | Swarm / secrets / plugin APIs — off-limits surfaces with no legitimate dev-loop use |

All other Docker API calls fall through to `allow`: `_ping`, `version`, `info`, container create / start / stop / kill / restart / pause / wait / attach, image create / build / push / pull / rm, volume / network CRUD, events, logs, inspect, stats, top, and so on.

### Docker body (`BodyRule`, 2 rules — the container-escape guardrails)

Body rules are evaluated independently of the HTTP rule match and support all three decisions:

- `deny` — returns `403 Forbidden` with no prompt; always wins over the `HTTPServiceRule` decision.
- `approve` — blocks the request and prompts the operator (same approval flow as an HTTP-rule approve), keyed on `docker:<METHOD>:body:<rule_id>` so "Allow for session" caches per body rule rather than per request path.
- `allow` — passes through silently to the upstream daemon (still subject to the `HTTPServiceRule` decision falling through normally).

Bodies larger than `MaxBodyBytes` (1 MiB default) skip body inspection and fall through to the `HTTPServiceRule` decision.

**`POST /containers/create`** — denies any of:

| JSON path | Op | Values | What it blocks |
|---|---|---|---|
| `HostConfig.Binds[*]` | `source_path_in` | `^/$`, `^/etc(/\|$)`, `^/root`, `^/home`, `^/boot`, `^/usr`, `^/lib(64)?`, `^/proc`, `^/sys`, `^/dev`, `^/var/run/docker\.sock$`, `^/run/loop/` | Host bind-mount of sensitive paths (escape via file write) |
| `HostConfig.Mounts[*].Source` | `source_path_in` | same set as above | Same, via the newer long-form `Mounts` API used by docker-compose v2 and `docker run --mount` |
| `HostConfig.Privileged` | `equals` | `true` | Privileged container |
| `HostConfig.PidMode` | `equals` | `host` | Host PID namespace (visibility into host processes) |
| `HostConfig.NetworkMode` | `equals` | `host` | Host network namespace |
| `HostConfig.IpcMode` | `equals` | `host` | Host IPC namespace |
| `HostConfig.UsernsMode` | `equals` | `host` | Disable user-namespace remapping |
| `HostConfig.CapAdd[*]` | `contains_any` | `SYS_ADMIN`, `SYS_PTRACE`, `SYS_MODULE`, `DAC_READ_SEARCH`, `DAC_OVERRIDE`, `SYS_RAWIO`, `SYS_BOOT`, `NET_ADMIN` | Capability escalation — each is an independent escape path |
| `HostConfig.SecurityOpt[*]` | `contains_any` | `apparmor=unconfined`, `seccomp=unconfined`, `:unconfined` variants, `systempaths=unconfined` | Dropping the container sandbox |
| `HostConfig.Devices[*]` | `present` | — | Device passthrough |
| `HostConfig.DeviceCgroupRules[*]` | `present` | — | Cgroup device allowlist bypass |
| `HostConfig.VolumesFrom[*]` | `present` | — | Inherit another container's mounts |
| `HostConfig.MaskedPaths` | `empty_array` | — | Explicit `[]` un-masks kernel files (`/proc/kcore` etc.) |
| `HostConfig.ReadonlyPaths` | `empty_array` | — | Explicit `[]` makes kernel paths writable |

**`POST /containers/{id}/update`** — denies:

| JSON path | Op | Values |
|---|---|---|
| `Privileged` | `equals` | `true` |
| `CapAdd[*]` | `contains_any` | `SYS_ADMIN`, `SYS_PTRACE`, `SYS_MODULE` |

Together these body rules are the reason the HTTP layer can afford to default-allow: no amount of `docker run` or `docker create` can produce a privileged container, a host-namespace container, or a host-bind-mounted container.

---

## Approval UI

Approve decisions cross three platforms through the same `orchestrator.Bot.ApprovalPrompt` interface. Each prompt shows three buttons:

- **Allow once** — default action; allows this single call, nothing cached.
- **Allow for session** — caches on `CacheKey` so the same operation skips future prompts for the life of the container.
- **Deny** — blocks this call (subsequent identical calls prompt again).

Discord renders an `ActionsRow` with three buttons; Slack renders a `NewActionBlock`; the desktop renders the `ApprovalCard` component via the [`gate.approval_requested` WebSocket event](events.md#gateapproval_requested) and resolves via [`POST /api/gate/approvals/{id}`](api.md#post-apigateapprovalsid). See [Chat: Gate Approval Card](chat.md#gate-approval-card) for the desktop rendering.

**Cache key scheme:**

- `connect:<absolute-socket-path>` — one allow-for-session covers every `docker ps` / `docker logs` / `docker exec` round.
- `execve:<basename>:<first-2-argv-tokens>` — "Allow git push for session" doesn't also cover `git config --global`.
- `docker:<METHOD>:<normalized-path>` — container IDs and hex hashes collapse to `*` so repeat `docker rm <id>` doesn't re-prompt after session approval.

**Scope:** cache lives inside the per-container `Manager`. Container dies → cache dies. No cross-session persistence in v1.

**Teardown:** when the container goes away the runner calls `gateResolver.Remove(containerID)`, which invokes `Manager.Shutdown()` before unregistering. `Shutdown()` walks every still-pending request, resolves each one as `deny`/actor=`shutdown`, and lets the normal resolve path broadcast [`gate.approval_resolved`](events.md#gateapproval_resolved) on every channel that had a card up. The renderer and the electron dock-bouncer both clear their pending entries from that event — no orphan cards, no orphan bounces.

**WS-reconnect rehydration:** the renderer doesn't trust that it saw every event. On every WebSocket `onOpen` (page reload, network blip, daemon restart) it `GET`s [`/api/gate/approvals`](api.md#get-apigateapprovals) — a snapshot built from `MultiManagerResolver.ListPending()` — and reconciles three places against it:

1. The chat-state `gateApprovals` map: locally-known entries missing from the snapshot are synthesized as resolved (decision=`deny`, actor=`rehydrate`); snapshot entries the renderer didn't know about are synthesized as requested. Both go through the same `applyEvent`/listener path as a real WS event, so the UI converges without a special-case code path.
2. The electron-main dock-bouncer's `pendingApprovals` set: the renderer hands the full snapshot id list back over the `reconcile-approvals` IPC; any bouncer entry not in the snapshot is dropped, and the bounce loop is stopped if the set ends up empty.
3. Per-channel running-state derived from the union of (a) and (b).

The net result: a missed `gate.approval_resolved` (deny-by-timeout that fired while the renderer was disconnected, container teardown during a network drop, etc.) self-heals on the next reconnect, and any pending entry that *is* still live is reattached to its card. This is the only path that can fix state desync today — there is no per-event resend.

**Rate limits (`gates.rate_limits`, per container, shared across both gate layers):** `pending: 30`, `per_minute: 60`, `total: 500`. Tripping a cap returns `Outcome{RateLimited: true}` to the requester (which surfaces as a deny at the syscall); the agent sees `EACCES` on the blocked call.

### Body details surfaced in the prompt

For Docker-HTTP approvals, the proxy decodes the request body and attaches a small `Details` map to `ApprovalRequest` (`internal/dockerproxy/details.go`) so the operator sees what is actually being asked for, not just `POST /containers/abc/exec`. The card renders the keys sorted; long values are truncated with `…`. Only fields meaningful to a human-in-the-loop decision are extracted:

| Endpoint | Keys |
|---|---|
| `POST /containers/create` | `image`, `cmd`, `entrypoint`, `user`, `working_dir`, `binds`, `privileged`, `network_mode`, `pid_mode`, `ipc_mode`, `userns_mode`, `cap_add`, `devices`, `security_opt` |
| `POST /containers/{id}/exec` | `cmd`, `user`, `privileged`, `attach_stdin`, `tty` |
| `POST /networks/create` | `name`, `driver`, `internal`, `attachable` |
| `POST /volumes/create` | `name`, `driver` |

Default-valued / absent fields are omitted (e.g. `Privileged: false` → no `privileged` key, `NetworkMode: "default"` → no `network_mode` key) so the prompt only highlights non-default values. `POST /exec/{id}/start` carries no useful body, so its prompt falls back to `Target` only — the relevant detail (the `Cmd`) was approved at the preceding `POST /containers/{id}/exec`. Bodies larger than `MaxBodyBytes` (1 MiB) skip detail extraction along with body-rule evaluation.

The same map flows through `agentgate.ApprovalRequest.Details` to every renderer: the desktop `ApprovalCard`, Discord embeds, and Slack section blocks (see [Chat: Gate Approval Card](chat.md#gate-approval-card)).

---

## Runtime wiring

Set when `cfg.Gates.Agentgate.Enabled` is true (`cmd/loop/serve.go`):

1. `gateResolver := agentgate.NewMultiManagerResolver()` — one resolver for the whole server. Same struct serves two roles: `Resolve(reqID, decision, actorID)` for bot clicks and `ByToken(token)` for in-container HTTP call-ins.
2. `policyDir := filepath.Join(cfg.LoopDir, "run")` + `os.MkdirAll(policyDir, 0o750)` — per-container policy JSON files live here. Lives under the user's home rather than `/run/loop` because macOS `/run` is on the read-only system volume; the same path works on Linux so there's one code path for both OSes.
3. `runner.SetGateDeps(gateResolver, &orchestrator.GateBotRouter{Bot: chatBot}, cfg.Gates.RateLimits)` — runner builds an `agentgate.Manager` per spawn and registers it with the resolver via `AddWithToken(cid, token, mgr, channelID)`.
4. `runner.SetDockerProxyDeps(policyDir, "")` — plumbs the same `policyDir` through to the proxy-policy writer; second arg is the host docker socket (`""` → `/var/run/docker.sock`).
5. `a.wireGatePolicy(cfg, runner, policyDir, logger)` — calls `agentgate.CompilePolicy` once at startup so a broken user config fails fast, then `runner.SetGatePolicy(policy, policyDir)`. The runner re-serialises the policy to JSON per spawn; `loop syscallwrap` parent recompiles it in-container.
6. `apiSrv.SetApprovalResolver(gateResolver)` — wires `POST /api/gate/approvals/{id}` (bot-click resolve) to the same resolver.

Per container (`internal/container/runner.go#createAndStartContainer`):

1. Generate a 32-byte `crypto/rand` bearer token (`newGateToken`) — shared by the proxy and gate layers.
2. `writeProxyPolicyFile` marshals `cfg.Gates.DockerProxy` to `{policyDir}/<channel>/proxy-policy.json` (0640). Bind-mount `hostSock:/var/run/docker.sock.host:ro` + `.../proxy-policy.json:/etc/loop/proxy-policy.json:ro`. Env: `LOOP_DOCKERPROXY_ENABLED=1`, `LOOP_DOCKERPROXY_POLICY_FILE=/etc/loop/proxy-policy.json`, `LOOP_DOCKERPROXY_UPSTREAM=/var/run/docker.sock.host`.
3. `writeGatePolicyFile` marshals the gate rule subset (`DefaultDecision`, `PathRules`, `CommandRules`, `FileRules`) to `{policyDir}/<channel>/gate-policy.json` (0640). Bind-mount `.../gate-policy.json:/etc/loop/gate-policy.json:ro`. Env: `LOOP_GATE_ENABLED=1`, `LOOP_GATE_POLICY_FILE=/etc/loop/gate-policy.json`.
4. Shared env on both layers: `LOOP_CHANNEL_ID=<channelID>`, `LOOP_GATE_TOKEN=<32-hex>`.
5. After `ContainerCreate` returns a `containerID`, call `gateResolver.AddWithToken(containerID, token, mgr, channelID)` — the token starts authenticating HTTP calls as soon as the container is up.
6. On container remove: `gateResolver.Remove(containerID)` — frees the token and Manager. Policy files under `{policyDir}/<channel>/` are left on disk (overwritten next spawn for the same channel — the payload is derived from global config + the channel's stable workDir, so overwrites are idempotent).

The in-container side (see `internal/container/image/entrypoint.sh`):

1. When `LOOP_DOCKERPROXY_ENABLED=1` and `/usr/local/bin/loop` is executable, entrypoint forks `loop dockerproxy &` as root, then waits up to 2s for `/var/run/docker.sock` to appear. The proxy stamps `filepath.EvalSymlinks` onto every `source_path_in` body-rule check via `Policy.SetSymlinkResolver`, so an agent that creates `/workdir/link → /` then submits `docker run -v /workdir/link:/host` is matched against the resolved `/` rather than the literal source. Resolve failures fire deny rules (suspect path → block); allow/approve rules with `source_path_in` are not auto-fired on failure. Named-volume sources (the `<name>:<target>` short syntax in `HostConfig.Binds[*]`, where the source has no leading `/`) are not host paths, so the resolver is skipped and the deny does not fire — `docker run -v loop-cache:/cache` and compose v2 named volumes pass through cleanly.
2. The existing docker-sock group logic adds `$AGENT_USER` to the GID that owns `/var/run/docker.sock` — whether that's the real host socket (legacy direct-mount) or the in-container proxy socket.
3. When `LOOP_GATE_ENABLED=1`, entrypoint `exec`s `/usr/local/bin/loop syscallwrap -- "$@"` **as root** (no `gosu` wrapper). Otherwise it falls back to `exec gosu "$AGENT_USER" "$@"`.
4. `loop syscallwrap` parent (root): loads the gate policy from `LOOP_GATE_POLICY_FILE`, builds an `httpapprover.Approver` against `$API_URL` + `$LOOP_GATE_TOKEN`, creates a `socketpair(AF_UNIX, SOCK_STREAM, 0)`, re-execs `/proc/self/exe` with `LOOP_SYSCALLWRAP_MODE=child`, `ExtraFiles=[child-end]`, `SysProcAttr.Credential={uid, gid}` (looked up from `$HOST_USER`), and `SysProcAttr.Pdeathsig=SIGKILL`. Receives the SCM_RIGHTS handshake on the parent-end, acks, and runs `agentgate.Server` on the notify fd.
5. `loop syscallwrap` child (agent user): `runtime.LockOSThread` → `prctl(PR_SET_PDEATHSIG, SIGKILL)` (belt-and-braces on top of the parent-set value) → install the seccomp filter with `SECCOMP_SET_MODE_FILTER | NEW_LISTENER | TSYNC` → send the notify fd + `$LOOP_CHANNEL_ID` over fd 3 via SCM_RIGHTS → read the 1-byte ack → `syscall.Exec` the target command (claude). The filter carries into claude and every descendant by kernel inheritance.

No notify fd ever crosses the container boundary — this is what lets the gate work on macOS. Docker Desktop's virtiofs cannot expose host-created unix sockets as sockets inside the Linux VM, but it has no issue with the forwarded `/var/run/docker.sock` or with bind-mounting JSON policy files read-only.

### Terminal (docker-exec) mode

Stream-mode claude is spawned as PID 1 by `entrypoint.sh`, so the gate hooks the process at its one entry point. The terminal-mode path is different: the UI `docker exec`s into a long-lived shell container and types a `claude …` line into the shell's stdin. `docker exec` uses `setns(2)` to join the container's namespaces, but seccomp is a per-process attribute — the exec'd shell does **not** inherit the filter the container's PID 1 installed. A naive `claude` invocation would run ungated.

The fix is at `BuildInteractiveClaudeCmd` (`internal/container/runner.go`): when `cfg.Gates.Agentgate.Enabled`, the command is prefixed with `loop syscallwrap --`. The shell's exec then re-enters the same parent/child dance as the stream-mode path — install filter, hand notify fd to an in-exec parent, run `agentgate.Server` until claude exits.

One twist: the exec'd shell is already running as the agent uid, not root. The parent detects this (`a.getuid() == lookupUser.uid`) and passes the sentinel `(-1, -1)` to `startChild`; `childSysProcAttr` then omits the `Credential` so the fork inherits the current uid instead of attempting a setuid it can't perform. The trade-off is that the gate-parent in terminal mode runs at the same uid as claude — so the "agent can't signal its parent" property weakens. `pdeathsig=SIGKILL` still couples parent-death → child-death, so the agent cannot kill the parent and keep itself alive on the now-orphan notify fd; the worst it can do is cause its own gate-parent (and itself) to exit together.

---

## Project config merge — full rule-authoring surface

Project `.loop/config.json` has the same rule-authoring capability as global — it can prepend rules with any decision (`allow` / `deny` / `approve`) so a project can punch a surgical hole in a baseline deny (e.g. allow `/var/run/docker.sock` as a bind source) without turning a whole layer off. Enabled flag and default decision stay narrow to preserve the global kill-switch:

| Field | Merge rule |
|---|---|
| `gates.agentgate.enabled` | Project can flip `true → false`; cannot flip `false → true` (global kill-switch wins) |
| `gates.agentgate.path_rules` / `command_rules` / `file_rules` | Prepended to global rules (first-match-wins applies project rules first); any decision permitted |
| `gates.agentgate.default_decision` | Ignored — global wins |
| `gates.docker_proxy.enabled` | Same narrow rule as `gates.agentgate.enabled` |
| `gates.docker_proxy.http_rules` / `body_rules` | Prepended to global rules; any decision permitted |
| `gates.docker_proxy.default_decision` | Ignored — global wins |
| `gates.rate_limits` / `gates.audit` | Ignored — global wins |

Trust model: project config lives in the same repo as the agent workspace and is authored by the same operator who authored global config — there is no anonymous-contributor surface to defend against at this layer. See [Configuration: Project Config](configuration.md#project-config) for the full merge table.

---

## Known gaps

Gaps operators should know about — what's enforced vs what's aspirational:

1. **No host-side audit aggregation / shipping.** `FileAuditor` writes rotating `agentgate-YYYY-MM-DD.jsonl` records into `{policyDir}/<channel>/audit/` (bind-mounted into the container at `/var/log/loop-gate`); the dir is keyed by channel/thread — not per container — so restarts of the same channel accumulate one continuous history rather than fragmenting into a new dir per spawn. Channel is the primary key so all per-channel artifacts (policy file, audit log, future per-channel state) live under one tree. `gates.audit.retention_days` is honoured via `LOOP_GATE_AUDIT_RETENTION_DAYS`. `gates.audit.verbose` (default `false`) controls log verbosity via `LOOP_GATE_AUDIT_VERBOSE=1`: when unset, `FileAuditor.Write` drops entries whose `decision=="allow"` AND `prompted_who==""` — i.e. silent policy-allow matches and cache-hit allows — so the default trail focuses on every deny plus every user-clicked decision. Set `verbose=true` when debugging rule authoring or exporting a full trace. Each record carries `{ts, channel, pid, kind, target, rule_id, decision, event, prompted_who, latency_ns}` — `pid` is the requesting process so operators can attribute spam (e.g. `grep '"pid":7'` to find the gate's own process). File-op cache hits do not emit audit lines (only the first-miss decision is recorded) so directory walks over allow paths don't drown the log. The `event` field distinguishes two flavors of record: it is omitted on the standard *decision* record (existing shape, written after the gate resolves), and set to `"request"` on a *pre-decision* record emitted the moment a prompt is dispatched to the user surface. Crucially, a `request` record is written only when a real prompt fires — `Manager.Request` short-circuits cache hits and rate-limit denies *before* invoking the prompt callback, so neither produces a `request` entry; the audit log therefore reflects only approvals that actually reached a human. A `request` record carries the same `{ts, channel, pid, kind, target, rule_id}` context but with `decision`, `prompted_who`, and `latency_ns` all zero — pair it with the matching decision record (same `kind`+`target`+`rule_id`) to reconstruct the prompt's wait time. The pre-decision record falls outside the silent-allow filter (its `decision` is empty) so it survives non-verbose mode by design. Pre-decision records are emitted by the connect / file / execve handlers and by the dockerproxy `runApprovalFlow` (both the HTTP-rule and body-rule branches). The desktop app ships a singleton **Audit** panel (see [Layouts: Panel Types](layouts.md#panel-types)) with a file list on the left and a live `tail -f -n 100` xterm on the right (run via `docker exec` against `/var/log/loop-gate/agentgate-<date>.jsonl`); the same files are also exposed over HTTP for tooling — `GET /api/channels/{id}/audit` (list) and `DELETE /api/channels/{id}/audit/{date}` (unlink); content is read live via the `tail -f` terminal, not via HTTP. There is still no SIEM-side fan-out or unified cross-channel log; the per-channel files remain the source of truth.
2. **No performance benchmark test.** There is no `perf_bench_test.go` pinning P50 / P99 latency over a 10K-file `find` pass.
3. **No end-to-end integration test.** Per-component `_linux_test.go` files exist but there's no `integration_linux_test.go` driving the full install → trap → handler → policy → `EACCES` loop against a real kernel.
4. **No first-run banner.** A one-shot "Loop gate is active" notification per channel per install is not implemented.
5. **No rate-limit-tripped user notification.** When `checkLimits` denies, the caller sees the deny; no single-shot Discord/Slack/Local notification is posted to the operator.
6. **File-op decision cache is FIFO, not LRU.** Functionally equivalent under the workload; the code comment acknowledges the simplification.

---

## v1 non-goals (intentional)

These are deferred by design, not missing work:

- **TCP/DNS egress** — `WebFetch` / `WebSearch` are not gated. Operator mitigation: Docker network with no internet route + HTTPS egress proxy with URL allowlist. eBPF `cgroup_connect` is the v2 plan.
- **Multi-threaded argv / path TOCTOU** — a sibling thread can mutate argv/path between our `ProcessVMReadv` read and the kernel's re-dereference. Single-threaded callers (Claude's `Bash` tool per invocation) are unaffected; exploit requires the agent to write+compile+run a multi-threaded program.
- **Policy hot-reload** — rule changes require a container restart (the policy JSON is read once when `loop syscallwrap` / `loop dockerproxy` starts).
- **Docker daemon-level authorization plugin** — out of scope; the HTTP proxy is per-container.
- **Podman / nerdctl / docker-over-TCP** — only unix-socket Docker is in scope.
- **Image content trust** — `docker pull evil/image` runs; only the request is inspected, not the image runtime behaviour.
- **LLM-response intercept for MCP tool-call allowlisting** — MCP servers run in `loop-server` on the host; gate scope is the container only.
