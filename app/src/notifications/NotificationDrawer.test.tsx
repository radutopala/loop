import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { clearNotifications, deleteNotification, fetchNotifications, markNotificationsRead, type NotificationInfo } from "../api/notifications";
import { fetchPlugins, PLUGINS_CHANGED, type PluginInfo } from "../api/plugins";
import { ThemeProvider } from "../ThemeContext";
import { darkColors } from "../theme";
import { NotificationButton, NotificationDrawer } from "./NotificationDrawer";
import { NotificationsContext, type NotificationsState } from "./useNotifications";

vi.mock("../api/notifications", () => ({
  clearNotifications: vi.fn(),
  deleteNotification: vi.fn(),
  fetchNotifications: vi.fn(),
  markNotificationsRead: vi.fn(),
}));

vi.mock("../api/plugins", () => ({
  fetchPlugins: vi.fn(),
  PLUGINS_CHANGED: "loop:plugins-changed",
}));

vi.mock("../plugins/PluginCard", () => ({
  PluginCard: (p: { pluginId: string; cardId: string; label: string; data: unknown; channelId: string; channelName: string; dirPath: string; plugins: PluginInfo[] | null }) => (
    <div data-testid="plugin-card">{JSON.stringify({ ...p, plugins: p.plugins?.map((x) => x.id) ?? null })}</div>
  ),
}));

const fetchMock = vi.mocked(fetchNotifications);
const markMock = vi.mocked(markNotificationsRead);
const clearMock = vi.mocked(clearNotifications);
const deleteMock = vi.mocked(deleteNotification);
const pluginsMock = vi.mocked(fetchPlugins);

const note = (over: Partial<NotificationInfo>): NotificationInfo => ({ id: 1, kind: "run", read: true, created_at: "", ...over });
const plugin = (id: string, name?: string): PluginInfo => ({ id, name, version: "1", capabilities: [], status: "ok" });

const state = (over: Partial<NotificationsState> = {}): NotificationsState => ({ unread: {}, epoch: 0, pluginsEpoch: 0, ...over });

function wrap(children: ReactNode, s: NotificationsState = state()) {
  return <NotificationsContext.Provider value={s}>{children}</NotificationsContext.Provider>;
}

describe("NotificationButton", () => {
  it("shows no count and a plain title without unread notifications", async () => {
    const onToggle = vi.fn();
    render(wrap(<NotificationButton channelId="ch-1" open={false} onToggle={onToggle} />));
    const btn = screen.getByTestId("notifications-button");
    expect(btn.title).toBe("Notifications");
    expect(screen.queryByTestId("notifications-unread")).toBeNull();
    expect(btn.style.background).toBe("none");
    await userEvent.click(btn);
    expect(onToggle).toHaveBeenCalledOnce();
  });

  it("shows the channel's unread count while open", () => {
    render(wrap(<NotificationButton channelId="ch-1" open onToggle={() => {}} />, state({ unread: { "ch-1": 3, other: 1 } })));
    const btn = screen.getByTestId("notifications-button");
    expect(btn.title).toBe("Notifications (3 unread notifications)");
    expect(screen.getByTestId("notifications-unread").textContent).toBe("3");
    expect(btn.style.background).not.toBe("none");
  });
});

