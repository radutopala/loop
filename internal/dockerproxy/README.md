# dockerproxy

Per-container HTTP reverse proxy that sits between the agent's
`/var/run/docker.sock` and the real Docker daemon socket. The proxy
runs **inside the agent container** as the `loop dockerproxy`
subcommand: it listens on `/var/run/docker.sock` (tmpfs inside the
container) and reverse-proxies to `/var/run/docker.sock.host`, which
is the real daemon socket bind-mounted read-only from the host.
Every byte the agent writes is reparsed as HTTP here before reaching
the daemon.

The package exposes both the reusable library pieces (`Server`,
`Policy`, `CompilePolicy`, `HTTPServiceRule`, `BodyRule`, `Approver`)
and the subcommand entry point (`Run` in `app.go`) that loads the
policy JSON, wires an `httpapprover.Approver`, and runs `Server`
until SIGTERM.

Enforces `HTTPServiceRule` (per method + path regex) and `BodyRule`
(per JSONPath-lite assertion over container create/update and volume
create bodies — denies bind-mounts of system paths, `--privileged`,
host-namespace flags, capabilities outside an allowlist, etc.). Body
keys match case-insensitively, as the daemon decodes them; bodies
holding two case variants of one key are rejected. Approve decisions
round-trip back to loop-server over HTTP (`POST
/api/gate/container-approval`) authenticated by the per-container
bearer token; deny decisions are terminal (no prompt).

Two rewrites of `POST /containers/create` bodies run alongside the
rules:

- `nested.go` — docker socket binds become a mount of the proxy's
  second socket, which it serves from an anonymous volume at
  `LOOP_DOCKERPROXY_NESTED_DIR` (`/run/loop-dproxy`). The daemon
  resolves bind sources on its own filesystem, where
  `/var/run/docker.sock` is the real engine socket, so containers the
  agent starts would otherwise get unfiltered daemon access. The same
  pass keeps the workspace's `.loop` dirs
  (`LOOP_DOCKERPROXY_READONLY_DIRS`) read-only under any bind. Runs
  before the body rules.
- `hostmnt.go` — Docker Desktop `/host_mnt/<path>` bind sources under
  `LOOP_DOCKERPROXY_BIND_ROOTS` (or their host paths) are mapped back to
  the agent's path first, so the rules and pinning see what the agent
  sees. Other `/host_mnt` sources are left for the baseline deny.
- `pin.go` — after the rules pass, binds under the agent's read-write
  directory mounts (`LOOP_DOCKERPROXY_BIND_ROOTS`) become mounts of a
  `loop-bind-*` named volume bound to that mount, with the rest of the
  path as `VolumeOptions.Subpath`, so a symlink swapped in between
  create and start can't redirect them. An existing volume must be bound
  to the mount, or to its resolved host path
  (`LOOP_DOCKERPROXY_BIND_HOST_PATHS`), which Docker Desktop reports.

Both use `Subpath`, so a request they rewrite needs Docker API 1.45+. See
[docs/gates.md](../../docs/gates.md#nested-docker-socket) for the
full behaviour and failure modes.

Design inspired by [agentsh](https://agentsh.org) — independent
implementation. Hijack + streaming + body-parse logic is our own,
written against `net/http/httputil` and the Docker Engine API docs.
