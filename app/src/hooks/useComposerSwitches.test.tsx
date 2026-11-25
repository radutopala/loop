import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { type ComposerSwitch, fetchComposerSwitches, turnComposerSwitch } from "../api/composerSwitches";
import { PLUGINS_CHANGED } from "../api/plugins";
import { NotificationsContext } from "../notifications/useNotifications";
import type { WSEvent } from "../types";
import type { ChatEventListener } from "./useChatStateStore";
import { useComposerSwitches } from "./useComposerSwitches";

vi.mock("../api/composerSwitches", () => ({ fetchComposerSwitches: vi.fn(), turnComposerSwitch: vi.fn() }));

const sw: ComposerSwitch = { plugin_id: "p", switch_id: "s", on: false, status: "", title: "Off" };
const other: ComposerSwitch = { plugin_id: "p", switch_id: "t", on: true };

function switchEvent(channelId: string, data: ComposerSwitch, type = "plugin.composer_switch"): WSEvent {
  return { type, channel_id: channelId, data, timestamp: 0 } as WSEvent;
}

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe("useComposerSwitches", () => {
  afterEach(() => {
    vi.mocked(fetchComposerSwitches).mockReset();
    vi.mocked(turnComposerSwitch).mockReset();
    vi.restoreAllMocks();
  });

  it("lists the chat's switches and follows its switch events", async () => {
    vi.mocked(fetchComposerSwitches).mockResolvedValue([sw, other]);
    const subscribe = vi.fn((_l: ChatEventListener) => () => {});
    const { result } = renderHook(() => useComposerSwitches("ch", subscribe));
    await waitFor(() => expect(result.current.switches).toEqual([sw, other]));
    expect(fetchComposerSwitches).toHaveBeenCalledWith("ch");

    const listener = subscribe.mock.calls[0]![0];
    act(() => listener(switchEvent("other-ch", { ...sw, on: true })));
    act(() => listener(switchEvent("ch", { ...sw, on: true }, "plugin.badge")));
    expect(result.current.switches[0]!.on).toBe(false);
    act(() => listener(switchEvent("ch", { ...sw, on: true, status: "ok", title: "On" })));
    expect(result.current.switches[0]).toEqual({ ...sw, on: true, status: "ok", title: "On" });
  });

  it("lists them again on a reconnect, a plugin change, and a plugins epoch bump", async () => {
    vi.mocked(fetchComposerSwitches).mockResolvedValue([sw]);
    let epoch = 0;
    const wrapper = ({ children }: { children: ReactNode }) => <NotificationsContext.Provider value={{ unread: {}, epoch: 0, pluginsEpoch: epoch }}>{children}</NotificationsContext.Provider>;
    const { rerender } = renderHook(({ opens }) => useComposerSwitches("ch", undefined, opens), { initialProps: { opens: 0 }, wrapper });
    await act(async () => {});
    expect(fetchComposerSwitches).toHaveBeenCalledTimes(1);

    rerender({ opens: 1 });
    await act(async () => {});
    expect(fetchComposerSwitches).toHaveBeenCalledTimes(2);

    await act(async () => {
      window.dispatchEvent(new Event(PLUGINS_CHANGED));
    });
    expect(fetchComposerSwitches).toHaveBeenCalledTimes(3);

    epoch = 1;
    rerender({ opens: 1 });
    await act(async () => {});
    expect(fetchComposerSwitches).toHaveBeenCalledTimes(4);
  });

  it("shows a click turned while its plugin answers, then the plugin's state", async () => {
    vi.mocked(fetchComposerSwitches).mockResolvedValue([sw, other]);
    const answer = deferred<{ on: boolean; status?: ""; title?: string }>();
    vi.mocked(turnComposerSwitch).mockReturnValue(answer.promise);
    const { result } = renderHook(() => useComposerSwitches("ch"));
    await waitFor(() => expect(result.current.switches).toHaveLength(2));

    let turning!: Promise<void>;
    act(() => {
      turning = result.current.turn(sw);
    });
    expect(turnComposerSwitch).toHaveBeenCalledWith("ch", "p", "s", true);
    expect(result.current.pending.has("p/s")).toBe(true);
    expect(result.current.switches[0]).toMatchObject({ on: true, status: "" });

    await act(async () => {
      answer.resolve({ on: true, title: "Done" });
      await turning;
    });
    expect(result.current.pending.size).toBe(0);
    expect(result.current.switches[0]).toMatchObject({ on: true, title: "Done" });
    expect(result.current.switches[1]).toEqual(other);
  });

  it.each([
    ["an Error", new Error("plugin down"), "plugin down"],
    ["a non-Error", "nope", "nope"],
  ])("turns the switch back and keeps the error when the click fails with %s", async (_name, err, want) => {
    vi.mocked(fetchComposerSwitches).mockResolvedValue([sw]);
    vi.mocked(turnComposerSwitch).mockRejectedValueOnce(err).mockResolvedValueOnce({ on: true });
    const { result } = renderHook(() => useComposerSwitches("ch"));
    await waitFor(() => expect(result.current.switches).toHaveLength(1));

    await act(async () => result.current.turn(sw));
    expect(result.current.switches[0]).toEqual(sw);
    expect(result.current.errors.get("p/s")).toBe(want);
    expect(result.current.pending.size).toBe(0);

    // The next click clears the error.
    await act(async () => result.current.turn(sw));
    expect(result.current.errors.has("p/s")).toBe(false);
    expect(result.current.switches[0]!.on).toBe(true);
  });

  it("clears everything on a channel switch", async () => {
    vi.mocked(fetchComposerSwitches)
      .mockResolvedValueOnce([sw])
      .mockReturnValue(new Promise(() => {}));
    vi.mocked(turnComposerSwitch).mockRejectedValue(new Error("x"));
    const { result, rerender } = renderHook(({ ch }) => useComposerSwitches(ch), { initialProps: { ch: "a" } });
    await waitFor(() => expect(result.current.switches).toHaveLength(1));
    await act(async () => result.current.turn(sw));
    expect(result.current.errors.size).toBe(1);

    rerender({ ch: "b" });
    expect(result.current.switches).toEqual([]);
    expect(result.current.errors.size).toBe(0);
    expect(result.current.pending.size).toBe(0);
  });

  it("stops listening for plugin changes on unmount", async () => {
    vi.mocked(fetchComposerSwitches).mockResolvedValue([]);
    const remove = vi.spyOn(window, "removeEventListener");
    const { unmount } = renderHook(() => useComposerSwitches("ch"));
    await act(async () => {});
    unmount();
    expect(remove).toHaveBeenCalledWith(PLUGINS_CHANGED, expect.any(Function));
  });
});
