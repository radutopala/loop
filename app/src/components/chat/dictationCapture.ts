import { DICTATION_CHUNK_SAMPLES, DICTATION_SAMPLE_RATE, PCM_WORKLET_NAME, pcmWorkletSource } from "./dictationPcm";

/** What the dictation controller needs from the microphone. */
export interface AudioCapture {
  /** Opens the microphone; chunks are 16 kHz mono Int16 LE buffers. */
  start(onChunk: (buf: ArrayBuffer) => void, onError: (err: unknown) => void): void;
  /** Hands over what's left (less than a chunk) and closes the microphone. */
  finish(): Promise<void>;
  /** Closes the microphone, dropping what's left. */
  stop(): void;
}

let workletURL: string | null = null;

function workletModuleURL(): string {
  if (!workletURL) workletURL = URL.createObjectURL(new Blob([pcmWorkletSource()], { type: "text/javascript" }));
  return workletURL;
}

const FLUSH_TIMEOUT_MS = 300;

/** Microphone capture through getUserMedia and an AudioWorklet that encodes
 *  to the daemon's PCM format. Calls made while the microphone is still
 *  opening take effect once it has. */
export class MicCapture implements AudioCapture {
  private stream: MediaStream | null = null;
  private ctx: AudioContext | null = null;
  private node: AudioWorkletNode | null = null;
  private closed = false;
  private opened: Promise<void> = Promise.resolve();
  private onChunk: (buf: ArrayBuffer) => void = () => {};
  private flushed: (() => void) | null = null;

  start(onChunk: (buf: ArrayBuffer) => void, onError: (err: unknown) => void): void {
    this.onChunk = onChunk;
    this.opened = this.open().catch((err) => {
      this.release();
      if (!this.closed) onError(err);
    });
  }

  private async open(): Promise<void> {
    const stream = await navigator.mediaDevices.getUserMedia({
      audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true },
    });
    this.stream = stream;
    if (this.closed) return this.release();
    const ctx = new AudioContext();
    this.ctx = ctx;
    await ctx.audioWorklet.addModule(workletModuleURL());
    if (this.closed) return this.release();
    const node = new AudioWorkletNode(ctx, PCM_WORKLET_NAME, {
      numberOfInputs: 1,
      numberOfOutputs: 0,
      channelCount: 1,
      channelCountMode: "explicit",
      processorOptions: { outRate: DICTATION_SAMPLE_RATE, chunkSamples: DICTATION_CHUNK_SAMPLES },
    });
    this.node = node;
    node.port.onmessage = (e: MessageEvent<{ type: string; buf: ArrayBuffer | null }>) => {
      if (e.data.buf) this.onChunk(e.data.buf);
      if (e.data.type === "flushed") this.flushed?.();
    };
    ctx.createMediaStreamSource(stream).connect(node);
    if (ctx.state === "suspended") await ctx.resume();
  }

  async finish(): Promise<void> {
    await this.opened;
    const node = this.node;
    if (node && !this.closed) {
      await new Promise<void>((done) => {
        const timer = setTimeout(done, FLUSH_TIMEOUT_MS);
        this.flushed = () => {
          clearTimeout(timer);
          done();
        };
        node.port.postMessage("flush");
      });
    }
    this.stop();
  }

  stop(): void {
    this.closed = true;
    this.onChunk = () => {};
    this.release();
  }

  private release(): void {
    this.flushed = null;
    this.node?.port.close();
    this.node?.disconnect();
    this.node = null;
    for (const t of this.stream?.getTracks() ?? []) t.stop();
    this.stream = null;
    void this.ctx?.close().catch(() => {});
    this.ctx = null;
  }
}