describe("NotificationDrawer", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    fetchMock.mockResolvedValue({ notifications: [], unread: 0 });
    markMock.mockResolvedValue(undefined);
    clearMock.mockResolvedValue(undefined);
    deleteMock.mockResolvedValue(undefined);
    pluginsMock.mockImplementation(async (id) => (id ? [plugin("quiz", "Quiz")] : [plugin("quiz", "Quiz"), plugin("other")]));
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  const props = { channelId: "ch-1", channelName: "general", dirPath: "/work/p", onClose: () => {} };

  it("renders nothing while closed", () => {
    render(wrap(<NotificationDrawer open={false} {...props} />));
    expect(screen.queryByTestId("notifications-drawer")).toBeNull();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("says nothing yet for a channel without notifications and offers no clear", async () => {
    render(wrap(<NotificationDrawer open {...props} />));
    expect(await screen.findByText("Nothing yet.")).toBeTruthy();
    expect(fetchMock).toHaveBeenCalledWith("ch-1");
    expect(screen.queryByTestId("notifications-clear")).toBeNull();
    expect(markMock).not.toHaveBeenCalled();
  });

  it("lists notifications, marks them read up to the newest and keeps a dot on the fresh ones", async () => {
    fetchMock.mockResolvedValue({
      notifications: [
        note({ id: 7, read: false, title: "Run failed", body: "exit 1" }),
        note({ id: 5, read: true, plugin_id: "quiz", kind: "notify", title: "Hello" }),
        note({ id: 3, read: false, plugin_id: "quiz", kind: "card", card: "score", data: { n: 2 } }),
        note({ id: 2, read: true, kind: "card", plugin_id: "gone" }),
      ],
      unread: 2,
    });
    render(wrap(<NotificationDrawer open {...props} />));
    await waitFor(() => expect(screen.getAllByTestId("notification")).toHaveLength(4));
    const rows = screen.getAllByTestId("notification");
    expect(rows.map((r) => r.dataset.kind)).toEqual(["run", "notify", "card", "card"]);
    expect(rows[0]!.textContent).toContain("Loop");
    expect(rows[0]!.textContent).toContain("Run failed");
    expect(rows[0]!.textContent).toContain("exit 1");
    await waitFor(() => expect(rows[1]!.textContent).toContain("Quiz"));
    expect(rows[3]!.textContent).toContain("gone");
    // Fresh dots only on those unread when the drawer opened.
    const dots = rows.map((r) => r.querySelectorAll("span").length === 3);
    expect(dots).toEqual([true, false, true, false]);
    expect(markMock).toHaveBeenCalledWith("ch-1", 7);
    const card = screen.getByTestId("plugin-card");
    await waitFor(() => expect(JSON.parse(card.textContent!).plugins).toEqual(["quiz"]));
    expect(JSON.parse(card.textContent!)).toMatchObject({ pluginId: "quiz", cardId: "score", label: "score", data: { n: 2 }, channelId: "ch-1", channelName: "general", dirPath: "/work/p" });
    expect(pluginsMock).toHaveBeenCalledWith("ch-1");
    expect(pluginsMock).toHaveBeenCalledWith(null);
  });

  it("passes null data to a card posted without any", async () => {
    fetchMock.mockResolvedValue({ notifications: [note({ id: 1, kind: "card", plugin_id: "quiz", card: "score" })], unread: 0 });
    render(wrap(<NotificationDrawer open {...props} />));
    const card = await screen.findByTestId("plugin-card");
    expect(JSON.parse(card.textContent!).data).toBeNull();
  });

  it("keeps the first set of fresh ids when the list is fetched again", async () => {
    fetchMock.mockResolvedValueOnce({ notifications: [note({ id: 4, read: false, title: "A" })], unread: 1 });
    const { rerender } = render(wrap(<NotificationDrawer open {...props} />));
    await screen.findByText("A");
    fetchMock.mockResolvedValueOnce({ notifications: [note({ id: 6, read: false, title: "B" }), note({ id: 4, read: true, title: "A" })], unread: 1 });
    rerender(wrap(<NotificationDrawer open {...props} />, state({ epoch: 1 })));
    await screen.findByText("B");
    const rows = screen.getAllByTestId("notification");
    expect(rows[0]!.querySelectorAll("span")).toHaveLength(2);
    expect(rows[1]!.querySelectorAll("span")).toHaveLength(3);
    expect(markMock).toHaveBeenLastCalledWith("ch-1", 6);
  });

  it("clears, dismisses and closes", async () => {
    const onClose = vi.fn();
    fetchMock.mockResolvedValue({ notifications: [note({ id: 9, title: "X" })], unread: 0 });
    render(wrap(<NotificationDrawer open {...props} onClose={onClose} />));
    await screen.findByText("X");
    const user = userEvent.setup();
    await user.click(screen.getByTestId("notifications-clear"));
    expect(clearMock).toHaveBeenCalledWith("ch-1");
    await user.click(screen.getByTestId("notification-dismiss"));
    expect(deleteMock).toHaveBeenCalledWith(9);
    await user.click(screen.getByTestId("notifications-close"));
    await user.click(screen.getByTestId("notifications-backdrop"));
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it("logs failed clears, dismissals, reads and listings", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    fetchMock.mockResolvedValue({ notifications: [note({ id: 9, read: false, title: "X" })], unread: 1 });
    markMock.mockRejectedValue(new Error("mark"));
    clearMock.mockRejectedValue(new Error("clear"));
    deleteMock.mockRejectedValue(new Error("delete"));
    pluginsMock.mockRejectedValue(new Error("plugins"));
    render(wrap(<NotificationDrawer open {...props} />));
    await screen.findByText("X");
    fireEvent.click(screen.getByTestId("notifications-clear"));
    fireEvent.click(screen.getByTestId("notification-dismiss"));
    await waitFor(() => {
      const contexts = warn.mock.calls.map((c) => c[0]);
      expect(contexts).toEqual(expect.arrayContaining(["[loop] marking notifications read:", "[loop] clearing notifications:", "[loop] dismissing a notification:", "[loop] listing plugins:"]));
    });
    // A plugin listing that failed leaves the source as the plugin id.
    expect(screen.getByTestId("notification").textContent).toContain("Loop");
  });

  it("logs a failed listing and shows nothing", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    fetchMock.mockRejectedValue(new Error("down"));
    render(wrap(<NotificationDrawer open {...props} />));
    await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] listing notifications:", expect.any(Error)));
    expect(screen.queryByText("Nothing yet.")).toBeNull();
  });

  it("ignores a listing that lands after the drawer closed", async () => {
    let resolve!: (v: { notifications: NotificationInfo[]; unread: number }) => void;
    fetchMock.mockReturnValue(new Promise((r) => (resolve = r)));
    const { unmount } = render(wrap(<NotificationDrawer open {...props} />));
    unmount();
    await act(async () => resolve({ notifications: [note({ id: 1, read: false })], unread: 1 }));
    expect(markMock).not.toHaveBeenCalled();
  });

  it("only uses the latest plugin listing", async () => {
    let first!: (v: PluginInfo[]) => void;
    pluginsMock.mockReturnValueOnce(new Promise((r) => (first = r)));
    fetchMock.mockResolvedValue({ notifications: [note({ id: 1, plugin_id: "quiz", title: "Q" })], unread: 0 });
    render(wrap(<NotificationDrawer open {...props} />));
    await screen.findByText("Q");
    act(() => {
      window.dispatchEvent(new Event(PLUGINS_CHANGED));
    });
    await waitFor(() => expect(screen.getByTestId("notification").textContent).toContain("Quiz"));
    await act(async () => first([plugin("quiz", "Stale")]));
    expect(screen.getByTestId("notification").textContent).toContain("Quiz");
    expect(screen.getByTestId("notification").textContent).not.toContain("Stale");
  });

  it("closes on Escape and ignores other keys", () => {
    const onClose = vi.fn();
    render(wrap(<NotificationDrawer open {...props} onClose={onClose} />));
    fireEvent.keyDown(window, { key: "a" });
    expect(onClose).not.toHaveBeenCalled();
    fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("highlights header buttons on hover", async () => {
    fetchMock.mockResolvedValue({ notifications: [note({ id: 1, title: "X" })], unread: 0 });
    render(wrap(<NotificationDrawer open {...props} />));
    await screen.findByText("X");
    for (const id of ["notifications-clear", "notifications-close", "notification-dismiss"]) {
      const b = screen.getByTestId(id);
      fireEvent.mouseEnter(b);
      expect(b.style.backgroundColor).not.toBe("transparent");
      fireEvent.mouseLeave(b);
      expect(b.style.backgroundColor).toBe("transparent");
    }
  });

  it("slides in after mounting and unmounts after sliding out", async () => {
    vi.useFakeTimers();
    const { rerender } = render(wrap(<NotificationDrawer open={false} {...props} />));
    rerender(wrap(<NotificationDrawer open {...props} />));
    const drawer = screen.getByTestId("notifications-drawer");
    expect(drawer.style.transform).toContain("calc");
    expect(drawer.style.boxShadow).toBe("none");
    await act(async () => {
      await vi.advanceTimersByTimeAsync(50);
    });
    expect(drawer.style.transform).toBe("translateX(0)");
    expect(drawer.style.transition).toContain("220ms");
    rerender(wrap(<NotificationDrawer open={false} {...props} />));
    expect(screen.getByTestId("notifications-backdrop").style.pointerEvents).toBe("none");
    await act(async () => {
      await vi.advanceTimersByTimeAsync(220);
    });
    expect(screen.queryByTestId("notifications-drawer")).toBeNull();
  });

  it("skips the slide when the OS asks for less motion", () => {
    vi.stubGlobal("matchMedia", (q: string) => ({ matches: q.includes("reduce") }));
    render(wrap(<NotificationDrawer open {...props} />));
    expect(screen.getByTestId("notifications-drawer").style.transition).toContain("0ms");
  });

  it("draws a plain border for a theme without an island border", () => {
    render(
      <ThemeProvider initialTheme="flat" customThemes={{ flat: { ...darkColors, islandBorder: "" } }}>
        {wrap(<NotificationDrawer open {...props} />)}
      </ThemeProvider>,
    );
    expect(screen.getByTestId("notifications-drawer").style.border).toContain("1px solid");
  });
});
