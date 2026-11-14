/** A file the page's browser is saving, as the backend reports it. */
export interface PageDownload {
  guid: string;
  url?: string;
  // The site's suggestion while it downloads, the saved name once completed.
  filename?: string;
  state: "started" | "completed" | "canceled";
}

/** How many download toasts the pane keeps at once; older ones drop off. */
export const MAX_DOWNLOAD_TOASTS = 3;

/**
 * withDownload folds d into the list by GUID. Events can arrive out of order,
 * so a finished download never goes back to started, and a later event
 * without a name or URL keeps the ones already known.
 */
export function withDownload(list: readonly PageDownload[], d: PageDownload): PageDownload[] {
  const i = list.findIndex((x) => x.guid === d.guid);
  if (i < 0) return [...list, d].slice(-MAX_DOWNLOAD_TOASTS);
  const prev = list[i]!;
  const merged: PageDownload = {
    guid: d.guid,
    url: d.url || prev.url,
    filename: d.filename || prev.filename,
    state: d.state === "started" && prev.state !== "started" ? prev.state : d.state,
  };
  return list.map((x, j) => (j === i ? merged : x));
}

/** Drops guid's toast; returns list itself when it had none. */
export function withoutDownload(list: readonly PageDownload[], guid: string): readonly PageDownload[] {
  return list.some((x) => x.guid === guid) ? list.filter((x) => x.guid !== guid) : list;
}

/** What the toast says. */
export function downloadLabel(d: PageDownload): string {
  const name = d.filename || "a file";
  switch (d.state) {
    case "completed":
      return `Downloaded ${name}`;
    case "canceled":
      return `Download canceled: ${name}`;
    default:
      return `Downloading ${name}…`;
  }
}

/** A file in the channel's downloads dir, as list_downloads reports it. */
export interface DownloadedFile {
  name: string;
  path: string;
  size: number;
  mod_time: string;
  // Chrome is still writing it.
  partial?: boolean;
}

/** The channel's downloads dir and what is in it, newest first. */
export interface DownloadList {
  dir: string;
  files: readonly DownloadedFile[];
}

/** formatBytes renders n as B, KB, MB or GB with one decimal past bytes. */
export function formatBytes(n: number): string {
  const units = ["KB", "MB", "GB"];
  if (n < 1024) return `${n} B`;
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(1)} ${units[i]}`;
}

/** What a row of the downloads list says after the name. */
export function downloadedFileDetail(f: DownloadedFile): string {
  return f.partial ? `downloading… ${formatBytes(f.size)} so far` : formatBytes(f.size);
}
