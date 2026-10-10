import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { getReclaimable, reclaimDockerSpace } from "./image";

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

describe("getReclaimable", () => {
  it("returns the estimate", async () => {
    const estimate = { volumes_sized: false, build_cache: 1, dangling_images: 2, unused_images: 3, unused_image_tags: ["old:1"], anonymous_volumes: 4, orphan_volumes: 5, orphan_volume_names: [] };
    fetchMock.mockResolvedValue(new Response(JSON.stringify(estimate)));
    await expect(getReclaimable()).resolves.toEqual(estimate);
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/image/reclaimable");
  });

  it("asks for volume sizes", async () => {
    fetchMock.mockResolvedValue(new Response("{}"));
    await getReclaimable(true);
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/image/reclaimable?volume_sizes=true");
  });

  it("throws the server's error", async () => {
    fetchMock.mockResolvedValue(new Response("daemon down", { status: 500 }));
    await expect(getReclaimable()).rejects.toThrow("daemon down");
  });
});

describe("reclaimDockerSpace", () => {
  it.each([
    [undefined, false],
    [true, true],
  ])("sends unused_images=%s as %s", async (arg, sent) => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ total_reclaimed: 10 })));
    await expect(reclaimDockerSpace(arg)).resolves.toEqual({ total_reclaimed: 10 });
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe("http://localhost:8222/api/image/reclaim");
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body)).toEqual({ unused_images: sent });
  });

  it("throws the server's error", async () => {
    fetchMock.mockResolvedValue(new Response("daemon down", { status: 500 }));
    await expect(reclaimDockerSpace()).rejects.toThrow("daemon down");
  });
});
