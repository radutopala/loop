import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { API, type FetchMock, fail, json, sent, stubFetch } from "../test/hooks-c-api-fetch";
import { fetchContainers } from "./containers";

let fetchMock: FetchMock;

beforeEach(() => {
  fetchMock = stubFetch();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("fetchContainers", () => {
  it("returns the containers", async () => {
    fetchMock.mockResolvedValue(json([{ container_id: "x" }]));
    await expect(fetchContainers()).resolves.toEqual([{ container_id: "x" }]);
    expect(sent(fetchMock).url).toBe(`${API}/api/containers`);
  });

  it("throws on an error status", async () => {
    fetchMock.mockResolvedValue(fail(500, "boom"));
    await expect(fetchContainers()).rejects.toThrow("Failed to fetch containers: boom");
  });
});
