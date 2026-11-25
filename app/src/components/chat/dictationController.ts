import type { AudioCapture } from "./dictationCapture";
import { DICTATION_HOLD_CODE, type HoldKey, type HoldMode, type HoldTimers, HoldToTalk } from "./dictationHold";
import type { DictationAvailability } from "./dictationModel";
import { pcmLevel } from "./dictationPcm";
import { DictationStream, type SocketLike } from "./dictationStream";
import { type ComposerSnapshot, previewDictation, spliceDictation, stripSendPhrase } from "./dictationText";

/** The OS's answer on microphone access (macOS asks once per app). */
export type MicAccess = "granted" | "denied" | "not-determined" | "restricted" | "unknown";

export type DictationNotice = "needs_model" | "unavailable" | "mic_denied";

export interface DictationUiState {
  /** editing: an edit hold's instruction is being applied to the draft. */
  phase: "idle" | "recording" | "transcribing" | "editing";
  /** What the current or last hold does with the speech. */
  mode: HoldMode;
  /** An edit hold's instruction as heard so far; it never goes in the
   *  composer. */
  instruction: string;
  /** The last edit can be undone (Undo, or Cmd/Ctrl+Z in the composer). */
  undo: boolean;
  /** Input level, 0..1, while recording. */
  level: number;
  error: string | null;
  notice: DictationNotice | null;
}

export const IDLE_DICTATION: DictationUiState = { phase: "idle", mode: "dictate", instruction: "", undo: false, level: 0, error: null, notice: null };

/** A draft, its selection, and the spoken instruction to apply to it. */
export interface PromptEditRequest {
  draft: string;
  start: number;
  end: number;
  instruction: string;
}

/** The edited draft and the span in it the edit wrote. */
export interface PromptEditResult {
  text: string;
  from: number;
  to: number;
}

