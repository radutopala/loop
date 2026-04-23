# agentgate

Seccomp `RET_USER_NOTIF` gate for the agent container. Traps `connect`,
`execve`/`execveat`, and the `openat`/`renameat2`/`unlinkat`/... file-op
family; routes each trap through a policy matcher and, for `approve`
decisions, an approval UI on Discord, Slack, or the local desktop app.

Filter install happens late (inside `loop-syscallwrap`, just before the
target `syscall.Exec`), so the `entrypoint.sh → su-exec` chain runs
unfiltered and only the agent's process tree is gated.

Design inspired by [agentsh](https://agentsh.org) — independent
implementation written from the kernel man pages (`seccomp(2)`,
`seccomp_unotify(2)`, `process_vm_readv(2)`) and
`golang.org/x/sys/unix`. No source copied.
