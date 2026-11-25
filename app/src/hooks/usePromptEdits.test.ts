// @vitest-environment jsdom
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchPromptEditState, onPromptEditSettled } from "../api/promptEdit";
import type { WSEvent } from "../types";
import type { ChatEventListener } from "./useChatStateStore";
import { promptEditRunning, usePromptEdits } from "./usePromptEdits";

vi.mock("../api/promptEdit", () => ({ fetchPromptEditState: vi.fn(), onPromptEditSettled: vi.fn() }));

describe("promptEditRunning", () => {
  it.each([
    ["running", true],
    ["completed", false],
    ["error", false],
  ] as const)("%s → %j", (status, want) => {
    expect(promptEditRunning(status)).toBe(want);
  });
});

function status(channelId: string, s: string): WSEvent {
  return { type: "agent.status", channel_id: channelId, data: { status: s }, timestamp: 0 } as WSEvent;
}

describe("usePromptEdits", () => {
  afterEach(() => {
    cleanup();
    vi.mocked(fetchPromptEditState).mockReset();
    vi.mocked(onPromptEditSettled).mockReset();
  });

  it("looks the thread up and follows its run status", async () => {
    vi.mocked(fetchPromptEditState).mockResolvedValue({ edit_channel_id: "edit-1" });
    const unsubscribe = vi.fn();
    const subscribe = vi.fn((_id: string, _l: ChatEventListener) => unsubscribe);
    const { result, unmount } = renderHook(() => usePromptEdits("ch", subscribe));
    await waitFor(() => expect(result.current.editChannelId).toBe("edit-1"));
    expect(fetchPromptEditState).toHaveBeenCalledWith("ch");
    expect(subscribe).toHaveBeenCalledWith("edit-1", expect.any(Function));

    const listener = subscribe.mock.calls[0]![1];
    act(() => listener(status("edit-1", "running")));
    expect(result.current.running).toBe(true);
    act(() => listener({ type: "message.created", channel_id: "edit-1", data: {}, timestamp: 0 } as WSEvent));
    expect(result.current.running).toBe(true);
    act(() => listener(status("edit-1", "completed")));
    expect(result.current.running).toBe(false);

    unmount();
    expect(unsubscribe).toHaveBeenCalled();
  });

  it("looks again once an edit of its channel settles, before the thread exists", async () => {
    vi.mocked(fetchPromptEditState).mockResolvedValueOnce({ edit_channel_id: "" }).mockResolvedValueOnce({ edit_channel_id: "edit-2" });
    const stop = vi.fn();
    vi.mocked(onPromptEditSettled).mockReturnValue(stop);
    const { result } = renderHook(() => usePromptEdits("ch"));
    await act(async () => {});
    expect(result.current.editChannelId).toBe("");
    const settled = vi.mocked(onPromptEditSettled).mock.calls[0]![0];

    act(() => settled("other"));
    expect(fetchPromptEditState).toHaveBeenCalledTimes(1);

    await act(async () => settled("ch"));
    await waitFor(() => expect(result.current.editChannelId).toBe("edit-2"));
    expect(fetchPromptEditState).toHaveBeenCalledTimes(2);
    expect(stop).toHaveBeenCalled();
  });

  it("looks again on a reconnect and resets on a channel switch", async () => {
    vi.mocked(fetchPromptEditState).mockResolvedValue({ edit_channel_id: "edit-1" });
    const subscribe = vi.fn((_id: string, _l: ChatEventListener) => () => {});
    const { result, rerender } = renderHook(({ ch, opens }) => usePromptEdits(ch, subscribe, opens), { initialProps: { ch: "a", opens: 0 } });
    await waitFor(() => expect(result.current.editChannelId).toBe("edit-1"));
    act(() => subscribe.mock.calls[0]![1](status("edit-1", "running")));
    expect(result.current.running).toBe(true);

    rerender({ ch: "a", opens: 1 });
    await act(async () => {});
    expect(fetchPromptEditState).toHaveBeenCalledTimes(2);

    vi.mocked(fetchPromptEditState).mockReturnValue(new Promise(() => {}));
    rerender({ ch: "b", opens: 1 });
    expect(result.current).toEqual({ editChannelId: "", running: false });
    expect(fetchPromptEditState).toHaveBeenLastCalledWith("b");
  });
});
