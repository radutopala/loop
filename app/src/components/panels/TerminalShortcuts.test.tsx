import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { type BashShortcut, fetchBashShortcuts, fetchShortcuts, type PromptShortcut } from "../../api/configApi";
import { ShortcutsVersionContext } from "../../hooks/shortcutsVersion";
import { TerminalShortcuts } from "./TerminalShortcuts";

vi.mock("../../api/configApi", () => ({
  fetchShortcuts: vi.fn(),
  fetchBashShortcuts: vi.fn(),
}));

const promptsMock = vi.mocked(fetchShortcuts);
const bashMock = vi.mocked(fetchBashShortcuts);

const prompts: PromptShortcut[] = [
  { name: "review", description: "Review the diff", prompt: "review this", scope: "global" },
  { name: "fix", description: "Fix the build", prompt: "fix it", scope: "project" },
];
const bash: BashShortcut[] = [{ name: "ls", description: "List files", command: "ls -la" } as BashShortcut];

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe("TerminalShortcuts", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    promptsMock.mockResolvedValue(prompts);
    bashMock.mockResolvedValue(bash);
  });

  it("renders nothing when there are no shortcuts and no Continue", async () => {
    promptsMock.mockResolvedValue([]);
    const { container } = render(<TerminalShortcuts channelId="ch-1" leafId="L1" onPick={vi.fn()} />);
    await waitFor(() => expect(promptsMock).toHaveBeenCalledWith("ch-1"));
    expect(container.innerHTML).toBe("");
    expect(bashMock).not.toHaveBeenCalled();
  });

  it("lists prompt shortcuts for the agent and picks one", async () => {
    const onPick = vi.fn();
    const user = userEvent.setup();
    render(<TerminalShortcuts channelId="ch-1" leafId="L1" onPick={onPick} />);
    const btn = await screen.findByTestId("terminal-shortcuts-btn-L1");
    vi.spyOn(btn, "getBoundingClientRect").mockReturnValue({ top: 500, left: 30 } as DOMRect);
    await user.click(btn);
    const menu = screen.getByTestId("terminal-shortcuts-menu-L1");
    expect(menu.style.left).toBe("30px");
    expect(menu.style.bottom).toBe(`${window.innerHeight - 500 + 4}px`);
    expect(screen.getByText("#review")).toBeTruthy();
    expect(screen.getByText("Fix the build")).toBeTruthy();

    // The first item starts selected; hovering another moves the selection.
    const first = screen.getByText("#review").parentElement!;
    const second = screen.getByText("#fix").parentElement!;
    expect(first.style.backgroundColor).not.toBe("transparent");
    expect(second.style.backgroundColor).toBe("transparent");
    fireEvent.mouseEnter(second);
    expect(second.style.backgroundColor).not.toBe("transparent");
    expect(first.style.backgroundColor).toBe("transparent");

    fireEvent.mouseDown(second);
    expect(onPick).toHaveBeenCalledWith("fix it");
    expect(screen.queryByTestId("terminal-shortcuts-menu-L1")).toBeNull();
  });

  it("toggles the menu with its button and closes it on an outside mousedown", async () => {
    const user = userEvent.setup();
    render(<TerminalShortcuts channelId="ch-1" leafId="L1" onPick={vi.fn()} />);
    const btn = await screen.findByTestId("terminal-shortcuts-btn-L1");
    await user.click(btn);
    expect(screen.getByTestId("terminal-shortcuts-menu-L1")).toBeTruthy();
    // Clicking the button again closes it; its mousedown doesn't count as outside.
    await user.click(btn);
    expect(screen.queryByTestId("terminal-shortcuts-menu-L1")).toBeNull();

    await user.click(btn);
    // A mousedown inside the menu (not on an item) keeps it open.
    fireEvent.mouseDown(screen.getByTestId("terminal-shortcuts-menu-L1"));
    expect(screen.getByTestId("terminal-shortcuts-menu-L1")).toBeTruthy();
    fireEvent.mouseDown(document.body);
    expect(screen.queryByTestId("terminal-shortcuts-menu-L1")).toBeNull();
  });

  it("lists bash shortcuts when prompts are off and picks a command", async () => {
    const onPick = vi.fn();
    const user = userEvent.setup();
    render(<TerminalShortcuts channelId="ch-1" leafId="L1" onPick={onPick} target="host" />);
    const btn = await screen.findByTestId("terminal-bash-shortcuts-btn-L1");
    expect(promptsMock).not.toHaveBeenCalled();
    expect(bashMock).toHaveBeenCalledWith("ch-1");
    expect(screen.queryByTestId("terminal-shortcuts-btn-L1")).toBeNull();

    await user.click(btn);
    expect(screen.getByText("$ls")).toBeTruthy();
    // A mousedown on the button itself doesn't close the open menu.
    fireEvent.mouseDown(btn);
    expect(screen.getByTestId("terminal-shortcuts-menu-L1")).toBeTruthy();
    fireEvent.mouseDown(document.body);
    expect(screen.queryByTestId("terminal-shortcuts-menu-L1")).toBeNull();

    await user.click(btn);
    fireEvent.mouseDown(screen.getByText("$ls"));
    expect(onPick).toHaveBeenCalledWith("ls -la");
  });

  it("shows only bash shortcuts on an agent pane with prompts turned off", async () => {
    render(<TerminalShortcuts channelId="ch-1" leafId="L1" onPick={vi.fn()} showPrompts={false} />);
    await screen.findByTestId("terminal-bash-shortcuts-btn-L1");
    expect(promptsMock).not.toHaveBeenCalled();
  });

  it("highlights the prompt and Continue buttons on hover", async () => {
    render(<TerminalShortcuts channelId="ch-1" leafId="L1" onPick={vi.fn()} onContinue={vi.fn()} />);
    const promptBtn = await screen.findByTestId("terminal-shortcuts-btn-L1");
    for (const el of [promptBtn, screen.getByTestId("terminal-continue-L1")]) {
      fireEvent.mouseEnter(el);
      expect(el.style.backgroundColor).not.toBe("transparent");
      fireEvent.mouseLeave(el);
      expect(el.style.backgroundColor).toBe("transparent");
    }
  });

  it("highlights the bash button on hover", async () => {
    render(<TerminalShortcuts channelId="ch-1" leafId="L1" onPick={vi.fn()} target="host" />);
    const bashBtn = await screen.findByTestId("terminal-bash-shortcuts-btn-L1");
    fireEvent.mouseEnter(bashBtn);
    expect(bashBtn.style.backgroundColor).not.toBe("transparent");
    fireEvent.mouseLeave(bashBtn);
    expect(bashBtn.style.backgroundColor).toBe("transparent");
  });

  it("shows Continue without shortcuts and calls it", async () => {
    promptsMock.mockResolvedValue([]);
    const onContinue = vi.fn();
    const user = userEvent.setup();
    render(<TerminalShortcuts channelId="ch-1" leafId="L1" onPick={vi.fn()} onContinue={onContinue} />);
    await user.click(screen.getByRole("button", { name: "Continue" }));
    expect(onContinue).toHaveBeenCalledOnce();
    expect(screen.queryByTestId("terminal-shortcuts-btn-L1")).toBeNull();
  });

  it("clears the list when the fetch fails", async () => {
    promptsMock.mockRejectedValue(new Error("boom"));
    bashMock.mockRejectedValue(new Error("boom"));
    const { container, rerender } = render(<TerminalShortcuts channelId="ch-1" leafId="L1" onPick={vi.fn()} />);
    await waitFor(() => expect(promptsMock).toHaveBeenCalled());
    await act(async () => {});
    expect(container.innerHTML).toBe("");
    rerender(<TerminalShortcuts channelId="ch-1" leafId="L1" onPick={vi.fn()} target="host" />);
    await waitFor(() => expect(bashMock).toHaveBeenCalled());
    await act(async () => {});
    expect(container.innerHTML).toBe("");
  });

  it("refetches when the shortcuts version bumps", async () => {
    const { rerender } = render(
      <ShortcutsVersionContext.Provider value={0}>
        <TerminalShortcuts channelId="ch-1" leafId="L1" onPick={vi.fn()} />
      </ShortcutsVersionContext.Provider>,
    );
    await waitFor(() => expect(promptsMock).toHaveBeenCalledTimes(1));
    rerender(
      <ShortcutsVersionContext.Provider value={1}>
        <TerminalShortcuts channelId="ch-1" leafId="L1" onPick={vi.fn()} />
      </ShortcutsVersionContext.Provider>,
    );
    await waitFor(() => expect(promptsMock).toHaveBeenCalledTimes(2));
  });

  it("ignores fetch results that land after unmount", async () => {
    const p1 = deferred<PromptShortcut[]>();
    const p2 = deferred<PromptShortcut[]>();
    const b1 = deferred<BashShortcut[]>();
    const b2 = deferred<BashShortcut[]>();
    promptsMock.mockReturnValueOnce(p1.promise).mockReturnValueOnce(p2.promise);
    bashMock.mockReturnValueOnce(b1.promise).mockReturnValueOnce(b2.promise);
    const errSpy = vi.spyOn(console, "error");
    for (const target of ["agent", "agent", "host", "host"] as const) {
      render(<TerminalShortcuts channelId="ch-1" leafId="L1" onPick={vi.fn()} target={target} />).unmount();
    }
    await act(async () => {
      p1.resolve(prompts);
      p2.reject(new Error("late"));
      b1.resolve(bash);
      b2.reject(new Error("late"));
    });
    expect(promptsMock).toHaveBeenCalledTimes(2);
    expect(bashMock).toHaveBeenCalledTimes(2);
    expect(errSpy).not.toHaveBeenCalled();
    errSpy.mockRestore();
  });
});
