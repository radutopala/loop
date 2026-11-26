import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { contentCapBase } from "./api";
import {
  buildFileUrl,
  buildRawFileBase,
  CopyTargetExistsError,
  checkFilesExist,
  copyFile,
  createDir,
  deleteFile,
  FileNotFoundError,
  fetchEditorConfig,
  fetchFileContent,
  fetchFiles,
  fetchRoots,
  grepFiles,
  isImagePath,
  isMediaPath,
  isPdfPath,
  isVideoPath,
  renameFile,
  saveFileContent,
  searchFiles,
  updateExtraDirs,
} from "./files";

// Content links are minted by the daemon; stand in a fixed base per scope.
vi.mock("./api", async (importOriginal) => {
  const mod = await importOriginal<typeof import("./api")>();
  return { ...mod, contentCapBase: vi.fn() };
});

const API = "http://localhost:8222";

let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.stubGlobal("window", { location: { hash: "" } });
  vi.stubGlobal("sessionStorage", { getItem: () => null, setItem: () => {} });
  fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);
  vi.mocked(contentCapBase).mockImplementation(async (scope) => `${API}/c/${"root" in scope ? scope.root : "x"}/`);
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

function respond(body: string | null, init: ResponseInit = {}) {
  fetchMock.mockImplementation(() => Promise.resolve(new Response(body, init)));
}

function json(value: unknown) {
  respond(JSON.stringify(value));
}

function sent(i = 0): { url: string; method?: string; body?: unknown } {
  const [url, init] = fetchMock.mock.calls[i]!;
  return { url: String(url), method: (init as RequestInit | undefined)?.method, body: (init as RequestInit | undefined)?.body };
}

const fail = (status = 500) => respond("", { status, statusText: "Bad" });

describe("fetchRoots", () => {
  it("returns the roots", async () => {
    json({ roots: [{ index: 0, path: "/p" }] });
    await expect(fetchRoots("ch1")).resolves.toEqual([{ index: 0, path: "/p" }]);
    expect(sent().url).toBe(`${API}/api/channels/ch1/roots`);
  });

  it("throws the daemon's message", async () => {
    respond("no channel", { status: 404 });
    await expect(fetchRoots("ch1")).rejects.toThrow("no channel");
  });
});

describe("updateExtraDirs", () => {
  const configUrl = `${API}/api/config/project?channel_id=ch1`;

  function saved(): unknown {
    const put = fetchMock.mock.calls.find(([, init]) => (init as RequestInit | undefined)?.method === "PUT")!;
    return JSON.parse(JSON.parse((put[1] as RequestInit).body as string).content);
  }

  function reply(get: Response) {
    fetchMock.mockImplementation((_url: string, init?: RequestInit) => Promise.resolve(init?.method === "PUT" ? new Response(JSON.stringify({ hash: "h2" })) : get.clone()));
  }

  it("merges the dirs into the existing project config", async () => {
    reply(new Response(JSON.stringify({ content: { model: "opus" }, raw: '{"model": "opus"}', hash: "h1" })));
    await updateExtraDirs("ch1", ["/a", "/b"]);
    expect(sent().url).toBe(configUrl);
    expect(sent(1)).toMatchObject({ url: configUrl, method: "PUT" });
    expect(JSON.parse(sent(1).body as string).base_hash).toBe("h1");
    expect(saved()).toEqual({ model: "opus", extra_dirs: ["/a", "/b"] });
  });

  it("drops the key when no dirs are left", async () => {
    reply(new Response(JSON.stringify({ content: { model: "opus", extra_dirs: ["/a"] } })));
    await updateExtraDirs("ch1", []);
    expect(saved()).toEqual({ model: "opus" });
  });

  it("starts from an empty config when there is none", async () => {
    reply(new Response(JSON.stringify({})));
    await updateExtraDirs("ch1", ["/a"]);
    expect(saved()).toEqual({ extra_dirs: ["/a"] });
  });

  it.each([
    ["can't be read", new Response("broken", { status: 500 }), "broken"],
    ["doesn't parse", new Response(JSON.stringify({ content: null, raw: "{broken" })), "doesn't parse"],
  ])("leaves a config that %s alone", async (_name, get, message) => {
    reply(get);
    await expect(updateExtraDirs("ch1", ["/a"])).rejects.toThrow(message);
    expect(fetchMock.mock.calls.some(([, init]) => (init as RequestInit | undefined)?.method === "PUT")).toBe(false);
  });
});

describe("fetchFiles", () => {
  it.each([
    [undefined, `${API}/api/channels/ch1/files?path=src`],
    [0, `${API}/api/channels/ch1/files?path=src`],
    [2, `${API}/api/channels/ch1/files?path=src&root=2`],
  ])("GETs root %s", async (root, url) => {
    json({ entries: [{ name: "a.ts" }] });
    await expect(fetchFiles("ch1", "src", root)).resolves.toEqual([{ name: "a.ts" }]);
    expect(sent().url).toBe(url);
  });

  it("throws with the status text", async () => {
    fail();
    await expect(fetchFiles("ch1", "")).rejects.toThrow("Failed to fetch files: Bad");
  });
});

