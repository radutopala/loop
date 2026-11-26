import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { fetchGlobalConfig } from "./api/configApi";
import {
  createChannel,
  createThread,
  createWorktreeThread,
  deleteChannel,
  deleteThread,
  editChannelTags,
  ensureChannel,
  fetchChannels,
  fetchPlaygroundShares,
  getImageStatus,
  importWorktree,
  initApiUrl,
  isDaemonHealthy,
  listTags,
  rebuildImage,
  renameChannel,
  setChannelDescription,
  setChannelLocked,
  setChannelTicketURL,
} from "./api/loopApi";
import { fetchUnreadNotifications } from "./api/notifications";
import { isInboxWindow } from "./components/InboxView";
import { WorkspaceLayout } from "./components/layout/WorkspaceLayout";
import { ContainersPanel } from "./components/panels/ContainersPanel";
import { MarkdownFilePanel } from "./components/panels/FilePanel";
import { GlobalSharesPanel } from "./components/panels/GlobalSharesPanel";
import { GlobalTasksPanel } from "./components/panels/GlobalTasksPanel";
import { WorkflowsGlobalPanel } from "./components/panels/WorkflowsGlobalPanel";
import { CommandPalette } from "./components/shared/CommandPalette";
import { DaemonUnavailable } from "./components/shared/DaemonUnavailable";
import { Settings } from "./components/shared/Settings";
import { Sidebar } from "./components/sidebar/Sidebar";
import { useChatStateStore } from "./hooks/useChatStateStore";
import type { makeChatStore } from "./test/app-shell-app-stubs";
import type { Channel, WSEvent } from "./types";
import { useUiBridge } from "./uiBridge/useUiBridge";

const h = vi.hoisted(() => ({
  layout: { switchToLayout: vi.fn() },
  containers: { handleContainerEvent: vi.fn() },
  workflows: { handleWorkflowEvent: vi.fn() },
  layoutMounts: vi.fn(),
  noop: () => {},
}));

vi.mock("./api/configApi", () => ({ fetchGlobalConfig: vi.fn() }));
vi.mock("./api/loopApi", () => ({
  createChannel: vi.fn(),
  createThread: vi.fn(),
  createWorktreeThread: vi.fn(),
  deleteChannel: vi.fn(),
  deleteThread: vi.fn(),
  ensureChannel: vi.fn(),
  fetchChannels: vi.fn(),
  fetchPlaygroundShares: vi.fn(),
  getApiUrl: vi.fn(() => "http://daemon"),
  getImageStatus: vi.fn(),
  importWorktree: vi.fn(),
  initApiUrl: vi.fn(),
  isDaemonHealthy: vi.fn(),
  rebuildImage: vi.fn(),
  renameChannel: vi.fn(),
  setChannelDescription: vi.fn(),
  setChannelLocked: vi.fn(),
  setChannelTicketURL: vi.fn(),
  editChannelTags: vi.fn(),
  listTags: vi.fn(),
}));
vi.mock("./api/notifications", () => ({ fetchUnreadNotifications: vi.fn() }));
vi.mock("./components/InboxView", async () => ({ default: (await import("./test/app-shell-app-stubs")).stub("inbox"), isInboxWindow: vi.fn(() => false) }));
vi.mock("./components/layout/WorkspaceLayout", async () => ({ WorkspaceLayout: (await import("./test/app-shell-app-stubs")).refStub("workspace", h.layout, h.layoutMounts) }));
vi.mock("./components/panels/ContainersPanel", async () => ({ ContainersPanel: (await import("./test/app-shell-app-stubs")).refStub("containers", h.containers, h.noop) }));
vi.mock("./components/panels/FilePanel", async () => ({ MarkdownFilePanel: (await import("./test/app-shell-app-stubs")).stub("readme") }));
vi.mock("./components/panels/GlobalSharesPanel", async () => ({ GlobalSharesPanel: (await import("./test/app-shell-app-stubs")).stub("shares") }));
vi.mock("./components/panels/GlobalTasksPanel", async () => ({ GlobalTasksPanel: (await import("./test/app-shell-app-stubs")).stub("tasks") }));
vi.mock("./components/panels/WorkflowsGlobalPanel", async () => ({ WorkflowsGlobalPanel: (await import("./test/app-shell-app-stubs")).refStub("workflows", h.workflows, h.noop) }));
vi.mock("./components/shared/CommandPalette", async () => ({ CommandPalette: (await import("./test/app-shell-app-stubs")).stub("palette") }));
vi.mock("./components/shared/DaemonUnavailable", async () => ({ DaemonUnavailable: (await import("./test/app-shell-app-stubs")).stub("daemon-unavailable") }));
vi.mock("./components/shared/Settings", async () => ({ Settings: (await import("./test/app-shell-app-stubs")).stub("settings") }));
vi.mock("./components/sidebar/Sidebar", async () => ({ Sidebar: (await import("./test/app-shell-app-stubs")).stub("sidebar") }));
vi.mock("./hooks/useChatStateStore", async () => ({ useChatStateStore: vi.fn((await import("./test/app-shell-app-stubs")).useFakeChatStore) }));
vi.mock("./uiBridge/useUiBridge", () => ({ useUiBridge: vi.fn() }));

const ch = (id: string, over: Partial<Channel> = {}): Channel => ({
  id,
  name: id,
  parent_id: "",
  dir_path: `/p/${id}`,
  session_id: "",
  active: true,
  container_running: false,
  agent_running: false,
  branch: "main",
  commit: "",
  worktree: false,
  locked: false,
  diff_additions: 0,
  diff_deletions: 0,
  review_enabled: false,
  ...over,
});

const dm = ch("dm-id", { name: "dm" });
const proj = ch("proj", { name: "proj" });
const wt = ch("wt", { name: "feature", parent_id: "proj", worktree: true, root_dir_path: "/root/proj", dir_path: "/p/wt" });
const thread = ch("th", { name: "talk", parent_id: "wt" });

