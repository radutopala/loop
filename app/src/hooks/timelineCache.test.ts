import { describe, expect, it } from "vitest";
import type { TimelineItem } from "../types";
import { TimelineCache } from "./timelineCache";

function items(...ids: number[]): TimelineItem[] {
  return ids.map((id) => ({ kind: "compacting", position: id, id }));
}

describe("TimelineCache", () => {
  it("returns nothing for a channel it hasn't seen", () => {
    expect(new TimelineCache(2, 3).get("c1")).toBeUndefined();
  });

  it("keeps the newest items of a channel", () => {
    const cache = new TimelineCache(2, 3);
    cache.set("c1", items(1, 2, 3, 4, 5));
    expect(cache.get("c1")).toEqual(items(3, 4, 5));
  });

  it("keeps what a channel had when given no items", () => {
    const cache = new TimelineCache(2, 3);
    cache.set("c1", items(1));
    cache.set("c1", []);
    expect(cache.get("c1")).toEqual(items(1));
  });

  it("drops the channel set longest ago", () => {
    const cache = new TimelineCache(2, 3);
    cache.set("c1", items(1));
    cache.set("c2", items(2));
    cache.set("c1", items(1, 11));
    cache.set("c3", items(3));
    expect(cache.get("c1")).toEqual(items(1, 11));
    expect(cache.get("c2")).toBeUndefined();
    expect(cache.get("c3")).toEqual(items(3));
  });
});
