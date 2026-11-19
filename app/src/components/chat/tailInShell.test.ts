import { afterEach, describe, expect, it, vi } from "vitest";
import type { SessionStatus } from "../../types";
import { registerTerminalInput, registerWorkspace } from "../../uiBridge/registry";
import { SIDE_PANE } from "../../uiBridge/sideText";
import type { TerminalInput, WorkspaceController } from "../../uiBridge/types";
import { INTERRUPT_PAUSE_MS, outputFile, SHELL_READY_TIMEOUT_MS, shellQuote, tailInShell } from "./tailInShell";

const cleanups: (() => void)[] = [];
afterEach(() => {
  for (const fn of cleanups.splice(0)) fn();
});

function workspace(side?: string) {
  const ws = { view: () => ({ side }), openSide: vi.fn() } as unknown as WorkspaceController;
  cleanups.push(registerWorkspace("c1", ws));
  return ws;
}

function shell(status: SessionStatus, lastOutputAt = 1) {
  const sent: string[] = [];
  const t = { status: () => status, lastOutputAt: () => lastOutputAt, send: (d: string) => sent.push(d) } as unknown as TerminalInput;
  cleanups.push(registerTerminalInput("c1", SIDE_PANE, t));
  return sent;
}

describe("shellQuote", () => {
  it.each([
    ["/tmp/a b.output", "'/tmp/a b.output'"],
    ["/tmp/it's.output", "'/tmp/it'\\''s.output'"],
  ])("quotes %s", (s, want) => {
    expect(shellQuote(s)).toBe(want);
  });
});

describe("outputFile", () => {
  const task = { id: "b1", started_at: "2026-01-02T03:04:05Z" };
  it.each([
    ["a named file", { ...task, type: "local_bash", output_path: "/tmp/a b.output" }, { word: "'/tmp/a b.output'", shown: "/tmp/a b.output" }],
    ["a Monitor's file in its session's folder", { ...task, type: "monitor", session_id: "s1" }, { word: "/tmp/claude-*/*/'s1''/tasks/b1.output'", shown: "/tmp/claude-*/*/s1/tasks/b1.output" }],
    ["no file for another task without one", { ...task, type: "local_bash", session_id: "s1" }, undefined],
    ["no file for a Monitor without a session", { ...task, type: "monitor" }, undefined],
    ["no file for a Monitor without an id", { ...task, id: "", type: "monitor", session_id: "s1" }, undefined],
  ])("gives %s", (_name, t, want) => {
    expect(outputFile(t)).toEqual(want);
  });
});

describe("tailInShell", () => {
  it("opens the Shell view and tails the file after an interrupt", async () => {
    const ws = workspace();
    const sent = shell("running");
    const sleep = vi.fn(async (_ms: number) => {
      // The command comes only after the pause.
      expect(sent).toEqual(["\x03"]);
    });
    await tailInShell("c1", "'/tmp/claude-1000/s/tasks/b1.output'", sleep);
    expect(ws.openSide).toHaveBeenCalledWith("shell");
    expect(sleep).toHaveBeenCalledWith(INTERRUPT_PAUSE_MS);
    expect(sent).toEqual(["\x03", "tail -n +1 -f '/tmp/claude-1000/s/tasks/b1.output'\n"]);
  });

  it("leaves an open Shell view as it is", async () => {
    const ws = workspace("shell");
    shell("running");
    await tailInShell("c1", "/tmp/b1.output", async () => {});
    expect(ws.openSide).not.toHaveBeenCalled();
  });

  it("waits for the shell to start and print its prompt", async () => {
    workspace();
    let sent: string[] = [];
    const steps = [() => {}, () => (sent = shell("connecting", 0)), () => shell("running", 0), () => (sent = shell("running"))];
    const sleep = vi.fn(async () => {
      steps.shift()?.();
    });
    await tailInShell("c1", "'/tmp/b1.output'", sleep);
    expect(sent).toEqual(["\x03", "tail -n +1 -f '/tmp/b1.output'\n"]);
  });

  it("rejects when the channel isn't open", async () => {
    await expect(tailInShell("c1", "/tmp/b1.output", async () => {})).rejects.toThrow("channel c1 isn't open");
  });

  it.each<SessionStatus>(["completed", "failed"])("rejects when the shell has %s", async (status) => {
    workspace();
    shell(status);
    await expect(tailInShell("c1", "/tmp/b1.output", async () => {})).rejects.toThrow("the Shell view's shell has ended");
  });

  it("gives up when the shell doesn't start", async () => {
    workspace();
    const sleep = vi.fn(async () => {});
    await expect(tailInShell("c1", "/tmp/b1.output", sleep)).rejects.toThrow("the Shell view's shell didn't start");
    expect(sleep).toHaveBeenCalledTimes(SHELL_READY_TIMEOUT_MS / 100);
  });

  it("waits with a timer by default", async () => {
    vi.useFakeTimers();
    try {
      workspace();
      const sent = shell("running");
      const done = tailInShell("c1", "/tmp/b1.output");
      await vi.advanceTimersByTimeAsync(INTERRUPT_PAUSE_MS);
      await done;
      expect(sent).toHaveLength(2);
    } finally {
      vi.useRealTimers();
    }
  });
});
