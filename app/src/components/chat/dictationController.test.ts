import { describe, expect, it } from "vitest";
import type { AudioCapture } from "./dictationCapture";
import { DictationController, type DictationUiState, IDLE_DICTATION, type MicAccess } from "./dictationController";
import { DICTATION_HOLD_CODE, type HoldTimers } from "./dictationHold";
import type { DictationAvailability } from "./dictationModel";
import { FakeSocket } from "./dictationTestSocket";

class FakeCapture implements AudioCapture {
  onChunk: ((buf: ArrayBuffer) => void) | null = null;
  onError: ((err: unknown) => void) | null = null;
  stopped = false;
  finished = false;
  /** Sent by finish, as the worklet's last partial chunk. */
  tail: ArrayBuffer | null = null;
  start(onChunk: (buf: ArrayBuffer) => void, onError: (err: unknown) => void) {
    this.onChunk = onChunk;
    this.onError = onError;
  }
  async finish() {
    if (this.tail) this.onChunk?.(this.tail);
    this.finished = true;
    this.stopped = true;
  }
  stop() {
    this.stopped = true;
  }
}

function make(opts: { access?: MicAccess; availability?: DictationAvailability; refreshed?: DictationAvailability; text?: string; caret?: number } = {}) {
  const composer = { text: opts.text ?? "", start: opts.caret ?? (opts.text ?? "").length, end: opts.caret ?? (opts.text ?? "").length };
  const sockets: FakeSocket[] = [];
  const captures: FakeCapture[] = [];
  const states: DictationUiState[] = [];
  let fire: (() => void) | null = null;
  const timers: HoldTimers = {
    set: (fn) => {
      fire = fn;
      return 1;
    },
    clear: () => {
      fire = null;
    },
  };
  const access = { value: opts.access ?? "granted", asked: 0 };
  const avail = { value: opts.availability ?? "ready", refreshes: 0 };
  const c = new DictationController({
    read: () => ({ ...composer }),
    write: (text, start, end) => Object.assign(composer, { text, start, end }),
    openSocket: () => {
      const s = new FakeSocket();
      sockets.push(s);
      return s;
    },
    createCapture: () => {
      const cap = new FakeCapture();
      captures.push(cap);
      return cap;
    },
    micAccess: async (ask) => {
      if (ask) access.asked++;
      return access.value as MicAccess;
    },
    availability: () => avail.value as DictationAvailability,
    refresh: async () => {
      avail.refreshes++;
      await Promise.resolve();
      if (opts.refreshed) avail.value = opts.refreshed;
    },
    onState: (s) => states.push(s),
    timers,
  });
  const alt = { code: DICTATION_HOLD_CODE, key: "Alt" };
  const press = () => c.keyDown(alt);
  const held = () => {
    const f = fire;
    fire = null;
    f?.();
  };
  const release = () => c.keyUp(alt);
  const last = () => states[states.length - 1];
  const flush = () => new Promise((r) => setTimeout(r, 0));
  return { c, composer, sockets, captures, states, access, avail, press, held, release, last, flush };
}

const pcm = (n: number) => new Uint8Array([n, 0]).buffer;

