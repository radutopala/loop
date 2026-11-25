import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { holdQueuedMessage, releaseQueuedHold, updateQueuedMessage } from "../api/loopApi";
import { message } from "../test/hooks-b-fixtures";
import type { Message } from "../types";
import { EDIT_STARTED_NOTICE, useQueuedEdit } from "./useQueuedEdit";

vi.mock("../api/loopApi", () => ({ holdQueuedMessage: vi.fn(), releaseQueuedHold: vi.fn(), updateQueuedMessage: vi.fn() }));

const RENEW_MS = 2 * 60 * 1000;
const m1 = message("m1");
const m2 = message("m2");

function setup(channelId: string | null = "ch", queued: Message[] = [m1, m2]) {
  return renderHook(({ ch, q }) => useQueuedEdit(ch, q), { initialProps: { ch: channelId, q: queued } });
}

describe("useQueuedEdit", () => {
  beforeEach(() => {
    vi.mocked(holdQueuedMessage).mockResolvedValue(true);
    vi.mocked(releaseQueuedHold).mockResolvedValue();
    vi.mocked(updateQueuedMessage).mockResolvedValue(true);
  });
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    vi.mocked(holdQueuedMessage).mockReset();
    vi.mocked(releaseQueuedHold).mockReset();
    vi.mocked(updateQueuedMessage).mockReset();
    vi.restoreAllMocks();
  });

  it("holds the message and enters edit mode", async () => {
    const { result } = setup();
    let ok: boolean | undefined;
    await act(async () => {
      ok = await result.current.start(m1);
    });
    expect(ok).toBe(true);
    expect(holdQueuedMessage).toHaveBeenCalledWith("ch", "m1");
    expect(result.current.editing).toEqual(m1);
    expect(result.current.notice).toBeNull();
  });

  it("refuses without a channel or when the hold fails", async () => {
    const none = setup(null);
    expect(await none.result.current.start(m1)).toBe(false);
    expect(holdQueuedMessage).not.toHaveBeenCalled();

    vi.mocked(holdQueuedMessage).mockResolvedValue(false);
    const { result } = setup();
    let ok: boolean | undefined;
    await act(async () => {
      ok = await result.current.start(m1);
    });
    expect(ok).toBe(false);
    expect(result.current.editing).toBeNull();
  });

  it("saves the edit and leaves edit mode", async () => {
    const { result } = setup();
    await act(async () => void (await result.current.start(m1)));
    let res: string | undefined;
    await act(async () => {
      res = await result.current.save("new text");
    });
    expect(res).toBe("saved");
    expect(updateQueuedMessage).toHaveBeenCalledWith("ch", "m1", "new text");
    expect(result.current.editing).toBeNull();
    expect(result.current.notice).toBeNull();
  });

  it("ends with a notice when the message started before the save", async () => {
    vi.mocked(updateQueuedMessage).mockResolvedValue(false);
    const { result } = setup();
    await act(async () => void (await result.current.start(m1)));
    let res: string | undefined;
    await act(async () => {
      res = await result.current.save("x");
    });
    expect(res).toBe("started");
    expect(result.current.editing).toBeNull();
    expect(result.current.notice).toBe(EDIT_STARTED_NOTICE);

    act(() => result.current.dismissNotice());
    expect(result.current.notice).toBeNull();
  });

  it("reports started for a save with no edit open", async () => {
    const { result } = setup();
    expect(await result.current.save("x")).toBe("started");
    expect(updateQueuedMessage).not.toHaveBeenCalled();
  });

  it("releases the hold on cancel, and logs a failed release", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    vi.mocked(releaseQueuedHold).mockRejectedValue(new Error("gone"));
    const { result } = setup();
    await act(async () => void (await result.current.start(m1)));
    await act(async () => result.current.cancel());
    expect(releaseQueuedHold).toHaveBeenCalledWith("ch", "m1");
    expect(result.current.editing).toBeNull();
    expect(warn).toHaveBeenCalledWith("[loop] releasing queued message:", expect.any(Error));

    // Nothing to release once closed.
    act(() => result.current.cancel());
    expect(releaseQueuedHold).toHaveBeenCalledTimes(1);
  });

  it("renews the hold while the edit is open, and ends it when renewal fails", async () => {
    vi.useFakeTimers();
    const { result } = setup();
    await act(async () => void (await result.current.start(m1)));
    expect(holdQueuedMessage).toHaveBeenCalledTimes(1);

    await act(async () => vi.advanceTimersByTime(RENEW_MS));
    expect(holdQueuedMessage).toHaveBeenCalledTimes(2);
    expect(result.current.editing).toEqual(m1);

    vi.mocked(holdQueuedMessage).mockResolvedValueOnce(false);
    await act(async () => vi.advanceTimersByTime(RENEW_MS));
    expect(result.current.editing).toBeNull();
    expect(result.current.notice).toBe(EDIT_STARTED_NOTICE);
  });

  it("logs a renewal that errors and keeps the edit", async () => {
    vi.useFakeTimers();
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const { result } = setup();
    await act(async () => void (await result.current.start(m1)));
    vi.mocked(holdQueuedMessage).mockRejectedValueOnce(new Error("net"));
    await act(async () => vi.advanceTimersByTime(RENEW_MS));
    expect(warn).toHaveBeenCalledWith("[loop] renewing queued message hold:", expect.any(Error));
    expect(result.current.editing).toEqual(m1);
  });

  it.each([
    ["leaves the queue", [m2]],
    ["starts running", [{ ...m1, is_running: true }, m2]],
  ])("ends the edit when the message %s", async (_name, queued) => {
    const { result, rerender } = setup();
    await act(async () => void (await result.current.start(m1)));
    rerender({ ch: "ch", q: queued });
    expect(result.current.editing).toBeNull();
    expect(result.current.notice).toBe(EDIT_STARTED_NOTICE);
  });

  it("releases the hold and clears on a channel switch", async () => {
    vi.mocked(updateQueuedMessage).mockResolvedValue(false);
    const { result, rerender } = setup();
    await act(async () => void (await result.current.start(m1)));
    await act(async () => {
      rerender({ ch: "other", q: [m1, m2] });
    });
    expect(releaseQueuedHold).toHaveBeenCalledWith(expect.any(String), "m1");
    expect(result.current.editing).toBeNull();
    expect(result.current.notice).toBeNull();
  });

  it("logs a failed release on a channel switch", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    vi.mocked(releaseQueuedHold).mockRejectedValue(new Error("gone"));
    const { result, unmount } = setup();
    await act(async () => void (await result.current.start(m1)));
    await act(async () => unmount());
    expect(warn).toHaveBeenCalledWith("[loop] releasing queued message:", expect.any(Error));
  });
});