// biome-ignore lint/suspicious/noExplicitAny: mocked components' props
type Props = Record<string, any>;
const props = (c: unknown): Props => vi.mocked(c as (p: Props) => unknown).mock.lastCall![0];
const sidebar = () => props(Sidebar);
const workspace = () => props(WorkspaceLayout);
const store = (): ReturnType<typeof makeChatStore> => vi.mocked(useChatStateStore).mock.results.at(-1)!.value as never;
const appEvent = (e: Partial<WSEvent>) => act(() => vi.mocked(useChatStateStore).mock.lastCall![0].onAppEvent({ channel_id: "", timestamp: 0, ...e } as WSEvent));
// The corner logo is always up; the empty view adds a second one.
const emptyViewShown = () => screen.getAllByRole("img", { name: "Loop" }).length === 2;
const call = async (fn: () => unknown) => act(async () => void (await fn()));

function loopAPI(over: Record<string, unknown> = {}) {
  const api = {
    onOpenSettings: vi.fn((_cb: () => void) => () => {}),
    getUpdateStatus: vi.fn(async () => ({ available: true, version: "2", downloading: false, downloaded: false })),
    onUpdateStatus: vi.fn((_cb: (s: unknown) => void) => () => {}),
    downloadUpdate: vi.fn(async () => {}),
    installUpdate: vi.fn(),
    onNavigateChannel: vi.fn((_cb: (t: string) => void) => () => {}),
    onboardLocal: vi.fn(async () => ({ ok: true })),
    ...over,
  };
  vi.stubGlobal("loopAPI", api);
  return api;
}

async function renderApp() {
  const r = render(<App />);
  await screen.findByTestId("sidebar");
  return r;
}

/** Renders the app with dm auto-selected and its layout up. */
async function renderWithDm() {
  const r = await renderApp();
  await screen.findByTestId("workspace");
  // The layout's DOM can land before its mount effect runs; wait for the
  // mount itself, so a test counting mounts doesn't count this one later.
  await waitFor(() => expect(h.layoutMounts).toHaveBeenCalled());
  return r;
}

