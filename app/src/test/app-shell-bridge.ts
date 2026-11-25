import { fireEvent } from "@testing-library/react";
import { vi } from "vitest";
import { BRIDGE_INIT } from "../plugins/bridge";

/** The page's end of a plugin frame's bridge, as a sandboxed page holds it. */
export interface PageEnd {
  /** Calls method and resolves with the host's JSON-RPC reply. */
  call: (method: string, params?: unknown) => Promise<{ result?: unknown; error?: { code: number; message: string } }>;
  /** The notifications (calls without an id) the host sent, in order. */
  notifications: { method: string; params: unknown }[];
  /** Resolves once a notification of method arrives. */
  next: (method: string) => Promise<unknown>;
  close: () => void;
}

/** Loads frame and returns the page's end of the bridge the host connects
 *  on load: the port the host transfers in its init message. */
export function loadFrame(frame: HTMLIFrameElement): PageEnd {
  const win = frame.contentWindow!;
  const post = vi.spyOn(win, "postMessage").mockImplementation(() => {});
  fireEvent.load(frame);
  // The host calls the (message, targetOrigin, transfer) form.
  const call = post.mock.calls.at(-1) as unknown[];
  post.mockRestore();
  if ((call[0] as { type?: string }).type !== BRIDGE_INIT || call[1] !== "*") throw new Error("the host didn't send the bridge's init message");
  const port = (call[2] as MessagePort[])[0]!;
  const notifications: PageEnd["notifications"] = [];
  const pending = new Map<number, (reply: never) => void>();
  const waiting: { method: string; resolve: (p: unknown) => void }[] = [];
  port.onmessage = (ev: MessageEvent) => {
    const msg = ev.data as { id?: number; method?: string; params?: unknown };
    if (msg.id !== undefined) {
      pending.get(msg.id)?.(msg as never);
      pending.delete(msg.id);
      return;
    }
    notifications.push({ method: msg.method!, params: msg.params });
    for (const w of waiting.splice(0)) {
      if (w.method === msg.method) w.resolve(msg.params);
      else waiting.push(w);
    }
  };
  let id = 0;
  return {
    call: (method, params = {}) =>
      new Promise((resolve) => {
        const n = ++id;
        pending.set(n, resolve);
        port.postMessage({ jsonrpc: "2.0", id: n, method, params });
      }),
    notifications,
    next: (method) => new Promise((resolve) => waiting.push({ method, resolve })),
    close: () => port.close(),
  };
}
