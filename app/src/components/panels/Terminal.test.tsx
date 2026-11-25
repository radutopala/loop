import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchBashShortcuts, fetchShortcuts } from "../../api/configApi";
import { fetchRoots, type RootEntry } from "../../api/files";
import { resolveGateApproval } from "../../api/loopApi";
import { useTerminalWs } from "../../hooks/useTerminalWs";
import { useXTerminal } from "../../hooks/useXTerminal";
import type { GateApprovalRequestedData } from "../../types";
import { getTerminalInput, subscribeUiChanged } from "../../uiBridge/registry";
import { uploadPastedImage } from "../../utils/clipboardImage";
import { getCloseForInstance, Terminal } from "./Terminal";

vi.mock("../../hooks/useTerminalWs", () => ({ useTerminalWs: vi.fn() }));
vi.mock("../../hooks/useXTerminal", () => ({ useXTerminal: vi.fn() }));
vi.mock("../../api/files", () => ({ fetchRoots: vi.fn() }));
vi.mock("../../api/configApi", () => ({ fetchShortcuts: vi.fn(), fetchBashShortcuts: vi.fn() }));
vi.mock("../../api/loopApi", () => ({ resolveGateApproval: vi.fn(), sendCommand: vi.fn(), sendMessage: vi.fn() }));
vi.mock("../../utils/clipboardImage", () => ({ uploadPastedImage: vi.fn() }));

const wsMock = vi.mocked(useTerminalWs);
const xtermMock = vi.mocked(useXTerminal);
const rootsMock = vi.mocked(fetchRoots);
const shortcutsMock = vi.mocked(fetchShortcuts);
const bashMock = vi.mocked(fetchBashShortcuts);
const resolveMock = vi.mocked(resolveGateApproval);
const uploadMock = vi.mocked(uploadPastedImage);

type WsOpts = Parameters<typeof useTerminalWs>[0];
type XtermOpts = Parameters<typeof useXTerminal>[0];

function makeWs() {
  return {
    connected: true,
    sendInput: vi.fn(),
    sendResize: vi.fn(),
    sendKill: vi.fn(),
    sendClose: vi.fn(),
    sendCreate: vi.fn(),
    getStartTime: vi.fn(() => undefined as number | undefined),
  };
}

function makeTerm(cursorX = 0, lines: string[] = []) {
  return {
    cols: 120,
    rows: 40,
    reset: vi.fn(),
    buffer: {
      active: {
        cursorX,
        length: lines.length,
        getLine: (y: number) => ({ translateToString: () => lines[y] ?? "" }),
      },
    },
  };
}

/** Renders a Terminal with the WebSocket and xterm hooks stubbed. */
function setup(props: Partial<Parameters<typeof Terminal>[0]> = {}, term: ReturnType<typeof makeTerm> | null = makeTerm()) {
  const ws = makeWs();
  const write = vi.fn();
  wsMock.mockReturnValue(ws);
  xtermMock.mockReturnValue({ write, xtermRef: { current: term } } as unknown as ReturnType<typeof useXTerminal>);
  const utils = render(<Terminal channelId="ch-1" instanceId="inst-1" {...props} />);
  const wsOpts = () => wsMock.mock.lastCall![0] as WsOpts;
  const xtOpts = () => xtermMock.mock.lastCall![0] as XtermOpts;
  return { ...utils, ws, write, wsOpts, xtOpts };
}

function decode(arg: unknown): string {
  return typeof arg === "string" ? arg : new TextDecoder().decode(arg as Uint8Array);
}

const roots: RootEntry[] = [
  { index: 0, path: "/work/main", name: "main" },
  { index: 1, path: "/work/extra", name: "extra" },
];

const gate: GateApprovalRequestedData = { req_id: "req-1", kind: "network", target: "api.example.com:443", message: "wants out" };

function addSlot(leafId = "inst-1") {
  const slot = document.createElement("div");
  slot.id = `pane-header-slot-${leafId}`;
  document.body.appendChild(slot);
  return slot;
}