beforeEach(() => {
  // Reset, not clear: a once-value a test left unused must not reach the next.
  vi.resetAllMocks();
  localStorage.clear();
  window.history.replaceState(null, "", "/");
  vi.mocked(isInboxWindow).mockReturnValue(false);
  vi.mocked(initApiUrl).mockResolvedValue(undefined as never);
  vi.mocked(fetchGlobalConfig).mockResolvedValue({ content: {} } as never);
  vi.mocked(fetchChannels).mockResolvedValue([dm, proj, wt, thread]);
  vi.mocked(fetchPlaygroundShares).mockResolvedValue([]);
  vi.mocked(getImageStatus).mockResolvedValue(null as never);
  vi.mocked(fetchUnreadNotifications).mockResolvedValue({});
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("App startup", () => {
  it("renders nothing until the config is in, then the app with its desktop settings", async () => {
    let resolve!: (v: unknown) => void;
    vi.mocked(fetchGlobalConfig).mockReturnValue(new Promise((r) => (resolve = r)) as never);
    const { container } = render(<App />);
    expect(container.innerHTML).toBe("");
    await act(async () => resolve({ content: { desktop: { theme: "light", font_sizes: { chat: 20 }, islands: false, hidden_pills: ["ci"] } } }));
    expect(screen.getByTestId("sidebar")).toBeTruthy();
    // Islands off: the app has no gap around its panes.
    expect(screen.getByTestId("sidebar").parentElement!.style.padding).toBe("0px");
  });

  it("renders with the defaults when the config has no desktop section", async () => {
    vi.mocked(fetchGlobalConfig).mockResolvedValue({} as never);
    await renderApp();
    expect(screen.getByTestId("sidebar").parentElement!.style.padding).not.toBe("0px");
  });

  it("shows the inbox in the inbox window", async () => {
    vi.mocked(isInboxWindow).mockReturnValue(true);
    render(<App />);
    expect(await screen.findByTestId("inbox")).toBeTruthy();
    expect(screen.queryByTestId("sidebar")).toBeNull();
  });

  it("explains a failed load and retries", async () => {
    vi.mocked(fetchGlobalConfig).mockRejectedValueOnce(new Error("refused")).mockRejectedValueOnce("plain");
    vi.mocked(isDaemonHealthy).mockResolvedValue(false);
    render(<App />);
    await screen.findByTestId("daemon-unavailable");
    expect(props(DaemonUnavailable)).toMatchObject({ apiUrl: "http://daemon", error: "refused", running: false });
    vi.mocked(isDaemonHealthy).mockResolvedValue(true);
    await call(() => props(DaemonUnavailable).onRetry());
    expect(props(DaemonUnavailable)).toMatchObject({ error: "plain", running: true });
    await call(() => props(DaemonUnavailable).onRetry());
    expect(await screen.findByTestId("sidebar")).toBeTruthy();
    expect(screen.queryByTestId("daemon-unavailable")).toBeNull();
  });
});

describe("selection", () => {
  it("auto-selects the DM channel and keeps the hash and storage in step", async () => {
    await renderWithDm();
    expect(workspace()).toMatchObject({ channelId: "dm-id", channel: dm });
    expect(store().markRead).toHaveBeenCalledWith("dm-id");
    expect(window.location.hash).toBe("#dm-id");
    expect(localStorage.getItem("loop-last-channel")).toBe("dm-id");
    expect(document.title).toBe("dm — Loop");
  });

  it("creates the DM channel once when there is none", async () => {
    vi.mocked(fetchChannels).mockResolvedValueOnce([proj]).mockResolvedValue([proj, dm]);
    vi.mocked(createChannel).mockResolvedValue("dm-id");
    await renderWithDm();
    expect(createChannel).toHaveBeenCalledWith("dm");
    expect(createChannel).toHaveBeenCalledOnce();
  });

  it("carries on without a DM channel when it can't be created", async () => {
    vi.mocked(fetchChannels).mockResolvedValue([proj]);
    vi.mocked(createChannel).mockRejectedValue(new Error("off"));
    await renderApp();
    await waitFor(() => expect(sidebar().channels).toEqual([proj]));
    expect(screen.queryByTestId("workspace")).toBeNull();
    expect(document.title).toBe("Loop");
  });

  it("opens the channel in the hash, scrolled to its message", async () => {
    window.history.replaceState(null, "", "/#proj/42");
    await renderApp();
    await screen.findByTestId("workspace");
    expect(workspace()).toMatchObject({ channelId: "proj", scrollToMessageId: 42 });
    expect(store().markRead).not.toHaveBeenCalled();
  });

  it("restores the last channel without a hash and drops a stored value that isn't one", async () => {
    localStorage.setItem("loop-last-channel", "wt");
    const first = await renderApp();
    await screen.findByTestId("workspace");
    expect(workspace().channelId).toBe("wt");
    expect(document.title).toBe("proj › feature — Loop");
    expect(workspace().parentIsWorktree).toBe(false);
    first.unmount();
    window.history.replaceState(null, "", "/");
    localStorage.setItem("loop-last-channel", "a/b/c");
    await renderWithDm();
    expect(workspace().channelId).toBe("dm-id");
  });

  it("titles a thread under a worktree and one whose parent isn't listed", async () => {
    window.history.replaceState(null, "", "/#th");
    vi.mocked(fetchChannels).mockResolvedValue([dm, proj, wt, thread, ch("lone", { parent_id: "gone" })]);
    await renderApp();
    await screen.findByTestId("workspace");
    expect(document.title).toBe("feature › talk — Loop");
    expect(workspace().parentIsWorktree).toBe(true);
    act(() => sidebar().onSelect("lone"));
    expect(document.title).toBe("lone — Loop");
    expect(workspace().parentIsWorktree).toBe(false);
  });

  it("titles a selected channel not in the list yet as Loop", async () => {
    window.history.replaceState(null, "", "/#missing");
    await renderApp();
    expect(document.title).toBe("Loop");
    expect(screen.queryByTestId("workspace")).toBeNull();
  });

  it("selects from the sidebar, remounts on selecting the same channel, and clears", async () => {
    await renderWithDm();
    const mounts = h.layoutMounts.mock.calls.length;
    vi.mocked(fetchChannels).mockClear();
    act(() => sidebar().onSelect("proj"));
    expect(workspace().channelId).toBe("proj");
    expect(store().markRead).toHaveBeenCalledWith("proj");
    expect(fetchChannels).toHaveBeenCalled();
    act(() => sidebar().onSelect("proj"));
    expect(h.layoutMounts.mock.calls.length).toBe(mounts + 2);
    act(() => sidebar().onSelect(null));
    // Clearing the selection auto-selects the DM channel again only once.
    expect(window.location.hash).toBe("");
  });

  it("keeps the selection empty when the stored channel can't be forgotten", async () => {
    localStorage.setItem("loop-last-channel", "proj");
    await renderApp();
    await screen.findByTestId("workspace");
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    act(() => sidebar().onSelect(null));
    expect(screen.queryByTestId("workspace")).toBeNull();
    expect(emptyViewShown()).toBe(true);
  });

  it("asks before switching away from unsaved config changes", async () => {
    await renderWithDm();
    act(() => sidebar().onOpenSettings());
    expect(screen.getByTestId("settings")).toBeTruthy();
    act(() => props(Settings).onConfigDirtyChange(true));
    act(() => sidebar().onSelect("proj"));
    expect(workspace().channelId).toBe("dm-id");
    expect(screen.getByText("Unsaved Changes")).toBeTruthy();
    // A click inside the dialog keeps it; the backdrop and Cancel close it.
    fireEvent.click(screen.getByText("Unsaved Changes"));
    expect(screen.getByText("Unsaved Changes")).toBeTruthy();
    fireEvent.click(screen.getByText("Unsaved Changes").parentElement!.parentElement!);
    expect(screen.queryByText("Unsaved Changes")).toBeNull();
    act(() => sidebar().onSelect("proj"));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByText("Unsaved Changes")).toBeNull();
    act(() => sidebar().onSelect("proj"));
    fireEvent.click(screen.getByRole("button", { name: "Discard & Switch" }));
    expect(workspace().channelId).toBe("proj");
    expect(screen.queryByTestId("settings")).toBeNull();
  });

  it("lets UI commands select a channel, fetching the list for a new one", async () => {
    await renderWithDm();
    const select = () => vi.mocked(useUiBridge).mock.lastCall![1];
    expect(vi.mocked(useUiBridge).mock.lastCall![0]).toBe("dm-id");
    await call(() => select()("proj"));
    expect(workspace().channelId).toBe("proj");
    vi.mocked(fetchChannels).mockResolvedValue([dm, proj, wt, thread, ch("new")]);
    await call(() => select()("new"));
    expect(workspace().channelId).toBe("new");
    await expect(select()("nope")).rejects.toThrow("no channel nope");
  });
});

describe("messages", () => {
  it("opens a message from the palette, remounting when it's in the open channel", async () => {
    await renderWithDm();
    const mounts = h.layoutMounts.mock.calls.length;
    act(() => props(CommandPalette).onSelectMessage("proj", 7));
    expect(workspace()).toMatchObject({ channelId: "proj", scrollToMessageId: 7 });
    act(() => props(CommandPalette).onSelectMessage("proj", 8));
    expect(workspace().scrollToMessageId).toBe(8);
    expect(h.layoutMounts.mock.calls.length).toBe(mounts + 2);
    act(() => workspace().onScrollComplete(true));
    expect(workspace().scrollToMessageId).toBeNull();
  });

  it("waits for a UI command's message to scroll into view", async () => {
    await renderWithDm();
    const show = () => vi.mocked(useUiBridge).mock.lastCall![2];
    const mounts = h.layoutMounts.mock.calls.length;
    let first!: Promise<boolean>;
    act(() => {
      first = show()("dm-id", 3);
    });
    expect(workspace().scrollToMessageId).toBe(3);
    expect(h.layoutMounts.mock.calls.length).toBe(mounts);
    let second!: Promise<boolean>;
    act(() => {
      second = show()("proj", 4);
    });
    // A newer step settles the one before it as not found.
    await expect(first).resolves.toBe(false);
    expect(workspace()).toMatchObject({ channelId: "proj", scrollToMessageId: 4 });
    act(() => workspace().onScrollComplete(true));
    await expect(second).resolves.toBe(true);
  });
});

describe("channel targets", () => {
  const navigate = async (hash: string) => {
    await act(async () => {
      window.history.replaceState(null, "", `/${hash}`);
      window.dispatchEvent(new HashChangeEvent("hashchange"));
    });
  };

  it("follows hash changes to channels and messages", async () => {
    await renderWithDm();
    await navigate("#proj");
    expect(workspace().channelId).toBe("proj");
    await navigate("#wt/9");
    expect(workspace()).toMatchObject({ channelId: "wt", scrollToMessageId: 9 });
    expect(window.location.hash).toBe("#wt");
    // The open channel's own hash changes nothing.
    vi.mocked(fetchChannels).mockClear();
    await navigate("#wt");
    expect(fetchChannels).not.toHaveBeenCalled();
    // Junk is ignored.
    await navigate("#a/b/c");
    expect(workspace().channelId).toBe("wt");
  });

  it("restores the stored channel when the hash is emptied", async () => {
    await renderWithDm();
    act(() => sidebar().onSelect("proj"));
    await navigate("");
    expect(workspace().channelId).toBe("proj");
  });

  it("says when the channel is gone and puts the hash back", async () => {
    await renderWithDm();
    await navigate("#gone");
    expect(workspace().error).toBe("That channel or thread doesn't exist anymore.");
    expect(window.location.hash).toBe("#dm-id");
    act(() => workspace().onDismissError());
    expect(workspace().error).toBeNull();
  });

  it("puts an empty hash back when nothing is selected", async () => {
    vi.mocked(fetchChannels).mockResolvedValue([proj]);
    vi.mocked(createChannel).mockRejectedValue(new Error("off"));
    const api = loopAPI();
    await renderApp();
    await call(() => api.onNavigateChannel.mock.lastCall![0]("gone"));
    expect(screen.getByRole("alert").textContent).toContain("That channel or thread doesn't exist anymore.");
    expect(window.location.hash).toBe("");
    fireEvent.click(screen.getByRole("button", { name: "×" }));
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("shows a failed lookup", async () => {
    await renderWithDm();
    vi.mocked(fetchChannels).mockRejectedValue(new Error("offline"));
    await navigate("#other");
    expect(workspace().error).toBe("offline");
    vi.mocked(fetchChannels).mockRejectedValue("plain");
    await navigate("#other2");
    expect(workspace().error).toBe("Failed to open the channel");
  });

  it("opens deep links from the desktop app", async () => {
    const api = loopAPI();
    await renderWithDm();
    await call(() => api.onNavigateChannel.mock.lastCall![0]("proj/5"));
    expect(workspace()).toMatchObject({ channelId: "proj", scrollToMessageId: 5 });
  });
});

describe("live events", () => {
  it("reloads on channel created and deleted, and drops a deleted selection", async () => {
    await renderWithDm();
    vi.mocked(fetchChannels).mockClear();
    appEvent({ type: "channel.created" });
    await waitFor(() => expect(fetchChannels).toHaveBeenCalledTimes(1));
    appEvent({ type: "channel.deleted", channel_id: "proj" });
    await waitFor(() => expect(fetchChannels).toHaveBeenCalledTimes(2));
    expect(workspace().channelId).toBe("dm-id");
    appEvent({ type: "channel.deleted", channel_id: "dm-id" });
    expect(screen.queryByTestId("workspace")).toBeNull();
  });

  it("patches channels from updates, review status, PRs, badges and agent config", async () => {
    await renderWithDm();
    const find = (id: string) => (sidebar().channels as Channel[]).find((c) => c.id === id)!;
    appEvent({ type: "channel.updated", data: { channel_id: "proj", name: "renamed" } as never });
    expect(find("proj").name).toBe("renamed");
    expect(find("dm-id").name).toBe("dm");
    appEvent({ type: "review.status", channel_id: "proj", timestamp: 5, data: { status: "reviewing" } as never });
    expect(find("proj").review_running).toBe(true);
    appEvent({ type: "channel.pr", channel_id: "proj", data: { number: 3, kind: "pr", title: "T", pr_attention: "failed" } as never });
    expect(find("proj")).toMatchObject({ pr_number: 3, pr_attention: "failed" });
    appEvent({ type: "plugin.badge", channel_id: "proj", data: { plugin_id: "q", text: "1" } as never });
    expect(find("proj").plugin_badges).toEqual([{ plugin_id: "q", text: "1" }]);
    appEvent({ type: "channel.agent_config", channel_id: "proj", data: { model_override: "m", effort_override: "high" } as never });
    expect(find("proj")).toMatchObject({ model_override: "m", effort_override: "high" });
    // The CI pill follows the channels.
    await waitFor(() => expect(store().syncPill).toHaveBeenCalledWith("ci", new Set(["proj"])));
    expect(store().syncPill).toHaveBeenCalledWith("trust", expect.any(Set));
    expect(store().syncPill).toHaveBeenCalledWith("conflict", expect.any(Set));
    expect(store().syncPill).toHaveBeenCalledWith("comment", expect.any(Set));
  });

  it("keeps a live patch over an older fetch's list", async () => {
    let t = 0;
    vi.spyOn(performance, "now").mockImplementation(() => ++t);
    await renderWithDm();
    let resolveSlow!: (v: Channel[]) => void;
    let resolveFast!: (v: Channel[]) => void;
    vi.mocked(fetchChannels)
      .mockReturnValueOnce(new Promise((r) => (resolveSlow = r)))
      .mockReturnValueOnce(new Promise((r) => (resolveFast = r)));
    appEvent({ type: "channel.created" });
    appEvent({ type: "channel.updated", data: { channel_id: "proj", name: "live" } as never });
    appEvent({ type: "channel.created" });
    await act(async () => resolveFast([dm, ch("proj", { name: "fast" })]));
    expect((sidebar().channels as Channel[]).map((c) => c.name)).toEqual(["dm", "fast"]);
    await act(async () => resolveSlow([dm, ch("proj", { name: "slow" })]));
    expect((sidebar().channels as Channel[]).map((c) => c.name)).toEqual(["dm", "fast"]);
  });

  it("refetches the unread notifications and bumps the plugins epoch", async () => {
    await renderWithDm();
    const state = () => JSON.parse(screen.getByTestId("workspace").dataset.notifications!);
    await waitFor(() => expect(fetchUnreadNotifications).toHaveBeenCalledOnce());
    vi.mocked(fetchUnreadNotifications).mockResolvedValue({ "dm-id": 2 });
    appEvent({ type: "notifications.changed" });
    await waitFor(() => expect(state()).toEqual({ unread: { "dm-id": 2 }, epoch: 1, pluginsEpoch: 0 }));
    appEvent({ type: "plugins.changed" });
    expect(state().pluginsEpoch).toBe(1);
  });

  it("tracks the agent image's build and update", async () => {
    await renderWithDm();
    appEvent({ type: "image.build_status", data: { state: "building" } as never });
    expect(sidebar().imageBuildStatus).toEqual({ state: "building" });
    appEvent({ type: "image.update_available", data: { latest_version: "v2" } as never });
    expect(sidebar().imageUpdateAvailable).toEqual({ latest_version: "v2" });
    appEvent({ type: "image.update_available", data: { latest_version: "" } as never });
    expect(sidebar().imageUpdateAvailable).toBeNull();
  });

  it("hands container and workflow events to their open panels", async () => {
    await renderWithDm();
    appEvent({ type: "container.started" });
    appEvent({ type: "workflow.run" });
    expect(h.containers.handleContainerEvent).not.toHaveBeenCalled();
    act(() => sidebar().onOpenContainers());
    appEvent({ type: "container.started" });
    expect(h.containers.handleContainerEvent).toHaveBeenCalledWith(expect.objectContaining({ type: "container.started" }));
    act(() => sidebar().onOpenWorkflows());
    appEvent({ type: "workflow.run" });
    expect(h.workflows.handleWorkflowEvent).toHaveBeenCalledWith(expect.objectContaining({ type: "workflow.run" }));
  });

  it("counts the shared playgrounds again after a share changes", async () => {
    vi.mocked(fetchPlaygroundShares)
      .mockResolvedValueOnce([])
      .mockResolvedValueOnce([{}, {}] as never);
    await renderWithDm();
    appEvent({ type: "playground.update", data: { kind: "file" } as never });
    appEvent({ type: "playground.update" });
    appEvent({ type: "something.else" });
    expect(fetchPlaygroundShares).toHaveBeenCalledOnce();
    appEvent({ type: "playground.update", data: { kind: "share" } as never });
    await waitFor(() => expect(sidebar().shareCount).toBe(2));
  });

  it("ignores a failed share count", async () => {
    vi.mocked(fetchPlaygroundShares).mockRejectedValue(new Error("x"));
    await renderWithDm();
    expect(sidebar().shareCount).toBe(0);
  });
});

describe("polling and status", () => {
  it("reads the channels every 30 seconds and survives a failed read", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    await renderWithDm();
    vi.mocked(fetchChannels).mockClear();
    vi.mocked(fetchChannels).mockRejectedValueOnce(new Error("down"));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000);
    });
    expect(fetchChannels).toHaveBeenCalledOnce();
    expect(sidebar().channels).toHaveLength(4);
  });

  it("seeds the image status", async () => {
    vi.mocked(getImageStatus).mockResolvedValue({ status: { state: "building" }, update_available: { latest_version: "v3" } } as never);
    await renderWithDm();
    await waitFor(() => expect(sidebar().imageBuildStatus).toEqual({ state: "building" }));
    expect(sidebar().imageUpdateAvailable).toEqual({ latest_version: "v3" });
  });

  it("ignores an idle image status", async () => {
    vi.mocked(getImageStatus).mockResolvedValue({ status: { state: "idle" } } as never);
    await renderWithDm();
    await waitFor(() => expect(getImageStatus).toHaveBeenCalled());
    expect(sidebar().imageBuildStatus).toBeNull();
    expect(sidebar().imageUpdateAvailable).toBeNull();
  });

  it("logs a failed image status", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    vi.mocked(getImageStatus).mockRejectedValue(new Error("x"));
    await renderWithDm();
    await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] fetching agent-image status:", expect.any(Error)));
  });

  it("follows the desktop app's updater and settings menu", async () => {
    const api = loopAPI();
    await renderWithDm();
    await waitFor(() => expect(sidebar().updateStatus).toMatchObject({ version: "2" }));
    act(() => api.onUpdateStatus.mock.lastCall![0]({ available: true, version: "3", downloading: true, downloaded: false }));
    expect(sidebar().updateStatus).toMatchObject({ version: "3" });
    await call(() => sidebar().onDownloadUpdate());
    expect(api.downloadUpdate).toHaveBeenCalledOnce();
    act(() => sidebar().onInstallUpdate());
    expect(api.installUpdate).toHaveBeenCalledOnce();
    act(() => api.onOpenSettings.mock.lastCall![0]());
    expect(screen.getByTestId("settings")).toBeTruthy();
  });

  it("logs a failed update download and image rebuild", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    loopAPI({ downloadUpdate: vi.fn(async () => Promise.reject(new Error("dl"))) });
    vi.mocked(rebuildImage).mockRejectedValueOnce(new Error("rb")).mockResolvedValueOnce(undefined);
    await renderWithDm();
    await call(() => sidebar().onDownloadUpdate());
    expect(warn).toHaveBeenCalledWith("download update failed:", expect.any(Error));
    await call(() => sidebar().onRebuildImage());
    expect(warn).toHaveBeenCalledWith("rebuild image failed:", expect.any(Error));
    await call(() => sidebar().onRebuildImage());
    expect(rebuildImage).toHaveBeenCalledTimes(2);
  });

  it("works without the desktop bridge", async () => {
    await renderWithDm();
    await call(() => sidebar().onDownloadUpdate());
    act(() => sidebar().onInstallUpdate());
    expect(sidebar().updateStatus).toBeNull();
  });
});

