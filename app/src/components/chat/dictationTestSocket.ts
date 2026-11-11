import type { SocketLike } from "./dictationStream";

/** A WebSocket stand-in for the dictation tests: it records what was sent
 *  and lets a test open it, push server frames and close it. */
export class FakeSocket implements SocketLike {
  readyState = 0;
  binaryType = "blob";
  onopen: ((ev: any) => void) | null = null;
  onmessage: ((ev: any) => void) | null = null;
  onclose: ((ev: any) => void) | null = null;
  onerror: ((ev: any) => void) | null = null;
  sent: Array<string | ArrayBuffer> = [];
  closed = false;

  send(data: string | ArrayBuffer): void {
    if (this.readyState !== 1) throw new Error("send before open");
    this.sent.push(data);
  }

  close(): void {
    if (this.closed) return;
    this.closed = true;
    this.readyState = 3;
    this.onclose?.({});
  }

  open(): void {
    this.readyState = 1;
    this.onopen?.({});
  }

  server(msg: object): void {
    this.onmessage?.({ data: JSON.stringify(msg) });
  }

  /** The text frames sent, parsed. */
  get controls(): unknown[] {
    return this.sent.filter((d) => typeof d === "string").map((d) => JSON.parse(d as string));
  }

  /** The binary frames sent. */
  get audio(): ArrayBuffer[] {
    return this.sent.filter((d) => typeof d !== "string") as ArrayBuffer[];
  }
}
