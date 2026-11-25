import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { type ReactNode, useEffect } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { contentCapBase } from "../api/api";
import { checkFilesExist } from "../api/files";
import { fetchPluginAgent, type PluginInfo, pluginXRequest, postPluginNotification, setPluginEnabled } from "../api/plugins";
import type { ChatEventListener } from "../hooks/useChatStateStore";
import { useEventStream } from "../hooks/useEventStream";
import { ThemeProvider, useTheme } from "../ThemeContext";
import { loadFrame } from "../test/app-shell-bridge";
import { darkColors, lightColors } from "../theme";
import type { WSEvent } from "../types";
import type { PluginPaneRef } from "../types/panels";
import { writeClipboard } from "../utils/copyToClipboard";
import { pluginTheme } from "./bridge";
import { PluginPanel, type PluginPanelProps } from "./PluginPanel";
import { PluginPanelsProvider } from "./PluginPanelsContext";
import { paneTitle } from "./paneTitles";

vi.mock("../api/api", () => ({ contentCapBase: vi.fn() }));
vi.mock("../api/files", () => ({ checkFilesExist: vi.fn() }));
vi.mock("../api/plugins", () => ({ fetchPluginAgent: vi.fn(), pluginXRequest: vi.fn(), postPluginNotification: vi.fn(), setPluginEnabled: vi.fn() }));
vi.mock("../utils/copyToClipboard", () => ({ writeClipboard: vi.fn() }));
vi.mock("../hooks/useContentCapsEpoch", () => ({ useContentCapsEpoch: () => 0 }));
vi.mock("../hooks/useEventStream", () => ({ useEventStream: vi.fn() }));
vi.mock("../hooks/useChatState", () => ({ useChatState: vi.fn(() => ({})) }));
vi.mock("../components/chat/ChatView", () => ({ ChatView: ({ channelId }: { channelId: string }) => <div data-testid="chat-view">{channelId}</div> }));
// The animated icon measures its SVG path, which jsdom doesn't lay out.
vi.mock("../components/LoopInfinityIcon", () => ({ LoopInfinityIcon: () => <span /> }));
// The view's header is the layout's pane header: a stub with its slot stands in.
vi.mock("../splitPane/PaneLeafHeader", () => ({
  PaneLeafHeader: ({ leafId, titleLeafId }: { leafId: string; titleLeafId?: string }) => <div data-testid="view-header" data-title-leaf={titleLeafId} id={`pane-header-slot-${leafId}`} />,
}));

const capMock = vi.mocked(contentCapBase);
const existsMock = vi.mocked(checkFilesExist);
const agentMock = vi.mocked(fetchPluginAgent);
const requestMock = vi.mocked(pluginXRequest);
const notifyMock = vi.mocked(postPluginNotification);
const enableMock = vi.mocked(setPluginEnabled);
const copyMock = vi.mocked(writeClipboard);
const streamMock = vi.mocked(useEventStream);

const cogtest: PluginInfo = {
  id: "cogtest",
  name: "Cognitive tests",
  version: "1",
  capabilities: ["ui.notification.notify", "ui.open_file", "ui.copy"],
  status: "installed",
  settings: { level: 2 },
  panels: [{ id: "quiz", label: "Quiz", entry: "quiz/index.html" }],
};
const quizRef: PluginPaneRef = { id: "cogtest", panel: "quiz", label: "Quiz" };

const base: PluginPanelProps = { plugin: quizRef, channelId: "ch1", channelName: "proj", dirPath: "/src/proj", leafId: "leaf1" };

let setTheme: (name: string) => void = () => {};
function ThemeSwitch() {
  const { setThemeName } = useTheme();
  useEffect(() => {
    setTheme = setThemeName;
  }, [setThemeName]);
  return null;
}

function Shell({ plugins, children }: { plugins: PluginInfo[] | null; children: ReactNode }) {
  return (
    <ThemeProvider initialTheme="dark">
      <ThemeSwitch />
      <PluginPanelsProvider value={{ plugins, shown: new Set() }}>{children}</PluginPanelsProvider>
    </ThemeProvider>
  );
}