describe("keyboard", () => {
  it("toggles the palette and settings and switches to the editor", async () => {
    await renderWithDm();
    fireEvent.keyDown(window, { key: "k", metaKey: true });
    expect(props(CommandPalette).open).toBe(true);
    fireEvent.keyDown(window, { key: "k", ctrlKey: true });
    expect(props(CommandPalette).open).toBe(false);
    fireEvent.keyDown(window, { key: ",", metaKey: true });
    expect(screen.getByTestId("settings")).toBeTruthy();
    fireEvent.keyDown(window, { key: ",", ctrlKey: true });
    expect(screen.queryByTestId("settings")).toBeNull();
    fireEvent.keyDown(window, { key: "e", metaKey: true });
    expect(h.layout.switchToLayout).toHaveBeenCalledWith("Editor");
    fireEvent.keyDown(window, { key: "k" });
    fireEvent.keyDown(window, { key: "e" });
    expect(props(CommandPalette).open).toBe(false);
    expect(h.layout.switchToLayout).toHaveBeenCalledOnce();
  });

  it("does nothing for the editor shortcut without a layout", async () => {
    vi.mocked(fetchChannels).mockResolvedValue([proj]);
    vi.mocked(createChannel).mockRejectedValue(new Error("off"));
    await renderApp();
    fireEvent.keyDown(window, { key: "e", ctrlKey: true });
    expect(h.layout.switchToLayout).not.toHaveBeenCalled();
  });

  it("opens and closes the palette from the layout and the palette", async () => {
    await renderWithDm();
    act(() => workspace().onOpenPalette());
    expect(props(CommandPalette).open).toBe(true);
    act(() => props(CommandPalette).onClose());
    expect(props(CommandPalette).open).toBe(false);
    act(() => props(CommandPalette).onSelect("proj"));
    expect(workspace().channelId).toBe("proj");
  });

  it("opens a memory file in the Memory layout", async () => {
    await renderWithDm();
    act(() => props(CommandPalette).onSelectMemoryFile("/m/notes.md"));
    expect(workspace().openMemoryFile).toBe("/m/notes.md");
    expect(h.layout.switchToLayout).toHaveBeenCalledWith("Memory");
    act(() => workspace().onOpenMemoryFileComplete());
    expect(workspace().openMemoryFile).toBeNull();
  });
});

