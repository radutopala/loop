---
title: Sessions Panel
---
The Sessions panel lists the past Claude Code sessions for a channel and lets you resume any of them. Each agent run in a channel produces a session transcript on disk; the panel reads those and shows them newest-first with a one-line summary.

**Related docs:** [Agent](agent.md) | [Orchestrator](orchestrator.md) | [Layouts](layouts.md)

---

## Overview

![The Sessions panel listing past Claude sessions for the channel, with a summary and "Select a session to resume"](static/images/features/sessions.png)

**Component:** `app/src/components/panels/SessionsPanel.tsx`

Sessions are read from `GET /api/channels/{id}/sessions`, which scans the Claude session store under `$HOME/.claude/projects/<encoded-workdir>/*.jsonl` (the workdir path is encoded the way Claude Code stores it). Each entry shows its session id and a short summary derived from the transcript, along with how long ago it ran.

## Resuming

Selecting a session resumes that conversation — the next message continues from where it left off (`claude --resume <session-id>`), so context, file state, and history carry over. A filter box narrows the list, and **+ New** starts a fresh session instead of resuming.

The empty state ("No sessions found") simply means no agent has run in the channel yet.

## Pruned transcripts

Claude Code deletes transcripts under `$HOME/.claude/projects` once they pass
its retention window (`cleanupPeriodDays`, 30 days by default), while Loop pins
a channel's session id in the database and only replaces it after a successful
run. A channel that sat idle for longer than the window therefore points at a
file that no longer exists, and `claude --resume` fails the turn outright with
`No conversation found with session ID: …` — every turn, permanently.

Loop checks for the transcript before it resumes:

- **Agent runs and terminal panes** drop `--resume`/`--fork-session` and start a
  fresh session when the file is provably absent, logging
  `session transcript not found; starting a fresh session`. A stat that fails
  for any other reason (unreadable home dir, permissions) is *not* treated as
  absence — the conversation is kept.
- **New worktrees** (the `+wt` button, ticket assignment, worktree tasks, thread
  forks) copy the source transcript into the worktree's own project dir, because
  Claude keys sessions by working directory. When that copy fails, the new
  thread is left with no session id rather than inheriting one it cannot resume.

The conversation itself is not recoverable once pruned; the channel simply
starts a new session on its next turn.
