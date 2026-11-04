import { describe, expect, it } from "vitest";
import { latestGuard } from "./latestGuard";

describe("latestGuard", () => {
  it("keeps the newest request and drops older ones, whichever answers first", () => {
    const start = latestGuard();
    const first = start();
    expect(first()).toBe(true);

    const second = start();
    expect(second()).toBe(true);
    expect(first()).toBe(false);
  });

  it("tracks each guard apart", () => {
    const a = latestGuard()();
    latestGuard()();
    expect(a()).toBe(true);
  });
});
