import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { API, type FetchMock, fail, json, sent, stubFetch } from "../test/hooks-c-api-fetch";
import { searchChannelMessages, searchMessages } from "./search";

let fetchMock: FetchMock;

beforeEach(() => {
  fetchMock = stubFetch();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("search", () => {
  it("searches messages with and without a limit", async () => {
    fetchMock.mockImplementation(async () => json([{ id: 1 }]));
    await expect(searchMessages("hi there", 5)).resolves.toEqual([{ id: 1 }]);
    await searchMessages("hi");
    expect(sent(fetchMock, 0).url).toBe(`${API}/api/messages/search?q=hi+there&limit=5`);
    expect(sent(fetchMock, 1).url).toBe(`${API}/api/messages/search?q=hi`);
  });

  it("throws when the search fails", async () => {
    fetchMock.mockResolvedValue(fail(500, "boom"));
    await expect(searchMessages("x")).rejects.toThrow("Failed to search messages: boom");
  });

  it("searches one channel and passes the abort signal", async () => {
    fetchMock.mockResolvedValue(json({ ids: [3, 1] }));
    const signal = new AbortController().signal;
    await expect(searchChannelMessages("c 1", "q", signal)).resolves.toEqual([3, 1]);
    expect(sent(fetchMock).url).toBe(`${API}/api/channels/c%201/messages/search?q=q`);
    expect(fetchMock.mock.calls[0]![1]?.signal).toBe(signal);
  });

  it("throws when the channel search fails", async () => {
    fetchMock.mockResolvedValue(fail(500, "boom"));
    await expect(searchChannelMessages("c1", "q")).rejects.toThrow("Failed to search messages: boom");
  });
});