describe("fetchFileContent", () => {
  it.each<[[number?, string?], string]>([
    [[], `${API}/api/channels/ch1/file?path=a.ts`],
    [[0, ""], `${API}/api/channels/ch1/file?path=a.ts`],
    [[3, "HEAD~1"], `${API}/api/channels/ch1/file?path=a.ts&root=3&ref=HEAD%7E1`],
  ])("GETs %o and returns the text", async (args, url) => {
    respond("hello");
    await expect(fetchFileContent("ch1", "a.ts", ...args)).resolves.toEqual({ content: "hello", binary: false });
    expect(sent().url).toBe(url);
  });

  it("reports a binary file without its content", async () => {
    respond("\u0000\u0001", { headers: { "X-File-Binary": "true" } });
    await expect(fetchFileContent("ch1", "a.bin")).resolves.toEqual({ content: "", binary: true });
  });

  it("throws FileNotFoundError on 404", async () => {
    fail(404);
    const err = await fetchFileContent("ch1", "a.ts").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(FileNotFoundError);
    expect((err as Error).message).toBe("Failed to fetch file: Bad");
  });

  it("throws a plain error on other failures", async () => {
    fail();
    const err = await fetchFileContent("ch1", "a.ts").catch((e: unknown) => e);
    expect(err).not.toBeInstanceOf(FileNotFoundError);
    expect((err as Error).message).toBe("Failed to fetch file: Bad");
  });
});

describe("media paths", () => {
  it.each([
    ["a.PNG", true, false, false],
    ["a.jpeg", true, false, false],
    ["clip.MOV", false, true, false],
    ["doc.Pdf", false, false, true],
    ["a.ts", false, false, false],
    ["Makefile", false, false, false],
  ])("%s", (path, image, video, pdf) => {
    expect(isImagePath(path)).toBe(image);
    expect(isVideoPath(path)).toBe(video);
    expect(isPdfPath(path)).toBe(pdf);
    expect(isMediaPath(path)).toBe(image || video || pdf);
  });
});

describe("content links", () => {
  it.each<[[string, number?, number?], string, number]>([
    [["dir/a b.png"], `${API}/c/0/dir/a%20b.png`, 0],
    [["a.png", 2, 99], `${API}/c/2/a.png?t=99`, 2],
    [["a.png", undefined, 0], `${API}/c/0/a.png?t=0`, 0],
  ])("buildFileUrl %o", async (args, want, root) => {
    await expect(buildFileUrl("ch1", ...args)).resolves.toBe(want);
    expect(contentCapBase).toHaveBeenCalledWith({ kind: "raw", channelId: "ch1", root });
  });

  it.each([
    ["docs/sub dir/a.md", undefined, `${API}/c/0/docs/sub%20dir/`],
    ["a.md", 1, `${API}/c/1/`],
  ])("buildRawFileBase %s", async (path, root, want) => {
    await expect(buildRawFileBase("ch1", path, root)).resolves.toBe(want);
  });
});

describe("saveFileContent", () => {
  it("keeps the root out of the query for the first root", async () => {
    respond(null, { status: 204 });
    await expect(saveFileContent("ch1", "a.ts", "x")).resolves.toEqual({});
    expect(sent().url).toBe(`${API}/api/channels/ch1/file?path=a.ts`);
  });

  it("returns no content when the formatted reply isn't JSON", async () => {
    respond("oops");
    await expect(saveFileContent("ch1", "a.ts", "x", 0, true)).resolves.toEqual({ content: undefined, formatError: undefined });
    expect(sent().url).toBe(`${API}/api/channels/ch1/file?path=a.ts&format=1`);
  });

  it("throws with the status text", async () => {
    fail();
    await expect(saveFileContent("ch1", "a.ts", "x")).rejects.toThrow("Failed to save file: Bad");
  });
});

describe("fetchEditorConfig", () => {
  it("throws with the status text", async () => {
    fail();
    await expect(fetchEditorConfig("ch1")).rejects.toThrow("Failed to fetch editor config: Bad");
  });
});

describe("deleteFile and createDir", () => {
  it.each([
    ["deleteFile", deleteFile, "file", "DELETE", "Failed to delete: Bad"],
    ["createDir", createDir, "dir", "POST", "Failed to create directory: Bad"],
  ] as const)("%s sends the path and root", async (_name, run, path, method, err) => {
    respond(null, { status: 204 });
    await run("ch1", "a/b");
    expect(sent()).toMatchObject({ url: `${API}/api/channels/ch1/${path}?path=a%2Fb`, method });
    await run("ch1", "a/b", 2);
    expect(sent(1).url).toBe(`${API}/api/channels/ch1/${path}?path=a%2Fb&root=2`);
    fail();
    await expect(run("ch1", "a")).rejects.toThrow(err);
  });
});

