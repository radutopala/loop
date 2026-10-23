import { describe, expect, it } from "vitest";
import type { PRInfo } from "../api/git";
import { prKind, prRef, prState, prSummary } from "./prState";

const pr = (over: Partial<PRInfo>): PRInfo => ({ number: 12, url: "https://github.com/o/r/pull/12", base_ref: "main", head_ref: "feat", state: "OPEN", ...over });

describe("prState", () => {
  it.each([
    ["open", pr({}), "open", "#3fb950"],
    ["merged", pr({ state: "MERGED" }), "merged", "#a371f7"],
    ["closed", pr({ state: "CLOSED" }), "closed", "#cf222e"],
    ["draft", pr({ is_draft: true }), "draft", "dim"],
    ["a GitLab opened MR", pr({ kind: "mr", state: "opened" }), "open", "#3fb950"],
    ["a GitLab merged MR", pr({ kind: "mr", state: "merged" }), "merged", "#a371f7"],
  ])("%s", (_name, p, label, dot) => {
    expect(prState(p, "dim")).toEqual({ label, dot });
  });
});

describe("prSummary", () => {
  it.each([
    ["with a title", pr({ title: "Fix the login" }), "#12 Fix the login (open)"],
    ["without a title", pr({}), "#12 (open)"],
    ["a draft", pr({ title: "WIP", is_draft: true }), "#12 WIP (draft)"],
    ["a GitLab MR", pr({ kind: "mr", state: "opened", title: "Fix" }), "!12 Fix (open)"],
  ])("%s", (_name, p, want) => {
    expect(prSummary(p)).toBe(want);
  });
});

describe("prKind and prRef", () => {
  it.each([
    ["a GitHub PR", pr({}), "PR", "#12"],
    ["a GitLab MR", pr({ kind: "mr" }), "MR", "!12"],
  ])("%s", (_name, p, kind, ref) => {
    expect(prKind(p)).toBe(kind);
    expect(prRef(p)).toBe(ref);
  });
});
