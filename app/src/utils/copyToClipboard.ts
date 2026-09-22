/** The slice of navigator.clipboard this module uses. */
export type ClipboardWriter = {
  write?: (items: ClipboardItem[]) => Promise<void>;
  writeText: (text: string) => Promise<void>;
};

/** Builds a clipboard item from a mime -> payload map (ClipboardItem in the browser). */
export type ItemFactory = (data: Record<string, Blob>) => ClipboardItem;

/**
 * Put `text` on the clipboard, and when `html` is given, put both flavors on
 * it at once — the same pair a browser writes when you select a rendered table
 * and hit copy. A target that understands rich text (Slack, a spreadsheet, a
 * doc) takes the text/html copy and rebuilds the table; a plain one takes
 * `text`.
 *
 * The clipboard and item factory are injectable so this is testable without a
 * DOM; they default to the browser's. Falls back to a plain-text write when
 * the browser has no ClipboardItem or refuses the rich write, so a copy never
 * silently does nothing.
 */
export async function writeClipboard(
  text: string,
  html?: string,
  clipboard: ClipboardWriter | undefined = defaultClipboard(),
  makeItem: ItemFactory | undefined = defaultItemFactory(),
): Promise<void> {
  if (!clipboard) throw new Error("clipboard unavailable");
  if (html && clipboard.write && makeItem) {
    try {
      await clipboard.write([makeItem({ "text/plain": new Blob([text], { type: "text/plain" }), "text/html": new Blob([html], { type: "text/html" }) })]);
      return;
    } catch {
      // Rich write refused — fall through and at least copy the text.
    }
  }
  await clipboard.writeText(text);
}

function defaultClipboard(): ClipboardWriter | undefined {
  return typeof navigator === "undefined" ? undefined : navigator.clipboard;
}

function defaultItemFactory(): ItemFactory | undefined {
  if (typeof ClipboardItem === "undefined") return undefined;
  return (data) => new ClipboardItem(data);
}
