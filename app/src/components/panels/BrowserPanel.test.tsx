import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { TabInfo } from "../../hooks/useBrowserWs";
import { ThemeProvider } from "../../ThemeContext";
import { BROWSER_OPEN_EVENT, browserOpenQueue } from "../../utils/browserOpen";
import { BrowserPanel } from "./BrowserPanel";
import type { PageContextMenu } from "./browserContextMenu";
import type { PageDialog, PageDialogs } from "./browserDialog";
import type { DownloadList, PageDownload } from "./browserDownloads";
import type { ChosenFile, PageFileChooser } from "./browserFileChooser";
import type { PageSelectMenu } from "./browserSelectMenu";

const mockSwitchBrowserMode = vi.fn<() => Promise<{ mode?: string }>>();
const mockResetBrowserProfile = vi.fn<() => Promise<void>>();
const mockDrawFrame = vi.fn<(data: ArrayBuffer) => void>();
const mockReadChosenFiles = vi.fn<(files: readonly File[]) => Promise<ChosenFile[]>>();

vi.mock("../../api/loopApi", () => ({
  switchBrowserMode: (...args: unknown[]) => mockSwitchBrowserMode(...(args as [])),
  resetBrowserProfile: (...args: unknown[]) => mockResetBrowserProfile(...(args as [])),
}));

vi.mock("./browserStream", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./browserStream")>();
  return {
    ...actual,
    createFrameRenderer: (getCanvas: () => HTMLCanvasElement | null) => (data: ArrayBuffer) => {
      getCanvas();
      mockDrawFrame(data);
    },
    // Flush immediately in tests so mousemove/scroll don't wait on rAF
    createInputCoalescer: (send: (ev: unknown) => void) => actual.createInputCoalescer(send as never, (cb) => cb()),
  };
});

vi.mock("./browserFileChooser", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./browserFileChooser")>();
  return {
    ...actual,
    readChosenFiles: (files: readonly File[]) => mockReadChosenFiles(files),
  };
});

vi.mock("./CookieImportDialog", () => ({
  CookieImportDialog: ({ onClose, onImported }: { channelId: string; onClose: () => void; onImported: (count: number) => void }) => (
    <div data-testid="mock-cookie-dialog">
      <button type="button" onClick={onClose}>
        Close Cookie Dialog
      </button>
      <button type="button" onClick={() => onImported(1)}>
        Import One
      </button>
      <button type="button" onClick={() => onImported(3)}>
        Import Three
      </button>
    </div>
  ),
}));

interface CapturedCallbacks {
  onFrame?: (data: ArrayBuffer) => void;
  onPageInfo?: (url: string, title: string) => void;
  onError?: (msg: string) => void;
  onStarted?: () => void;
  onContextMenu?: (menu: PageContextMenu) => void;
  onSelectMenu?: (menu: PageSelectMenu) => void;
  onDownload?: (d: PageDownload) => void;
  onDownloadList?: (list: DownloadList) => void;
}

interface WsMockState {
  connected: boolean;
  started: boolean;
  tabs: TabInfo[];
  activeTargetId: string;
  dialog: PageDialog | null;
  dialogs: PageDialogs;
  fileChooser: PageFileChooser | null;
}

let capturedCallbacks: CapturedCallbacks = {};
let wsState: WsMockState;
const wsActions = {
  startBrowser: vi.fn(),
  stopBrowser: vi.fn(),
  startStreaming: vi.fn(),
  navigate: vi.fn(),
  reload: vi.fn(),
  goBack: vi.fn(),
  goForward: vi.fn(),
  sendInput: vi.fn(),
  switchTab: vi.fn(),
  newTab: vi.fn(),
  closeTab: vi.fn(),
  answerDialog: vi.fn(),
  answerFileChooser: vi.fn(),
  chooseSelectOption: vi.fn(),
  listDownloads: vi.fn(),
};

