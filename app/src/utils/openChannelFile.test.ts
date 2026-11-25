// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { checkFilesExist } from "../api/files";
import type { FileLinkOpenDetail } from "../components/chat/FileLink";
import { openChannelFile } from "./openChannelFile";

vi.mock("../api/files", () => ({ checkFilesExist: vi.fn() }));

const exists = vi.mocked(checkFilesExist);

describe("openChannelFile", () => {
  const opened: FileLinkOpenDetail[] = [];
  const onOpen = (e: Event) => opened.push((e as CustomEvent<FileLinkOpenDetail>).detail);
  beforeEach(() => {
    opened.length = 0;
    exists.mockReset();
    window.addEventListener("loop:open-file", onOpen);
  });
  afterEach(() => {
    window.removeEventListener("loop:open-file", onOpen);
  });

  it("opens a found file at the line and returns where it is", async () => {
    exists.mockResolvedValue([{ path: "a.ts", exists: true, root_index: 1, rel_path: "src/a.ts" }]);
    await expect(openChannelFile("ch-1", "a.ts", 12)).resolves.toEqual({ rootIndex: 1, relPath: "src/a.ts" });
    expect(exists).toHaveBeenCalledWith("ch-1", ["a.ts"]);
    expect(opened).toEqual([{ channelId: "ch-1", target: { rootIndex: 1, relPath: "src/a.ts" }, line: 12 }]);
  });

  it("opens with no line and pinned when asked", async () => {
    exists.mockResolvedValue([{ path: "a.ts", exists: true, root_index: 0, rel_path: "a.ts" }]);
    await openChannelFile("ch-1", "a.ts", undefined, true);
    expect(opened).toEqual([{ channelId: "ch-1", target: { rootIndex: 0, relPath: "a.ts" }, line: null, pin: true }]);
  });

  it.each([
    ["no result", []],
    ["a missing file", [{ path: "a.ts", exists: false }]],
    ["no root index", [{ path: "a.ts", exists: true, rel_path: "a.ts" }]],
    ["no relative path", [{ path: "a.ts", exists: true, root_index: 0 }]],
  ])("rejects on %s", async (_name, results) => {
    exists.mockResolvedValue(results);
    await expect(openChannelFile("ch-1", "a.ts")).rejects.toThrow('no file "a.ts" in the channel\'s roots');
    expect(opened).toEqual([]);
  });
});
