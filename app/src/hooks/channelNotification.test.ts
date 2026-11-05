import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { notifyChannel } from "./channelNotification";

class FakeNotification {
  static last: FakeNotification;
  onclick: (() => void) | null = null;
  onclose: (() => void) | null = null;
  constructor(
    readonly title: string,
    readonly options: NotificationOptions,
  ) {
    FakeNotification.last = this;
  }
}

describe("notifyChannel", () => {
  const stubWindow = (loopAPI?: { openChannel: (id: string) => void }) => {
    const win = { location: { hash: "" }, focus: vi.fn(), loopAPI };
    vi.stubGlobal("window", win);
    return win;
  };

  beforeEach(() => {
    vi.stubGlobal("Notification", FakeNotification);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("shows the title and body", () => {
    stubWindow();
    notifyChannel("Loop — chat", "Completed in 3s", "c1");
    expect(FakeNotification.last.title).toBe("Loop — chat");
    expect(FakeNotification.last.options).toEqual({ body: "Completed in 3s" });
  });

  it("opens the channel through the app when clicked, and only then", () => {
    const openChannel = vi.fn();
    const win = stubWindow({ openChannel });
    notifyChannel("t", "b", "thread-1");
    expect(openChannel).not.toHaveBeenCalled();

    FakeNotification.last.onclick?.();
    expect(openChannel).toHaveBeenCalledExactlyOnceWith("thread-1");
    expect(win.location.hash).toBe("");
    expect(win.focus).not.toHaveBeenCalled();
  });

  it("opens the channel in the tab when there is no app", () => {
    const win = stubWindow();
    notifyChannel("t", "b", "c1");
    FakeNotification.last.onclick?.();
    expect(win.location.hash).toBe("c1");
    expect(win.focus).toHaveBeenCalledOnce();
  });

  it("opens the channel each notification was shown for", () => {
    const openChannel = vi.fn();
    stubWindow({ openChannel });
    notifyChannel("t", "b", "c1");
    const first = FakeNotification.last;
    notifyChannel("t", "b", "c2");
    FakeNotification.last.onclick?.();
    first.onclick?.();
    expect(openChannel.mock.calls).toEqual([["c2"], ["c1"]]);
  });

  it("ignores a closed notification without opening anything", () => {
    const openChannel = vi.fn();
    stubWindow({ openChannel });
    notifyChannel("t", "b", "c1");
    FakeNotification.last.onclose?.();
    expect(openChannel).not.toHaveBeenCalled();
  });
});
