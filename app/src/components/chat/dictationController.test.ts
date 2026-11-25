import { describe, expect, it } from "vitest";
import type { AudioCapture } from "./dictationCapture";
import { DictationController, type DictationDeps, type DictationUiState, IDLE_DICTATION, type MicAccess, type PromptEditRequest } from "./dictationController";
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

function make(
  opts: {
    access?: MicAccess;
    availability?: DictationAvailability;
    refreshed?: DictationAvailability;
    text?: string;
    caret?: number;
    end?: number;
    sendPhrases?: string[];
    editPrompt?: DictationDeps["editPrompt"];
    micAccess?: DictationDeps["micAccess"];
  } = {},
) {
  const composer = { text: opts.text ?? "", start: opts.caret ?? (opts.text ?? "").length, end: opts.end ?? opts.caret ?? (opts.text ?? "").length };
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
  const sent: string[] = [];
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
    micAccess:
      opts.micAccess ??
      (async (ask) => {
        if (ask) access.asked++;
        return access.value as MicAccess;
      }),
    availability: () => avail.value as DictationAvailability,
    refresh: async () => {
      avail.refreshes++;
      await Promise.resolve();
      if (opts.refreshed) avail.value = opts.refreshed;
    },
    sendPhrases: () => opts.sendPhrases ?? [],
    send: () => sent.push(composer.text),
    onState: (s) => states.push(s),
    editPrompt: opts.editPrompt,
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
  return { c, composer, sockets, captures, states, access, avail, sent, press, held, release, last, flush };
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
    expect(t.sent).toEqual([]);
  });

  it("sends the message when the dictation ends with a send phrase, without the phrase", async () => {
    const t = make({ text: "Note:", sendPhrases: ["send it", "trimite"] });
    await t.c.init();
    t.press();
    t.held();
    const ws = t.sockets[0]!;
    ws.open();
    t.release();
    await t.flush();

    ws.server({ type: "final", text: "repară testul, Trimite." });

    expect(t.composer.text).toBe("Note: repară testul");
    expect(t.sent).toEqual(["Note: repară testul"]);
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

  it("leaves the hold key alone while dictation is turned off", async () => {
    const t = make({ availability: "disabled" });
    await t.c.init();
    expect(t.press()).toBe(false);
    expect(t.c.hold.busy).toBe(false);
    expect(t.captures).toHaveLength(0);
    expect(t.avail.refreshes).toBe(0);
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

  it("takes a failed access check at init as not granted, and a failed ask as unknown", async () => {
    const t = make({ micAccess: () => Promise.reject(new Error("no bridge")) });
    await t.c.init();
    t.press();
    expect(t.captures).toHaveLength(0);
    t.held();
    await t.flush();
    expect(t.captures).toHaveLength(1);
    expect(t.last()?.phase).toBe("recording");
  });

  it("doesn't open the microphone when the hold ended before access was known", async () => {
    const t = make({ access: "not-determined" });
    await t.c.init();
    t.press();
    t.access.value = "granted";
    t.held();
    t.release();
    await t.flush();
    expect(t.last()?.phase).toBe("transcribing");
    expect(t.captures).toHaveLength(0);
  });

  it("drops an access answer that arrives after the hold was cancelled", async () => {
    const t = make({ access: "not-determined", text: "keep" });
    await t.c.init();
    t.press();
    t.held();
    t.c.keyDown({ code: "KeyA", key: "a" });
    await t.flush();
    expect(t.captures).toHaveLength(0);
    expect(t.last()).toMatchObject({ phase: "idle", notice: null });
    expect(t.composer.text).toBe("keep");
  });

  it("ignores chunks and errors from a microphone it already let go", async () => {
    const t = make({ text: "keep" });
    await t.c.init();
    t.press();
    t.held();
    const cap = t.captures[0]!;
    t.c.keyDown({ code: "Escape", key: "Escape" });
    const before = t.states.length;
    cap.onChunk!(pcm(1));
    cap.onError!(new Error("late"));
    expect(t.states).toHaveLength(before);
    expect(t.sockets[0]!.audio).toEqual([]);
  });

  it("drops a microphone that fails before the hold counts, without a message", async () => {
    const t = make();
    await t.c.init();
    t.press();
    t.captures[0]!.onChunk!(pcm(1));
    t.captures[0]!.onError!("busy");
    t.held();
    expect(t.sockets[0]!.audio).toEqual([]);
    expect(t.last()?.error).toBeNull();
  });

  it("shows the denied state when the microphone is refused before the hold counts", async () => {
    const t = make();
    await t.c.init();
    t.press();
    t.captures[0]!.onError!(Object.assign(new Error("no"), { name: "SecurityError" }));
    expect(t.last()?.notice).toBe("mic_denied");
    expect(t.sockets).toHaveLength(0);
  });

  it("reports a microphone failure without a message by its value", async () => {
    const t = make();
    await t.c.init();
    t.press();
    t.held();
    t.captures[0]!.onError!("device gone");
    expect(t.last()?.error).toBe("Couldn't open the microphone: device gone");
  });

  it("still asks for the final text when the microphone fails to finish", async () => {
    const t = make();
    await t.c.init();
    t.press();
    t.held();
    t.sockets[0]!.open();
    t.captures[0]!.finish = () => Promise.reject(new Error("flush failed"));
    t.release();
    await t.flush();
    expect(t.sockets[0]!.controls).toEqual([{ type: "stop" }]);
  });

  it("Esc during a refused hold leaves the session that is transcribing", async () => {
    const t = make();
    await t.c.init();
    t.press();
    t.held();
    t.release();
    await t.flush();
    t.press();
    t.held();
    expect(t.c.keyDown({ code: "Escape", key: "Escape" })).toBe(true);
    expect(t.sockets[0]!.closed).toBe(false);
    expect(t.last()?.phase).toBe("transcribing");
  });

  it("turns a binary_missing error into the unavailable notice", async () => {
    const t = make();
    await t.c.init();
    t.press();
    t.held();
    t.sockets[0]!.open();
    t.sockets[0]!.server({ type: "error", code: "binary_missing", message: "no whisper" });
    expect(t.last()).toMatchObject({ notice: "unavailable", error: null });
  });

  it("dismiss leaves a clean state alone", () => {
    const t = make();
    t.c.dismiss();
    expect(t.states).toEqual([]);
  });

  it("an edit-button hold without editPrompt leaves the draft", async () => {
    const t = make({ text: "draft" });
    await t.c.init();
    t.c.hold.pointerDown("edit");
    t.sockets[0]!.open();
    t.c.hold.pointerDown();
    await t.flush();
    t.sockets[0]!.server({ type: "final", text: "make it shorter" });
    expect(t.composer.text).toBe("draft");
    expect(t.last()?.phase).toBe("idle");
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

/** An editPrompt whose calls a test resolves or rejects by hand. */
function manualEdit() {
  const calls: Array<{ req: PromptEditRequest; signal: AbortSignal; resolve: (r: { text: string; from: number; to: number }) => void; reject: (e: unknown) => void }> = [];
  const editPrompt: DictationDeps["editPrompt"] = (req, signal) =>
    new Promise((resolve, reject) => {
      calls.push({ req, signal, resolve, reject });
    });
  return { calls, editPrompt };
}

describe("DictationController edit holds", () => {
  const shiftAlt = { code: DICTATION_HOLD_CODE, key: "Alt", shiftKey: true };

  /** Holds Shift+key, hears instruction, releases and gets the final. */
  async function speak(t: ReturnType<typeof make>, partial: [string, string], final: string) {
    await t.c.init();
    t.c.keyDown(shiftAlt);
    t.held();
    const ws = t.sockets[t.sockets.length - 1]!;
    ws.open();
    ws.server({ type: "partial", committed: partial[0], tentative: partial[1] });
    t.release();
    await t.flush();
    ws.server({ type: "final", text: final });
    return ws;
  }

  it("shows the instruction without touching the composer, then applies the edit and selects it", async () => {
    const e = manualEdit();
    const t = make({ text: "fix the bug", caret: 4, end: 7, editPrompt: e.editPrompt });
    await t.c.init();
    t.c.keyDown(shiftAlt);
    t.held();
    expect(t.last()).toMatchObject({ phase: "recording", mode: "edit", instruction: "" });
    const ws = t.sockets[0]!;
    ws.open();
    ws.server({ type: "partial", committed: "make it", tentative: "plural" });
    expect(t.last()?.instruction).toBe("make it plural");
    expect(t.composer).toEqual({ text: "fix the bug", start: 4, end: 7 });
    t.release();
    await t.flush();
    ws.server({ type: "final", text: " make it plural " });
    expect(t.last()).toMatchObject({ phase: "editing", instruction: "make it plural" });
    expect(t.c.busy).toBe(true);
    expect(e.calls[0]!.req).toEqual({ draft: "fix the bug", start: 4, end: 7, instruction: "make it plural" });

    e.calls[0]!.resolve({ text: "fix the bugs", from: 4, to: 8 });
    await t.flush();
    expect(t.composer).toEqual({ text: "fix the bugs", start: 4, end: 8 });
    expect(t.last()).toMatchObject({ phase: "idle", undo: true });
    expect(t.sent).toEqual([]);
  });

  it("ignores send phrases", async () => {
    const e = manualEdit();
    const t = make({ text: "x", sendPhrases: ["send it"], editPrompt: e.editPrompt });
    await speak(t, ["", ""], "shorter, send it");
    expect(e.calls[0]!.req.instruction).toBe("shorter, send it");
    expect(t.sent).toEqual([]);
  });

  it("undoes with Cmd/Ctrl+Z or undoEdit, until another key is typed", async () => {
    const e = manualEdit();
    const t = make({ text: "draft", caret: 1, editPrompt: e.editPrompt });
    await speak(t, ["", ""], "longer");
    e.calls[0]!.resolve({ text: "a longer draft", from: 0, to: 14 });
    await t.flush();
    // Modifiers alone keep the undo.
    expect(t.c.keyDown({ code: "MetaLeft", key: "Meta", metaKey: true })).toBe(false);
    expect(t.c.keyDown({ code: "KeyZ", key: "z", metaKey: true })).toBe(true);
    expect(t.composer).toEqual({ text: "draft", start: 1, end: 1 });
    expect(t.last()?.undo).toBe(false);
    // Once undone, Cmd+Z is the textarea's again.
    expect(t.c.keyDown({ code: "KeyZ", key: "z", metaKey: true })).toBe(false);

    await speak(t, ["", ""], "again");
    e.calls[1]!.resolve({ text: "edited", from: 0, to: 6 });
    await t.flush();
    t.c.undoEdit();
    expect(t.composer.text).toBe("draft");

    await speak(t, ["", ""], "third");
    e.calls[2]!.resolve({ text: "third", from: 0, to: 5 });
    await t.flush();
    expect(t.c.keyDown({ code: "KeyA", key: "a" })).toBe(false);
    expect(t.last()?.undo).toBe(false);
    t.c.undoEdit();
    expect(t.composer.text).toBe("third");
  });

  it("doesn't undo over a draft changed since the edit", async () => {
    const e = manualEdit();
    const t = make({ text: "a", editPrompt: e.editPrompt });
    await speak(t, ["", ""], "b");
    e.calls[0]!.resolve({ text: "b", from: 0, to: 1 });
    await t.flush();
    t.composer.text = "b typed";
    t.c.undoEdit();
    expect(t.composer.text).toBe("b typed");
  });

  it("a new hold drops the undo", async () => {
    const e = manualEdit();
    const t = make({ text: "a", editPrompt: e.editPrompt });
    await speak(t, ["", ""], "b");
    e.calls[0]!.resolve({ text: "b", from: 0, to: 1 });
    await t.flush();
    t.c.keyDown({ code: DICTATION_HOLD_CODE, key: "Alt" });
    t.held();
    expect(t.last()).toMatchObject({ mode: "dictate", undo: false });
  });

  it("Esc drops an edit under way, and its late result changes nothing", async () => {
    const e = manualEdit();
    const t = make({ text: "keep", editPrompt: e.editPrompt });
    await speak(t, ["", ""], "rewrite");
    // Other keys pass through to the read-only composer.
    expect(t.c.keyDown({ code: "KeyA", key: "a" })).toBe(false);
    expect(t.c.keyDown({ code: "Escape", key: "Escape" })).toBe(true);
    expect(e.calls[0]!.signal.aborted).toBe(true);
    expect(t.last()).toMatchObject({ phase: "idle", instruction: "" });
    e.calls[0]!.resolve({ text: "late", from: 0, to: 4 });
    await t.flush();
    expect(t.composer.text).toBe("keep");
  });

  it("a rejected edit after Esc says nothing", async () => {
    const e = manualEdit();
    const t = make({ text: "keep", editPrompt: e.editPrompt });
    await speak(t, ["", ""], "rewrite");
    t.c.keyDown({ code: "Escape", key: "Escape" });
    e.calls[0]!.reject(new Error("aborted"));
    await t.flush();
    expect(t.last()?.error).toBeNull();
  });

  it("reports a failed edit and leaves the draft", async () => {
    const e = manualEdit();
    const t = make({ text: "keep", editPrompt: e.editPrompt });
    await speak(t, ["", ""], "rewrite");
    e.calls[0]!.reject(new Error("rate limited"));
    await t.flush();
    expect(t.composer.text).toBe("keep");
    expect(t.last()).toMatchObject({ phase: "idle", error: "Couldn't edit the prompt: rate limited" });

    await speak(t, ["", ""], "again");
    e.calls[1]!.reject("plain");
    await t.flush();
    expect(t.last()?.error).toBe("Couldn't edit the prompt: plain");
  });

  it("drops the result when the draft changed meanwhile", async () => {
    const e = manualEdit();
    const t = make({ text: "keep", editPrompt: e.editPrompt });
    await speak(t, ["", ""], "rewrite");
    t.composer.text = "changed";
    e.calls[0]!.resolve({ text: "new", from: 0, to: 3 });
    await t.flush();
    expect(t.composer.text).toBe("changed");
    expect(t.last()?.error).toMatch(/draft changed/);
  });

  it("does nothing for an empty instruction", async () => {
    const e = manualEdit();
    const t = make({ text: "keep", editPrompt: e.editPrompt });
    await speak(t, ["", ""], "  ");
    expect(e.calls).toHaveLength(0);
    expect(t.last()?.phase).toBe("idle");
  });

  it("doesn't start another hold while an edit is under way", async () => {
    const e = manualEdit();
    const t = make({ text: "keep", editPrompt: e.editPrompt });
    await speak(t, ["", ""], "rewrite");
    const n = t.sockets.length;
    // Shift+key types as usual meanwhile, and a plain hold doesn't start.
    expect(t.c.hold.keyDown(shiftAlt)).toBe(false);
    t.c.hold.keyDown({ code: DICTATION_HOLD_CODE, key: "Alt" });
    t.held();
    expect(t.sockets).toHaveLength(n);
    expect(t.last()?.phase).toBe("editing");
  });

  it("a failed stream in an edit hold leaves the composer as it was", async () => {
    const e = manualEdit();
    const t = make({ text: "keep", editPrompt: e.editPrompt });
    await t.c.init();
    t.c.keyDown(shiftAlt);
    t.held();
    const ws = t.sockets[0]!;
    ws.open();
    ws.server({ type: "partial", committed: "words", tentative: "" });
    ws.server({ type: "error", code: "transcribe_failed", message: "boom" });
    expect(t.composer.text).toBe("keep");
    expect(t.last()?.error).toBe("boom");
  });

  it("without editPrompt, Shift with the key types as usual", async () => {
    const t = make({ text: "x" });
    await t.c.init();
    expect(t.c.keyDown(shiftAlt)).toBe(false);
    expect(t.c.hold.busy).toBe(false);
  });

  it("dispose aborts an edit under way", async () => {
    const e = manualEdit();
    const t = make({ text: "keep", editPrompt: e.editPrompt });
    await speak(t, ["", ""], "rewrite");
    t.c.dispose();
    expect(e.calls[0]!.signal.aborted).toBe(true);
  });

  it("the edit button starts an edit hold", async () => {
    const e = manualEdit();
    const t = make({ text: "a", editPrompt: e.editPrompt });
    await t.c.init();
    t.c.hold.pointerDown("edit");
    expect(t.last()).toMatchObject({ phase: "recording", mode: "edit" });
  });
});