describe("Terminal", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    rootsMock.mockResolvedValue([]);
    shortcutsMock.mockResolvedValue([]);
    bashMock.mockResolvedValue([]);
    resolveMock.mockResolvedValue(undefined);
  });

  afterEach(() => {
    vi.useRealTimers();
    for (const el of document.querySelectorAll('[id^="pane-header-slot-"]')) el.remove();
  });

  it("asks for a channel when none is selected", async () => {
    const { ws, xtOpts } = setup({ channelId: null });
    expect(screen.getByText("Select a channel to open terminal")).toBeTruthy();
    expect(rootsMock).not.toHaveBeenCalled();
    // A pasted image has nowhere to go without a channel.
    await xtOpts().onPasteImage!(new File(["x"], "a.png"));
    expect(uploadMock).not.toHaveBeenCalled();
    expect(ws.sendInput).not.toHaveBeenCalled();
  });

  it("passes the session options and the terminal size to the socket", () => {
    const { wsOpts, xtOpts, ws } = setup({ target: "agent", claudeSessionId: "s-1", newSession: true, openMode: "fork" });
    const opts = wsOpts();
    expect(opts).toMatchObject({ channelId: "ch-1", target: "agent", instanceId: "inst-1", claudeSessionId: "s-1", newSession: true, rootIndex: 0 });
    expect(opts.getTerminalSize!()).toEqual({ cols: 120, rows: 40 });
    expect(xtOpts().onInput).toBe(ws.sendInput);
    expect(xtOpts().onResize).toBe(ws.sendResize);
  });

  it("reports no terminal size before xterm mounts", () => {
    const { wsOpts } = setup({}, null);
    expect(wsOpts().getTerminalSize!()).toBeNull();
  });

  it("runs, stops and restarts a session from the toolbar", async () => {
    const onStatusChange = vi.fn();
    const onPaneStatus = vi.fn();
    const onSessionEnd = vi.fn();
    const user = userEvent.setup();
    const { ws, write, wsOpts } = setup({ onStatusChange, onPaneStatus, onSessionEnd });
    const stop = screen.getByRole("button", { name: "Stop" });
    expect(stop.title).toBe("Stop container and end session");
    expect((stop as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText("connecting")).toBeTruthy();

    ws.getStartTime.mockReturnValue(Date.now() - 5000);
    act(() => wsOpts().onStatus("running"));
    expect(ws.getStartTime).toHaveBeenCalled();
    expect(onStatusChange).toHaveBeenCalledTimes(1);
    expect(onPaneStatus).toHaveBeenLastCalledWith("running");
    expect(screen.getByText("running")).toBeTruthy();
    // Hovering the badge shows the time elapsed since the session's start.
    fireEvent.mouseEnter(screen.getByText("running"));
    expect(screen.getByText("00:00:05")).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Stop" }));
    expect(ws.sendKill).toHaveBeenCalledOnce();

    act(() => wsOpts().onStatus("completed"));
    expect(write).toHaveBeenCalledWith("\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l");
    expect(onSessionEnd).toHaveBeenCalledOnce();
    expect(onPaneStatus).toHaveBeenLastCalledWith("completed");

    await user.click(screen.getByRole("button", { name: "Restart" }));
    expect(ws.sendCreate).toHaveBeenCalledOnce();
    expect(screen.queryByText("00:00:05")).toBeNull();

    act(() => wsOpts().onStatus("failed"));
    expect(onSessionEnd).toHaveBeenCalledTimes(2);
    expect(screen.getByText("failed")).toBeTruthy();
  });

  it("works without status callbacks", () => {
    const { wsOpts, write } = setup();
    act(() => wsOpts().onStatus("completed"));
    expect(write).toHaveBeenCalledOnce();
    expect(screen.getByRole("button", { name: "Restart" })).toBeTruthy();
  });

  it("labels the kill button for host and chat shells", () => {
    const { unmount } = setup({ target: "host" });
    expect(screen.getByRole("button", { name: "Close" }).title).toBe("Close shell session");
    unmount();
    setup({ target: "chat", uiPane: "shell" });
    expect(screen.getByRole("button", { name: "Stop" }).title).toBe("End the shell (the chat's container keeps running)");
  });

  it("shows the status in the pane header when the parent owns the actions", async () => {
    addSlot();
    setup({ hideActions: true });
    expect(await screen.findByText("Connecting")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Stop" })).toBeNull();
  });

  it("writes socket errors in red", () => {
    const { wsOpts, write } = setup();
    wsOpts().onError("connection lost");
    expect(decode(write.mock.lastCall![0])).toBe("\r\n\x1b[31m[error] connection lost\x1b[0m\r\n");
  });

  it("starts a new session's output on a fresh line", () => {
    const { wsOpts, write, unmount } = setup({}, makeTerm(5));
    wsOpts().onCreated!();
    expect(write).toHaveBeenCalledWith("\r\n");
    unmount();

    const atStart = setup({}, makeTerm(0));
    atStart.wsOpts().onCreated!();
    expect(atStart.write).not.toHaveBeenCalled();
    atStart.unmount();

    const noTerm = setup({}, null);
    noTerm.wsOpts().onCreated!();
    expect(noTerm.write).not.toHaveBeenCalled();
  });

  it("writes output and reports busy until the terminal goes quiet", () => {
    vi.useFakeTimers();
    const changed = vi.fn();
    const unsub = subscribeUiChanged(changed);
    const { wsOpts, write } = setup();
    const input = getTerminalInput("ch-1", "inst-1")!;
    expect(input.busy()).toBe(false);
    expect(input.lastOutputAt()).toBe(0);

    const bytes = new TextEncoder().encode("hello").buffer;
    act(() => wsOpts().onData(bytes));
    expect(decode(write.mock.lastCall![0])).toBe("hello");
    expect(input.busy()).toBe(true);
    expect(input.lastOutputAt()).toBe(Date.now());
    expect(changed).toHaveBeenCalledTimes(1);

    // More output while busy restarts the idle window without a new notice.
    vi.advanceTimersByTime(1000);
    act(() => wsOpts().onData(bytes));
    expect(changed).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(1000);
    expect(input.busy()).toBe(true);
    vi.advanceTimersByTime(500);
    expect(input.busy()).toBe(false);
    expect(changed).toHaveBeenCalledTimes(2);
    unsub();
  });

  it("clears a pending busy timer on unmount", () => {
    vi.useFakeTimers();
    const changed = vi.fn();
    const unsub = subscribeUiChanged(changed);
    const { wsOpts, unmount } = setup();
    act(() => wsOpts().onData(new ArrayBuffer(1)));
    unmount();
    vi.advanceTimersByTime(5000);
    expect(changed).toHaveBeenCalledTimes(1);
    unsub();
  });

  it("registers the agent TUI for UI commands", () => {
    const { ws, unmount, wsOpts } = setup({}, makeTerm(0, ["one", "two", "three", ""]));
    const input = getTerminalInput("ch-1", "inst-1")!;
    expect(input.kind).toBe("agent");
    expect(input.status()).toBe("connecting");
    act(() => wsOpts().onStatus("running"));
    expect(input.status()).toBe("running");
    expect(input.read(2)).toBe("two\nthree");
    input.send("ls\r");
    expect(ws.sendInput).toHaveBeenCalledWith("ls\r");
    unmount();
    expect(getTerminalInput("ch-1", "inst-1")).toBeUndefined();
  });

  it("reads nothing before xterm mounts", () => {
    setup({}, null);
    expect(getTerminalInput("ch-1", "inst-1")!.read(5)).toBe("");
  });

  it("registers a container shell as a shell and a chat shell by its UI pane", () => {
    const { unmount } = setup({ cmd: ["bash"] });
    expect(getTerminalInput("ch-1", "inst-1")!.kind).toBe("shell");
    unmount();
    setup({ target: "chat", uiPane: "chat-shell", instanceId: undefined, cmd: [] });
    expect(getTerminalInput("ch-1", "chat-shell")!.kind).toBe("agent");
  });

  it("doesn't register a host shell or a pane without an id", () => {
    const { unmount } = setup({ target: "host" });
    expect(getTerminalInput("ch-1", "inst-1")).toBeUndefined();
    unmount();
    setup({ instanceId: undefined });
    expect(getTerminalInput("ch-1", "undefined")).toBeUndefined();
  });

  it("exposes sendClose for its instance until it unmounts", () => {
    const { ws, unmount } = setup({ target: "host" });
    getCloseForInstance("host:ch-1:inst-1")!();
    expect(ws.sendClose).toHaveBeenCalledOnce();
    unmount();
    expect(getCloseForInstance("host:ch-1:inst-1")).toBeUndefined();
  });

  it("kills the session when the kill signal increases", () => {
    const ws = makeWs();
    wsMock.mockReturnValue(ws);
    xtermMock.mockReturnValue({ write: vi.fn(), xtermRef: { current: null } } as unknown as ReturnType<typeof useXTerminal>);
    const { rerender } = render(<Terminal channelId="ch-1" instanceId="inst-1" killSignal={1} />);
    expect(ws.sendKill).not.toHaveBeenCalled();
    rerender(<Terminal channelId="ch-1" instanceId="inst-1" killSignal={2} />);
    expect(ws.sendKill).toHaveBeenCalledOnce();
    rerender(<Terminal channelId="ch-1" instanceId="inst-1" killSignal={1} />);
    rerender(<Terminal channelId="ch-1" instanceId="inst-1" />);
    expect(ws.sendKill).toHaveBeenCalledOnce();
  });

  it("pastes an uploaded image's path as typed input", async () => {
    uploadMock.mockResolvedValue("/work/.loop/paste.png");
    const { ws, xtOpts } = setup();
    const file = new File(["x"], "paste.png", { type: "image/png" });
    await xtOpts().onPasteImage!(file);
    expect(uploadMock).toHaveBeenCalledWith("ch-1", file);
    expect(ws.sendInput).toHaveBeenCalledWith("/work/.loop/paste.png");
  });

  it("pastes a prompt shortcut into the agent TUI and continues a running session", async () => {
    shortcutsMock.mockResolvedValue([{ name: "review", description: "Review", prompt: "review this", scope: "global" }]);
    const user = userEvent.setup();
    const { ws, wsOpts } = setup();
    await user.click(await screen.findByTestId("terminal-shortcuts-btn-inst-1"));
    fireEvent.mouseDown(screen.getByText("#review"));
    expect(ws.sendInput).toHaveBeenCalledWith("\x1b[200~review this\x1b[201~\r");

    expect(screen.queryByRole("button", { name: "Continue" })).toBeNull();
    act(() => wsOpts().onStatus("running"));
    await user.click(screen.getByRole("button", { name: "Continue" }));
    expect(ws.sendInput).toHaveBeenLastCalledWith("\x1b[200~continue\x1b[201~\r");
  });

  it("runs a bash shortcut in a shell with a newline", async () => {
    bashMock.mockResolvedValue([{ name: "ls", description: "List", command: "ls -la" } as never]);
    const user = userEvent.setup();
    const { ws } = setup({ cmd: ["bash"] });
    await user.click(await screen.findByTestId("terminal-bash-shortcuts-btn-inst-1"));
    fireEvent.mouseDown(screen.getByText("$ls"));
    expect(ws.sendInput).toHaveBeenCalledWith("ls -la\n");
    expect(shortcutsMock).not.toHaveBeenCalled();
  });

  it("renders no shortcut footer without an instance id", () => {
    setup({ instanceId: undefined });
    expect(shortcutsMock).not.toHaveBeenCalled();
  });

  it("lets a shell switch workspace root and re-creates the session there", async () => {
    addSlot();
    rootsMock.mockResolvedValue(roots);
    const term = makeTerm();
    const user = userEvent.setup();
    const { ws, wsOpts } = setup({ target: "host" }, term);
    expect(rootsMock).toHaveBeenCalledWith("ch-1");
    const picker = await screen.findByTestId("terminal-root-select");
    expect(picker.title).toBe("Workspace root the shell opens in\n/work/main");
    expect(ws.sendCreate).not.toHaveBeenCalled();

    await user.click(picker);
    await user.click(screen.getByRole("menuitemradio", { name: /extra/ }));
    await waitFor(() => expect(wsOpts().rootIndex).toBe(1));
    expect(term.reset).toHaveBeenCalledOnce();
    expect(ws.sendCreate).toHaveBeenCalledOnce();
  });

  it("hides the root picker with a single root or a failed fetch", async () => {
    addSlot();
    rootsMock.mockResolvedValueOnce(roots.slice(0, 1));
    const { unmount } = setup({ cmd: ["bash"] });
    await act(async () => {});
    expect(screen.queryByTestId("terminal-root-select")).toBeNull();
    unmount();

    rootsMock.mockRejectedValueOnce(new Error("nope"));
    setup({ target: "host" });
    await act(async () => {});
    expect(rootsMock).toHaveBeenCalledTimes(2);
    expect(screen.queryByTestId("terminal-root-select")).toBeNull();
  });

  it("doesn't fetch roots for the agent TUI", () => {
    setup();
    expect(rootsMock).not.toHaveBeenCalled();
  });

  it("ignores roots that arrive after unmount", async () => {
    let resolveRoots!: (r: RootEntry[]) => void;
    let rejectRoots!: (e: unknown) => void;
    rootsMock.mockReturnValueOnce(new Promise((res) => (resolveRoots = res))).mockReturnValueOnce(new Promise((_, rej) => (rejectRoots = rej)));
    const errSpy = vi.spyOn(console, "error");
    setup({ target: "host" }).unmount();
    setup({ target: "host" }).unmount();
    await act(async () => {
      resolveRoots(roots);
      rejectRoots(new Error("late"));
    });
    expect(errSpy).not.toHaveBeenCalled();
    errSpy.mockRestore();
  });

  describe("gate approval overlay", () => {
    const denyWithPrompt = async (user: ReturnType<typeof userEvent.setup>, text: string) => {
      await user.click(screen.getByRole("button", { name: "More deny options" }));
      await user.click(screen.getByRole("menuitem", { name: "Deny with prompt…" }));
      await user.type(screen.getByPlaceholderText(/Tell the agent what to do instead/), `${text}{Enter}`);
    };

    it("overlays the approval card and resolves through it", async () => {
      const onGateApprovalResolved = vi.fn();
      const user = userEvent.setup();
      setup({ gateApproval: gate, onGateApprovalResolved });
      expect(screen.getByTestId("pane-gate-overlay")).toBeTruthy();
      await user.click(screen.getByRole("button", { name: "Allow once" }));
      await waitFor(() => expect(onGateApprovalResolved).toHaveBeenCalledOnce());
      expect(resolveMock).toHaveBeenCalledWith("req-1", "once");
    });

    it("pastes a deny prompt into the pane and submits it once output goes quiet", async () => {
      const user = userEvent.setup();
      const { ws, wsOpts } = setup({ gateApproval: gate, onGateApprovalResolved: vi.fn() });
      await denyWithPrompt(user, "use the mirror");
      await waitFor(() => expect(ws.sendInput).toHaveBeenCalledWith("\x1b[200~use the mirror\x1b[201~"));
      expect(resolveMock).toHaveBeenCalledWith("req-1", "deny");

      vi.useFakeTimers();
      act(() => wsOpts().onData(new ArrayBuffer(1)));
      vi.advanceTimersByTime(300);
      // More output pushes the submit back.
      act(() => wsOpts().onData(new ArrayBuffer(1)));
      vi.advanceTimersByTime(300);
      expect(ws.sendInput).not.toHaveBeenCalledWith("\r");
      vi.advanceTimersByTime(100);
      expect(ws.sendInput).toHaveBeenLastCalledWith("\r");

      // The submit fires once; later output doesn't re-send it.
      act(() => wsOpts().onData(new ArrayBuffer(1)));
      vi.advanceTimersByTime(1000);
      expect(ws.sendInput.mock.calls.filter((c) => c[0] === "\r")).toHaveLength(1);
    });

    it("drops a pending deny submit when the pane unmounts", async () => {
      const user = userEvent.setup();
      const { ws, wsOpts, unmount } = setup({ gateApproval: gate });
      await denyWithPrompt(user, "stop");
      await waitFor(() => expect(ws.sendInput).toHaveBeenCalledOnce());
      vi.useFakeTimers();
      act(() => wsOpts().onData(new ArrayBuffer(1)));
      unmount();
      vi.advanceTimersByTime(1000);
      expect(ws.sendInput).toHaveBeenCalledOnce();
    });
  });
});
