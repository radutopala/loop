import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { DEFAULT_EDITOR_CONFIG, fetchEditorConfig, grepFiles, saveFileContent } from "./files";

let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.stubGlobal("window", { location: { hash: "" }, dispatchEvent: () => true });
  vi.stubGlobal("sessionStorage", { getItem: () => null, setItem: () => {} });
  fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

const url = () => String(fetchMock.mock.calls[0]![0]);

describe("saveFileContent", () => {
  it("saves without asking to format", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await expect(saveFileContent("ch", "a.go", "x", 2)).resolves.toEqual({});
    expect(url()).toBe("http://localhost:8222/api/channels/ch/file?path=a.go&root=2");
    expect(fetchMock.mock.calls[0]![1]).toMatchObject({ method: "PUT", body: "x" });
  });

  it.each([
    ["formatted", JSON.stringify({ content: "y\n" }), { content: "y\n", formatError: undefined }],
    ["format failed", JSON.stringify({ format_error: "gofmt failed: bad" }), { content: undefined, formatError: "gofmt failed: bad" }],
    ["empty format error", JSON.stringify({ format_error: "" }), { content: undefined, formatError: undefined }],
    ["no body", "", { content: undefined, formatError: undefined }],
  ])("asks to format: %s", async (_name, body, want) => {
    fetchMock.mockResolvedValue(new Response(body, { status: 200 }));
    await expect(saveFileContent("ch", "a.go", "x", undefined, true)).resolves.toEqual(want);
    expect(url()).toBe("http://localhost:8222/api/channels/ch/file?path=a.go&format=1");
  });

  it("throws when the save fails", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 500, statusText: "boom" }));
    await expect(saveFileContent("ch", "a.go", "x", 0, true)).rejects.toThrow("Failed to save file: boom");
  });
});

describe("fetchEditorConfig", () => {
  it("fills what the daemon leaves out with the defaults", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ follow_active_file: false, follow_active_file_from: "project" })));
    await expect(fetchEditorConfig("ch")).resolves.toEqual({ ...DEFAULT_EDITOR_CONFIG, follow_active_file: false, follow_active_file_from: "project" });
    expect(url()).toBe("http://localhost:8222/api/channels/ch/editor-config");
  });

  it("throws on a failed request", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 404, statusText: "Not Found" }));
    await expect(fetchEditorConfig("ch")).rejects.toThrow("Failed to fetch editor config: Not Found");
  });
});

describe("grepFiles", () => {
  it("passes the options and fills missing fields", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({})));
    await expect(grepFiles("ch", "a b", { regex: true, caseSensitive: true })).resolves.toEqual({ files: [], truncated: false, engine: "" });
    expect(url()).toBe("http://localhost:8222/api/channels/ch/files/grep?q=a+b&regex=1&case=1");
  });

  it("returns the results", async () => {
    const files = [{ root_index: 0, rel_path: "a.go", matches: [{ line: 1, column: 2, length: 3, text: "abc" }] }];
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ files, truncated: true, engine: "ripgrep" })));
    await expect(grepFiles("ch", "ab")).resolves.toEqual({ files, truncated: true, engine: "ripgrep" });
    expect(url()).toBe("http://localhost:8222/api/channels/ch/files/grep?q=ab");
  });

  it.each([
    ["the server's reason", "invalid regex: missing )\n", "invalid regex: missing )"],
    ["the status text without one", "", "Search failed: Bad Request"],
  ])("throws with %s", async (_name, body, want) => {
    fetchMock.mockResolvedValue(new Response(body, { status: 400, statusText: "Bad Request" }));
    await expect(grepFiles("ch", "(ab", { regex: true })).rejects.toThrow(want);
  });
});
