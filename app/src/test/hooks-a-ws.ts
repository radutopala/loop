/** A WebSocket stand-in that records what it was opened with and sent, and
 *  lets a test drive its open, message, error and close events. */
export class FakeWebSocket {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;

  readonly url: string;
  readonly protocols: string | string[] | undefined;
  binaryType = "blob";
  readyState = FakeWebSocket.CONNECTING;
  sent: unknown[] = [];
  closed = false;
  onopen: ((ev: Event) => void) | null = null;
  onmessage: ((ev: MessageEvent) => void) | null = null;
  onclose: ((ev: CloseEvent) => void) | null = null;
  onerror: ((ev: Event) => void) | null = null;

  constructor(registry: FakeWebSocket[], url: string, protocols?: string | string[]) {
    this.url = url;
    this.protocols = protocols;
    registry.push(this);
  }

  send(data: unknown): void {
    this.sent.push(data);
  }

  close(): void {
    this.closed = true;
    this.readyState = FakeWebSocket.CLOSED;
  }

  /** The server accepted the connection. */
  open(): void {
    this.readyState = FakeWebSocket.OPEN;
    this.onopen?.(new Event("open"));
  }

  /** A message arrived. */
  receive(data: unknown): void {
    this.onmessage?.(new MessageEvent("message", { data }));
  }

  /** The connection dropped. */
  drop(): void {
    this.readyState = FakeWebSocket.CLOSED;
    this.onclose?.(new Event("close") as CloseEvent);
  }

  /** The connection failed. */
  fail(): void {
    this.onerror?.(new Event("error"));
  }
}

/** Builds a WebSocket constructor whose sockets land in the returned list. */
export function fakeWebSocketClass(): { sockets: FakeWebSocket[]; WebSocket: typeof WebSocket } {
  const sockets: FakeWebSocket[] = [];
  class Bound extends FakeWebSocket {
    constructor(url: string, protocols?: string | string[]) {
      super(sockets, url, protocols);
    }
  }
  return { sockets, WebSocket: Bound as unknown as typeof WebSocket };
}
