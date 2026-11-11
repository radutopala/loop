/** The parts of a WebSocket the dictation stream uses. */
export interface SocketLike {
  readonly readyState: number;
  binaryType: string;
  onopen: ((ev: any) => void) | null;
  onmessage: ((ev: any) => void) | null;
  onclose: ((ev: any) => void) | null;
  onerror: ((ev: any) => void) | null;
  send(data: string | ArrayBuffer): void;
  close(code?: number, reason?: string): void;
}

const OPEN = 1;

export interface DictationStreamHandlers {
  onReady?: () => void;
  onPartial?: (committed: string, tentative: string) => void;
  onFinal?: (text: string) => void;
  onError?: (code: string, message: string) => void;
  /** The stream is over: after a final, an error, a cancel, or the socket dropping. */
  onEnd?: () => void;
}

/**
 * One dictation session over the daemon's stream socket. Audio and a stop
 * given before the socket opens wait in order and go out once it does (the
 * daemon itself buffers what comes before its "ready"). After stop the
 * stream waits for "final"; a socket that closes first counts as an error.
 * Cancel drops everything and reports nothing more but the end.
 */
export class DictationStream {
  private queue: Array<string | ArrayBuffer> = [];
  private stopped = false;
  private ended = false;

  constructor(
    private readonly ws: SocketLike,
    private readonly h: DictationStreamHandlers,
  ) {
    ws.binaryType = "arraybuffer";
    ws.onopen = () => this.flushQueue();
    ws.onmessage = (ev) => this.message(ev.data);
    ws.onerror = () => {};
    ws.onclose = () => {
      if (this.ended) return;
      this.fail("closed", this.stopped ? "The dictation connection closed before the text arrived." : "The dictation connection closed.");
    };
  }

  /** Whether the stream has ended. */
  get done(): boolean {
    return this.ended;
  }

  sendAudio(buf: ArrayBuffer): void {
    if (this.ended || this.stopped) return;
    this.send(buf);
  }

  /** The key was released: ask for the final text. */
  stop(): void {
    if (this.ended || this.stopped) return;
    this.stopped = true;
    this.send(JSON.stringify({ type: "stop" }));
  }

  /** Drop the session without a final. */
  cancel(): void {
    if (this.ended) return;
    if (this.ws.readyState === OPEN) this.ws.send(JSON.stringify({ type: "cancel" }));
    this.queue = [];
    this.end();
    this.ws.close();
  }

  private send(data: string | ArrayBuffer): void {
    if (this.ws.readyState === OPEN && this.queue.length === 0) this.ws.send(data);
    else this.queue.push(data);
  }

  private flushQueue(): void {
    if (this.ended) return;
    const q = this.queue;
    this.queue = [];
    for (const d of q) this.ws.send(d);
  }

  private message(data: unknown): void {
    if (this.ended || typeof data !== "string") return;
    let msg: { type?: string; committed?: string; tentative?: string; text?: string; code?: string; message?: string };
    try {
      msg = JSON.parse(data);
    } catch {
      return;
    }
    switch (msg.type) {
      case "ready":
        this.h.onReady?.();
        break;
      case "partial":
        this.h.onPartial?.(msg.committed ?? "", msg.tentative ?? "");
        break;
      case "final":
        this.h.onFinal?.(msg.text ?? "");
        this.end();
        this.ws.close();
        break;
      case "error":
        this.fail(msg.code || "error", msg.message || "Dictation failed.");
        this.ws.close();
        break;
    }
  }

  private fail(code: string, message: string): void {
    if (this.ended) return;
    this.h.onError?.(code, message);
    this.end();
  }

  private end(): void {
    if (this.ended) return;
    this.ended = true;
    this.h.onEnd?.();
  }
}
