import { describe, expect, it, vi } from "vitest";
import { type BrowserInput, createFrameRenderer, createInputCoalescer, mergeInput } from "./browserStream";

describe("mergeInput", () => {
  const cases: { name: string; pending: BrowserInput[]; ev: BrowserInput; want: BrowserInput[] }[] = [
    {
      name: "appends the first event",
      pending: [],
      ev: { type: "scroll", deltaY: 100 },
      want: [{ type: "scroll", deltaY: 100 }],
    },
    {
      name: "sums consecutive scroll deltas and keeps the latest position",
      pending: [{ type: "scroll", x: 1, y: 2, deltaX: 10, deltaY: 100 }],
      ev: { type: "scroll", x: 3, y: 4, deltaX: 5, deltaY: 20 },
      want: [{ type: "scroll", x: 3, y: 4, deltaX: 15, deltaY: 120 }],
    },
    {
      name: "collapses consecutive moves to the latest position",
      pending: [{ type: "mousemove", x: 1, y: 1 }],
      ev: { type: "mousemove", x: 9, y: 9 },
      want: [{ type: "mousemove", x: 9, y: 9 }],
    },
    {
      name: "treats a click between scrolls as a barrier",
      pending: [
        { type: "scroll", deltaY: 100 },
        { type: "click", x: 5, y: 5 },
      ],
      ev: { type: "scroll", deltaY: 20 },
      want: [
        { type: "scroll", deltaY: 100 },
        { type: "click", x: 5, y: 5 },
        { type: "scroll", deltaY: 20 },
      ],
    },
    {
      name: "never merges a move into a scroll",
      pending: [{ type: "scroll", deltaY: 100 }],
      ev: { type: "mousemove", x: 2, y: 2 },
      want: [
        { type: "scroll", deltaY: 100 },
        { type: "mousemove", x: 2, y: 2 },
      ],
    },
    {
      name: "never merges clicks",
      pending: [{ type: "click", x: 1, y: 1 }],
      ev: { type: "click", x: 2, y: 2 },
      want: [
        { type: "click", x: 1, y: 1 },
        { type: "click", x: 2, y: 2 },
      ],
    },
  ];

  for (const c of cases) {
    it(c.name, () => {
      expect(mergeInput(c.pending, c.ev)).toEqual(c.want);
    });
  }

  it("treats missing deltas as zero", () => {
    expect(mergeInput([{ type: "scroll" }], { type: "scroll" })).toEqual([{ type: "scroll", deltaX: 0, deltaY: 0 }]);
  });
});

/** Runs the i-th scheduled frame callback, asserting it exists. */
function runFrame(frames: (() => void)[], i: number) {
  const cb = frames[i];
  expect(cb).toBeDefined();
  cb?.();
}

describe("createInputCoalescer", () => {
  it("collapses a scroll burst into a single send per frame", () => {
    const send = vi.fn();
    const frames: (() => void)[] = [];
    const c = createInputCoalescer(send, (cb) => frames.push(cb));

    for (let i = 0; i < 50; i++) c.push({ type: "scroll", x: 10, y: 20, deltaY: 120 });
    expect(send).not.toHaveBeenCalled();
    expect(frames).toHaveLength(1);

    runFrame(frames, 0);
    expect(send).toHaveBeenCalledTimes(1);
    expect(send).toHaveBeenCalledWith({ type: "scroll", x: 10, y: 20, deltaX: 0, deltaY: 50 * 120 });
  });

  it("sends discrete input immediately, after any pending gesture", () => {
    const send = vi.fn();
    const c = createInputCoalescer(send, () => {});

    c.push({ type: "mousemove", x: 1, y: 1 });
    c.push({ type: "click", x: 1, y: 1 });

    expect(send.mock.calls.map((call) => call[0].type)).toEqual(["mousemove", "click"]);
  });

  it("schedules a new frame after the previous one flushed", () => {
    const send = vi.fn();
    const frames: (() => void)[] = [];
    const c = createInputCoalescer(send, (cb) => frames.push(cb));

    c.push({ type: "mousemove", x: 1, y: 1 });
    runFrame(frames, 0);
    c.push({ type: "mousemove", x: 2, y: 2 });
    expect(frames).toHaveLength(2);
    runFrame(frames, 1);

    expect(send).toHaveBeenCalledTimes(2);
  });

  it("flushes on demand", () => {
    const send = vi.fn();
    const c = createInputCoalescer(send, () => {});
    c.push({ type: "mousemove", x: 3, y: 3 });
    c.flush();
    expect(send).toHaveBeenCalledWith({ type: "mousemove", x: 3, y: 3 });
  });

  it("defaults to requestAnimationFrame", () => {
    const raf = vi.fn().mockReturnValue(1);
    vi.stubGlobal("requestAnimationFrame", raf);
    createInputCoalescer(vi.fn()).push({ type: "mousemove" });
    expect(raf).toHaveBeenCalled();
    vi.unstubAllGlobals();
  });
});

function fakeCanvas() {
  const ctx = { drawImage: vi.fn() };
  return {
    canvas: { width: 0, height: 0, getContext: () => ctx } as unknown as HTMLCanvasElement,
    ctx,
  };
}

function fakeBitmap(width: number, height: number) {
  return { width, height, close: vi.fn() } as unknown as ImageBitmap;
}

describe("createFrameRenderer", () => {
  it("draws a frame at its own size", async () => {
    const { canvas, ctx } = fakeCanvas();
    const bitmap = fakeBitmap(800, 600);
    const render = createFrameRenderer(
      () => canvas,
      async () => bitmap,
    );

    render(new ArrayBuffer(4));
    await vi.waitFor(() => expect(ctx.drawImage).toHaveBeenCalledWith(bitmap, 0, 0));
    expect(canvas.width).toBe(800);
    expect(canvas.height).toBe(600);
    expect(bitmap.close).toHaveBeenCalled();
  });

  it("skips frames superseded while a decode is in flight", async () => {
    const { canvas, ctx } = fakeCanvas();
    const gate: { release: (() => void) | null } = { release: null };
    const decoded: ArrayBuffer[] = [];
    const render = createFrameRenderer(
      () => canvas,
      async (blob) => {
        decoded.push(await blob.arrayBuffer());
        if (decoded.length === 1) {
          await new Promise<void>((resolve) => {
            gate.release = resolve;
          });
        }
        return fakeBitmap(10, 10);
      },
    );

    render(new Uint8Array([1]).buffer);
    await vi.waitFor(() => expect(gate.release).not.toBeNull());
    render(new Uint8Array([2]).buffer);
    render(new Uint8Array([3]).buffer);
    gate.release?.();

    await vi.waitFor(() => expect(ctx.drawImage).toHaveBeenCalledTimes(2));
    expect(decoded.map((b) => new Uint8Array(b)[0])).toEqual([1, 3]);
  });

  it("ignores a frame that fails to decode", async () => {
    const { canvas, ctx } = fakeCanvas();
    const render = createFrameRenderer(
      () => canvas,
      async () => {
        throw new Error("truncated");
      },
    );

    render(new ArrayBuffer(4));
    await vi.waitFor(() => expect(ctx.drawImage).not.toHaveBeenCalled());
  });

  it("drops a frame when the canvas has gone away", async () => {
    const bitmap = fakeBitmap(10, 10);
    const render = createFrameRenderer(
      () => null,
      async () => bitmap,
    );

    render(new ArrayBuffer(4));
    await vi.waitFor(() => expect(bitmap.close).toHaveBeenCalled());
  });
});