describe("sidebar", () => {
  it("opens the way it was left and remembers toggles from every place", async () => {
    localStorage.setItem("loop-sidebar-open", "false");
    await renderWithDm();
    expect(sidebar().collapsed).toBe(true);
    act(() => sidebar().onToggleSidebar());
    expect(sidebar().collapsed).toBe(false);
    expect(localStorage.getItem("loop-sidebar-open")).toBe("true");
    act(() => workspace().onToggleSidebar());
    expect(sidebar().collapsed).toBe(true);
    expect(workspace().sidebarOpen).toBe(false);
  });

  it("passes the chat store through", async () => {
    await renderWithDm();
    const s = store();
    expect(sidebar()).toMatchObject({ isRunningMapRef: s.isRunningMapRef, unreadIdsRef: s.unreadIdsRef, pillsRef: s.pillsRef, unreadCount: 0, onMarkAllRead: s.markAllRead });
    expect(workspace()).toMatchObject({ getChatState: s.getState, subscribeChatEvents: s.subscribeChatEvents, registerReviewView: s.registerReviewView, wsOpens: 0 });
    act(() => workspace().onChatStateUnmount("dm-id", { isRunning: false }));
    expect(s.saveState).toHaveBeenCalledWith("dm-id", { isRunning: false });
    expect(s.getState).toHaveBeenCalledWith("dm-id");
    await call(() => workspace().onStatusChange());
  });
});

