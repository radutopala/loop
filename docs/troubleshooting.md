---
title: Troubleshooting
---
## Permission denied writing LaunchAgents plist

When running `loop daemon:start`, you may see:

```
Error: writing plist: open /Users/<user>/Library/LaunchAgents/com.loop.agent.plist: permission denied
```

This happens when `~/Library/LaunchAgents/` or the plist file is owned by `root` instead of your user. Deleting or writing the plist also requires write permission on the directory itself. Fix both by reclaiming ownership:

```bash
sudo chown $(whoami):staff ~/Library/LaunchAgents
sudo chown $(whoami):staff ~/Library/LaunchAgents/com.loop.agent.plist
```

Then retry `loop daemon:start`.

## Docker build fails with TLS certificate errors

When running `loop serve`, the Docker image build may fail with:

```
E: Failed to fetch https://deb.debian.org/debian/dists/.../Release.gpg  Server certificate verification failed. CAfile: ...
E: Some index files failed to download.
```

This occurs on corporate networks where a proxy or firewall (e.g. Palo Alto, Zscaler, Netskope) performs SSL inspection using a custom CA certificate that Docker containers don't trust.

### Fix: inject corporate CA certificates into the build

1. **Export your corporate CA certificates** from the macOS system keychain:

   ```bash
   security find-certificate -a -p /Library/Keychains/System.keychain > ~/.loop/container/corporate-ca.pem
   ```

2. **Patch `~/.loop/container/Dockerfile`** to inject the certificates before any network operations. Add these lines after each `FROM` line and before any `RUN apt-get` or `RUN curl` commands:

   ```dockerfile
   COPY corporate-ca.pem /usr/local/share/ca-certificates/corporate-ca.crt
   RUN update-ca-certificates
   ```

   For example, the builder stage becomes:

   ```dockerfile
   FROM golang:1.26 AS builder
   COPY corporate-ca.pem /usr/local/share/ca-certificates/corporate-ca.crt
   RUN update-ca-certificates
   ```

   Apply the same pattern to the final stage and to `~/.loop/container/chrome.Dockerfile` (note: `chrome.Dockerfile` is still Alpine — use the `cat … >> /etc/ssl/certs/ca-certificates.crt` form there).

3. **Retry** `loop serve`.

> **Note:** When `loop serve` ships a new version of the embedded container files, your edited `~/.loop/container/Dockerfile` (and `chrome.Dockerfile`) is preserved as `Dockerfile.bkp` (and `chrome.Dockerfile.bkp`) before being overwritten. Diff the `.bkp` against the refreshed file to re-apply your local changes.

## Daemon not healthy after `daemon:start`

The desktop app polls `GET /api/health` for ~15 seconds after launching the daemon; if it never turns healthy you'll see a "daemon not running" state.

1. **Check the log** — the daemon writes to `~/.loop/loop.log` by default (configurable via `log_file`). Startup failures (bad config, port in use, Docker unreachable) land there with the reason.
2. **Run it in the foreground** — `loop serve` runs the same daemon attached to your terminal, which surfaces config errors immediately. `daemon:start` is the supervised background variant (LaunchAgents on macOS); `loop daemon:status` shows whether it's loaded.
3. **Common causes**: another process bound to the API port (`api_addr`, default `:8222`), a malformed `~/.loop/config.json` (it's HJSON — comments are fine, but unbalanced braces are not), or Docker not running (the daemon starts without Docker but container features will error).

## Security gate: command blocked or approval prompt never resolves

The seccomp gate (`gates.agentgate`) is **enabled by default** and runs inside every agent container. Most operations are allowed silently; credential-adjacent writes and container-escape shapes either deny or prompt.

- **A prompt is waiting somewhere you're not looking.** Approval cards render in the chat for chat-initiated work, and as an overlay on the specific terminal pane for terminal-initiated work. The blocked syscall waits until you resolve the card — an agent that seems "stuck" often has a pending approval on another surface. The Audit panel lists recent gate decisions.
- **A legitimate operation keeps getting blocked.** Decisions come from first-match-wins rules; the fall-through is `gates.agentgate.default_decision` (`"allow"` by default). Add a project-level rule for the specific path/command rather than loosening globals — note project rules can only tighten policy (project `decision: "allow"` rules are rejected at load time).
- **Kill switch.** Set `gates.agentgate.enabled: false` globally (or per-project to disable for one project; a project cannot re-enable a globally disabled gate). Disabling the gate also disables the Docker proxy. Containers created before a config change keep their old policy — recreate them to pick up new rules.

## Docker proxy blocks an agent's Docker command

When the gate is enabled, agents talk to a filtered Docker socket (`loop dockerproxy`), not the real daemon. Denied calls return `403` inside the container; `approve`-rule matches block until you resolve the approval card.

- Check the Audit panel to see which rule matched.
- Add or adjust `gates.docker_proxy.http_rules` for the method/path being blocked; like the gate, project-level rules can only tighten policy, and `gates.docker_proxy.enabled` can be turned off per-project but not re-enabled past a global off.

## Memory search returns nothing / Ollama issues

Semantic memory is **off by default** — `search_memory` silently returns no results until you set `memory.enabled: true` in the global config and configure `memory.paths`.

When enabled, the first `Embed()` call lazily starts a local `loop-ollama` Docker container (port `11434`, model `nomic-embed-text`, cached in the `loop-ollama` named volume), and the container auto-stops after ~5 minutes idle. Failure modes:

- **Port conflict** — if something else (e.g. a native Ollama install) is already bound to `11434`, the container can't start. Stop the other instance or point `memory.embeddings` at it instead of the managed container.
- **First-use model pull is slow** — the initial `nomic-embed-text` download happens inside the container on first index/search; on corporate networks it's subject to the same TLS-inspection issues as the image build above. Subsequent runs use the named volume.
- **Indexing lag** — re-indexing runs at startup and then on a ticker (default 5 minutes, `memory.reindex_interval_sec`); files edited moments ago may not be searchable yet.