const MODIFIER_KEYS = new Set(["Shift", "Meta", "Control", "Alt", "AltGraph", "CapsLock"]);

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
  /** Phrases that, said last, send the message (dictation.send_phrases). */
  sendPhrases(): readonly string[];
  /** Sends the composer's message, once the final text is written. */
  send(): void;
  onState(s: DictationUiState): void;
  /** Applies an edit hold's instruction to the draft; without it there are
   *  no edit holds. */
  editPrompt?(req: PromptEditRequest, signal: AbortSignal): Promise<PromptEditResult>;
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
 * back as it was. The message is sent only when the dictation ends with a
 * send phrase, which is left out of it.
 *
 * An edit hold (Shift with the hold key, or the edit button) takes the
 * speech as an instruction instead: it shows above the composer as it's
 * heard, and on release editPrompt rewrites the draft (or the selection) as
 * it asks. The composer is read-only meanwhile, and Esc drops the edit. The
 * rewritten span is selected, and Undo or Cmd/Ctrl+Z puts the draft back
 * until the next key typed there.
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
  private mode: HoldMode = "dictate";
  /** The edit under way, to abort with Esc. */
  private editAbort: AbortController | null = null;
  /** The draft before the last edit, and the text the edit left. */
  private lastEdit: { before: ComposerSnapshot; after: string } | null = null;

  constructor(private readonly d: DictationDeps) {
    this.hold = new HoldToTalk(
      {
        canEdit: () => !!d.editPrompt && !this.editAbort,
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
    if (this.editAbort) {
      if (e.key !== "Escape") return false;
      this.cancelEdit();
      return true;
    }
    if (this.lastEdit && !this.hold.busy) {
      if ((e.metaKey || e.ctrlKey) && !e.shiftKey && e.key.toLowerCase() === "z") {
        this.undoEdit();
        return true;
      }
      if (e.code !== DICTATION_HOLD_CODE && !MODIFIER_KEYS.has(e.key)) this.clearUndo();
    }
    // Turned off in the config, the hold key types as usual.
    if (!this.hold.busy && this.d.availability() === "disabled") return false;
    return this.hold.keyDown(e);
  }

  /** Puts the draft back as it was before the last edit, unless it has
   *  changed since. */
  undoEdit(): void {
    const e = this.lastEdit;
    this.clearUndo();
    if (e && this.d.read().text === e.after) this.d.write(e.before.text, e.before.start, e.before.end);
  }

  private clearUndo(): void {
    if (!this.lastEdit) return;
    this.lastEdit = null;
    this.set({ undo: false });
  }

  private cancelEdit(): void {
    this.editAbort?.abort();
    this.editAbort = null;
    this.set({ phase: "idle", instruction: "" });
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
    this.editAbort?.abort();
    this.editAbort = null;
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
    if (this.editAbort) {
      this.abort();
      this.hold.release();
      return;
    }
    const a = this.d.availability();
    if (a !== "ready") {
      this.abort();
      this.hold.release();
      void this.refreshStatus().then(() => this.notReady(this.d.availability()));
      return;
    }
    this.holding = true;
    this.mode = this.hold.mode;
    this.clearUndo();
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
    this.set({ phase: "recording", mode: this.mode, instruction: "", level: 0, error: null, notice: null });
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
    // A hold under way always has its stream: reset clears both together.
    const stream = this.stream!;
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

  // The stream reports only while its session runs, and base is set for as
  // long as it does.
  private partial(committed: string, tentative: string): void {
    this.committed = committed;
    if (this.mode === "edit") {
      this.set({ instruction: previewDictation({ text: "", start: 0, end: 0 }, committed, tentative).text });
      return;
    }
    const p = previewDictation(this.base!, committed, tentative);
    this.d.write(p.text, p.tentativeFrom, p.to);
  }

  private final(text: string): void {
    const base = this.base!;
    if (this.mode === "edit") {
      void this.edit(base, text.trim());
      return;
    }
    const rest = stripSendPhrase(text, this.d.sendPhrases());
    const out = spliceDictation(base, rest ?? text);
    this.d.write(out.text, out.to, out.to);
    if (rest !== null) this.d.send();
  }

  private failed(code: string, message: string): void {
    // Keep what was already committed; only the unstable tail goes. An edit
    // hold wrote nothing.
    if (this.base && this.mode === "dictate") {
      const out = spliceDictation(this.base, this.committed);
      this.d.write(out.text, out.to, out.to);
    }
    if (code === "model_missing") this.set({ notice: "needs_model" });
    else if (code === "binary_missing") this.set({ notice: "unavailable" });
    else this.set({ error: message });
  }

  /** Applies an edit hold's instruction to the draft it began on. */
  private async edit(base: ComposerSnapshot, instruction: string): Promise<void> {
    if (!instruction) return;
    const editPrompt = this.d.editPrompt;
    if (!editPrompt) return;
    const abort = new AbortController();
    this.editAbort = abort;
    this.set({ phase: "editing", instruction, level: 0 });
    try {
      const res = await editPrompt({ draft: base.text, start: base.start, end: base.end, instruction }, abort.signal);
      if (this.editAbort !== abort) return;
      this.editAbort = null;
      if (this.d.read().text !== base.text) {
        this.set({ phase: "idle", error: "The draft changed meanwhile; the edit was dropped." });
        return;
      }
      this.d.write(res.text, res.from, res.to);
      this.lastEdit = { before: base, after: res.text };
      this.set({ phase: "idle", undo: true });
    } catch (err) {
      if (this.editAbort !== abort) return;
      this.editAbort = null;
      this.set({ phase: "idle", error: `Couldn't edit the prompt: ${err instanceof Error ? err.message : String(err)}` });
    }
  }

  /** Called only while a session runs, so base is set. */
  private restore(): void {
    const b = this.base!;
    this.d.write(b.text, b.start, b.end);
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
    if (this.editAbort) return;
    if (this.state.phase !== "idle" || this.state.level !== 0) this.set({ phase: "idle", level: 0 });
  }
}
