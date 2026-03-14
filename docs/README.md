# Loop Documentation

Comprehensive documentation for every feature of the Loop platform.

## Core System

- [**Configuration**](configuration.md) — Global and project config reference, all fields with defaults, merge rules
- [**Platforms**](platforms.md) — Discord, Slack, and Local platform support, setup, and differences
- [**Orchestrator**](orchestrator.md) — Message processing flow, session management, streaming, concurrency
- [**Permissions**](permissions.md) — RBAC system, role hierarchy, bootstrap rule, allow/deny commands
- [**Commands**](commands.md) — All slash commands with syntax, parameters, and permission requirements

## Agent & Containers

- [**Containers**](containers.md) — Docker container lifecycle, environment, mounts, MCP config, cleanup
- [**Scheduling**](scheduling.md) — Task types (cron/interval/once), templates, auto-deletion, thread creation

## API & Data

- [**API**](api.md) — HTTP API reference (27 endpoints), request/response schemas, error codes
- [**Events**](events.md) — Real-time WebSocket events, subscription model, event payloads
- [**Terminal**](terminal.md) — Terminal WebSocket protocol, Docker exec, host PTY, ring buffer
- [**Memory**](memory.md) — Semantic search, Ollama embeddings, chunking, re-indexing

## Desktop App

- [**Desktop App**](desktop-app.md) — Electron architecture, windows, deep links, auto-update, daemon management
- [**Layouts**](layouts.md) — Split pane workspaces, named layouts, drag-to-split, persistence
- [**Chat**](chat.md) — Chat view, message rendering, streaming, agent activity, input with autocomplete
- [**Editor**](editor.md) — CodeMirror editor, file tree, tabs, dirty tracking, auto-save
- [**Sidebar**](sidebar.md) — Channel/thread navigation, ordering, search, batch operations
- [**Settings**](settings.md) — Settings panel, command palette (Cmd+K), daemon management
