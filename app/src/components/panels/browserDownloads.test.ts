import { describe, expect, it } from "vitest";
import { downloadedFileDetail, downloadLabel, formatBytes, MAX_DOWNLOAD_TOASTS, type PageDownload, withDownload, withoutDownload } from "./browserDownloads";

const started = (guid: string, filename = `${guid}.pdf`): PageDownload => ({ guid, url: `https://a.com/${guid}`, filename, state: "started" });

describe("withDownload", () => {
  it("adds a new download and completes it in place, taking the saved name", () => {
    let list = withDownload([], started("g1", "r.pdf"));
    list = withDownload(list, started("g2"));
    list = withDownload(list, { guid: "g1", filename: "r (1).pdf", state: "completed" });
    expect(list).toEqual([{ guid: "g1", url: "https://a.com/g1", filename: "r (1).pdf", state: "completed" }, started("g2")]);
  });

  it("keeps the known name when the update has none", () => {
    const list = withDownload([started("g1", "r.pdf")], { guid: "g1", state: "canceled" });
    expect(list[0]).toMatchObject({ filename: "r.pdf", state: "canceled" });
  });

  it("does not take a finished download back to started", () => {
    const list = withDownload([{ guid: "g1", filename: "r.pdf", state: "completed" }], started("g1", "r.pdf"));
    expect(list[0]?.state).toBe("completed");
  });

  it("keeps only the latest few", () => {
    let list: PageDownload[] = [];
    for (let i = 0; i < MAX_DOWNLOAD_TOASTS + 2; i++) list = withDownload(list, started(`g${i}`));
    expect(list.map((d) => d.guid)).toEqual(["g2", "g3", "g4"]);
  });
});

describe("withoutDownload", () => {
  it("drops one toast and returns the same list when there was none", () => {
    const list = [started("g1"), started("g2")];
    expect(withoutDownload(list, "g1").map((d) => d.guid)).toEqual(["g2"]);
    expect(withoutDownload(list, "g9")).toBe(list);
  });
});

describe("downloadLabel", () => {
  it.each<[PageDownload, string]>([
    [started("g1", "r.pdf"), "Downloading r.pdf…"],
    [{ guid: "g1", filename: "r.pdf", state: "completed" }, "Downloaded r.pdf"],
    [{ guid: "g1", state: "canceled" }, "Download canceled: a file"],
  ])("%j", (d, label) => {
    expect(downloadLabel(d)).toBe(label);
  });
});

describe("formatBytes", () => {
  it.each<[number, string]>([
    [0, "0 B"],
    [1023, "1023 B"],
    [1024, "1.0 KB"],
    [1536, "1.5 KB"],
    [5 * 1024 * 1024, "5.0 MB"],
    [3 * 1024 ** 3, "3.0 GB"],
    [2048 * 1024 ** 3, "2048.0 GB"],
  ])("%d", (n, want) => {
    expect(formatBytes(n)).toBe(want);
  });
});

describe("downloadedFileDetail", () => {
  const f = { name: "a.pdf", path: "/d/a.pdf", size: 2048, mod_time: "2026-01-01T00:00:00Z" };
  it("gives a finished file's size", () => {
    expect(downloadedFileDetail(f)).toBe("2.0 KB");
  });
  it("says a partial file is still downloading", () => {
    expect(downloadedFileDetail({ ...f, partial: true })).toBe("downloading… 2.0 KB so far");
  });
});
