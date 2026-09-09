/**
 * Input coalescing and frame decoding for the browser pane.
 *
 * Both halves exist for the same reason: the pane produces work far faster than
 * it can be consumed. A trackpad emits 60-120 wheel/move events a second while
 * the sidecar dispatches one every ~22ms (~45/s), and the screencast delivers
 * ~17 JPEG frames a second that each have to be decoded on the main thread.
 * Left alone, both build a backlog that keeps replaying after the user's
 * fingers stop — which is what "the picture lags behind my input" actually was.
 */

export interface BrowserInput {
  type: string;
  x?: number;
  y?: number;
  button?: string;
  clickCount?: number;
  deltaX?: number;
  deltaY?: number;
  key?: string;
  text?: string;
  modifiers?: number;
}

/** Schedules a flush; defaults to the next animation frame. */
export type Scheduler = (cb: () => void) => void;

/**
 * mergeInput appends ev to pending, folding it into the tail when the two are
 * the same continuous gesture: moves collapse to the latest position, scrolls
 * sum their deltas. Summing is lossless — Chrome scrolls to the same offset for
 * 10 x 120 as for 1 x 1200.
 *
 * Only the tail merges, so a click or keystroke between two scrolls is a
 * barrier and the user's actions keep their original order.
 */
export function mergeInput(pending: BrowserInput[], ev: BrowserInput): BrowserInput[] {
  const last = pending[pending.length - 1];
  if (last) {
    if (ev.type === "mousemove" && last.type === "mousemove") {
      pending[pending.length - 1] = ev;
      return pending;
    }
    if (ev.type === "scroll" && last.type === "scroll") {
      pending[pending.length - 1] = {
        ...ev,
        deltaX: (last.deltaX ?? 0) + (ev.deltaX ?? 0),
        deltaY: (last.deltaY ?? 0) + (ev.deltaY ?? 0),
      };
      return pending;
    }
  }
  pending.push(ev);
  return pending;
}

function isContinuous(ev: BrowserInput): boolean {
  return ev.type === "mousemove" || ev.type === "scroll";
}

export interface InputCoalescer {
  push(ev: BrowserInput): void;
  flush(): void;
}

/**
 * createInputCoalescer batches move/scroll bursts into one event per frame and
 * sends discrete input (clicks, keys, paste) straight away, so nothing the user
 * deliberately did waits on a gesture.
 */
export function createInputCoalescer(send: (ev: BrowserInput) => void, schedule: Scheduler = (cb) => requestAnimationFrame(cb)): InputCoalescer {
  let pending: BrowserInput[] = [];
  let scheduled = false;

  const flush = () => {
    scheduled = false;
    const batch = pending;
    pending = [];
    for (const ev of batch) send(ev);
  };

  return {
    push(ev: BrowserInput) {
      pending = mergeInput(pending, ev);
      if (!isContinuous(ev)) {
        flush();
        return;
      }
      if (!scheduled) {
        scheduled = true;
        schedule(flush);
      }
    },
    flush,
  };
}

/** Decodes an encoded frame; defaults to the browser's off-thread decoder. */
export type FrameDecoder = (blob: Blob) => Promise<ImageBitmap>;

/**
 * createFrameRenderer draws screencast frames onto the canvas, keeping only the
 * newest one while a decode is in flight.
 *
 * `createImageBitmap` decodes off the main thread, unlike the `Image` +
 * object-URL round trip it replaces, and dropping superseded frames means a
 * decode that falls behind is skipped rather than queued — a stale frame has no
 * value once a newer one has arrived.
 */
export function createFrameRenderer(getCanvas: () => HTMLCanvasElement | null, decode: FrameDecoder = (blob) => createImageBitmap(blob)): (data: ArrayBuffer) => void {
  let pending: ArrayBuffer | null = null;
  let running = false;

  const pump = async () => {
    if (running) return;
    running = true;
    try {
      while (pending) {
        const data = pending;
        pending = null;
        let bitmap: ImageBitmap;
        try {
          bitmap = await decode(new Blob([data], { type: "image/jpeg" }));
        } catch {
          // A truncated or malformed frame is not worth reporting: the next one
          // is already on its way.
          continue;
        }
        const canvas = getCanvas();
        const ctx = canvas?.getContext("2d");
        if (canvas && ctx) {
          canvas.width = bitmap.width;
          canvas.height = bitmap.height;
          ctx.drawImage(bitmap, 0, 0);
        }
        bitmap.close?.();
      }
    } finally {
      running = false;
    }
  };

  return (data: ArrayBuffer) => {
    pending = data;
    void pump();
  };
}
