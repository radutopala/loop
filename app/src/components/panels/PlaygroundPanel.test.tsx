import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  contentCapBase,
  fetchPlayground,
  fetchPlaygroundItems,
  fetchPlaygroundShareStatus,
  fetchPlaygroundState,
  type PlaygroundItem,
  patchPlaygroundState,
  sharePlayground,
  unsharePlayground,
} from "../../api/loopApi";
import { useContentCapsEpoch } from "../../hooks/useContentCapsEpoch";
import { useEventStream } from "../../hooks/useEventStream";
import { ThemeProvider } from "../../ThemeContext";
import type { WSEvent } from "../../types";
import { openExternalUrl } from "../../utils/openExternal";
import { PlaygroundPanel } from "./PlaygroundPanel";

vi.mock("../../api/loopApi", () => ({
  contentCapBase: vi.fn(),
  fetchPlayground: vi.fn(),
  fetchPlaygroundItems: vi.fn(),
  fetchPlaygroundShareStatus: vi.fn(),
  fetchPlaygroundState: vi.fn(),
  patchPlaygroundState: vi.fn(),
  sharePlayground: vi.fn(),
  unsharePlayground: vi.fn(),
}));

vi.mock("../../hooks/useContentCapsEpoch", () => ({
  useContentCapsEpoch: vi.fn(() => 0),
}));

vi.mock("../../hooks/useEventStream", () => ({
  useEventStream: vi.fn(),
}));

vi.mock("../../utils/openExternal", () => ({
  openExternalUrl: vi.fn(),
}));

const contentCapBaseMock = vi.mocked(contentCapBase);
const fetchPlaygroundMock = vi.mocked(fetchPlayground);
const fetchItemsMock = vi.mocked(fetchPlaygroundItems);
const fetchShareStatusMock = vi.mocked(fetchPlaygroundShareStatus);
const fetchStateMock = vi.mocked(fetchPlaygroundState);
const patchStateMock = vi.mocked(patchPlaygroundState);
const shareMock = vi.mocked(sharePlayground);
const unshareMock = vi.mocked(unsharePlayground);
const capsEpochMock = vi.mocked(useContentCapsEpoch);

const sampleItems: PlaygroundItem[] = [
  { name: "snake", title: "Snake Game", description: "Classic snake", scope: "global" },
  { name: "chart", title: "", description: "", scope: "project" },
];

function emit(event: WSEvent) {
  const opts = vi.mocked(useEventStream).mock.lastCall?.[0];
  act(() => opts?.onEvent(event));
}

function stubClipboard(writeText: (text: string) => Promise<void>) {
  Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
}

function getIframe(): HTMLIFrameElement {
  const el = document.querySelector("iframe");
  if (!el) throw new Error("iframe not found");
  return el;
}

