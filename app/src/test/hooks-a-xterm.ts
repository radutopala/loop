/** Stand-ins for xterm.js and its addons, which need a real canvas and
 *  layout. Each records what the hook did to it and lets a test fire the
 *  terminal's events. */

type Disposable = { dispose: () => void };

/** One line of a fake terminal buffer. */
export class FakeLine {
  constructor(private readonly text: string) {}
  translateToString(_trim: boolean): string {
    return this.text;
  }
}

export interface FakeCore {
  viewport?: object;
  _charSizeService?: { width?: number; height?: number };
}

export class FakeTerminal {
  options: Record<string, unknown>;
  cols = 80;
  rows = 24;
  addons: unknown[] = [];
  written: (Uint8Array | string)[] = [];
  scrolledToBottom = 0;
  cleared = 0;
  resets = 0;
  disposed = false;
  refreshed: [number, number][] = [];
  selection = "";
  lines: (FakeLine | undefined)[] = [];
  buffer = { active: { viewportY: 0, getLine: (r: number) => this.lines[r] } };
  _core: FakeCore | undefined = { viewport: {}, _charSizeService: { width: 10, height: 20 } };
  /** Whether open() renders xterm's .xterm and .xterm-screen elements. */
  renderElements = true;
  keyHandler: ((e: KeyboardEvent) => boolean) | null = null;
  dataHandler: ((data: string) => void) | null = null;
  resizeHandler: ((size: { cols: number; rows: number }) => void) | null = null;

  constructor(options: Record<string, unknown>) {
    this.options = { ...options };
  }

  loadAddon(addon: unknown): void {
    this.addons.push(addon);
  }

  open(el: HTMLElement): void {
    if (!this.renderElements) return;
    const root = document.createElement("div");
    root.className = "xterm";
    const screen = document.createElement("div");
    screen.className = "xterm-screen";
    root.appendChild(screen);
    el.appendChild(root);
  }

  write(data: Uint8Array | string, cb?: () => void): void {
    this.written.push(data);
    cb?.();
  }

  scrollToBottom(): void {
    this.scrolledToBottom++;
  }

  clear(): void {
    this.cleared++;
  }

  reset(): void {
    this.resets++;
  }

  dispose(): void {
    this.disposed = true;
  }

  refresh(start: number, end: number): void {
    this.refreshed.push([start, end]);
  }

  getSelection(): string {
    return this.selection;
  }

  attachCustomKeyEventHandler(fn: (e: KeyboardEvent) => boolean): void {
    this.keyHandler = fn;
  }

  onData(fn: (data: string) => void): Disposable {
    this.dataHandler = fn;
    return { dispose: () => {} };
  }

  onResize(fn: (size: { cols: number; rows: number }) => void): Disposable {
    this.resizeHandler = fn;
    return { dispose: () => {} };
  }
}

export class FakeFitAddon {
  fits = 0;
  fit(): void {
    this.fits++;
  }
}

export class FakeWebLinksAddon {
  constructor(readonly onClick: (event: MouseEvent, url: string) => void) {}
}

/** A ResizeObserver stand-in a test triggers by hand. */
export class FakeResizeObserver {
  observed: Element[] = [];
  disconnected = false;
  constructor(
    registry: FakeResizeObserver[],
    readonly callback: (entries: { contentRect: { width: number; height: number } }[]) => void,
  ) {
    registry.push(this);
  }
  observe(el: Element): void {
    this.observed.push(el);
  }
  disconnect(): void {
    this.disconnected = true;
  }
  /** The observed element changed size; null fires with no entries. */
  resize(size: { width: number; height: number } | null): void {
    this.callback(size ? [{ contentRect: size }] : []);
  }
}

/** Builds a ResizeObserver constructor whose observers land in the returned list. */
export function fakeResizeObserverClass(): { observers: FakeResizeObserver[]; ResizeObserver: typeof ResizeObserver } {
  const observers: FakeResizeObserver[] = [];
  class Bound extends FakeResizeObserver {
    constructor(cb: (entries: { contentRect: { width: number; height: number } }[]) => void) {
      super(observers, cb);
    }
  }
  return { observers, ResizeObserver: Bound as unknown as typeof ResizeObserver };
}