vi.mock("../../hooks/useBrowserWs", () => ({
  useBrowserWs: (opts: CapturedCallbacks & { channelId: string }) => {
    capturedCallbacks = opts;
    return {
      ...wsState,
      ...wsActions,
    };
  },
}));

const SAFE_ICON = "data:image/png;base64,AAAA";
const SAFE_ICON_2 = "data:image/png;base64,BBBB";

function resetWsState(over: Partial<WsMockState> = {}) {
  wsState = {
    connected: true,
    started: true,
    tabs: [
      { target_id: "t1", url: "https://example.com", title: "Example", favicon_url: SAFE_ICON },
      { target_id: "t2", url: "https://two.test", title: "", favicon_url: "javascript:alert(1)" },
      { target_id: "t3", url: "", title: "" },
    ],
    activeTargetId: "t1",
    dialog: null,
    dialogs: {},
    fileChooser: null,
    ...over,
  };
}

function renderPanel(props: Partial<Parameters<typeof BrowserPanel>[0]> = {}, theme: "dark" | "light" = "dark") {
  return render(
    <ThemeProvider initialTheme={theme}>
      <BrowserPanel channelId="ch-1" {...props} />
    </ThemeProvider>,
  );
}

describe("BrowserPanel", () => {
  beforeEach(() => {
    localStorage.clear();
    capturedCallbacks = {};
    resetWsState();
    for (const fn of Object.values(wsActions)) fn.mockReset();
    mockSwitchBrowserMode.mockReset();
    mockResetBrowserProfile.mockReset();
    mockDrawFrame.mockReset();
    mockReadChosenFiles.mockReset();
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: {
        readText: vi.fn().mockResolvedValue("pasted text"),
        writeText: vi.fn().mockResolvedValue(undefined),
      },
    });
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("shows Connecting... when disconnected and auto-starts when connected", () => {
    resetWsState({ connected: false, started: false });
    const { rerender } = renderPanel();
    expect(screen.getByText("Connecting...")).toBeTruthy();
    expect(wsActions.startBrowser).not.toHaveBeenCalled();

    resetWsState({ connected: true, started: false });
    rerender(
      <ThemeProvider initialTheme="dark">
        <BrowserPanel channelId="ch-1" />
      </ThemeProvider>,
    );
    expect(screen.getByText("Starting browser...")).toBeTruthy();
    expect(wsActions.startBrowser).toHaveBeenCalledWith("docker");
  });

  it("starts streaming when started, invokes onFrame/onPageInfo/onError/onStarted callbacks", () => {
    resetWsState({ tabs: [], activeTargetId: "" });
    renderPanel();
    expect(wsActions.startStreaming).toHaveBeenCalledWith(1920, 1080);

    const buf = new ArrayBuffer(4);
    act(() => {
      capturedCallbacks.onFrame?.(buf);
      capturedCallbacks.onPageInfo?.("https://loaded.test", "Loaded");
      capturedCallbacks.onError?.("boom");
    });
    expect(mockDrawFrame).toHaveBeenCalledWith(buf);
    expect((screen.getByPlaceholderText("Search or enter URL") as HTMLInputElement).value).toBe("https://loaded.test");
    expect(screen.getByText("boom")).toBeTruthy();

    act(() => {
      capturedCallbacks.onStarted?.();
    });
    expect(screen.queryByText("boom")).toBeNull();
    expect((screen.getByPlaceholderText("Search or enter URL") as HTMLInputElement).value).toBe("");
  });

  it("handles browserOpenQueue initial links and BROWSER_OPEN_EVENT filtering", () => {
    browserOpenQueue.request(window, "ch-1", "docker", "https://queued.test");
    renderPanel();
    expect(wsActions.newTab).toHaveBeenCalledWith("https://queued.test");

    wsActions.newTab.mockClear();
    // Non-matching event (wrong channel / mode)
    window.dispatchEvent(new CustomEvent(BROWSER_OPEN_EVENT, { detail: { channelId: "other", mode: "docker" } }));
    window.dispatchEvent(new CustomEvent(BROWSER_OPEN_EVENT, { detail: { channelId: "ch-1", mode: "host" } }));
    expect(wsActions.newTab).not.toHaveBeenCalled();

    // Matching event
    browserOpenQueue.request(window, "ch-1", "docker", "https://live.test");
    expect(wsActions.newTab).toHaveBeenCalledWith("https://live.test");
  });

  it("handles URL bar focus, submit, empty submit, activeElement guard, and nav buttons", () => {
    resetWsState({ tabs: [], activeTargetId: "" });
    renderPanel();
    const input = screen.getByPlaceholderText("Search or enter URL") as HTMLInputElement;

    const selectSpy = vi.spyOn(input, "select");
    fireEvent.focus(input);
    expect(selectSpy).toHaveBeenCalled();

    // While focused, displayUrl changes don't overwrite user typing
    input.focus();
    input.value = "https://typing.test";
    act(() => {
      capturedCallbacks.onPageInfo?.("https://ignored.test", "Ignored");
    });
    expect(input.value).toBe("https://typing.test");

    // Empty submit does nothing
    input.value = "   ";
    fireEvent.submit(input.closest("form")!);
    expect(wsActions.navigate).not.toHaveBeenCalled();

    // Valid submit navigates
    input.value = "example.org";
    fireEvent.submit(input.closest("form")!);
    expect(wsActions.navigate).toHaveBeenCalledWith("https://example.org");

    fireEvent.click(screen.getByTitle("Back"));
    expect(wsActions.goBack).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByTitle("Forward"));
    expect(wsActions.goForward).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByTitle("Reload"));
    expect(wsActions.reload).toHaveBeenCalledTimes(1);
  });

  it("respects fixedMode and saved browserMode, and toggles between docker and host modes", async () => {
    vi.useFakeTimers();

    // fixedMode="host" hides ModePill and tools menu
    const { unmount } = renderPanel({ fixedMode: "host" });
    expect(screen.queryByTitle(/Switch to/)).toBeNull();
    expect(screen.queryByLabelText("Browser menu")).toBeNull();
    unmount();

    // Saved host mode in localStorage
    localStorage.setItem("browserMode:ch-1", "host");
    const { unmount: unmount2 } = renderPanel();
    expect(screen.getByTitle("Switch to docker browser")).toBeTruthy();
    expect(screen.queryByLabelText("Browser menu")).toBeNull();

    // Toggle host -> docker when switchBrowserMode returns mode
    mockSwitchBrowserMode.mockResolvedValueOnce({ mode: "docker" });
    fireEvent.click(screen.getByTitle("Switch to docker browser"));
    expect(wsActions.stopBrowser).toHaveBeenCalledTimes(1);
    await act(async () => {
      await Promise.resolve();
    });
    expect(localStorage.getItem("browserMode:ch-1")).toBe("docker");
    expect(screen.getByTitle("Switch to host browser")).toBeTruthy();
    act(() => {
      vi.advanceTimersByTime(500);
    });
    expect(wsActions.startBrowser).toHaveBeenCalledWith("docker");

    // Toggle docker -> host when switchBrowserMode returns no mode (no-op)
    mockSwitchBrowserMode.mockResolvedValueOnce({});
    fireEvent.click(screen.getByTitle("Switch to host browser"));
    await act(async () => {
      await Promise.resolve();
    });
    expect(screen.getByTitle("Switch to host browser")).toBeTruthy();
    unmount2();
  });

  it("handles tools menu: toggle, close, cookie import (1 and >1 cookies), and profile reset", async () => {
    vi.useFakeTimers();
    mockResetBrowserProfile.mockResolvedValue(undefined);
    renderPanel();

    const menuBtn = screen.getByLabelText("Browser menu");
    // Open then toggle closed by clicking button again
    fireEvent.click(menuBtn);
    expect(screen.getByText("Import cookies…")).toBeTruthy();
    fireEvent.click(menuBtn);
    expect(screen.queryByText("Import cookies…")).toBeNull();

    // Open and close via Escape
    fireEvent.click(menuBtn);
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByText("Import cookies…")).toBeNull();

    // Open cookie import dialog, close it
    fireEvent.click(menuBtn);
    fireEvent.click(screen.getByText("Import cookies…"));
    expect(screen.getByTestId("mock-cookie-dialog")).toBeTruthy();
    fireEvent.click(screen.getByText("Close Cookie Dialog"));
    expect(screen.queryByTestId("mock-cookie-dialog")).toBeNull();

    // Import 1 cookie -> singular notice, clears after 4000ms
    fireEvent.click(menuBtn);
    fireEvent.click(screen.getByText("Import cookies…"));
    fireEvent.click(screen.getByText("Import One"));
    expect(wsActions.reload).toHaveBeenCalledTimes(1);
    expect(screen.getByText("Imported 1 cookie")).toBeTruthy();
    act(() => {
      vi.advanceTimersByTime(4000);
    });
    expect(screen.queryByText("Imported 1 cookie")).toBeNull();

    // Import 3 cookies -> plural notice
    fireEvent.click(menuBtn);
    fireEvent.click(screen.getByText("Import cookies…"));
    fireEvent.click(screen.getByText("Import Three"));
    expect(screen.getByText("Imported 3 cookies")).toBeTruthy();

    // Reset profile (confirm popover -> Yes)
    fireEvent.click(menuBtn);
    fireEvent.click(screen.getByText("Reset profile"));
    fireEvent.click(screen.getByText("Yes"));
    expect(wsActions.stopBrowser).toHaveBeenCalledTimes(1);
    expect(mockResetBrowserProfile).toHaveBeenCalledWith("ch-1");
    await act(async () => {
      await Promise.resolve();
    });
    act(() => {
      vi.advanceTimersByTime(500);
    });
    expect(wsActions.startBrowser).toHaveBeenCalledWith("docker");
  });

  it("handles canvas mouse, wheel, and keyboard events including copy/cut/paste and clipboard errors", async () => {
    renderPanel();
    const canvas = document.querySelector("canvas")!;
    canvas.width = 1920;
    canvas.height = 1080;
    vi.spyOn(canvas, "getBoundingClientRect").mockReturnValue({
      left: 10,
      top: 20,
      width: 960,
      height: 540,
      right: 970,
      bottom: 560,
      x: 10,
      y: 20,
      toJSON: () => {},
    });

    fireEvent.click(canvas, { clientX: 110, clientY: 120 });
    expect(wsActions.sendInput).toHaveBeenCalledWith({ type: "click", x: 200, y: 200, button: "left", clickCount: 1 });
    expect(document.activeElement).toBe(canvas);

    fireEvent.doubleClick(canvas, { clientX: 60, clientY: 70 });
    expect(wsActions.sendInput).toHaveBeenCalledWith({ type: "click", x: 100, y: 100, button: "left", clickCount: 2 });

    fireEvent.contextMenu(canvas, { clientX: 50, clientY: 60 });
    expect(wsActions.sendInput).toHaveBeenCalledWith({ type: "contextmenu", x: 80, y: 80 });

    fireEvent.mouseMove(canvas, { clientX: 30, clientY: 40 });
    expect(wsActions.sendInput).toHaveBeenCalledWith({ type: "mousemove", x: 40, y: 40 });

    fireEvent.wheel(canvas, { clientX: 20, clientY: 30, deltaX: 5, deltaY: -15 });
    expect(wsActions.sendInput).toHaveBeenCalledWith({ type: "scroll", x: 20, y: 20, deltaX: 5, deltaY: -15 });

    // Printable character -> typetext
    wsActions.sendInput.mockClear();
    fireEvent.keyDown(canvas, { key: "a" });
    expect(wsActions.sendInput).toHaveBeenCalledWith({ type: "typetext", text: "a" });

    // Special key with modifiers -> keypress
    fireEvent.keyDown(canvas, { key: "Enter", altKey: true, shiftKey: true });
    expect(wsActions.sendInput).toHaveBeenCalledWith({ type: "keypress", key: "Enter", modifiers: 9 });

    // Ctrl+A -> keypress with ctrl modifier
    fireEvent.keyDown(canvas, { key: "a", ctrlKey: true });
    expect(wsActions.sendInput).toHaveBeenCalledWith({ type: "keypress", key: "a", modifiers: 2 });

    // Meta+C -> copy
    wsActions.sendInput.mockClear();
    fireEvent.keyDown(canvas, { key: "c", metaKey: true });
    expect(wsActions.sendInput).toHaveBeenCalledWith({ type: "copy" });
    expect(wsActions.sendInput).toHaveBeenCalledTimes(1);

    // Ctrl+X -> copy then keypress x
    wsActions.sendInput.mockClear();
    fireEvent.keyDown(canvas, { key: "X", ctrlKey: true });
    expect(wsActions.sendInput).toHaveBeenNthCalledWith(1, { type: "copy" });
    expect(wsActions.sendInput).toHaveBeenNthCalledWith(2, { type: "keypress", key: "x", modifiers: 2 });

    // Ctrl+V -> paste with text
    wsActions.sendInput.mockClear();
    fireEvent.keyDown(canvas, { key: "v", ctrlKey: true });
    await act(async () => {
      await Promise.resolve();
    });
    expect(wsActions.sendInput).toHaveBeenCalledWith({ type: "paste", text: "pasted text" });

    // Ctrl+V -> empty clipboard does nothing
    wsActions.sendInput.mockClear();
    vi.mocked(navigator.clipboard.readText).mockResolvedValueOnce("");
    fireEvent.keyDown(canvas, { key: "v", ctrlKey: true });
    await act(async () => {
      await Promise.resolve();
    });
    expect(wsActions.sendInput).not.toHaveBeenCalled();

    // Ctrl+V -> clipboard read rejected shows error
    vi.mocked(navigator.clipboard.readText).mockRejectedValueOnce(new Error("denied"));
    fireEvent.keyDown(canvas, { key: "v", ctrlKey: true });
    await act(async () => {
      await Promise.resolve();
    });
    expect(screen.getByText("Clipboard read was blocked, so paste is unavailable.")).toBeTruthy();
  });

  it("handles page context menu actions and select menu", async () => {
    renderPanel();

    // Trigger page context menu with link, selection, editable
    act(() => {
      capturedCallbacks.onContextMenu?.({
        link: "https://link.test",
        selection: "hello",
        editable: true,
      });
    });

    fireEvent.click(screen.getByText("Open link in new tab"));
    expect(wsActions.newTab).toHaveBeenCalledWith("https://link.test");

    // Reopen and test Copy (success)
    act(() => {
      capturedCallbacks.onContextMenu?.({ selection: "copy-me" });
    });
    fireEvent.click(screen.getByText("Copy"));
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith("copy-me");

    // Reopen and test Copy (rejected)
    vi.mocked(navigator.clipboard.writeText).mockRejectedValueOnce(new Error("blocked"));
    act(() => {
      capturedCallbacks.onContextMenu?.({ selection: "fail-copy" });
    });
    fireEvent.click(screen.getByText("Copy"));
    await act(async () => {
      await Promise.resolve();
    });
    expect(screen.getByText("Clipboard write was blocked, so copy is unavailable.")).toBeTruthy();

    // Reopen and close via Escape
    act(() => {
      capturedCallbacks.onContextMenu?.({});
    });
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByText("Reload")).toBeNull();

    // Select menu: choose option & close
    act(() => {
      capturedCallbacks.onSelectMenu?.({
        selected: 0,
        options: [{ label: "Alpha" }, { label: "Beta" }],
      });
    });
    fireEvent.click(screen.getByText("Beta"));
    expect(wsActions.chooseSelectOption).toHaveBeenCalledWith(1);

    act(() => {
      capturedCallbacks.onSelectMenu?.({
        selected: 0,
        options: [{ label: "Alpha" }],
      });
    });
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByText("Alpha")).toBeNull();
  });

  it("handles download toasts and downloads list (loading, empty, files, copy path, refresh on new download, close)", async () => {
    renderPanel();

    // Receive a download while list is closed -> does NOT call listDownloads
    act(() => {
      capturedCallbacks.onDownload?.({ guid: "g1", filename: "report.pdf", url: "https://example.com/report.pdf", state: "completed" });
    });
    expect(wsActions.listDownloads).not.toHaveBeenCalled();
    expect(screen.getByText("Downloaded report.pdf")).toBeTruthy();

    // Click toast -> opens downloads list (loading state) and calls listDownloads
    fireEvent.click(screen.getByText("Downloaded report.pdf"));
    expect(wsActions.listDownloads).toHaveBeenCalledTimes(1);
    expect(screen.getByText("Loading…")).toBeTruthy();

    // Another download arrives while list is open -> calls listDownloads again
    act(() => {
      capturedCallbacks.onDownload?.({ guid: "g2", filename: "data.csv", state: "started" });
    });
    expect(wsActions.listDownloads).toHaveBeenCalledTimes(2);

    // Dismiss toast g1
    const dismissButtons = screen.getAllByLabelText("Dismiss");
    fireEvent.click(dismissButtons[0]!);
    expect(screen.queryByText("Downloaded report.pdf")).toBeNull();

    // Empty download list
    act(() => {
      capturedCallbacks.onDownloadList?.({ dir: "/downloads", files: [] });
    });
    expect(screen.getByText("Nothing downloaded yet")).toBeTruthy();

    // Populated download list + copy path
    act(() => {
      capturedCallbacks.onDownloadList?.({
        dir: "/downloads",
        files: [{ name: "report.pdf", path: "/downloads/report.pdf", size: 2048, mod_time: "2026-01-01T00:00:00Z" }],
      });
    });
    const fileBtn = screen.getByTitle("Copy /downloads/report.pdf");
    fireEvent.click(fileBtn);
    await act(async () => {
      await Promise.resolve();
    });
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith("/downloads/report.pdf");
    expect(screen.getByText("path copied")).toBeTruthy();

    // Close downloads list
    fireEvent.click(screen.getByLabelText("Close downloads"));
    expect(screen.queryByTestId("browser-downloads-list")).toBeNull();
  });

  it("handles confirm and prompt dialogs (accept, dismiss, Enter, Escape, light theme)", () => {
    // Confirm dialog without message
    resetWsState({
      dialog: { target_id: "t1", type: "confirm", message: "" },
    });
    const { unmount } = renderPanel({}, "dark");
    const bar = screen.getByTestId("browser-dialog");
    // Non-Escape key does nothing
    fireEvent.keyDown(bar, { key: "a" });
    expect(wsActions.answerDialog).not.toHaveBeenCalled();
    fireEvent.keyDown(bar, { key: "Escape" });
    expect(wsActions.answerDialog).toHaveBeenCalledWith("t1", false, undefined);

    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(wsActions.answerDialog).toHaveBeenCalledWith("t1", false, undefined);
    fireEvent.click(screen.getByRole("button", { name: "OK" }));
    expect(wsActions.answerDialog).toHaveBeenCalledWith("t1", true, undefined);

    // Prompt dialog in dark theme (without default_prompt)
    wsActions.answerDialog.mockClear();
    unmount();
    resetWsState({
      dialog: { target_id: "t1", type: "prompt", message: "Dark prompt?" },
    });
    const { unmount: unmountDark } = renderPanel({}, "dark");
    expect(screen.getByText("Dark prompt?")).toBeTruthy();
    unmountDark();

    // Prompt dialog with message and default_prompt in light theme (fresh ThemeProvider mount)
    resetWsState({
      dialog: { target_id: "t1", type: "prompt", message: "Your name?", default_prompt: "Alice" },
    });
    renderPanel({}, "light");
    expect(screen.getByText("Your name?")).toBeTruthy();
    const promptInput = screen.getByDisplayValue("Alice") as HTMLInputElement;
    fireEvent.change(promptInput, { target: { value: "Bob" } });
    fireEvent.keyDown(promptInput, { key: "a" });
    expect(wsActions.answerDialog).not.toHaveBeenCalled();
    fireEvent.keyDown(promptInput, { key: "Enter" });
    expect(wsActions.answerDialog).toHaveBeenCalledWith("t1", true, "Bob");
  });

  it("handles FileChooserBar (single/multiple, empty, too big, read success, read error, drag/drop, cancel, Escape)", async () => {
    resetWsState({
      fileChooser: { target_id: "t1", multiple: false },
    });
    const { rerender } = renderPanel();
    const chooserBar = screen.getByTestId("browser-file-chooser");
    expect(screen.getByText("The page asks for a file.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Choose file…" })).toBeTruthy();

    // Non-Escape key does nothing; Escape cancels
    fireEvent.keyDown(chooserBar, { key: "a" });
    expect(wsActions.answerFileChooser).not.toHaveBeenCalled();
    fireEvent.keyDown(chooserBar, { key: "Escape" });
    expect(wsActions.answerFileChooser).toHaveBeenCalledWith("t1", null);

    // Cancel button
    wsActions.answerFileChooser.mockClear();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(wsActions.answerFileChooser).toHaveBeenCalledWith("t1", null);

    // Choose file button triggers hidden input click
    const fileInput = chooserBar.querySelector('input[type="file"]') as HTMLInputElement;
    const clickSpy = vi.spyOn(fileInput, "click");
    fireEvent.click(screen.getByRole("button", { name: "Choose file…" }));
    expect(clickSpy).toHaveBeenCalledTimes(1);

    // Empty and null file selection -> no-op
    fireEvent.change(fileInput, { target: { files: [] } });
    fireEvent.change(fileInput, { target: { files: null } });
    expect(mockReadChosenFiles).not.toHaveBeenCalled();

    // Too large file (> 20MB) -> shows problem
    const huge = new File(["x"], "huge.bin", { type: "application/octet-stream" });
    Object.defineProperty(huge, "size", { value: 25 << 20 });
    fireEvent.change(fileInput, { target: { files: [huge] } });
    expect(screen.getByText(/over the 20 MB limit/)).toBeTruthy();

    // Drag over + drop with 2 files in single mode -> slices to 1; read fails
    const f1 = new File(["hello"], "a.txt", { type: "text/plain" });
    const f2 = new File(["world"], "b.txt", { type: "text/plain" });
    mockReadChosenFiles.mockRejectedValueOnce(new Error("io fail"));
    fireEvent.dragOver(chooserBar);
    fireEvent.drop(chooserBar, { dataTransfer: { files: [f1, f2] } });
    await act(async () => {
      await Promise.resolve();
    });
    expect(mockReadChosenFiles).toHaveBeenCalledWith([f1]);
    expect(screen.getByText("Reading the files failed.")).toBeTruthy();

    // Multiple mode + drop while sending is ignored + success
    resetWsState({
      fileChooser: { target_id: "t2", multiple: true },
    });
    rerender(
      <ThemeProvider initialTheme="dark">
        <BrowserPanel channelId="ch-1" />
      </ThemeProvider>,
    );
    expect(screen.getByText("The page asks for files.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Choose files…" })).toBeTruthy();

    let resolveRead!: (v: ChosenFile[]) => void;
    mockReadChosenFiles.mockReturnValueOnce(
      new Promise<ChosenFile[]>((res) => {
        resolveRead = res;
      }),
    );
    const bar2 = screen.getByTestId("browser-file-chooser");
    fireEvent.drop(bar2, { dataTransfer: { files: [f1, f2] } });
    expect(screen.getByText("Sending…")).toBeTruthy();
    // Second drop while sending is ignored
    fireEvent.drop(bar2, { dataTransfer: { files: [f1] } });
    expect(mockReadChosenFiles).toHaveBeenCalledTimes(2);

    await act(async () => {
      resolveRead([{ name: "a.txt", type: "text/plain", data: "aGVsbG8=" }]);
    });
    expect(wsActions.answerFileChooser).toHaveBeenCalledWith("t2", [{ name: "a.txt", type: "text/plain", data: "aGVsbG8=" }]);
  });

  it("handles TabStrip interactions, hover states, broken favicon, DialogBadge, and light theme", () => {
    resetWsState({
      dialogs: {
        t1: { target_id: "t1", type: "confirm", message: "Sure?" },
        t2: { target_id: "t2", type: "beforeunload", message: "" },
      },
    });
    const { unmount } = renderPanel({}, "dark");

    // Tab titles: title, url fallback, "New Tab" fallback
    expect(screen.getByText("Example")).toBeTruthy();
    expect(screen.getByText("https://two.test")).toBeTruthy();
    expect(screen.getByText("New Tab")).toBeTruthy();

    // DialogBadges with and without message
    const badges = screen.getAllByTestId("browser-tab-dialog");
    expect(badges).toHaveLength(2);
    expect(badges[0]!.getAttribute("title")).toBe("Waiting for an answer: Sure?");
    expect(badges[1]!.getAttribute("title")).toBe("Waiting for an answer: Leave this page? Changes you made may not be saved.");

    // Favicon img error marks it broken (fallback to dot)
    const img = document.querySelector("img")!;
    expect(img.getAttribute("src")).toBe(SAFE_ICON);
    fireEvent.error(img);
    expect(document.querySelector("img")).toBeNull();

    // Clicking active tab does not call switchTab; clicking inactive tab does
    fireEvent.click(screen.getByText("Example"));
    expect(wsActions.switchTab).not.toHaveBeenCalled();
    fireEvent.click(screen.getByText("https://two.test"));
    expect(wsActions.switchTab).toHaveBeenCalledWith("t2");

    // Hovering tab & close button in dark theme
    const tab2 = screen.getByText("https://two.test").parentElement!;
    fireEvent.mouseEnter(tab2);
    fireEvent.mouseLeave(tab2);

    const closeBtns = screen.getAllByText("✕");
    fireEvent.mouseEnter(closeBtns[0]!);
    fireEvent.mouseLeave(closeBtns[0]!);
    fireEvent.mouseEnter(closeBtns[1]!);
    fireEvent.mouseLeave(closeBtns[1]!);
    fireEvent.click(closeBtns[1]!);
    expect(wsActions.closeTab).toHaveBeenCalledWith("t2");

    // New tab button
    fireEvent.click(screen.getByTitle("New tab"));
    expect(wsActions.newTab).toHaveBeenCalledWith();
    unmount();

    // Light theme (fresh ThemeProvider mount): hover on close button & duplicate broken favicon
    resetWsState({
      tabs: [
        { target_id: "t1", url: "https://a.test", title: "A", favicon_url: SAFE_ICON_2 },
        { target_id: "t2", url: "https://b.test", title: "B", favicon_url: SAFE_ICON_2 },
      ],
      activeTargetId: "t1",
    });
    renderPanel({}, "light");
    const imgs = document.querySelectorAll("img");
    expect(imgs).toHaveLength(2);
    // Fire both errors in one act so the second hits prev.has(url)
    act(() => {
      fireEvent.error(imgs[0]!);
      fireEvent.error(imgs[1]!);
    });
    expect(document.querySelector("img")).toBeNull();

    const lightClose = screen.getAllByText("✕")[0]!;
    fireEvent.mouseEnter(lightClose);
    fireEvent.mouseLeave(lightClose);
  });
});