describe("renameFile", () => {
  it.each([
    [undefined, `${API}/api/channels/ch1/file/rename?path=a&to=b`],
    [1, `${API}/api/channels/ch1/file/rename?path=a&to=b&root=1`],
  ])("POSTs root %s", async (root, url) => {
    respond(null, { status: 204 });
    await renameFile("ch1", "a", "b", root);
    expect(sent()).toMatchObject({ url, method: "POST" });
  });

  it("says the target exists on 409", async () => {
    fail(409);
    await expect(renameFile("ch1", "a", "b")).rejects.toThrow("b already exists");
  });

  it("throws with the status text", async () => {
    fail();
    await expect(renameFile("ch1", "a", "b")).rejects.toThrow("Failed to rename: Bad");
  });
});

describe("copyFile", () => {
  it.each<[[number?, number?], string]>([
    [[], `${API}/api/channels/ch1/file/copy?path=a&to=b`],
    [[undefined, 0], `${API}/api/channels/ch1/file/copy?path=a&to=b`],
    [[2, 2], `${API}/api/channels/ch1/file/copy?path=a&to=b&root=2`],
    [[2, 0], `${API}/api/channels/ch1/file/copy?path=a&to=b&root=2&to_root=0`],
    [[undefined, 1], `${API}/api/channels/ch1/file/copy?path=a&to=b&to_root=1`],
  ])("POSTs %o", async (args, url) => {
    respond(null, { status: 204 });
    await copyFile("ch1", "a", "b", ...args);
    expect(sent()).toMatchObject({ url, method: "POST" });
  });

  it("throws CopyTargetExistsError on 409", async () => {
    fail(409);
    const err = await copyFile("ch1", "a", "b").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(CopyTargetExistsError);
    expect((err as Error).message).toBe("b already exists");
  });

  it("throws the daemon's message, or the status text", async () => {
    respond(" outside the root \n", { status: 400 });
    await expect(copyFile("ch1", "a", "b")).rejects.toThrow(/^outside the root$/);
    fail();
    await expect(copyFile("ch1", "a", "b")).rejects.toThrow("Failed to copy: Bad");
  });
});

describe("checkFilesExist", () => {
  it("skips the request for no paths", async () => {
    await expect(checkFilesExist("ch1", [])).resolves.toEqual([]);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("POSTs the paths and returns the results", async () => {
    json({ results: [{ path: "a", exists: true }] });
    await expect(checkFilesExist("ch1", ["a"])).resolves.toEqual([{ path: "a", exists: true }]);
    expect(sent()).toEqual({ url: `${API}/api/channels/ch1/files/exists`, method: "POST", body: '{"paths":["a"]}' });
  });

  it("returns an empty list when the reply has none", async () => {
    json({});
    await expect(checkFilesExist("ch1", ["a"])).resolves.toEqual([]);
  });

  it("marks every path missing on failure", async () => {
    fail();
    await expect(checkFilesExist("ch1", ["a", "b"])).resolves.toEqual([
      { path: "a", exists: false },
      { path: "b", exists: false },
    ]);
  });
});

describe("searchFiles", () => {
  it.each<[[number?, boolean?], string]>([
    [[], `${API}/api/channels/ch1/files/search?q=ma&limit=30`],
    [[5, true], `${API}/api/channels/ch1/files/search?q=ma&limit=5&dirs=1`],
  ])("GETs %o", async (args, url) => {
    json({ results: [{ path: "main.go" }] });
    await expect(searchFiles("ch1", "ma", ...args)).resolves.toEqual([{ path: "main.go" }]);
    expect(sent().url).toBe(url);
  });

  it("returns an empty list when the reply has none or the search fails", async () => {
    json({});
    await expect(searchFiles("ch1", "x")).resolves.toEqual([]);
    fail();
    await expect(searchFiles("ch1", "x")).resolves.toEqual([]);
  });
});

describe("grepFiles", () => {
  it("sends the options and fills defaults", async () => {
    json({});
    await expect(grepFiles("ch1", "foo", { regex: true, caseSensitive: true })).resolves.toEqual({ files: [], truncated: false, engine: "" });
    expect(sent().url).toBe(`${API}/api/channels/ch1/files/grep?q=foo&regex=1&case=1`);
  });

  it("uses no options by default", async () => {
    json({ files: [{ path: "a" }], truncated: true, engine: "rg" });
    await expect(grepFiles("ch1", "foo")).resolves.toEqual({ files: [{ path: "a" }], truncated: true, engine: "rg" });
    expect(sent().url).toBe(`${API}/api/channels/ch1/files/grep?q=foo`);
  });

  it("throws the daemon's message, or the status text", async () => {
    respond(" bad regex ", { status: 400 });
    await expect(grepFiles("ch1", "(")).rejects.toThrow(/^bad regex$/);
    fail();
    await expect(grepFiles("ch1", "(")).rejects.toThrow("Search failed: Bad");
  });

  it("falls back to the status text when the body can't be read", async () => {
    const res = new Response("", { status: 500, statusText: "Bad" });
    vi.spyOn(res, "text").mockRejectedValue(new Error("stream"));
    vi.spyOn(res, "clone").mockReturnValue(res);
    fetchMock.mockResolvedValue(res);
    await expect(grepFiles("ch1", "x")).rejects.toThrow("Search failed: Bad");
  });
});
