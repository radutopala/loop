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
(per JSONPath-lite assertion over `POST /containers/create` bodies —
denies bind-mounts of `/`, `--privileged`, host-namespace flags, cap
escalations, etc.). Approve decisions round-trip back to loop-server
over HTTP (`POST /api/gate/container-approval`) authenticated by the
per-container bearer token; deny decisions are terminal (no prompt).

Design inspired by [agentsh](https://agentsh.org) — independent
implementation. Hijack + streaming + body-parse logic is our own,
written against `net/http/httputil` and the Docker Engine API docs.
