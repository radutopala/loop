---
title: CI Auto-Fix
---
Loop watches the CI of a thread's pull request (GitHub) or merge request (GitLab). When a check fails on a commit the thread's agent made, Loop sends the failure back to that agent to fix. Any other failure lights a red `ci` pill on the thread so you know it needs you. You can also arm **merge when ready**, so the PR/MR merges once its checks pass.

**Related docs:** [Git Panel](git.md) | [Sidebar](sidebar.md) | [Configuration — CI](configuration.md#ci) | [HTTP API — CI](api.md#ci) | [Events — `channel.ci`](events.md#channelci)

---

## What gets watched

A thread is watched once its branch has a PR/MR. Lookups run through `gh` for GitHub and `glab api` (GitLab's REST API) for GitLab, under the same account the [PR lookup](api.md#get-apichannelsidpr) uses: `github.gh_user` or `gitlab.glab_config_dir`. A machine without `gh` or `glab` watches nothing.

Loop polls the PR/MR every `ci.poll_sec` seconds (30 by default) while any of these holds:

- its checks are running;
- a fix is in flight;
- a merge is armed;
- a commit or an agent run happened in the last two minutes, so a push is likely.

Otherwise the thread is left alone until its next commit, agent run or PR lookup. What Loop knows about each thread (the attempts, the agent's commits, an armed merge) is kept in the database, so a daemon restart picks up where it left off.

Loop acts only once all of a head's checks are done.

## Whose commit failed

A commit counts as the agent's when the thread's checkout moved to it while the agent was running, or within 15 seconds after the run ended. Every other commit is yours: one you pushed by hand, a merge from the base branch, a push from another machine.

## What happens on a failure

| Failure | What Loop does |
|---|---|
| On the agent's commit, auto-fix on | Sends the agent a message from `loop-ci` that names the PR/MR, the commit and the failed checks, with the tail of each failed log (up to five checks). The thread shows `ci…` while the agent works on it. |
| On someone else's commit | Lights the `ci` pill. Nothing is sent. |
| Auto-fix off for the thread | Lights the `ci` pill. |
| Only infrastructure failures (GitLab runner failures such as `runner_system_failure`) | Re-runs the failed jobs once for that head, with a notice in the thread. If they fail again, it lights the `ci` pill. |

Every failure a pill lights also adds a notification ("PR #342: lint failed", "MR !57: test failed"). That bumps the unread count and fires the desktop notification, even when you aren't viewing the thread.

### Guards

Auto-fix stops, lights the `ci` pill and posts a notice in the thread when:

- the checks still fail after `ci.max_attempts` fixes (3 by default);
- the last fix pushed no new commit;
- the same failure, with the same logs, came back after a fix.

The attempt count resets when the checks pass.

### Clearing

The `ci` and `ci…` pills clear when the checks pass, when the PR/MR is merged or closed, and when a new head's checks start running.

## The Git panel

When the thread's branch has a PR/MR, the Git panel shows a CI row under its toolbar:

- **The chip.** It reads `PR #342` or `MR !57`, then the state: `✓ checks`, `✗ 2 failing`, `◌ 3/7 running`, `merging when ready`, `merged` or `closed`. Clicking it opens the PR/MR.
- **The failed checks.** Each one links to its job on the forge. A runner failure is marked `(runner)`.
- **Auto-fix CI.** This toggle overrides `ci.auto_fix` for the thread. Its label shows the config's default when the thread differs from it, and flipping it back to the default clears the override.
- **Fix it.** Sends the current failure to the agent now, whoever made the commit and whatever the guards say.
- **Merge when ready**, **Cancel auto-merge** and **Merge now.** Each button shows the merge method it uses.

Each message `loop-ci` sends carries a red `CI` badge in the chat.

## Merge when ready

Arming merge when ready uses the forge's own auto-merge when the repo allows it: `gh pr merge --auto` on GitHub, or GitLab's auto-merge (`merge_when_pipeline_succeeds` on older instances). Otherwise Loop merges itself. Once the checks pass and the PR/MR can merge cleanly, it merges once. Loop disarms its own merge when:

- someone other than the agent pushes a new head (with a notice in the thread);
- the PR/MR closes.

The method is the one `ci.merge_method` names when the repo allows it, else the first the repo allows: squash, then merge, then rebase.

Merging is owner-only. The routes aren't open to the agent's token, and no MCP tool merges.

## Configuration

```jsonc
"ci": {
  "auto_fix": true,       // send failures on the agent's commits back to it
  "max_attempts": 3,      // fixes per PR/MR before auto-fix stops
  "merge_method": "",     // squash, merge or rebase; empty picks the first the repo allows
  "poll_sec": 30          // how often a watched PR/MR is polled
}
```

The block can be set globally and per project. A project's `ci` block waits for your [trust](configuration.md#project-config-trust), since an auto-fix starts agent runs on its own. See [Configuration — CI](configuration.md#ci).
