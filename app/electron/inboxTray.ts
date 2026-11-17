// The menu bar inbox: a tray icon showing how many things wait on you, and a
// small window under it listing them. The window's page is the app's, told
// apart by the query below.

/** The query string that puts the page in the inbox window's mode. */
export const INBOX_QUERY = "window=inbox";

/** The inbox window's size when it opens. */
export const INBOX_SIZE = { width: 380, height: 520 };

/** How often the icon switches while it flashes. */
export const FLASH_MS = 600;

export interface Rect {
  x: number;
  y: number;
  width: number;
  height: number;
}

/**
 * Where the inbox opens: centred under the tray icon, kept inside the
 * display's work area, a few points below the menu bar.
 */
export function inboxBounds(tray: Rect, size: { width: number; height: number }, workArea: Rect): Rect {
  const gap = 4;
  const maxX = workArea.x + workArea.width - size.width;
  const x = Math.round(Math.min(Math.max(tray.x + tray.width / 2 - size.width / 2, workArea.x), maxX));
  const y = Math.round(Math.max(tray.y + tray.height, workArea.y) + gap);
  const height = Math.min(size.height, workArea.y + workArea.height - y);
  return { x, y, width: size.width, height };
}

/** The text beside the tray icon: the count, or nothing when it's 0. */
export function trayTitle(count: number): string {
  return count > 0 ? String(count) : "";
}

/** What the inbox page tells the tray. */
export interface InboxReport {
  /** How many items wait on you. */
  count: number;
  /** Whether an item holding an agent hasn't been seen in the inbox yet. */
  flash: boolean;
}

/** The report in msg, or null when it isn't one. */
export function parseInboxReport(msg: unknown): InboxReport | null {
  if (!msg || typeof msg !== "object") return null;
  const { count, flash } = msg as Record<string, unknown>;
  if (typeof count !== "number" || !Number.isFinite(count) || count < 0 || typeof flash !== "boolean") return null;
  return { count: Math.floor(count), flash };
}

/** Where the inbox asks to be moved, in screen points. */
export interface InboxPosition {
  x: number;
  y: number;
}

/** The inbox's move request, or null when it isn't one. */
export function parseInboxMove(msg: unknown): InboxPosition | null {
  if (!msg || typeof msg !== "object") return null;
  const { x, y } = msg as Record<string, unknown>;
  if (typeof x !== "number" || typeof y !== "number" || !Number.isFinite(x) || !Number.isFinite(y)) return null;
  return { x: Math.round(x), y: Math.round(y) };
}

/** The timer functions the flasher runs on. */
export interface Timers {
  setInterval: (fn: () => void, ms: number) => unknown;
  clearInterval: (id: unknown) => void;
}

/**
 * Flasher switches the tray icon between its plain and alert images while
 * flashing is on, and leaves it plain when off.
 */
export class Flasher {
  private timer: unknown = null;
  private alert = false;

  constructor(
    private readonly show: (alert: boolean) => void,
    private readonly timers: Timers,
  ) {}

  /** Starts or stops the flashing; does nothing when it's already so. */
  set(on: boolean): void {
    if (on === (this.timer !== null)) return;
    if (on) {
      this.alert = true;
      this.show(true);
      this.timer = this.timers.setInterval(() => {
        this.alert = !this.alert;
        this.show(this.alert);
      }, FLASH_MS);
      return;
    }
    this.timers.clearInterval(this.timer);
    this.timer = null;
    this.alert = false;
    this.show(false);
  }

  /** Whether it's flashing. */
  get flashing(): boolean {
    return this.timer !== null;
  }
}