describe("channel actions", () => {
  it("opens a directory as a channel after onboarding it", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const api = loopAPI({ onboardLocal: vi.fn(async () => ({ ok: false, error: "no git" })) });
    vi.mocked(ensureChannel).mockResolvedValue(proj as never);
    await renderWithDm();
    await call(() => sidebar().onOpenDirectory("/p/proj"));
    expect(api.onboardLocal).toHaveBeenCalledWith("/p/proj");
    expect(warn).toHaveBeenCalledWith("onboard:local failed:", "no git");
    expect(ensureChannel).toHaveBeenCalledWith("/p/proj");
    expect(workspace().channelId).toBe("proj");
  });

  it("opens a directory that onboards cleanly, or without the desktop bridge", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    vi.mocked(ensureChannel).mockResolvedValue(proj as never);
    await renderWithDm();
    await call(() => sidebar().onOpenDirectory("/p/proj"));
    expect(workspace().channelId).toBe("proj");
    loopAPI();
    await call(() => sidebar().onOpenDirectory("/p/wt"));
    expect(warn).not.toHaveBeenCalled();
  });

  it("creates channels, threads and worktrees and selects them", async () => {
    vi.mocked(createChannel).mockResolvedValue("proj");
    vi.mocked(createThread).mockResolvedValue("th");
    vi.mocked(createWorktreeThread).mockResolvedValue({ threadId: "wt" } as never);
    vi.mocked(importWorktree).mockResolvedValue({ threadId: "wt" } as never);
    await renderWithDm();
    await call(() => sidebar().onCreateChannel("proj"));
    expect(createChannel).toHaveBeenLastCalledWith("proj");
    expect(workspace().channelId).toBe("proj");
    await call(() => sidebar().onCreateThread("wt", "talk"));
    expect(createThread).toHaveBeenCalledWith("wt", "talk");
    expect(workspace().channelId).toBe("th");
    act(() => sidebar().onSelect("dm-id"));
    await call(() => workspace().onCreateWorktree("proj", "feature"));
    expect(createWorktreeThread).toHaveBeenCalledWith("proj", "feature");
    expect(workspace().channelId).toBe("wt");
    act(() => sidebar().onSelect("dm-id"));
    await call(() => sidebar().onImportWorktree("proj", "/w/x"));
    expect(importWorktree).toHaveBeenCalledWith("proj", "/w/x");
    expect(workspace().channelId).toBe("wt");
  });

  it("opens a PR in its thread, its worktree imported, or a new worktree", async () => {
    vi.mocked(importWorktree).mockResolvedValue({ threadId: "wt" } as never);
    vi.mocked(createWorktreeThread).mockResolvedValue({ threadId: "th" } as never);
    await renderWithDm();
    const pr = { number: 12, base_ref: "fix" };
    await call(() => sidebar().onOpenPR("proj", pr, { thread_id: "proj", path: "/w" }));
    expect(workspace().channelId).toBe("proj");
    expect(importWorktree).not.toHaveBeenCalled();
    await call(() => sidebar().onOpenPR("proj", pr, { path: "/w/fix" }));
    expect(importWorktree).toHaveBeenCalledWith("proj", "/w/fix");
    expect(workspace().channelId).toBe("wt");
    await call(() => workspace().onOpenPR("proj", pr));
    expect(createWorktreeThread).toHaveBeenCalledWith("proj", "fix", undefined, 12);
    expect(workspace().channelId).toBe("th");
  });

  it.each([
    ["onOpenDirectory", () => vi.mocked(ensureChannel), ["/x"], "Failed to open directory", "open directory failed:"],
    ["onCreateChannel", () => vi.mocked(createChannel), ["x"], "Failed to create channel", "create channel failed:"],
    ["onCreateThread", () => vi.mocked(createThread), ["proj", "x"], "Failed to create thread", "create thread failed:"],
    ["onCreateWorktree", () => vi.mocked(createWorktreeThread), ["proj", "b"], "Failed to create worktree", "create worktree failed:"],
    ["onOpenPR", () => vi.mocked(createWorktreeThread), ["proj", { number: 1, base_ref: "b" }], "Failed to open the PR in a worktree", "open PR worktree failed:"],
    ["onImportWorktree", () => vi.mocked(importWorktree), ["proj", "/w"], "Failed to import worktree", "import worktree failed:"],
    ["onRenameThread", () => vi.mocked(renameChannel), ["proj", "n"], "Failed to rename", "rename failed:"],
    ["onSetDescription", () => vi.mocked(setChannelDescription), ["proj", "d"], "Failed to set description", "set description failed:"],
    ["onSetTicketURL", () => vi.mocked(setChannelTicketURL), ["proj", "u"], "Failed to set ticket", "set ticket failed:"],
    ["onEditTags", () => vi.mocked(editChannelTags), ["proj", ["a"], []], "Failed to edit tags", "edit tags failed:"],
    ["onSetLocked", () => vi.mocked(setChannelLocked), ["proj", true], "Failed to update lock", "set locked failed:"],
    ["onDeleteThread", () => vi.mocked(deleteChannel), ["proj"], "Failed to delete", "delete failed:"],
    ["onDeleteBatch", () => vi.mocked(deleteChannel), [["proj"]], "Failed to delete", "batch delete failed:"],
  ] as const)("%s shows its error", async (handler, api, args, fallback, logged) => {
    const error = vi.spyOn(console, "error").mockImplementation(() => {});
    await renderWithDm();
    // biome-ignore lint/suspicious/noExplicitAny: each mock rejects alike
    const m = api() as any;
    m.mockRejectedValueOnce(new Error("boom"));
    await call(() => sidebar()[handler](...args));
    expect(workspace().error).toBe("boom");
    expect(error).toHaveBeenCalledWith(logged, expect.any(Error));
    m.mockRejectedValueOnce("plain");
    await call(() => sidebar()[handler](...args));
    expect(workspace().error).toBe(fallback);
  });

  it("renames, describes, tickets and locks channels", async () => {
    await renderWithDm();
    vi.mocked(fetchChannels).mockClear();
    await call(() => sidebar().onRenameThread("proj", "new"));
    expect(renameChannel).toHaveBeenCalledWith("proj", "new");
    await call(() => sidebar().onSetDescription("proj", "about"));
    expect(setChannelDescription).toHaveBeenCalledWith("proj", "about");
    await call(() => sidebar().onSetTicketURL("proj", "https://t/1"));
    expect(setChannelTicketURL).toHaveBeenCalledWith("proj", "https://t/1");
    await call(() => sidebar().onEditTags("proj", ["a"], ["b"]));
    expect(editChannelTags).toHaveBeenCalledWith("proj", ["a"], ["b"]);
    expect(sidebar().loadTags).toBe(listTags);
    await call(() => sidebar().onSetLocked("proj", true));
    expect(setChannelLocked).toHaveBeenCalledWith("proj", true);
    // Rename and lock read the list again; description and ticket wait for the event.
    expect(fetchChannels).toHaveBeenCalledTimes(2);
  });

  it("deletes a thread under a cover and selects its parent", async () => {
    window.history.replaceState(null, "", "/#th");
    let finish!: () => void;
    vi.mocked(deleteThread).mockReturnValue(new Promise<void>((r) => (finish = r)) as never);
    await renderApp();
    await screen.findByTestId("workspace");
    let done!: Promise<void>;
    act(() => {
      done = sidebar().onDeleteThread("th");
    });
    expect(screen.getByTestId("deleting-overlay").textContent).toContain("Deleting thread talk…");
    await act(async () => {
      finish();
      await done;
    });
    expect(deleteThread).toHaveBeenCalledWith("th");
    expect(screen.queryByTestId("deleting-overlay")).toBeNull();
    expect(workspace().channelId).toBe("wt");
  });

  it("deletes a channel, or one not listed, keeping another selection", async () => {
    await renderWithDm();
    let finish!: () => void;
    vi.mocked(deleteChannel).mockReturnValueOnce(new Promise<void>((r) => (finish = r)) as never);
    let done!: Promise<void>;
    act(() => {
      done = sidebar().onDeleteThread("unknown");
    });
    expect(screen.getByTestId("deleting-overlay").textContent).toContain("Deleting channel …");
    await act(async () => {
      finish();
      await done;
    });
    expect(deleteChannel).toHaveBeenCalledWith("unknown");
    await call(() => sidebar().onDeleteThread("proj"));
    expect(deleteChannel).toHaveBeenCalledWith("proj");
    expect(workspace().channelId).toBe("dm-id");
  });

  it("deletes the selected channel and empties the view", async () => {
    await renderWithDm();
    act(() => sidebar().onSelect("proj"));
    await call(() => sidebar().onDeleteThread("proj"));
    expect(screen.queryByTestId("workspace")).toBeNull();
  });

  it("deletes a batch, threads and channels, and moves off a deleted selection", async () => {
    window.history.replaceState(null, "", "/#th");
    await renderApp();
    await screen.findByTestId("workspace");
    let finish!: () => void;
    vi.mocked(deleteThread).mockReturnValueOnce(new Promise<void>((r) => (finish = r)) as never);
    let done!: Promise<void>;
    act(() => {
      done = sidebar().onDeleteBatch(["th", "dm-id"]);
    });
    expect(screen.getByTestId("deleting-overlay").textContent).toContain("Deleting 2 channels…");
    await act(async () => {
      finish();
      await done;
    });
    expect(deleteThread).toHaveBeenCalledWith("th");
    expect(deleteChannel).toHaveBeenCalledWith("dm-id");
    expect(workspace().channelId).toBe("wt");
    let finish2!: () => void;
    vi.mocked(deleteChannel).mockReturnValueOnce(new Promise<void>((r) => (finish2 = r)) as never);
    act(() => {
      done = sidebar().onDeleteBatch(["proj"]);
    });
    expect(screen.getByTestId("deleting-overlay").textContent).toContain("Deleting 1 channel…");
    await act(async () => {
      finish2();
      await done;
    });
    expect(workspace().channelId).toBe("wt");
  });

  it("moves off a selection a failed batch deleted before failing", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    await renderWithDm();
    vi.mocked(deleteChannel)
      .mockResolvedValueOnce(undefined as never)
      .mockRejectedValueOnce(new Error("second"));
    await call(() => sidebar().onDeleteBatch(["dm-id", "proj"]));
    expect(screen.queryByTestId("workspace")).toBeNull();
    expect(screen.getByRole("alert").textContent).toContain("second");
  });
});

