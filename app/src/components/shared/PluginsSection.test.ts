import { describe, expect, it } from "vitest";
import { imageRepo } from "./PluginsSection";

describe("imageRepo", () => {
  it.each([
    [undefined, ""],
    ["echo", "echo"],
    ["echo:1", "echo"],
    ["ghcr.io/me/echo:1.2@sha256:ab", "ghcr.io/me/echo"],
    ["localhost:5000/echo", "localhost:5000/echo"],
    ["localhost:5000/echo:dev", "localhost:5000/echo"],
  ])("%s → %s", (image, want) => {
    expect(imageRepo(image)).toBe(want);
  });
});
