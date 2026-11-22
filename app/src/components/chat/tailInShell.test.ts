import { afterEach, describe, expect, it, vi } from "vitest";
import type { SessionStatus } from "../../types";
import { registerTerminalInput, registerWorkspace } from "../../uiBridge/registry";
import { SIDE_PANE } from "../../uiBridge/sideText";
import type { TerminalInput, WorkspaceController } from "../../uiBridge/types";
import { INTERRUPT_PAUSE_MS, outputFile, SHELL_READY_TIMEOUT_MS, shellQuote, tailCommand, tailInShell, tailPlan } from "./tailInShell";

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

describe("tailPlan", () => {
  const task = { id: "b1", type: "local_bash", started_at: "2026-01-02T03:04:05Z", output_path: "/tmp/s/tasks/b1.output" };
  const own = { word: "'/tmp/s/tasks/b1.output'", shown: "/tmp/s/tasks/b1.output" };

  it("follows the task's own file when its command redirects nothing", () => {
    expect(tailPlan({ ...task, command: "for i in 1 2; do echo $i; done" })).toEqual({ files: [own] });
    expect(tailPlan(task)).toEqual({ files: [own] });
  });

  it("follows the files the command redirects to, then its own", () => {
    expect(tailPlan({ ...task, command: "cd /w && make > b.log 2> ~/b.err; echo rc=$?" })).toEqual({
      files: [{ word: "'/w/b.log'", shown: "/w/b.log" }, { word: "~/'b.err'", shown: "~/b.err" }, own],
    });
    expect(tailPlan({ ...task, output_path: undefined, command: "make > ~ " })).toEqual({ files: [{ word: "~", shown: "~" }] });
  });

  it("says which filter holds the output back", () => {
    expect(tailPlan({ ...task, command: "go test ./... | tail -5" })).toEqual({ files: [own], heldBy: "tail" });
  });

  it("follows the files of the task a wait loop reads", () => {
    const build = { ...task, id: "b2", description: "build", output_path: "/tmp/s/tasks/b2.output", command: "make > /w/b.log 2>&1; echo rc=$?" };
    const wait = { ...task, description: "wait", command: "until grep -q rc= /tmp/s/tasks/b2.output; do sleep 5; done; cat /tmp/s/tasks/b2.output" };
    const plan = tailPlan(wait, [wait, build]);
    expect(plan?.waitsOn).toBe(build);
    expect(plan?.files.map((f) => f.shown)).toEqual(["/w/b.log", "/tmp/s/tasks/b2.output"]);
    // One that redirects nothing has nothing more to show.
    const plain = { ...build, command: "make" };
    expect(tailPlan(wait, [wait, plain])).toEqual({ files: [own] });
    expect(
      tailPlan(wait, [
        { ...build, command: undefined },
        { ...build, id: "" },
      ]),
    ).toEqual({ files: [own] });
  });

  it("is undefined with nothing to follow", () => {
    expect(tailPlan({ ...task, output_path: undefined, command: "sleep 9" })).toBeUndefined();
  });
});

describe("tailCommand", () => {
  const task = { id: "b1", started_at: "2026-01-02T03:04:05Z" };
  const files = [
    { word: "'/w/b.log'", shown: "/w/b.log" },
    { word: "'/tmp/b1.output'", shown: "/tmp/b1.output" },
  ];

  it("says what it follows, and why nothing may show yet, then follows it", () => {
    expect(tailCommand({ ...task, description: "Build it" }, { files })).toBe(
      "clear; printf '\\033[2m# %s\\033[0m\\n' 'Build it' 'following /w/b.log, /tmp/b1.output'; " +
        `[ -n "$(cat '/w/b.log' '/tmp/b1.output' 2>/dev/null | head -c 1)" ] || ` +
        "printf '\\033[2m# %s\\033[0m\\n' 'no output yet: a command that writes nothing until it ends shows it then'; " +
        "tail -n +1 -F '/w/b.log' '/tmp/b1.output'",
    );
  });

  it("names the task waited on and the filter that holds the output back", () => {
    const line = tailCommand(task, { files: files.slice(1), waitsOn: { id: "b2", started_at: "" }, heldBy: "tail" });
    expect(line).toContain("'waits on: b2' 'following /tmp/b1.output' 'its output goes through tail, which may hold it back until it ends'");
    expect(tailCommand(task, { files, waitsOn: { id: "b2", description: "build", started_at: "" } })).toContain("'waits on: build'");
  });
});

describe("tailInShell", () => {
  it("opens the Shell view and runs the line after an interrupt", async () => {
    const ws = workspace();
    const sent = shell("running");
    const sleep = vi.fn(async (_ms: number) => {
      // The command comes only after the pause.
      expect(sent).toEqual(["\x03"]);
    });
    await tailInShell("c1", "tail -F /tmp/b1.output", sleep);
    expect(ws.openSide).toHaveBeenCalledWith("shell");
    expect(sleep).toHaveBeenCalledWith(INTERRUPT_PAUSE_MS);
    expect(sent).toEqual(["\x03", "tail -F /tmp/b1.output\n"]);
  });

  it("tries the Shell view again until the chat's container is known to run", async () => {
    let tries = 0;
    const ws = {
      view: () => ({}),
      openSide: vi.fn(() => {
        if (++tries < 3) throw new Error("the chat's container isn't running; the Shell view needs it");
      }),
    } as unknown as WorkspaceController;
    cleanups.push(registerWorkspace("c1", ws));
    const sent = shell("running");
    await tailInShell("c1", "tail -F /tmp/b1.output", async () => {});
    expect(ws.openSide).toHaveBeenCalledTimes(3);
    expect(sent).toEqual(["\x03", "tail -F /tmp/b1.output\n"]);
  });

  it("says why the Shell view didn't open, when it never does", async () => {
    const ws = {
      view: () => ({}),
      openSide: vi.fn(() => {
        throw new Error("the chat's container isn't running; the Shell view needs it");
      }),
    } as unknown as WorkspaceController;
    cleanups.push(registerWorkspace("c1", ws));
    shell("running");
    const sleep = vi.fn(async () => {});
    await expect(tailInShell("c1", "tail -F /tmp/b1.output", sleep)).rejects.toThrow("the chat's container isn't running");
    expect(sleep).toHaveBeenCalledTimes(SHELL_READY_TIMEOUT_MS / 100);
  });

  it("gives up when the channel closes while it waits", async () => {
    const unregister = registerWorkspace("c1", { view: () => ({ side: "shell" }), openSide: vi.fn() } as unknown as WorkspaceController);
    const sleep = vi.fn(async () => unregister());
    await expect(tailInShell("c1", "tail -F /tmp/b1.output", sleep)).rejects.toThrow("channel c1 isn't open");
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
    await tailInShell("c1", "tail -F /tmp/b1.output", sleep);
    expect(sent).toEqual(["\x03", "tail -F /tmp/b1.output\n"]);
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
