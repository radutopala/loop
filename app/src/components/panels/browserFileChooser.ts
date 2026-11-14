/**
 * A file chooser a page's <input type=file> opened. Chrome runs without a
 * screen, so the backend intercepts it and the pane offers the pick instead.
 */
export interface PageFileChooser {
  target_id: string;
  multiple?: boolean;
}

/** The file chooser each tab has open, by target ID. */
export type PageFileChoosers = Readonly<Record<string, PageFileChooser>>;

/** One picked file as the backend takes it: its bytes base64-encoded. */
export interface ChosenFile {
  name: string;
  type: string;
  data: string;
}

/** What one pick may send the page; the backend refuses more. */
export const FILE_CHOOSER_MAX_BYTES = 20 << 20;

export function withFileChooser(choosers: PageFileChoosers, fc: PageFileChooser): PageFileChoosers {
  return { ...choosers, [fc.target_id]: fc };
}

/** Drops targetId's chooser; returns choosers itself when it had none. */
export function withoutFileChooser(choosers: PageFileChoosers, targetId: string): PageFileChoosers {
  if (!(targetId in choosers)) return choosers;
  const { [targetId]: _, ...rest } = choosers;
  return rest;
}

/** Why files can't go to the page, or null when they can. */
export function fileChooserProblem(files: readonly Pick<File, "size">[]): string | null {
  const total = files.reduce((sum, f) => sum + f.size, 0);
  return total > FILE_CHOOSER_MAX_BYTES ? `The files are over the ${FILE_CHOOSER_MAX_BYTES >> 20} MB limit.` : null;
}

/**
 * base64 of bytes. btoa takes a binary string, which is built in chunks: one
 * String.fromCharCode call over a whole file overflows the call stack.
 */
export function toBase64(bytes: Uint8Array): string {
  const chunk = 0x8000;
  let bin = "";
  for (let i = 0; i < bytes.length; i += chunk) {
    bin += String.fromCharCode(...bytes.subarray(i, i + chunk));
  }
  return btoa(bin);
}

/** Reads the picked files into what the backend takes. */
export async function readChosenFiles(files: readonly File[]): Promise<ChosenFile[]> {
  return Promise.all(
    files.map(async (f) => ({
      name: f.name,
      type: f.type,
      data: toBase64(new Uint8Array(await f.arrayBuffer())),
    })),
  );
}
