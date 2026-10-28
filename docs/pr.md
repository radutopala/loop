---
title: PR Watching
---
Loop watches the checks, merge state and review threads of a thread's pull request (GitHub) or merge request (GitLab). When a check fails on a commit the thread's agent made, Loop sends it back to that agent to fix. Any other failure lights a red `ci` pill on the thread, and a merge conflict with the base a red `cnf` pill, so you know it needs you. Hovering the thread says why each pill is lit. New unresolved review threads on the PR/MR are notified too, and light a `cmt` pill. You can also arm **merge when ready**, so the PR/MR merges once its checks pass.

**Related docs:** [Git Panel](git.md) | [Sidebar](sidebar.md) | [Configuration — PR watching](configuration.md#pr-watching) | [HTTP API — PR watching](api.md#pr-watching) | [Events — `channel.pr`](events.md#channelpr)

---

## What gets watched

A thread is watched once its branch has a PR/MR. Lookups run through `gh` for GitHub and `glab api` (GitLab's REST API) for GitLab, under the same account the [PR lookup](api.md#get-apichannelsidpr) uses: `github.gh_user` or `gitlab.glab_config_dir`. A machine without `gh` or `glab` watches nothing.

Loop polls the PR/MR every `pr.poll_sec` seconds (30 by default) while any of these holds:

- its checks are running;
- a fix is in flight;
- a merge is armed;
- a commit or an agent run happened in the last two minutes, so a push is likely.

Otherwise an open PR/MR is polled every `pr.idle_poll_sec` seconds (300 by default), so a merge conflict caused by its base moving, or a push from elsewhere, is seen while the thread is idle. A merged or closed one is left alone until the thread's next commit, agent run or PR lookup. What Loop knows about each thread (the attempts, the agent's commits, an armed merge) is kept in the database, so a daemon restart picks up where it left off.

Loop acts only once all of a head's checks are done.

## Whose commit failed

A commit counts as the agent's when the thread's checkout moved to it while the agent was running, or within 15 seconds after the run ended. Every other commit is yours: one you pushed by hand, a merge from the base branch, a push from another machine.

## What happens on a failure

| Failure | What Loop does |
|---|---|
| On the agent's commit, auto-fix on | Sends the agent a message from `loop-pr` that names the PR/MR, the commit and the failed checks, with the tail of each failed log (up to five checks). The thread shows `ci…` while the agent works on it. |
| On someone else's commit | Lights the `ci` pill. Nothing is sent. |
| Auto-fix off for the thread | Lights the `ci` pill. |
| Only infrastructure failures (GitLab runner failures such as `runner_system_failure`) | Re-runs the failed jobs once for that head, with a notice in the thread. If they fail again, it lights the `ci` pill. |

Every failure a pill lights also adds a notification ("PR #342: lint failed", "MR !57: test failed"). That bumps the unread count and fires the desktop notification, even when you aren't viewing the thread.

### Guards

Auto-fix stops, lights the `ci` pill and posts a notice in the thread when:

- the checks still fail after `pr.max_attempts` fixes (3 by default);
- the last fix pushed no new commit;
- the same failure, with the same logs, came back after a fix.

The attempt count resets when the checks pass.

### Clearing

The `ci` and `ci…` pills clear when the checks pass, when the PR/MR is merged or closed, and when a new head's checks start running.

## Merge conflicts

When the PR/MR conflicts with its base (GitHub's `DIRTY`, GitLab's `conflict`), the thread's `cnf` pill lights and the PR/MR tab shows "Merge conflicts with main." It takes precedence over failing checks, which GitHub doesn't run on a conflicting PR. A conflict is always yours to resolve: Loop sends it to no agent, raises no notification, and **Fix it** doesn't send it either. The pill clears once the PR/MR no longer conflicts.

## Unresolved review threads

Each poll of an open PR/MR also counts its unresolved review threads: GitHub review threads not marked resolved, or GitLab discussions with a resolvable note not yet resolved (the first 100 of either). A thread not seen before raises a notification, such as "PR #342: 2 unresolved review threads", once per thread, also across daemon restarts. With a new failure in the same poll, the two share one notification: "PR #342: lint failed; 1 unresolved review thread".

Review threads light the yellow `cmt` pill and show their count in the Git panel's PR/MR tab until they're resolved. Auto-fix doesn't send them to the agent.

## The Git panel

When the thread's branch has a PR/MR, the Git panel gets a **PR** tab (**MR** on GitLab). Its label carries the state, so it reads from any other tab: `PR ⚠` (a merge conflict), `PR ✗ 2`, `PR ◌ 3/7`, `PR ✓`, `PR merged` or `PR closed`. The tab shows:

- **The chip.** It reads `PR #342` or `MR !57`, then the state: `⚠ conflicts`, `✓ checks`, `✗ 2 failing`, `◌ 3/7 running`, `merging when ready`, `merged` or `closed`. Clicking it opens the PR/MR. Below it are the check counts, the head commit and the count of unresolved review threads, which links to the PR/MR.
- **A stale checkout.** When the thread's checkout is at another commit than the head, the tab says so: the head was pushed from elsewhere, or the checkout has commits not pushed yet. Auto-fix leaves a head pushed from elsewhere to you.
- **Why.** What the watcher last did about the failure, and why (see the table below).
- **Auto-fix failed checks.** This toggle overrides `pr.auto_fix` for the thread: whether failed checks on the agent's commits go back to it. Merge conflicts and review threads never do. Its label shows the config's default when the thread differs from it, and flipping it back to the default clears the override.
- **Fix it.** Sends the current failure to the agent now, whoever made the commit and whatever the guards say.
- **Re-run failed.** Re-runs the failed checks on the forge: each failed GitHub workflow run once, or each failed GitLab job.
- **Merge when ready**, **Cancel auto-merge** and **Merge now.** Each button shows the merge method it uses.
- **The failed checks.** Each one links to its job on the forge, and **Log** shows the tail of its log. A runner failure is marked `(runner)`.

| Why | The tab says |
|---|---|
| `not_agent_commit` | The head commit isn't one this thread's agent made, so auto-fix leaves it to you. |
| `auto_fix_off` | Auto-fix is off for this thread. |
| `max_attempts` | Auto-fix stopped after N attempts. |
| `no_new_commit` | The last fix pushed no new commit. |
| `same_failure` | The same failure came back after a fix. |
| `infra_rerun` | The checks failed for runner reasons, so Loop re-ran them. |
| `infra_again` | The checks failed for runner reasons again after a re-run. |
| `fix_sent` | Auto-fix sent the failure to the agent. |
| `fix_asked` | Fix it sent the failure to the agent. |

Each message `loop-pr` sends carries a red `PR` badge in the chat.

## Merge when ready

Arming merge when ready uses the forge's own auto-merge when the repo allows it: `gh pr merge --auto` on GitHub, or GitLab's auto-merge (`merge_when_pipeline_succeeds` on older instances). Otherwise Loop merges itself. Once the checks pass and the PR/MR can merge cleanly, it merges once. Loop disarms its own merge when:

- someone other than the agent pushes a new head (with a notice in the thread);
- the PR/MR closes.

The method is the one `pr.merge_method` names when the repo allows it, else the first the repo allows: squash, then merge, then rebase.

Merging is owner-only. The routes aren't open to the agent's token, and no MCP tool merges.

## Configuration

```jsonc
"pr": {
  "auto_fix": true,      // send failures on the agent's commits back to it
  "max_attempts": 3,     // fixes per PR/MR before auto-fix stops
  "merge_method": "",    // squash, merge or rebase; empty picks the first the repo allows
  "poll_sec": 30,        // how often a watched PR/MR is polled
  "idle_poll_sec": 300   // how often an open PR/MR is polled otherwise
}
```

The block can be set globally and per project. A project's `pr` block waits for your [trust](configuration.md#project-config-trust), since an auto-fix starts agent runs on its own. See [Configuration — PR watching](configuration.md#pr-watching).
