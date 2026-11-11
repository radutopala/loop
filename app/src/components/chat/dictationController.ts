import type { AudioCapture } from "./dictationCapture";
import { type HoldKey, type HoldTimers, HoldToTalk } from "./dictationHold";
import type { DictationAvailability } from "./dictationModel";
import { pcmLevel } from "./dictationPcm";
import { DictationStream, type SocketLike } from "./dictationStream";
import { type ComposerSnapshot, previewDictation, spliceDictation } from "./dictationText";

/** The OS's answer on microphone access (macOS asks once per app). */
export type MicAccess = "granted" | "denied" | "not-determined" | "restricted" | "unknown";

export type DictationNotice = "needs_model" | "unavailable" | "mic_denied";

export interface DictationUiState {
  phase: "idle" | "recording" | "transcribing";
  /** Input level, 0..1, while recording. */
  level: number;
  error: string | null;
  notice: DictationNotice | null;
}

export const IDLE_DICTATION: DictationUiState = { phase: "idle", level: 0, error: null, notice: null };

export interface DictationDeps {
  /** The composer's text and selection now. */
  read(): ComposerSnapshot;
  /** Replaces the composer's text and selects start..end. */
  write(text: string, start: number, end: number): void;
  openSocket(): SocketLike;
  createCapture(): AudioCapture;
  /** The microphone access state; with ask, prompts when it's undecided. */
  micAccess(ask: boolean): Promise<MicAccess>;
  availability(): DictationAvailability;
  /** Asks the daemon for the dictation status again. */
  refresh(): Promise<void>;
  onState(s: DictationUiState): void;
  timers?: HoldTimers;
}

function isPermissionError(err: unknown): boolean {
  const name = (err as { name?: string } | null)?.name;
  return name === "NotAllowedError" || name === "SecurityError";
}

/**
 * Runs push-to-talk dictation into the composer. The hold key or mic button
 * (see HoldToTalk) drives it: the microphone opens on key-down when access
 * is already granted, so the first words aren't lost, and audio waits until
 * the stream opens once the hold counts. Partials replace the dictated span
 * live, with the tentative tail selected so it stands apart; the final text
 * takes its place and the caret goes after it. A cancel puts the composer
 * back as it was. Nothing is ever sent.
 */
export class DictationController {
  readonly hold: HoldToTalk;
  private state: DictationUiState = IDLE_DICTATION;
  private capture: AudioCapture | null = null;
  private pending: ArrayBuffer[] = [];
  private stream: DictationStream | null = null;
  private base: ComposerSnapshot | null = null;
  private committed = "";
  /** Whether the current hold runs a session (false for a refused one). */
  private holding = false;
  private micGranted = false;
  /** Bumped per session, so late callbacks from an old one are dropped. */
  private session = 0;
  private refreshing: Promise<void> | null = null;

  constructor(private readonly d: DictationDeps) {
    this.hold = new HoldToTalk(
      {
        prepare: () => this.prepare(),
        start: () => this.start(),
        stop: () => void this.stop(),
        cancel: () => this.cancel(),
        abort: () => this.abort(),
      },
      d.timers,
    );
  }

  /** Learns whether microphone access is already granted, without asking. */
  async init(): Promise<void> {
    this.micGranted = (await this.d.micAccess(false).catch(() => "unknown")) === "granted";
  }

  get busy(): boolean {
    return this.state.phase !== "idle";
  }

  keyDown(e: HoldKey): boolean {
    return this.hold.keyDown(e);
  }

  keyUp(e: HoldKey): boolean {
    return this.hold.keyUp(e);
  }

  /** Clears a notice or error shown after a hold. */
  dismiss(): void {
    if (this.state.notice || this.state.error) this.set({ notice: null, error: null });
  }

  /** Drops any session without touching the composer (unmount). */
  dispose(): void {
    this.stream?.cancel();
    this.reset();
  }

  private set(patch: Partial<DictationUiState>): void {
    this.state = { ...this.state, ...patch };
    this.d.onState(this.state);
  }

  private prepare(): void {
    // A status that isn't ready may be stale (the daemon was restarting, or
    // the model was fetched elsewhere): ask again, so the hold that follows
    // goes by what the daemon says now.
    if (this.d.availability() !== "ready") void this.refreshStatus();
    if (this.stream || this.d.availability() !== "ready" || !this.micGranted) return;
    this.openMic();
  }

