import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { API, type FetchMock, fail, json, sent, stubFetch } from "../test/hooks-c-api-fetch";
import { fetchMemoryFileContent, fetchMemoryFiles, saveMemoryFileContent, searchMemoryFiles } from "./memory";

let fetchMock: FetchMock;

beforeEach(() => {
  fetchMock = stubFetch();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("memory", () => {
  it("lists the memory files", async () => {
    fetchMock.mockResolvedValue(json({ files: [{ file_path: "a", dir_path: "d" }] }));
    await expect(fetchMemoryFiles("c 1")).resolves.toEqual([{ file_path: "a", dir_path: "d" }]);
    expect(sent(fetchMock).url).toBe(`${API}/api/memory/files?channel_id=c%201`);
  });

  it("reads a file", async () => {
    fetchMock.mockResolvedValue(new Response("# notes"));
    await expect(fetchMemoryFileContent("c1", "a/b.md")).resolves.toBe("# notes");
    expect(sent(fetchMock).url).toBe(`${API}/api/memory/file?channel_id=c1&path=a%2Fb.md`);
  });

  it("searches the files", async () => {
    fetchMock.mockResolvedValue(json({ files: [] }));
    await expect(searchMemoryFiles("c1", "x y")).resolves.toEqual([]);
    expect(sent(fetchMock).url).toBe(`${API}/api/memory/files/search?channel_id=c1&q=x+y`);
  });

  it("saves a file", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await saveMemoryFileContent("c1", "a.md", "body");
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/memory/file?channel_id=c1&path=a.md`, method: "PUT", body: "body" });
  });

  it.each([
    ["fetchMemoryFiles", () => fetchMemoryFiles("c1"), "Failed to fetch memory files: boom"],
    ["fetchMemoryFileContent", () => fetchMemoryFileContent("c1", "a"), "Failed to fetch memory file: boom"],
    ["searchMemoryFiles", () => searchMemoryFiles("c1", "q"), "Failed to search memory files: boom"],
    ["saveMemoryFileContent", () => saveMemoryFileContent("c1", "a", ""), "Failed to save memory file: boom"],
  ])("%s throws on an error status", async (_name, run, msg) => {
    fetchMock.mockResolvedValue(fail(500, "boom"));
    await expect(run()).rejects.toThrow(msg);
  });
});
