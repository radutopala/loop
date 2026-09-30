import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { fetchLearnProposalPreview } from "./learn";

let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.stubGlobal("window", { location: { hash: "" } });
  vi.stubGlobal("sessionStorage", { getItem: () => null, setItem: () => {} });
  fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("fetchLearnProposalPreview", () => {
  it("returns the edit applying the proposal would make", async () => {
    const preview = { path: "/proj/.loop/config.json", diff: "--- /dev/null\n+++ /proj/.loop/config.json\n" };
    fetchMock.mockResolvedValue(new Response(JSON.stringify(preview)));
    await expect(fetchLearnProposalPreview(7)).resolves.toEqual(preview);
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/learn/proposals/7/preview");
  });

  it("throws on an error status", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 404, statusText: "Not Found" }));
    await expect(fetchLearnProposalPreview(7)).rejects.toThrow("Failed to preview proposal: Not Found");
  });
});