describe("panels", () => {
  it("opens each panel over the layout and closes it", async () => {
    await renderWithDm();
    const cases = [
      ["onOpenReadme", "readme", MarkdownFilePanel],
      ["onOpenContainers", "containers", ContainersPanel],
      ["onOpenTasks", "tasks", GlobalTasksPanel],
      ["onOpenWorkflows", "workflows", WorkflowsGlobalPanel],
      ["onOpenShares", "shares", GlobalSharesPanel],
    ] as const;
    for (const [open, testId, component] of cases) {
      act(() => sidebar()[open]());
      expect(screen.getByTestId(testId)).toBeTruthy();
      expect(workspace().style).toEqual({ display: "none" });
      expect(props(component).channel).toEqual(dm);
      act(() => props(component).onOpenPalette());
      expect(props(CommandPalette).open).toBe(true);
      act(() => props(CommandPalette).onClose());
      act(() => props(component).onToggleSidebar?.());
      act(() => props(component).onClose());
      expect(screen.queryByTestId(testId)).toBeNull();
      expect(workspace().style).toBeUndefined();
    }
    expect(props(MarkdownFilePanel)).toMatchObject({ dirPath: "/p/dm-id", branch: "main", maximized: true });
    expect(sidebar().collapsed).toBe(true);
  });

  it("selects a channel from the tasks and workflows panels", async () => {
    await renderWithDm();
    act(() => sidebar().onOpenTasks());
    act(() => props(GlobalTasksPanel).onSelectChannel("proj"));
    expect(screen.queryByTestId("tasks")).toBeNull();
    expect(workspace().channelId).toBe("proj");
    act(() => sidebar().onOpenWorkflows());
    act(() => props(WorkflowsGlobalPanel).onSelectChannel("wt"));
    expect(screen.queryByTestId("workflows")).toBeNull();
    expect(workspace().channelId).toBe("wt");
  });

  it("shows the global panels and the empty view without a channel", async () => {
    vi.mocked(fetchChannels).mockResolvedValue([proj]);
    vi.mocked(createChannel).mockRejectedValue(new Error("off"));
    await renderApp();
    await waitFor(() => expect(sidebar().channels).toHaveLength(1));
    expect(emptyViewShown()).toBe(true);
    const cases = [
      ["onOpenContainers", "containers", ContainersPanel],
      ["onOpenTasks", "tasks", GlobalTasksPanel],
      ["onOpenWorkflows", "workflows", WorkflowsGlobalPanel],
      ["onOpenShares", "shares", GlobalSharesPanel],
    ] as const;
    for (const [open, testId, component] of cases) {
      act(() => sidebar()[open]());
      expect(screen.getByTestId(testId)).toBeTruthy();
      expect(props(component).channel).toBeUndefined();
      act(() => props(component).onOpenPalette());
      act(() => props(component).onToggleSidebar?.());
      act(() => props(component).onClose());
      expect(screen.queryByTestId(testId)).toBeNull();
    }
    act(() => sidebar().onOpenTasks());
    act(() => props(GlobalTasksPanel).onSelectChannel("proj"));
    expect(workspace().channelId).toBe("proj");
    act(() => sidebar().onSelect(null));
    act(() => sidebar().onOpenWorkflows());
    act(() => props(WorkflowsGlobalPanel).onSelectChannel("proj"));
    expect(workspace().channelId).toBe("proj");
  });

  it("shows settings alone without a channel", async () => {
    vi.mocked(fetchChannels).mockResolvedValue([proj]);
    vi.mocked(createChannel).mockRejectedValue(new Error("off"));
    await renderApp();
    act(() => sidebar().onOpenSettings());
    expect(screen.getByTestId("settings")).toBeTruthy();
    expect(emptyViewShown()).toBe(false);
    expect(props(Settings)).toMatchObject({ channelId: null, channel: undefined, projectDirPath: null });
  });

  it("opens a project's settings for its channel", async () => {
    await renderWithDm();
    act(() => sidebar().onOpenConfig("/p/proj"));
    expect(props(Settings)).toMatchObject({ projectDirPath: "/p/proj", channelId: "proj", channel: proj });
    act(() => props(Settings).onToggleSidebar());
    expect(sidebar().collapsed).toBe(true);
    act(() => props(Settings).onOpenPalette());
    expect(props(CommandPalette).open).toBe(true);
    await call(() => props(Settings).onDaemonRestarted());
    act(() => props(Settings).onClose());
    expect(screen.queryByTestId("settings")).toBeNull();
    act(() => sidebar().onOpenConfig("/elsewhere"));
    expect(props(Settings)).toMatchObject({ projectDirPath: "/elsewhere", channelId: null, channel: dm });
  });

  it("reviews project trust at the worktree chain's root, or the channel's own dir", async () => {
    localStorage.setItem("loop-last-channel", "wt");
    await renderApp();
    await screen.findByTestId("workspace");
    act(() => workspace().onReviewProjectTrust());
    expect(props(Settings)).toMatchObject({ projectDirPath: "/root/proj", initialSection: "__project_json__" });
    act(() => props(Settings).onClose());
    act(() => sidebar().onSelect("proj"));
    act(() => workspace().onReviewProjectTrust());
    expect(props(Settings).projectDirPath).toBe("/p/proj");
    act(() => props(Settings).onClose());
    act(() => workspace().onFreeDockerSpace());
    expect(screen.getByTestId("settings")).toBeTruthy();
  });

  it("does nothing for project trust on a channel without a dir", async () => {
    vi.mocked(fetchChannels).mockResolvedValue([ch("dm-id", { name: "dm", dir_path: "" })]);
    await renderWithDm();
    act(() => workspace().onReviewProjectTrust());
    expect(screen.queryByTestId("settings")).toBeNull();
  });
});