function renderPanel(over: Partial<PluginPanelProps> = {}, plugins: PluginInfo[] | null = [cogtest]) {
  const view = render(
    <Shell plugins={plugins}>
      <PluginPanel {...base} {...over} />
    </Shell>,
  );
  return {
    ...view,
    rerenderPanel: (o: Partial<PluginPanelProps> = {}, p: PluginInfo[] | null = plugins) =>
      view.rerender(
        <Shell plugins={p}>
          <PluginPanel {...base} {...over} {...o} />
        </Shell>,
      ),
  };
}

const frame = async () => (await screen.findByTestId("plugin-panel-frame")) as HTMLIFrameElement;
const lastOnEvent = () => streamMock.mock.calls.at(-1)![0].onEvent;

function headerSlot(leafId: string) {
  const el = document.createElement("div");
  el.id = `pane-header-slot-${leafId}`;
  document.body.appendChild(el);
  return el;
}

describe("PluginPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    capMock.mockResolvedValue("https://content.test/cap/");
    agentMock.mockResolvedValue({ running: false });
  });
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    localStorage.clear();
    document.body.innerHTML = "";
  });

  it("shows an empty pane while the plugins are listed, and follows no stream", () => {
    renderPanel({}, null);
    expect(screen.getByTestId("plugin-panel-message").textContent).toBe("");
    expect(streamMock).toHaveBeenLastCalledWith(expect.objectContaining({ channelId: null }));
    expect(capMock).not.toHaveBeenCalled();
  });

  it.each([
    { name: "a plugin gone", ref: quizRef, want: "The Quiz panel isn't available: its plugin may have been removed or not approved for panels." },
    { name: "a pane without a label", ref: { ...quizRef, label: "" }, plugins: [], want: "The plugin panel isn't available: its plugin may have been removed or not approved for panels." },
    { name: "no ref", ref: undefined, want: "The plugin panel isn't available: its plugin may have been removed or not approved for panels." },
  ])("says the panel isn't available for $name", ({ ref, plugins = [], want }) => {
    renderPanel({ plugin: ref }, plugins);
    expect(screen.getByTestId("plugin-panel-message").textContent).toBe(want);
  });

  it.each([
    { name: "an error", err: new Error("no link"), want: "no link" },
    { name: "anything else", err: "refused", want: "refused" },
  ])("shows a failed link's $name", async ({ err, want }) => {
    capMock.mockRejectedValue(err);
    renderPanel();
    await waitFor(() => expect(screen.getByTestId("plugin-panel-message").textContent).toBe(want));
  });

  it("drops a link that arrives after unmounting", async () => {
    let ok!: (b: string) => void;
    let fail!: (e: Error) => void;
    capMock.mockReturnValueOnce(new Promise((r) => (ok = r))).mockReturnValueOnce(new Promise((_, r) => (fail = r)));
    renderPanel().unmount();
    renderPanel().unmount();
    await act(async () => {
      ok("late/");
      fail(new Error("late"));
    });
    expect(screen.queryByTestId("plugin-panel")).toBeNull();
  });

  it("frames the panel's page from the plugin's bundle, titled by its pane", async () => {
    renderPanel();
    const f = await frame();
    expect(capMock).toHaveBeenCalledWith({ kind: "plugin", name: "cogtest" });
    expect(f.getAttribute("src")).toBe("https://content.test/cap/quiz/index.html");
    expect(f.getAttribute("title")).toBe("Quiz");
    expect(f.getAttribute("sandbox")).toBe("allow-scripts allow-forms");
    expect(streamMock).toHaveBeenLastCalledWith(expect.objectContaining({ channelId: "ch1" }));
  });

  it("titles the frame by the plugin's id for a kept pane without a label", async () => {
    renderPanel({ plugin: { id: "cogtest", panel: "quiz" } as PluginPaneRef });
    expect((await frame()).getAttribute("title")).toBe("cogtest");
  });

  describe("a plugin off in the project", () => {
    const off = { ...cogtest, enabled: false };

    it("offers to enable it, and enables it", async () => {
      let done!: () => void;
      enableMock.mockReturnValue(new Promise<void>((r) => (done = r)));
      renderPanel({}, [off]);
      expect(screen.getByText("The Cognitive tests plugin isn't enabled in this project.")).toBeTruthy();
      expect(screen.getByText("Sets plugins.cogtest.enabled in the project's .loop/config.json.")).toBeTruthy();
      const button = screen.getByTestId("plugin-panel-enable") as HTMLButtonElement;
      await userEvent.setup().click(button);
      expect(enableMock).toHaveBeenCalledWith("cogtest", true, "ch1");
      expect(button.textContent).toBe("Enabling…");
      expect(button.disabled).toBe(true);
      await act(async () => done());
      expect(button.textContent).toBe("Enable in this project");
      expect(button.disabled).toBe(false);
    });

    it.each([
      { name: "an error", err: new Error("read-only"), want: "read-only" },
      { name: "anything else", err: "nope", want: "nope" },
    ])("shows a failed enable's $name, naming a plugin by id without a name", async ({ err, want }) => {
      enableMock.mockRejectedValue(err);
      renderPanel({}, [{ ...off, name: undefined }]);
      expect(screen.getByText("The cogtest plugin isn't enabled in this project.")).toBeTruthy();
      await userEvent.setup().click(screen.getByTestId("plugin-panel-enable"));
      expect(await screen.findByText(want)).toBeTruthy();
    });
  });

  it("answers the page's calls as the panel", async () => {
    requestMock.mockResolvedValue({ status: 201, body: null } as never);
    notifyMock.mockResolvedValue();
    copyMock.mockResolvedValue();
    renderPanel();
    const page = loadFrame(await frame());
    expect(await page.call("context.get")).toEqual({
      jsonrpc: "2.0",
      id: 1,
      result: { pluginId: "cogtest", panel: "quiz", channelId: "ch1", channelName: "proj", dirPath: "/src/proj" },
    });
    expect(await page.call("api.request", { method: "POST", path: "runs", body: { n: 1 } })).toMatchObject({ result: { status: 201 } });
    expect(requestMock).toHaveBeenCalledWith("cogtest", "ch1", "POST", "runs", { n: 1 });
    expect(await page.call("settings.get")).toMatchObject({ result: { level: 2 } });
    expect(await page.call("theme.get")).toMatchObject({ result: pluginTheme(darkColors) });
    await page.call("ui.notification.notify", { title: "Done", body: "" });
    expect(notifyMock).toHaveBeenCalledWith("cogtest", "ch1", "Done", "");
    await page.call("ui.copy", { text: "x" });
    expect(copyMock).toHaveBeenCalledWith("x");
    expect(await page.call("ui.resize", { height: 10 })).toMatchObject({ error: { message: "only a card sizes itself; a panel fills its pane" } });
    page.close();
  });

  it("answers with empty settings for a plugin without any", async () => {
    renderPanel({}, [{ ...cogtest, settings: undefined }]);
    const page = loadFrame(await frame());
    expect(await page.call("settings.get")).toMatchObject({ result: {} });
    page.close();
  });

  it("opens a file of the channel the page names", async () => {
    const opened = vi.fn();
    window.addEventListener("loop:open-file", opened);
    existsMock.mockResolvedValue([{ path: "a.ts", exists: true, root_index: 0, rel_path: "a.ts" }] as never);
    renderPanel();
    const page = loadFrame(await frame());
    await page.call("ui.open_file", { path: "a.ts", line: 9 });
    await page.call("ui.open_file", { path: "a.ts" });
    expect(opened.mock.calls.map((c) => (c[0] as CustomEvent).detail)).toEqual([
      { channelId: "ch1", target: { rootIndex: 0, relPath: "a.ts" }, line: 9 },
      { channelId: "ch1", target: { rootIndex: 0, relPath: "a.ts" }, line: null },
    ]);
    window.removeEventListener("loop:open-file", opened);
    page.close();
  });

  it.each([
    { name: "no answer", files: [] },
    { name: "a missing file", files: [{ path: "a.ts", exists: false }] },
    { name: "no root", files: [{ path: "a.ts", exists: true, rel_path: "a.ts" }] },
    { name: "no relative path", files: [{ path: "a.ts", exists: true, root_index: 0 }] },
  ])("fails to open a file for $name", async ({ files }) => {
    existsMock.mockResolvedValue(files as never);
    renderPanel();
    const page = loadFrame(await frame());
    expect(await page.call("ui.open_file", { path: "a.ts" })).toMatchObject({ error: { message: 'no file "a.ts" in the channel' } });
    page.close();
  });

  it("keeps the panel's state and tells the channel's other frames of the panel when it changes", async () => {
    render(
      <Shell plugins={[cogtest]}>
        <PluginPanel {...base} leafId="a" />
        <PluginPanel {...base} leafId="b" />
      </Shell>,
    );
    await waitFor(() => expect(screen.getAllByTestId("plugin-panel-frame")).toHaveLength(2));
    const [fa, fb] = screen.getAllByTestId("plugin-panel-frame") as HTMLIFrameElement[];
    const a = loadFrame(fa!);
    const b = loadFrame(fb!);
    const told = b.next("state");
    await a.call("state.set", { value: { round: 3 } });
    expect(await told).toEqual({ round: 3 });
    expect(a.notifications.some((n) => n.method === "state")).toBe(false);
    expect(JSON.parse(localStorage.getItem("plugin-state:cogtest:ch1:quiz")!)).toEqual({ round: 3 });
    expect(await b.call("state.get")).toMatchObject({ result: { round: 3 } });
    a.close();
    b.close();
  });

  it("names its pane until the page goes", async () => {
    const { unmount } = renderPanel();
    const f = await frame();
    const page = loadFrame(f);
    await page.call("ui.set_title", { title: " Round 2 " });
    expect(paneTitle("leaf1")).toBe("Round 2");
    // Loading again is a new page: it starts from the pane's label.
    loadFrame(f).close();
    expect(paneTitle("leaf1")).toBe("");
    page.close();
    unmount();
  });

  it("drops its pane's title when unmounted, and when the plugin goes off", async () => {
    const { unmount, rerenderPanel } = renderPanel();
    let page = loadFrame(await frame());
    await page.call("ui.set_title", { title: "Kept" });
    rerenderPanel({}, [{ ...cogtest, enabled: false }]);
    expect(paneTitle("leaf1")).toBe("");
    rerenderPanel({}, [cogtest]);
    page.close();
    page = loadFrame(await frame());
    await page.call("ui.set_title", { title: "Kept" });
    expect(paneTitle("leaf1")).toBe("Kept");
    unmount();
    expect(paneTitle("leaf1")).toBe("");
    page.close();
  });

  it("tells the page of a new theme and of new settings", async () => {
    const { rerenderPanel } = renderPanel();
    const page = loadFrame(await frame());
    const theme = page.next("theme");
    act(() => setTheme("light"));
    expect(await theme).toEqual(pluginTheme(lightColors));
    const settings = page.next("settings");
    rerenderPanel({}, [{ ...cogtest, settings: { level: 3 } }]);
    expect(await settings).toEqual({ level: 3 });
    page.close();
  });

  it("passes its plugin's events to the page", async () => {
    renderPanel();
    const page = loadFrame(await frame());
    const got = page.next("event");
    act(() => {
      lastOnEvent()({ type: "agent.status", data: {} } as WSEvent);
      lastOnEvent()({ type: "plugin.event", data: { plugin_id: "echo", type: "x", data: 0 } } as WSEvent);
      lastOnEvent()({ type: "plugin.event", data: { plugin_id: "cogtest", type: "scored", data: { n: 4 } } } as WSEvent);
    });
    expect(await got).toEqual({ type: "scored", data: { n: 4 } });
    expect(page.notifications.filter((n) => n.method === "event")).toHaveLength(1);
    page.close();
  });

  it("answers with the last host after the panel's plugin goes", async () => {
    const { rerenderPanel } = renderPanel();
    const page = loadFrame(await frame());
    rerenderPanel({}, []);
    expect(screen.getByTestId("plugin-panel-message")).toBeTruthy();
    expect(await page.call("settings.get")).toMatchObject({ result: { level: 2 } });
    page.close();
  });

  it("connects nothing to a frame without a window", async () => {
    renderPanel();
    const f = await frame();
    vi.spyOn(f, "contentWindow", "get").mockReturnValue(null);
    expect(() => act(() => f.dispatchEvent(new Event("load")))).not.toThrow();
  });

  describe("the plugin's agent", () => {
    const observers: { cb: () => void; observed: Element[]; disconnected: boolean }[] = [];
    beforeEach(() => {
      observers.length = 0;
      vi.stubGlobal(
        "ResizeObserver",
        class {
          entry: { cb: () => void; observed: Element[]; disconnected: boolean };
          constructor(cb: () => void) {
            this.entry = { cb, observed: [], disconnected: false };
            observers.push(this.entry);
          }
          observe(el: Element) {
            this.entry.observed.push(el);
          }
          disconnect() {
            this.entry.disconnected = true;
          }
        },
      );
      agentMock.mockResolvedValue({ thread_id: "plugin:t1", running: true });
    });

    it("shows no label without a host for its view", async () => {
      headerSlot("leaf1");
      renderPanel();
      await frame();
      await waitFor(() => expect(agentMock).toHaveBeenCalledWith("cogtest", "ch1"));
      await act(async () => {});
      expect(screen.queryByTestId("plugin-agent-badge")).toBeNull();
    });

    it("opens the agent's chat beside the panel, over the layout, and closes it", async () => {
      headerSlot("leaf1");
      const host = document.createElement("div");
      document.body.appendChild(host);
      vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
        const id = this.dataset.testid;
        if (id === "plugin-agent-frame-slot") return { left: 100, top: 50, width: 400, height: 300 } as DOMRect;
        if (id === "plugin-panel-frame-box") return { left: 110, top: 60, width: 400, height: 300 } as DOMRect;
        return { left: 0, top: 0, width: 1000, height: 600 } as DOMRect;
      });
      const subs: ChatEventListener[] = [];
      const subscribe = vi.fn((_: string, l: ChatEventListener) => {
        subs.push(l);
        return () => {};
      });
      const { unmount } = renderPanel({ overlayHost: host, subscribeChannelEvents: subscribe, roots: [] });
      await frame();
      const badge = await screen.findByTestId("plugin-agent-badge");
      expect(badge.getAttribute("aria-expanded")).toBe("false");
      await userEvent.setup().click(badge);

      const split = await screen.findByTestId("plugin-agent-split");
      expect(host.contains(split)).toBe(true);
      expect(split.getAttribute("aria-label")).toBe("Quiz agent view");
      expect(screen.getByTestId("view-header").dataset.titleLeaf).toBe("leaf1");
      expect(screen.getByTestId("chat-view").textContent).toBe("plugin:t1");
      expect(localStorage.getItem("plugin-agent-open:cogtest:ch1:leaf1")).toBe("true");
      // The badge moves to the view's header, where it closes the view.
      await waitFor(() => expect(screen.getAllByTestId("plugin-agent-badge")).toHaveLength(2));

      // The frame lies over the view's slot, corrected for how far it landed off it.
      const box = screen.getByTestId("plugin-panel-frame-box");
      // The stub keeps the box 10px off its slot, so each placing moves it 10px back.
      await waitFor(() => expect(box.style.left).toBe("90px"));
      expect(box.style.position).toBe("fixed");
      expect(box.style.top).toBe("40px");
      expect(box.style.width).toBe("400px");
      expect(observers[0]!.observed).toEqual([screen.getByTestId("plugin-agent-frame-slot")]);
      const left = () => Number.parseFloat(box.style.left);
      const before = left();
      act(() => observers[0]!.cb());
      expect(left()).toBe(before - 10);
      act(() => {
        window.dispatchEvent(new Event("resize"));
      });
      expect(left()).toBe(before - 20);

      // Dragging the seam moves the split, kept for the plugin.
      const divider = split.querySelector<HTMLElement>('[style*="col-resize"]')!;
      fireEvent.mouseDown(divider);
      fireEvent.mouseMove(document, { clientX: 300 });
      fireEvent.mouseUp(document);
      expect(localStorage.getItem("plugin-agent-split:cogtest")).toBe("0.3");

      const [, viewBadge] = screen.getAllByTestId("plugin-agent-badge");
      expect(viewBadge!.getAttribute("aria-expanded")).toBe("true");
      await userEvent.setup().click(viewBadge!);
      expect(screen.queryByTestId("plugin-agent-split")).toBeNull();
      expect(box.style.position).toBe("");
      expect(observers[0]!.disconnected).toBe(true);
      unmount();
    });

    it("opens at the split last dragged for the plugin", async () => {
      headerSlot("leaf1");
      const host = document.createElement("div");
      document.body.appendChild(host);
      localStorage.setItem("plugin-agent-split:cogtest", "0.7");
      localStorage.setItem("plugin-agent-open:cogtest:ch1:leaf1", "true");
      renderPanel({ overlayHost: host, plugin: { ...quizRef, label: "" } });
      await frame();
      const logo = await screen.findByTestId("plugin-agent-split-logo");
      expect(logo.style.left).toBe("70%");
      expect(screen.getByTestId("plugin-agent-split").getAttribute("aria-label")).toBe("cogtest agent view");
    });
  });
});