  private start(): void {
    this.holding = false;
    if (this.stream) return;
    const a = this.d.availability();
    if (a !== "ready") {
      this.abort();
      this.hold.release();
      void this.refreshStatus().then(() => this.notReady(this.d.availability()));
      return;
    }
    this.holding = true;
    const id = ++this.session;
    this.base = this.d.read();
    this.committed = "";
    this.stream = new DictationStream(this.d.openSocket(), {
      onPartial: (c, t) => id === this.session && this.partial(c, t),
      onFinal: (text) => id === this.session && this.final(text),
      onError: (code, message) => id === this.session && this.failed(code, message),
      onEnd: () => id === this.session && this.reset(),
    });
    for (const buf of this.pending) this.stream.sendAudio(buf);
    this.pending = [];
    this.set({ phase: "recording", level: 0, error: null, notice: null });
    if (this.capture) return;
    void this.d
      .micAccess(true)
      .catch((): MicAccess => "unknown")
      .then((access) => {
        if (id !== this.session || !this.stream) return;
        if (access === "granted" || access === "unknown") {
          this.micGranted = access === "granted";
          if (this.state.phase === "recording") this.openMic();
          return;
        }
        this.micDenied();
      });
  }

  /** One status refresh at a time; the start that follows a prepare waits
   *  on the same one. */
  private refreshStatus(): Promise<void> {
    this.refreshing ??= this.d.refresh().finally(() => {
      this.refreshing = null;
    });
    return this.refreshing;
  }

  /** Tells why a hold didn't start; once ready, the next hold does. */
  private notReady(a: DictationAvailability): void {
    if (a === "ready") return;
    if (a === "unknown") this.set({ error: "Dictation isn't available from the daemon." });
    else this.set({ notice: a === "unavailable" ? "unavailable" : "needs_model", error: null });
  }

  private openMic(): void {
    const cap = this.d.createCapture();
    this.capture = cap;
    cap.start(
      (buf) => {
        if (this.capture !== cap && this.state.phase !== "transcribing") return;
        if (this.stream) {
          this.stream.sendAudio(buf);
          if (this.state.phase === "recording") this.set({ level: pcmLevel(buf) });
        } else this.pending.push(buf);
      },
      (err) => {
        if (this.capture !== cap) return;
        if (isPermissionError(err)) {
          this.micDenied();
          return;
        }
        this.capture = null;
        this.pending = [];
        if (!this.stream) return;
        this.restore();
        this.stream.cancel();
        this.set({ error: `Couldn't open the microphone: ${(err as Error)?.message || String(err)}` });
      },
    );
  }

  private micDenied(): void {
    this.micGranted = false;
    this.capture?.stop();
    this.capture = null;
    this.pending = [];
    if (this.stream) {
      this.restore();
      this.stream.cancel();
    }
    this.set({ notice: "mic_denied" });
  }

  private async stop(): Promise<void> {
    if (!this.holding) {
      this.abort();
      return;
    }
    this.holding = false;
    const stream = this.stream;
    if (!stream) return;
    this.set({ phase: "transcribing", level: 0 });
    const cap = this.capture;
    this.capture = null;
    await cap?.finish().catch(() => {});
    stream.stop();
  }

  private cancel(): void {
    if (!this.holding) {
      this.abort();
      return;
    }
    this.holding = false;
    this.restore();
    this.stream?.cancel();
    this.reset();
  }

  private abort(): void {
    if (this.stream) return;
    this.capture?.stop();
    this.capture = null;
    this.pending = [];
  }

  private partial(committed: string, tentative: string): void {
    if (!this.base) return;
    this.committed = committed;
    const p = previewDictation(this.base, committed, tentative);
    this.d.write(p.text, p.tentativeFrom, p.to);
  }

  private final(text: string): void {
    if (!this.base) return;
    const out = spliceDictation(this.base, text);
    this.d.write(out.text, out.to, out.to);
  }

  private failed(code: string, message: string): void {
    // Keep what was already committed; only the unstable tail goes.
    if (this.base) {
      const out = spliceDictation(this.base, this.committed);
      this.d.write(out.text, out.to, out.to);
    }
    if (code === "model_missing") this.set({ notice: "needs_model" });
    else if (code === "binary_missing") this.set({ notice: "unavailable" });
    else this.set({ error: message });
  }

  private restore(): void {
    const b = this.base;
    if (b) this.d.write(b.text, b.start, b.end);
  }

  private reset(): void {
    this.capture?.stop();
    this.capture = null;
    this.pending = [];
    this.stream = null;
    this.base = null;
    this.committed = "";
    this.holding = false;
    this.hold.release();
    if (this.state.phase !== "idle" || this.state.level !== 0) this.set({ phase: "idle", level: 0 });
  }
}
