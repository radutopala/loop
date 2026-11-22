import { describe, expect, it } from "vitest";
import { homeOf, tildePath } from "./homePath";

describe("homeOf", () => {
  it.each<[string, string | undefined, string | undefined, string | undefined]>([
    ["a dir under home", "/home/u/dev/x", "~/dev/x", "/home/u"],
    ["home itself", "/home/u", "~", "/home/u"],
    ["a dir outside home", "/srv/x", "/srv/x", undefined],
    ["a display form that doesn't match", "/home/u/dev/x", "~/dev/y", undefined],
    ["a display form as long as the path", "/dev/x", "~/dev/x", undefined],
    ["no path", undefined, "~/dev/x", undefined],
    ["no display form", "/home/u/dev/x", undefined, undefined],
  ])("finds it for %s", (_name, path, display, home) => {
    expect(homeOf(path, display)).toBe(home);
  });
});

describe("tildePath", () => {
  it.each<[string, string, string | undefined, string]>([
    ["a file under home", "/home/u/dev/x/a.go", "/home/u", "~/dev/x/a.go"],
    ["home itself", "/home/u", "/home/u", "~"],
    ["a sibling that shares the prefix", "/home/user2/a.go", "/home/u", "/home/user2/a.go"],
    ["a file outside home", "/srv/a.go", "/home/u", "/srv/a.go"],
    ["an unknown home", "/home/u/a.go", undefined, "/home/u/a.go"],
  ])("shows %s", (_name, path, home, shown) => {
    expect(tildePath(path, home)).toBe(shown);
  });
});