describe("PlaygroundPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    capsEpochMock.mockReturnValue(0);
    fetchItemsMock.mockResolvedValue(sampleItems);
    fetchPlaygroundMock.mockResolvedValue({
      html: "<h1>Snake</h1>",
      name: "snake",
      title: "Snake Game",
      description: "Play with arrow keys",
    });
    contentCapBaseMock.mockResolvedValue("https://cap.example/playground/snake");
    fetchShareStatusMock.mockResolvedValue({ shared: false, url: "" });
    fetchStateMock.mockResolvedValue({ score: 10 });
    patchStateMock.mockResolvedValue({ score: 20 });
    shareMock.mockResolvedValue({ url: "https://share.example/snake", token: "tok1" });
    unshareMock.mockResolvedValue(undefined);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
    Reflect.deleteProperty(navigator, "clipboard");
  });

  describe("empty state and item selection", () => {
    it("shows onboarding message when no playgrounds exist", async () => {
      fetchItemsMock.mockResolvedValue([]);
      render(<PlaygroundPanel channelId="ch1" />);
      expect(await screen.findByText(/No playgrounds yet/)).toBeTruthy();
      expect(screen.queryByRole("combobox")).toBeNull();
    });

    it("logs an error when fetching playground items fails", async () => {
      const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
      fetchItemsMock.mockRejectedValue(new Error("network down"));
      render(<PlaygroundPanel channelId="ch1" />);
      await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] fetching playground items:", expect.any(Error)));
    });

    it("shows selector in empty state and selects global or project item", async () => {
      render(<PlaygroundPanel channelId="ch1" />);
      const select = await screen.findByRole("combobox");
      expect(screen.getByText("Select a playground")).toBeTruthy();
      expect(screen.getByText("Choose playground...")).toBeTruthy();
      expect(screen.getByText("Snake Game")).toBeTruthy();
      expect(screen.getByText("chart")).toBeTruthy();

      // Ignoring unknown selection value
      fireEvent.change(select, { target: { value: "global:nonexistent" } });
      expect(screen.getByText("Select a playground")).toBeTruthy();

      // Select project item
      fireEvent.change(select, { target: { value: "project:chart" } });
      await waitFor(() => expect(contentCapBaseMock).toHaveBeenCalledWith({ kind: "playground", channelId: "ch1", name: "chart" }));
      expect(fetchPlaygroundMock).toHaveBeenCalledWith("chart", "project", "ch1");
      expect(JSON.parse(localStorage.getItem("playground-active:ch1:default") || "{}")).toEqual({ name: "chart", scope: "project" });
      await waitFor(() => expect(getIframe().src).toContain("https://cap.example/playground/snake?v="));
    });

    it("restores saved selection from localStorage using custom instanceId", async () => {
      localStorage.setItem("playground-active:ch1:pane-2", JSON.stringify({ name: "snake", scope: "global" }));
      render(<PlaygroundPanel channelId="ch1" instanceId="pane-2" />);
      await waitFor(() => expect(contentCapBaseMock).toHaveBeenCalledWith({ kind: "playground", name: "snake" }));
      expect(await screen.findByText("Snake Game", { selector: "span" })).toBeTruthy();
    });

    it("handles partial localStorage entry with missing name and scope", async () => {
      localStorage.setItem("playground-active:ch1:default", JSON.stringify({}));
      render(<PlaygroundPanel channelId="ch1" />);
      expect(await screen.findByText("Select a playground")).toBeTruthy();
    });

    it("renders selector with only project items (titled) and only global items (untitled)", async () => {
      fetchItemsMock.mockResolvedValueOnce([{ name: "proj-only", title: "Project Demo", description: "", scope: "project" }]);
      const { unmount } = render(<PlaygroundPanel channelId="ch1" />);
      expect(await screen.findByText("Project Demo")).toBeTruthy();
      expect(document.querySelector('optgroup[label="Project"]')).not.toBeNull();
      expect(document.querySelector('optgroup[label="Global"]')).toBeNull();
      unmount();

      fetchItemsMock.mockResolvedValueOnce([{ name: "glob-only", title: "", description: "", scope: "global" }]);
      render(<PlaygroundPanel channelId="ch1" />);
      expect(await screen.findByText("glob-only")).toBeTruthy();
      expect(document.querySelector('optgroup[label="Global"]')).not.toBeNull();
      expect(document.querySelector('optgroup[label="Project"]')).toBeNull();
    });

    it("hides toolbar selector when items list is empty even if activeItem is restored", async () => {
      localStorage.setItem("playground-active:ch1:default", JSON.stringify({ name: "snake" }));
      fetchItemsMock.mockResolvedValue([]);
      render(<PlaygroundPanel channelId="ch1" />);
      await waitFor(() => expect(fetchPlaygroundMock).toHaveBeenCalledWith("snake", "global", "ch1"));
      expect(screen.queryByRole("combobox")).toBeNull();
    });
  });

  describe("loading playground content and iframe cap URL", () => {
    it("clears code/title/description when fetchPlayground returns null and handles missing metadata", async () => {
      localStorage.setItem("playground-active:ch1:default", JSON.stringify({ name: "snake", scope: "global" }));
      fetchPlaygroundMock.mockResolvedValueOnce(null);
      render(<PlaygroundPanel channelId="ch1" />);
      await waitFor(() => expect(fetchPlaygroundMock).toHaveBeenCalled());
      expect(screen.queryByTitle("Toggle info")).toBeNull();

      // Switch to an item whose data has empty title/description
      fetchPlaygroundMock.mockResolvedValueOnce({ name: "chart", html: "<p>hi</p>" });
      const select = await screen.findByRole("combobox");
      fireEvent.change(select, { target: { value: "project:chart" } });
      await waitFor(() => expect(fetchPlaygroundMock).toHaveBeenCalledWith("chart", "project", "ch1"));
      expect(screen.queryByTitle("Toggle info")).toBeNull();
    });

    it("logs an error when fetchPlayground rejects", async () => {
      const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
      localStorage.setItem("playground-active:ch1:default", JSON.stringify({ name: "snake", scope: "global" }));
      fetchPlaygroundMock.mockRejectedValue(new Error("load failed"));
      render(<PlaygroundPanel channelId="ch1" />);
      await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] loading playground item:", expect.any(Error)));
    });

    it("shows error banner when contentCapBase rejects and clears it on Reset", async () => {
      localStorage.setItem("playground-active:ch1:default", JSON.stringify({ name: "snake", scope: "global" }));
      contentCapBaseMock.mockRejectedValue(new Error("cap failed"));
      render(<PlaygroundPanel channelId="ch1" />);
      expect(await screen.findByText("Error: cap failed")).toBeTruthy();

      contentCapBaseMock.mockResolvedValue("https://cap.example/playground/snake");
      await userEvent.click(screen.getByTitle("Reset"));
      await waitFor(() => expect(screen.queryByText("Error: cap failed")).toBeNull());
      await waitFor(() => expect(getIframe().src).toContain("https://cap.example/playground/snake?v="));
    });

    it("ignores stale contentCapBase resolve and reject after unmount", async () => {
      localStorage.setItem("playground-active:ch1:default", JSON.stringify({ name: "snake", scope: "global" }));
      let resolveCap: (v: string) => void = () => {};
      contentCapBaseMock.mockReturnValueOnce(new Promise((r) => (resolveCap = r)));
      const { unmount } = render(<PlaygroundPanel channelId="ch1" />);
      unmount();
      await act(async () => {
        resolveCap("https://cap.example/stale");
      });

      let rejectCap: (e: unknown) => void = () => {};
      contentCapBaseMock.mockReturnValueOnce(new Promise((_, rej) => (rejectCap = rej)));
      const { unmount: unmount2 } = render(<PlaygroundPanel channelId="ch1" />);
      unmount2();
      await act(async () => {
        rejectCap(new Error("stale err"));
      });
    });

    it("re-resolves iframe cap URL when capsEpoch increments", async () => {
      localStorage.setItem("playground-active:ch1:default", JSON.stringify({ name: "snake", scope: "global" }));
      const { rerender } = render(<PlaygroundPanel channelId="ch1" />);
      await waitFor(() => expect(contentCapBaseMock).toHaveBeenCalled());
      const callsBefore = contentCapBaseMock.mock.calls.length;

      capsEpochMock.mockReturnValue(1);
      rerender(<PlaygroundPanel channelId="ch1" />);
      await waitFor(() => expect(contentCapBaseMock.mock.calls.length).toBeGreaterThan(callsBefore));
    });
  });

  describe("info panel, console, theme colors, and iframe focus", () => {
    it("toggles info and console panels in both dark and light themes", async () => {
      localStorage.setItem("playground-active:ch1:default", JSON.stringify({ name: "snake", scope: "global" }));
      const { unmount } = render(
        <ThemeProvider initialTheme="dark">
          <PlaygroundPanel channelId="ch1" />
        </ThemeProvider>,
      );

      await userEvent.click(await screen.findByTitle("Toggle info"));
      expect(screen.getByText("Play with arrow keys")).toBeTruthy();

      await userEvent.click(screen.getByTitle("Toggle console"));
      expect(screen.getByText("No console output")).toBeTruthy();

      // Close info panel
      await userEvent.click(screen.getByTitle("Toggle info"));
      expect(screen.queryByText("Play with arrow keys")).toBeNull();
      unmount();

      // Fresh mount in light theme to cover light-mode background branches
      render(
        <ThemeProvider initialTheme="light">
          <PlaygroundPanel channelId="ch1" />
        </ThemeProvider>,
      );
      await userEvent.click(await screen.findByTitle("Toggle info"));
      expect(screen.getByText("Play with arrow keys")).toBeTruthy();
      await userEvent.click(screen.getByTitle("Toggle console"));
      expect(screen.getByText("No console output")).toBeTruthy();
    });

    it("blurs active HTMLElement and focuses iframe on mouseEnter", async () => {
      localStorage.setItem("playground-active:ch1:default", JSON.stringify({ name: "snake", scope: "global" }));
      render(<PlaygroundPanel channelId="ch1" />);
      const resetBtn = await screen.findByTitle("Reset");
      const iframe = getIframe();
      const focusSpy = vi.spyOn(iframe, "focus");

      // Active element is an HTMLElement
      resetBtn.focus();
      const blurSpy = vi.spyOn(resetBtn, "blur");
      fireEvent.mouseEnter(iframe);
      expect(blurSpy).toHaveBeenCalled();
      expect(focusSpy).toHaveBeenCalled();

      // Active element is not an HTMLElement (null)
      vi.spyOn(document, "activeElement", "get").mockReturnValue(null);
      fireEvent.mouseEnter(iframe);
      expect(focusSpy).toHaveBeenCalledTimes(2);
    });
  });

  describe("postMessage handling (console and window.loop.state)", () => {
    it("records console messages at info, warn, and error levels and ignores other sources", async () => {
      localStorage.setItem("playground-active:ch1:default", JSON.stringify({ name: "snake", scope: "global" }));
      render(<PlaygroundPanel channelId="ch1" />);
      const iframe = await waitFor(() => getIframe());
      const fakeWin = { postMessage: vi.fn() } as unknown as Window;
      Object.defineProperty(iframe, "contentWindow", { value: fakeWin, configurable: true });

      // Message from another window is ignored
      act(() => {
        window.dispatchEvent(new MessageEvent("message", { source: window, data: { type: "playground-console", level: 1, message: "ignored" } }));
      });
      expect(screen.queryByText(/\(1\)/)).toBeNull();

      // Null data from iframe is ignored safely
      act(() => {
        window.dispatchEvent(new MessageEvent("message", { source: fakeWin, data: null }));
      });

      // Console messages of levels 1, 2, 3
      act(() => {
        window.dispatchEvent(new MessageEvent("message", { source: fakeWin, data: { type: "playground-console", level: 1, message: "hello info" } }));
        window.dispatchEvent(new MessageEvent("message", { source: fakeWin, data: { type: "playground-console", level: 2, message: "careful warn" } }));
        window.dispatchEvent(new MessageEvent("message", { source: fakeWin, data: { type: "playground-console", level: 3, message: "fatal err" } }));
      });

      expect(screen.getByText(/Console \(3\)/)).toBeTruthy();
      await userEvent.click(screen.getByTitle("Toggle console"));
      expect(screen.getByText(/hello info/)).toBeTruthy();
      expect(screen.getByText(/careful warn/)).toBeTruthy();
      expect(screen.getByText(/fatal err/)).toBeTruthy();
    });

    it("handles playground-state get, set (with and without patch), and error replies", async () => {
      localStorage.setItem("playground-active:ch1:default", JSON.stringify({ name: "snake", scope: "global" }));
      render(<PlaygroundPanel channelId="ch1" />);
      const iframe = await waitFor(() => getIframe());
      const postMessage = vi.fn();
      const fakeWin = { postMessage } as unknown as Window;
      Object.defineProperty(iframe, "contentWindow", { value: fakeWin, configurable: true });

      // op: get
      act(() => {
        window.dispatchEvent(new MessageEvent("message", { source: fakeWin, data: { type: "playground-state", id: 1, op: "get" } }));
      });
      await waitFor(() => expect(postMessage).toHaveBeenCalledWith({ type: "playground-state-reply", id: 1, state: { score: 10 } }, "*"));
      expect(fetchStateMock).toHaveBeenCalledWith("snake", "global", "ch1");

      // op: set with patch
      act(() => {
        window.dispatchEvent(new MessageEvent("message", { source: fakeWin, data: { type: "playground-state", id: 2, op: "set", patch: { score: 20 } } }));
      });
      await waitFor(() => expect(postMessage).toHaveBeenCalledWith({ type: "playground-state-reply", id: 2, state: { score: 20 } }, "*"));
      expect(patchStateMock).toHaveBeenCalledWith("snake", { score: 20 }, "global", "ch1");

      // op: set without patch (defaults to {})
      act(() => {
        window.dispatchEvent(new MessageEvent("message", { source: fakeWin, data: { type: "playground-state", id: 3, op: "set" } }));
      });
      await waitFor(() => expect(patchStateMock).toHaveBeenCalledWith("snake", {}, "global", "ch1"));

      // Error rejection with Error instance
      fetchStateMock.mockRejectedValueOnce(new Error("read error"));
      act(() => {
        window.dispatchEvent(new MessageEvent("message", { source: fakeWin, data: { type: "playground-state", id: 4, op: "get" } }));
      });
      await waitFor(() => expect(postMessage).toHaveBeenCalledWith({ type: "playground-state-reply", id: 4, error: "read error" }, "*"));

      // Error rejection with non-Error value
      fetchStateMock.mockRejectedValueOnce("string failure");
      act(() => {
        window.dispatchEvent(new MessageEvent("message", { source: fakeWin, data: { type: "playground-state", id: 5, op: "get" } }));
      });
      await waitFor(() => expect(postMessage).toHaveBeenCalledWith({ type: "playground-state-reply", id: 5, error: "string failure" }, "*"));
    });
  });

  describe("sharing and copying share URL", () => {
    it("loads existing share status, opens share URL, copies it, and handles copy timeout and failure", async () => {
      localStorage.setItem("playground-active:ch1:default", JSON.stringify({ name: "snake", scope: "global" }));
      fetchShareStatusMock.mockResolvedValue({ shared: true, url: "https://share.example/snake" });
      render(<PlaygroundPanel channelId="ch1" />);

      const urlSpan = await screen.findByText("https://share.example/snake");
      expect(screen.getByTitle("Stop sharing publicly")).toBeTruthy();

      // Open external URL
      await userEvent.click(urlSpan);
      expect(openExternalUrl).toHaveBeenCalledWith("https://share.example/snake");

      // Copy succeeds and resets after 1500ms
      const writeText = vi.fn().mockResolvedValue(undefined);
      stubClipboard(writeText);
      vi.useFakeTimers();
      fireEvent.click(screen.getByTitle("Copy share URL"));
      await act(async () => {
        await Promise.resolve();
      });
      expect(writeText).toHaveBeenCalledWith("https://share.example/snake");
      expect(screen.getByText("copied!")).toBeTruthy();

      act(() => {
        vi.advanceTimersByTime(1500);
      });
      expect(screen.getByText("Copy")).toBeTruthy();
      vi.useRealTimers();

      // Copy fails and logs error
      const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
      stubClipboard(vi.fn().mockRejectedValue(new Error("clipboard denied")));
      fireEvent.click(screen.getByTitle("Copy share URL"));
      await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] copying share url:", expect.any(Error)));
    });

    it("shares and unshares, ignores clicks while pending, and displays share errors", async () => {
      localStorage.setItem("playground-active:ch1:default", JSON.stringify({ name: "snake", scope: "global" }));
      render(<PlaygroundPanel channelId="ch1" />);
      const shareBtn = await screen.findByTitle("Share publicly over the internet");

      // Share with pending state and ignored double-click
      let resolveShare: (v: { url: string; token: string }) => void = () => {};
      shareMock.mockReturnValueOnce(new Promise((r) => (resolveShare = r)));
      fireEvent.click(shareBtn);
      expect(screen.getByText("…")).toBeTruthy();
      // Second click while pending does nothing
      fireEvent.click(shareBtn);
      expect(shareMock).toHaveBeenCalledTimes(1);

      await act(async () => {
        resolveShare({ url: "https://share.example/live", token: "tok2" });
      });
      expect(await screen.findByText("https://share.example/live")).toBeTruthy();
      expect(screen.getByText("Unshare")).toBeTruthy();

      // Unshare
      await userEvent.click(screen.getByText("Unshare"));
      await waitFor(() => expect(unshareMock).toHaveBeenCalledWith("snake", "global", "ch1"));
      expect(screen.queryByText("https://share.example/live")).toBeNull();
      expect(screen.getByText("Share")).toBeTruthy();

      // Share fails with Error
      shareMock.mockRejectedValueOnce(new Error("tunnel disabled"));
      await userEvent.click(screen.getByText("Share"));
      expect(await screen.findByText("tunnel disabled")).toBeTruthy();

      // Share fails with non-Error
      shareMock.mockRejectedValueOnce("unknown");
      await userEvent.click(screen.getByText("Share"));
      expect(await screen.findByText("share failed")).toBeTruthy();
    });

    it("clears shareUrl when fetchPlaygroundShareStatus rejects", async () => {
      localStorage.setItem("playground-active:ch1:default", JSON.stringify({ name: "snake", scope: "global" }));
      fetchShareStatusMock.mockRejectedValue(new Error("status err"));
      render(<PlaygroundPanel channelId="ch1" />);
      await waitFor(() => expect(fetchShareStatusMock).toHaveBeenCalled());
      expect(screen.getByText("Share")).toBeTruthy();
    });
  });

  describe("live event stream updates", () => {
    it("ignores non-playground events and playground events without a name", async () => {
      localStorage.setItem("playground-active:ch1:default", JSON.stringify({ name: "snake", scope: "global" }));
      render(<PlaygroundPanel channelId="ch1" />);
      await waitFor(() => expect(fetchItemsMock).toHaveBeenCalledTimes(1));

      emit({ type: "message.new", data: { name: "snake" } } as WSEvent);
      emit({ type: "playground.update", data: {} } as WSEvent);
      expect(fetchItemsMock).toHaveBeenCalledTimes(1);
    });

    it("handles kind=share events for matching and non-matching playgrounds", async () => {
      localStorage.setItem("playground-active:ch1:default", JSON.stringify({ name: "snake", scope: "global" }));
      render(<PlaygroundPanel channelId="ch1" />);
      await waitFor(() => expect(fetchShareStatusMock).toHaveBeenCalledTimes(1));

      // Non-matching item/scope -> ignored
      emit({ type: "playground.update", data: { kind: "share", name: "other", scope: "global" } } as WSEvent);
      emit({ type: "playground.update", data: { kind: "share", name: "snake", scope: "project" } } as WSEvent);
      expect(fetchShareStatusMock).toHaveBeenCalledTimes(1);

      // Matching -> refreshes share status
      fetchShareStatusMock.mockResolvedValueOnce({ shared: true, url: "https://share.example/updated" });
      emit({ type: "playground.update", data: { kind: "share", name: "snake", scope: "global" } } as WSEvent);
      expect(await screen.findByText("https://share.example/updated")).toBeTruthy();
    });

    it("handles kind=state events (posts to iframe, logs error, ignores non-matching)", async () => {
      localStorage.setItem("playground-active:ch1:default", JSON.stringify({ name: "snake", scope: "global" }));
      render(<PlaygroundPanel channelId="ch1" />);
      const iframe = await waitFor(() => getIframe());
      const postMessage = vi.fn();
      Object.defineProperty(iframe, "contentWindow", { value: { postMessage }, configurable: true });

      // Non-matching -> ignored
      emit({ type: "playground.update", data: { kind: "state", name: "other", scope: "global" } } as WSEvent);
      expect(fetchStateMock).not.toHaveBeenCalled();

      // Matching -> fetches and posts to iframe
      fetchStateMock.mockResolvedValueOnce({ level: 3 });
      emit({ type: "playground.update", data: { kind: "state", name: "snake", scope: "global" } } as WSEvent);
      await waitFor(() => expect(postMessage).toHaveBeenCalledWith({ type: "playground-state-changed", state: { level: 3 } }, "*"));

      // Fetch state error -> logged
      const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
      fetchStateMock.mockRejectedValueOnce(new Error("state fail"));
      emit({ type: "playground.update", data: { kind: "state", name: "snake", scope: "global" } } as WSEvent);
      await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] fetching playground state:", expect.any(Error)));
    });

    it("ignores project playground updates from another channel", async () => {
      localStorage.setItem("playground-active:ch1:default", JSON.stringify({ name: "snake", scope: "global" }));
      render(<PlaygroundPanel channelId="ch1" />);
      await waitFor(() => expect(fetchItemsMock).toHaveBeenCalledTimes(1));

      emit({ type: "playground.update", data: { name: "chart", scope: "project", channel_id: "ch-other" } } as WSEvent);
      expect(fetchItemsMock).toHaveBeenCalledTimes(1);
    });

    it("auto-switches on update for a different item and resolves scope fallbacks", async () => {
      localStorage.setItem("playground-active:ch1:default", JSON.stringify({ name: "snake", scope: "global" }));
      render(<PlaygroundPanel channelId="ch1" />);
      await waitFor(() => expect(fetchItemsMock).toHaveBeenCalledTimes(1));

      // 1) Item found in returned list -> uses found.scope ("project")
      fetchItemsMock.mockResolvedValueOnce(sampleItems);
      emit({ type: "playground.update", data: { name: "chart", scope: "project", channel_id: "ch1" } } as WSEvent);
      await waitFor(() => expect(fetchPlaygroundMock).toHaveBeenCalledWith("chart", "project", "ch1"));

      // 2) Item not in returned list with matching scope, but exists in itemsRef -> falls back to itemsRef scope
      fetchItemsMock.mockResolvedValueOnce([]);
      emit({ type: "playground.update", data: { name: "snake", scope: "global" } } as WSEvent);
      await waitFor(() => expect(fetchPlaygroundMock).toHaveBeenCalledWith("snake", "global", "ch1"));

      // 3) Item in neither list nor itemsRef -> falls back to "global"
      fetchItemsMock.mockResolvedValueOnce([]);
      emit({ type: "playground.update", data: { name: "brand-new", scope: "global" } } as WSEvent);
      await waitFor(() => expect(fetchPlaygroundMock).toHaveBeenCalledWith("brand-new", "global", "ch1"));
    });

    it("reloads active item on full html update (with and without metadata) and file-level update, and logs refresh errors", async () => {
      localStorage.setItem("playground-active:ch1:default", JSON.stringify({ name: "snake", scope: "global" }));
      render(<PlaygroundPanel channelId="ch1" />);
      await screen.findByText("Snake Game", { selector: "span" });

      // Full HTML update with title and description
      emit({
        type: "playground.update",
        data: { name: "snake", scope: "global", html: "<h1>Updated</h1>", title: "New Title", description: "New Desc" },
      } as WSEvent);
      expect(await screen.findByText("New Title", { selector: "span" })).toBeTruthy();

      // Full HTML update without title/description
      emit({
        type: "playground.update",
        data: { name: "snake", scope: "global", html: "<h1>Minimal</h1>" },
      } as WSEvent);
      await waitFor(() => expect(screen.queryByText("New Title", { selector: "span" })).toBeNull());

      // File-level update (no html) bumps iframe version
      const srcBefore = getIframe().src;
      emit({
        type: "playground.update",
        data: { name: "snake", scope: "global" },
      } as WSEvent);
      await waitFor(() => expect(getIframe().src).not.toBe(srcBefore));

      // Refresh items failure logs error
      const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
      fetchItemsMock.mockRejectedValueOnce(new Error("refresh fail"));
      emit({
        type: "playground.update",
        data: { name: "snake", scope: "global" },
      } as WSEvent);
      await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] refreshing playground items:", expect.any(Error)));
    });
  });
});
