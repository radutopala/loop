import { describe, expect, it, vi } from "vitest";
import { getTerminalInput, getWorkspace, notifyUiChanged, registerTerminalInput, registerWorkspace, subscribeUiChanged } from "./registry";
import type { TerminalInput, WorkspaceController } from "./types";

const workspace = () => ({}) as WorkspaceController;
const terminal = () => ({}) as TerminalInput;

describe("registry", () => {
  it("registers a workspace and says so", () => {
    const changed = vi.fn();
    const unsubscribe = subscribeUiChanged(changed);
    const a = workspace();
    const unregister = registerWorkspace("c1", a);
    expect(getWorkspace("c1")).toBe(a);
    expect(changed).toHaveBeenCalledTimes(1);
    unregister();
    expect(getWorkspace("c1")).toBeUndefined();
    expect(changed).toHaveBeenCalledTimes(2);
    unsubscribe();
    notifyUiChanged();
    expect(changed).toHaveBeenCalledTimes(2);
  });

  it("keeps a newer workspace when an older one unregisters", () => {
    const unregisterOld = registerWorkspace("c1", workspace());
    const b = workspace();
    const unregister = registerWorkspace("c1", b);
    unregisterOld();
    expect(getWorkspace("c1")).toBe(b);
    unregister();
  });

  it("registers a terminal's input by channel and pane", () => {
    const old = terminal();
    const unregisterOld = registerTerminalInput("c1", "docker-agent-0", old);
    const t = terminal();
    const unregister = registerTerminalInput("c1", "docker-agent-0", t);
    unregisterOld();
    expect(getTerminalInput("c1", "docker-agent-0")).toBe(t);
    expect(getTerminalInput("c2", "docker-agent-0")).toBeUndefined();
    unregister();
    expect(getTerminalInput("c1", "docker-agent-0")).toBeUndefined();
  });
});
