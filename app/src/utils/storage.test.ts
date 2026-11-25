// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { storageGet, storageGetJSON, storageRemove, storageSet, storageSetJSON } from "./storage";

describe("storage", () => {
  beforeEach(() => {
    localStorage.clear();
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("sets, gets and removes a string", () => {
    expect(storageGet("k")).toBeNull();
    storageSet("k", "v");
    expect(storageGet("k")).toBe("v");
    storageRemove("k");
    expect(storageGet("k")).toBeNull();
  });

  it("round-trips JSON and reads a missing key as null", () => {
    expect(storageGetJSON("j")).toBeNull();
    storageSetJSON("j", { a: [1, 2] });
    expect(localStorage.getItem("j")).toBe('{"a":[1,2]}');
    expect(storageGetJSON<{ a: number[] }>("j")).toEqual({ a: [1, 2] });
  });

  it("reads malformed JSON as null", () => {
    localStorage.setItem("j", "{nope");
    expect(storageGetJSON("j")).toBeNull();
  });

  it("swallows storage that throws", () => {
    const fail = () => {
      throw new Error("denied");
    };
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(fail);
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(fail);
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation(fail);
    expect(storageGet("k")).toBeNull();
    expect(storageGetJSON("k")).toBeNull();
    expect(() => storageSet("k", "v")).not.toThrow();
    expect(() => storageSetJSON("k", 1)).not.toThrow();
    expect(() => storageRemove("k")).not.toThrow();
  });
});