describe("DictationController", () => {
  it("captures from key-down, streams once held, previews live and leaves the final text at the caret", async () => {
    const t = make({ text: "Fix  please", caret: 4 });
    await t.c.init();
    t.press();
    expect(t.captures).toHaveLength(1);
    expect(t.sockets).toHaveLength(0);
    // Audio before the hold counts waits.
    t.captures[0]!.onChunk!(pcm(1));
    t.held();
    expect(t.last()?.phase).toBe("recording");
    const ws = t.sockets[0]!;
    t.captures[0]!.onChunk!(pcm(2));
    ws.open();
    expect(ws.audio).toEqual([pcm(1), pcm(2)]);

    ws.server({ type: "partial", committed: "the", tentative: "bug" });
    expect(t.composer.text).toBe("Fix the bug please");
    // The tentative tail is selected so it stands apart.
    expect(t.composer.text.slice(t.composer.start, t.composer.end)).toBe("bug");

    t.captures[0]!.tail = pcm(3);
    t.release();
    expect(t.last()?.phase).toBe("transcribing");
    await t.flush();
    expect(ws.audio).toEqual([pcm(1), pcm(2), pcm(3)]);
    expect(ws.controls).toEqual([{ type: "stop" }]);
    expect(t.captures[0]!.finished).toBe(true);

    ws.server({ type: "final", text: "the login bug" });
    expect(t.composer.text).toBe("Fix the login bug please");
    expect(t.composer.start).toBe("Fix the login bug".length);
    expect(t.composer.end).toBe(t.composer.start);
    expect(t.last()?.phase).toBe("idle");
    // Nothing was sent as a message: the stream only ever saw audio and a stop.
    expect(ws.controls).toEqual([{ type: "stop" }]);
  });

  it("shows a level while recording", async () => {
    const t = make();
    await t.c.init();
    t.press();
    t.held();
    t.sockets[0]!.open();
    const loud = new Int16Array([20000, -20000]).buffer;
    t.captures[0]!.onChunk!(loud);
    expect(t.last()?.level).toBeGreaterThan(0.5);
  });

  it("cancels on another key and restores the composer and its selection", async () => {
    const t = make({ text: "draft", caret: 2 });
    await t.c.init();
    t.press();
    t.held();
    const ws = t.sockets[0]!;
    ws.open();
    ws.server({ type: "partial", committed: "words", tentative: "" });
    expect(t.composer.text).not.toBe("draft");
    expect(t.c.keyDown({ code: "KeyA", key: "a" })).toBe(true);
    expect(t.composer).toEqual({ text: "draft", start: 2, end: 2 });
    expect(ws.controls).toEqual([{ type: "cancel" }]);
    expect(t.captures[0]!.stopped).toBe(true);
    expect(t.last()?.phase).toBe("idle");
    // A late final from the dropped session changes nothing.
    ws.server({ type: "final", text: "late" });
    expect(t.composer.text).toBe("draft");
  });

  it("cancels on Esc", async () => {
    const t = make({ text: "x" });
    await t.c.init();
    t.press();
    t.held();
    t.sockets[0]!.open();
    t.sockets[0]!.server({ type: "partial", committed: "", tentative: "hm" });
    t.c.keyDown({ code: "Escape", key: "Escape" });
    expect(t.composer.text).toBe("x");
  });

  it("drops the pre-captured audio when the key was part of a combo", async () => {
    const t = make();
    await t.c.init();
    t.press();
    t.captures[0]!.onChunk!(pcm(1));
    expect(t.c.keyDown({ code: "KeyE", key: "´" })).toBe(false);
    expect(t.captures[0]!.stopped).toBe(true);
    expect(t.sockets).toHaveLength(0);
    expect(t.states).toEqual([]);
  });

  it("asks for the microphone at the start when access isn't granted yet, then captures", async () => {
    const t = make({ access: "not-determined" });
    await t.c.init();
    t.press();
    expect(t.captures).toHaveLength(0);
    t.access.value = "granted";
    t.held();
    await t.flush();
    expect(t.access.asked).toBe(1);
    expect(t.captures).toHaveLength(1);
    // Granted now: the next hold captures from key-down.
    t.release();
    await t.flush();
    t.sockets[0]!.open();
    t.sockets[0]!.server({ type: "final", text: "" });
    t.press();
    expect(t.captures).toHaveLength(2);
  });

  it("shows the denied state and leaves the composer when the microphone is refused", async () => {
    const t = make({ access: "denied", text: "keep" });
    await t.c.init();
    t.press();
    t.held();
    await t.flush();
    expect(t.last()?.notice).toBe("mic_denied");
    expect(t.last()?.phase).toBe("idle");
    expect(t.captures).toHaveLength(0);
    expect(t.sockets[0]!.closed).toBe(true);
    expect(t.composer.text).toBe("keep");
  });

  it("shows the denied state when getUserMedia refuses", async () => {
    const t = make({ text: "keep" });
    await t.c.init();
    t.press();
    t.held();
    t.captures[0]!.onError!(Object.assign(new Error("no"), { name: "NotAllowedError" }));
    expect(t.last()?.notice).toBe("mic_denied");
    expect(t.composer.text).toBe("keep");
  });

  it("reports another microphone failure as an error", async () => {
    const t = make();
    await t.c.init();
    t.press();
    t.held();
    t.captures[0]!.onError!(new Error("device busy"));
    expect(t.last()?.error).toBe("Couldn't open the microphone: device busy");
    expect(t.last()?.phase).toBe("idle");
  });

  it.each([
    ["needs_model", { notice: "needs_model", error: null }],
    ["downloading", { notice: "needs_model", error: null }],
    ["unavailable", { notice: "unavailable", error: null }],
    ["unknown", { notice: null, error: "Dictation isn't available from the daemon." }],
  ] as const)("doesn't start when dictation is %s", async (availability, want) => {
    const t = make({ availability });
    await t.c.init();
    t.press();
    expect(t.captures).toHaveLength(0);
    t.held();
    await t.flush();
    expect(t.sockets).toHaveLength(0);
    expect(t.last()).toMatchObject({ phase: "idle", ...want });
    t.release();
    expect(t.last()?.phase).toBe("idle");
    t.c.dismiss();
    expect(t.last()).toMatchObject({ notice: null, error: null });
  });

  it.each([
    ["needs_model", { notice: "needs_model", error: null }],
    ["ready", { notice: null, error: null }],
  ] as const)("asks the daemon again when its status was never read, then tells it's %s", async (refreshed, want) => {
    const t = make({ availability: "unknown", refreshed });
    await t.c.init();
    t.press();
    t.held();
    // The start waits on the refresh the press began instead of asking twice.
    expect(t.avail.refreshes).toBe(1);
    await t.flush();
    expect(t.sockets).toHaveLength(0);
    expect(t.last() ?? IDLE_DICTATION).toMatchObject({ phase: "idle", ...want });
  });

  it("asks again on the next hold once a refresh is done", async () => {
    const t = make({ availability: "unknown" });
    await t.c.init();
    t.press();
    await t.flush();
    t.held();
    await t.flush();
    expect(t.avail.refreshes).toBe(2);
    expect(t.last()?.error).toBe("Dictation isn't available from the daemon.");
  });

  it("keeps the committed words and shows the error when the server fails", async () => {
    const t = make({ text: "a" });
    await t.c.init();
    t.press();
    t.held();
    const ws = t.sockets[0]!;
    ws.open();
    ws.server({ type: "partial", committed: "solid", tentative: "shaky" });
    ws.server({ type: "error", code: "engine_failed", message: "whisper crashed" });
    expect(t.composer.text).toBe("a solid");
    expect(t.last()).toMatchObject({ phase: "idle", error: "whisper crashed" });
    // The key's release after that is nothing.
    t.release();
    await t.flush();
    expect(ws.controls).toEqual([]);
  });

  it("turns a model_missing error into the download prompt", async () => {
    const t = make();
    await t.c.init();
    t.press();
    t.held();
    t.sockets[0]!.open();
    t.sockets[0]!.server({ type: "error", code: "model_missing", message: "x" });
    expect(t.last()?.notice).toBe("needs_model");
  });

  it("works from the mic button: starts at once, ends on release", async () => {
    const t = make({ text: "" });
    await t.c.init();
    t.c.hold.pointerDown();
    expect(t.last()?.phase).toBe("recording");
    const ws = t.sockets[0]!;
    ws.open();
    t.held();
    t.c.hold.pointerUp();
    await t.flush();
    expect(ws.controls).toEqual([{ type: "stop" }]);
    ws.server({ type: "final", text: "from the button" });
    expect(t.composer.text).toBe("from the button");
  });

  it("keeps a clicked mic on until the next click", async () => {
    const t = make({ text: "" });
    await t.c.init();
    t.c.hold.pointerDown();
    t.c.hold.pointerUp();
    const ws = t.sockets[0]!;
    ws.open();
    expect(t.last()?.phase).toBe("recording");
    t.c.hold.pointerDown();
    await t.flush();
    expect(ws.controls).toEqual([{ type: "stop" }]);
    ws.server({ type: "final", text: "clicked" });
    ws.close();
    expect(t.composer.text).toBe("clicked");
    expect(t.c.hold.busy).toBe(false);
  });

  it("frees a clicked mic when its session fails, so the next click starts again", async () => {
    const t = make({ text: "" });
    await t.c.init();
    t.c.hold.pointerDown();
    t.c.hold.pointerUp();
    t.sockets[0]!.open();
    t.sockets[0]!.server({ type: "error", code: "internal", message: "boom" });
    t.sockets[0]!.close();
    expect(t.c.hold.busy).toBe(false);
    t.c.hold.pointerDown();
    expect(t.sockets).toHaveLength(2);
  });

  it("asks the daemon again when the model looked missing, and refreshes once per hold", async () => {
    const t = make({ availability: "needs_model", refreshed: "ready" });
    await t.c.init();
    t.c.hold.pointerDown();
    await t.flush();
    expect(t.avail.refreshes).toBe(1);
    expect(t.sockets).toHaveLength(0);
    expect(t.c.hold.busy).toBe(false);
    // The status is fresh now: the next press starts.
    t.c.hold.pointerDown();
    expect(t.sockets).toHaveLength(1);
  });

  it("ignores a new hold while the last one is still transcribing", async () => {
    const t = make();
    await t.c.init();
    t.press();
    t.held();
    t.release();
    await t.flush();
    t.press();
    t.held();
    expect(t.sockets).toHaveLength(1);
    expect(t.captures).toHaveLength(1);
    t.release();
    await t.flush();
    expect(t.last()?.phase).toBe("transcribing");
  });

  it("disposes without touching the composer", async () => {
    const t = make({ text: "x" });
    await t.c.init();
    t.press();
    t.held();
    t.sockets[0]!.open();
    t.sockets[0]!.server({ type: "partial", committed: "y", tentative: "" });
    t.c.dispose();
    expect(t.sockets[0]!.closed).toBe(true);
    expect(t.captures[0]!.stopped).toBe(true);
    expect(t.composer.text).toBe("x y");
  });
});
