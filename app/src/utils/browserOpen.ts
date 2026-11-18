export type BrowserMode = "docker" | "host";

/** The pane each browser mode shows in. */
export const BROWSER_PANEL = { docker: "docker-browser", host: "host-browser" } as const;

/** The event a browser pane listens for to open the links waiting for it. */
export const BROWSER_OPEN_EVENT = "loop:browser-open";

export interface BrowserOpenDetail {
  channelId: string;
  mode: BrowserMode;
}

/**
 * Links waiting to open in a channel's browser pane. A pane the request
 * opens isn't mounted yet, let alone running a browser, so the link waits
 * here until the pane takes it.
 */
export class BrowserOpenQueue {
  private pending = new Map<string, string[]>();

  /** Queues url for the channel's mode pane, then asks for the pane and wakes it. */
  request(target: EventTarget, channelId: string, mode: BrowserMode, url: string): void {
    const key = `${channelId}:${mode}`;
    this.pending.set(key, [...(this.pending.get(key) ?? []), url]);
    target.dispatchEvent(new CustomEvent("loop:open-panel", { detail: { channelId, panel: BROWSER_PANEL[mode], anchorPanel: "chat" } }));
    target.dispatchEvent(new CustomEvent<BrowserOpenDetail>(BROWSER_OPEN_EVENT, { detail: { channelId, mode } }));
  }

  /** The links waiting for the channel's mode pane, which no longer wait. */
  take(channelId: string, mode: BrowserMode): string[] {
    const key = `${channelId}:${mode}`;
    const urls = this.pending.get(key) ?? [];
    this.pending.delete(key);
    return urls;
  }
}

/** The app's one queue, shared by chat bubbles and browser panes. */
export const browserOpenQueue = new BrowserOpenQueue();
