import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { API, type FetchMock, fail, json, sent, stubFetch } from "../test/hooks-c-api-fetch";
import { restoreBuiltins } from "./builtins";

let fetchMock: FetchMock;

beforeEach(() => {
  fetchMock = stubFetch();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("restoreBuiltins", () => {
  it("posts the kind and returns the result", async () => {
    const out = { kind: "workflows", added: ["a"], patched: [], skipped: [] };
    fetchMock.mockResolvedValue(json(out));
    await expect(restoreBuiltins("workflows")).resolves.toEqual(out);
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/builtins/restore`, method: "POST", body: { kind: "workflows" } });
    expect(sent(fetchMock).headers.get("content-type")).toBe("application/json");
  });

  it("throws the server text", async () => {
    fetchMock.mockResolvedValue(fail(500, "x", "no such kind"));
    await expect(restoreBuiltins("shortcuts")).rejects.toThrow("no such kind");
  });
});
