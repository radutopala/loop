/** The key held to dictate: Right Option on macOS, Right Alt elsewhere. The
 *  app has no keybinding settings, so it's this one constant. A right-hand
 *  modifier alone types nothing, and the left Option/Alt stays free for the
 *  composer's Alt+Enter and for typing characters. */
export const DICTATION_HOLD_CODE = "AltRight";
/** How long the key must be held alone before dictation starts. */
export const DICTATION_HOLD_MS = 200;

/** The keyboard event fields the hold machine reads. */
export interface HoldKey {
  code: string;
  key: string;
  repeat?: boolean;
  ctrlKey?: boolean;
  metaKey?: boolean;
  shiftKey?: boolean;
}

export interface HoldCallbacks {
  /** The key went down (or the button was pressed): capture may begin, unseen. */
  prepare(): void;
  /** Dictation starts for real. */
  start(): void;
  /** Released after start: finish and take the final text. */
  stop(): void;
  /** Another key or Esc after start: drop it and restore the composer. */
  cancel(): void;
  /** Ended before start (a tap, or a combo such as Option+E): nothing to undo. */
  abort(): void;
}

export interface HoldTimers {
  set(fn: () => void, ms: number): unknown;
  clear(handle: unknown): void;
}

const realTimers: HoldTimers = {
  set: (fn, ms) => setTimeout(fn, ms),
  clear: (h) => clearTimeout(h as ReturnType<typeof setTimeout>),
};

type Phase = "idle" | "armed" | "active";

/**
 * The hold-to-talk state machine. The key alone, held for holdMs, starts
 * dictation; releasing it stops. Any other key while armed aborts (so the
 * key's combos still type); while active it cancels, as does Esc. The mic
 * button starts at once on press and stops on release; while it is held
 * only Esc cancels. Each key method returns true when it took the event, for
 * the caller to prevent its default.
 */
export class HoldToTalk {
  private phase: Phase = "idle";
  private source: "key" | "pointer" = "key";
  private timer: unknown = null;

  constructor(
    private readonly cb: HoldCallbacks,
    private readonly timers: HoldTimers = realTimers,
    private readonly code: string = DICTATION_HOLD_CODE,
    private readonly holdMs: number = DICTATION_HOLD_MS,
  ) {}

  /** Whether a hold is under way (armed or active). */
  get busy(): boolean {
    return this.phase !== "idle";
  }

  get active(): boolean {
    return this.phase === "active";
  }

  keyDown(e: HoldKey): boolean {
    if (e.code === this.code && this.source === "key" && this.phase !== "idle") return true;
    if (e.code === this.code && this.phase === "idle") {
      if (e.repeat || e.ctrlKey || e.metaKey || e.shiftKey) return false;
      this.phase = "armed";
      this.source = "key";
      this.cb.prepare();
      this.timer = this.timers.set(() => {
        this.timer = null;
        if (this.phase !== "armed") return;
        this.phase = "active";
        this.cb.start();
      }, this.holdMs);
      return false;
    }
    if (this.phase === "armed") {
      this.reset();
      this.cb.abort();
      return false;
    }
    if (this.phase === "active" && (this.source === "key" || e.key === "Escape")) {
      this.reset();
      this.cb.cancel();
      return true;
    }
    return false;
  }

  keyUp(e: HoldKey): boolean {
    if (e.code !== this.code || this.source !== "key" || this.phase === "idle") return false;
    const was = this.phase;
    this.reset();
    if (was === "armed") {
      this.cb.abort();
      return false;
    }
    this.cb.stop();
    return true;
  }

  /** The window or composer lost focus: the release may never arrive, so
   *  take it as one. */
  blur(): void {
    if (this.source !== "key" || this.phase === "idle") return;
    this.keyUp({ code: this.code, key: "" });
  }

  pointerDown(): void {
    if (this.phase !== "idle") return;
    this.phase = "active";
    this.source = "pointer";
    this.cb.prepare();
    this.cb.start();
  }

  pointerUp(): void {
    if (this.source !== "pointer" || this.phase !== "active") return;
    this.reset();
    this.cb.stop();
  }

  pointerCancel(): void {
    if (this.source !== "pointer" || this.phase !== "active") return;
    this.reset();
    this.cb.cancel();
  }

  private reset(): void {
    if (this.timer !== null) this.timers.clear(this.timer);
    this.timer = null;
    this.phase = "idle";
    this.source = "key";
  }
}
