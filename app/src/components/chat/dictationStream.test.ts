import { describe, expect, it, vi } from "vitest";
import { DictationStream } from "./dictationStream";
import { FakeSocket } from "./dictationTestSocket";

function make() {
  const ws = new FakeSocket();
  const h = { onReady: vi.fn(), onPartial: vi.fn(), onFinal: vi.fn(), onError: vi.fn(), onEnd: vi.fn() };
  const s = new DictationStream(ws, h);
  return { ws, h, s };
}

const pcm = (n: number) => new Uint8Array([n, 0]).buffer;

describe("DictationStream", () => {
  it("asks for array buffers", () => {
    expect(make().ws.binaryType).toBe("arraybuffer");
  });

  it("holds audio and a stop until the socket opens, then sends them in order", () => {
    const { ws, s } = make();
    s.sendAudio(pcm(1));
    s.sendAudio(pcm(2));
    s.stop();
    expect(ws.sent).toEqual([]);
    ws.open();
    expect(ws.sent).toEqual([pcm(1), pcm(2), JSON.stringify({ type: "stop" })]);
  });

  it("sends audio straight away once open, before and after ready", () => {
    const { ws, s, h } = make();
    ws.open();
    s.sendAudio(pcm(1));
    ws.server({ type: "ready" });
    s.sendAudio(pcm(2));
    expect(h.onReady).toHaveBeenCalledOnce();
    expect(ws.audio).toEqual([pcm(1), pcm(2)]);
  });

  it("passes partials on and ends with the final text", () => {
    const { ws, s, h } = make();
    ws.open();
    ws.server({ type: "partial", committed: "hello", tentative: "wor" });
    ws.server({ type: "partial", committed: "hello world" });
    s.stop();
    // Audio after stop is not the session's any more.
    s.sendAudio(pcm(9));
    ws.server({ type: "final", text: "Hello world." });
    expect(h.onPartial.mock.calls).toEqual([
      ["hello", "wor"],
      ["hello world", ""],
    ]);
    expect(h.onFinal).toHaveBeenCalledWith("Hello world.");
    expect(h.onError).not.toHaveBeenCalled();
    expect(h.onEnd).toHaveBeenCalledOnce();
    expect(ws.closed).toBe(true);
    expect(ws.audio).toEqual([]);
    expect(s.done).toBe(true);
  });

  it("reports a server error and ends", () => {
    const { ws, h } = make();
    ws.open();
    ws.server({ type: "error", code: "model_missing", message: "no model" });
    expect(h.onError).toHaveBeenCalledWith("model_missing", "no model");
    expect(h.onEnd).toHaveBeenCalledOnce();
    expect(ws.closed).toBe(true);
    // Nothing more is reported after the end.
    ws.server({ type: "final", text: "late" });
    expect(h.onFinal).not.toHaveBeenCalled();
  });

  it("treats a close before the final as an error", () => {
    const { ws, s, h } = make();
    ws.open();
    s.stop();
    ws.close();
    expect(h.onError).toHaveBeenCalledWith("closed", expect.stringContaining("before the text arrived"));
    expect(h.onEnd).toHaveBeenCalledOnce();
  });

  it("cancels: tells an open server, drops what was queued, ends without a final or error", () => {
    const { ws, s, h } = make();
    ws.open();
    s.sendAudio(pcm(1));
    s.cancel();
    expect(ws.controls).toEqual([{ type: "cancel" }]);
    expect(ws.closed).toBe(true);
    expect(h.onEnd).toHaveBeenCalledOnce();
    expect(h.onError).not.toHaveBeenCalled();
    s.sendAudio(pcm(2));
    s.stop();
    expect(ws.sent).toHaveLength(2);
  });

  it("cancels before the socket opens by closing it", () => {
    const { ws, s, h } = make();
    s.sendAudio(pcm(1));
    s.cancel();
    expect(ws.closed).toBe(true);
    expect(ws.sent).toEqual([]);
    expect(h.onEnd).toHaveBeenCalledOnce();
  });

  it("ignores binary and malformed frames", () => {
    const { ws, h } = make();
    ws.open();
    ws.onmessage?.({ data: new ArrayBuffer(2) });
    ws.onmessage?.({ data: "{nope" });
    ws.server({ type: "something-new" });
    expect(h.onPartial).not.toHaveBeenCalled();
    expect(h.onError).not.toHaveBeenCalled();
  });
});
