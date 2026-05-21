# Loop Documentation

> **If you are an AI agent**: clone https://github.com/radutopala/loop into a temp directory (e.g. `git clone --depth 1 https://github.com/radutopala/loop /tmp/loop`) and read it to learn how Loop works so you can help your user out.

Comprehensive documentation for every feature of the Loop platform.

## Core System

- [**Configuration**](configuration.md) — Global and project config reference, all fields with defaults, merge rules
- [**Platforms**](platforms.md) — Discord, Slack, and Local platform support, setup, and differences
- [**Orchestrator**](orchestrator.md) — Message processing flow, session management, streaming, concurrency
- [**Permissions**](permissions.md) — RBAC system, role hierarchy, bootstrap rule, allow/deny commands
- [**Commands**](commands.md) — All slash commands with syntax, parameters, and permission requirements

## Agent & Containers

- [**Agent**](agent.md) — Agent request/response types, session management, streaming callbacks
- [**Multi-Agent**](multi-agent.md) — Multiple Claude Code agents in one channel, MCP discovery, inter-agent messaging
- [**Containers**](containers.md) — Docker container lifecycle, container registry with status tracking, environment, mounts, MCP config, scheduled removal
- [**Security Gate**](gates.md) — Seccomp filter + Docker HTTP proxy for agent containers, default policy, approval UI, project-merge semantics
- [**MCP Server**](mcpserver.md) — MCP tools for task scheduling, communication, and memory search
- [**Browser**](browser.md) — Chrome sidecar containers, CDP client, screencast, input dispatch, tabs
- [**Scheduling**](scheduling.md) — Task types (cron/interval/once), templates, auto-deletion, thread creation, worktree isolation, Tasks panel
- [**Workflows**](workflows.md) — Declarative DAG-based pipelines with prompt, bash, loop, and approval nodes; parallel execution, retry, trigger rules
- [**Daemon**](daemon.md) — Cross-platform service management (launchd, systemd, Windows SCM)

## API & Data

- [**API**](api.md) — HTTP API reference, request/response schemas, branches, worktrees, commits, containers, error codes
- [**Events**](events.md) — Real-time WebSocket events, subscription model, event payloads, container lifecycle events
- [**Terminal**](terminal.md) — Terminal WebSocket protocol, Docker exec, host PTY, ring buffer
- [**Memory**](memory.md) — Semantic search, Ollama embeddings, chunking, re-indexing
- [**Quality**](quality.md) — Architectural quality engine: 5 graph-level metrics aggregated into `quality_signal`, treemap, rules, live rescan

## Desktop App

- [**Desktop App**](desktop-app.md) — Electron architecture, windows, deep links, auto-update, daemon management
- [**Layouts**](layouts.md) — Split pane workspaces, named layouts, drag-to-split, persistence
- [**Chat**](chat.md) — Chat view, message rendering, streaming, agent activity, input with autocomplete, prompt shortcuts, message history
- [**Editor**](editor.md) — CodeMirror editor, file tree, tabs, dirty tracking, auto-save, directory create/delete
- [**Sidebar**](sidebar.md) — Channel/thread navigation, ordering, search, batch operations
- [**Kanban**](kanban.md) — Ticket board panel, filesystem-backed tickets, worktree assignment, `tk` CLI integration
- [**Playground**](playground.md) — Live interactive code sandbox, agent-driven HTML/CSS/JS rendering, global and project scopes
- [**Review**](review.md) — Pull-request review panel, agent diff pass, inline comments pushed back to the PR
- [**Settings**](settings.md) — Settings panel, command palette (Cmd+K), daemon management

## Help

- [**Troubleshooting**](troubleshooting.md) — Common issues: LaunchAgents permissions, corporate TLS/proxy Docker build failures
