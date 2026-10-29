import { describe, expect, it } from "vitest";
import { locateStep } from "./walkthroughAnchor";

const file = (...lines: string[]) => lines.join("\n");

const doc = file("package main", "", "func a() {", "\treturn", "}", "", "func b() {", "\treturn", "}");

describe("locateStep", () => {
  it.each<{ name: string; text: string; start: number; end: number; anchor?: string; want: ReturnType<typeof locateStep> }>([
    { name: "lines still where they were", text: doc, start: 3, end: 5, anchor: file("func a() {", "\treturn", "}"), want: { start: 3, end: 5, status: "same" } },
    { name: "a step without an anchor", text: doc, start: 3, end: 5, want: { start: 3, end: 5, status: "same" } },
    { name: "a step without an anchor past the end", text: doc, start: 20, end: 25, want: { start: 9, end: 9, status: "same" } },
    {
      name: "lines moved down",
      text: file("package main", "", "// added", "// more", "func a() {", "\treturn", "}"),
      start: 3,
      end: 5,
      anchor: file("func a() {", "\treturn", "}"),
      want: { start: 5, end: 7, status: "moved" },
    },
    {
      name: "the occurrence nearest to where they were",
      text: file("x()", "a", "a", "a", "x()", "b", "x()"),
      start: 6,
      end: 6,
      anchor: "x()",
      want: { start: 5, end: 5, status: "moved" },
    },
    {
      name: "only whole lines",
      text: file("foo(bar)", "bar)", "z"),
      start: 3,
      end: 3,
      anchor: "bar)",
      want: { start: 2, end: 2, status: "moved" },
    },
    {
      name: "a long anchor by its first lines, its end edited",
      text: file("x", "one", "two", "three", "four (edited)"),
      start: 1,
      end: 4,
      anchor: file("one", "two", "three", "four"),
      want: { start: 2, end: 5, status: "moved" },
    },
    {
      name: "a long anchor's first lines where they were",
      text: file("one", "two", "changed"),
      start: 1,
      end: 3,
      anchor: file("one", "two", "three"),
      want: { start: 1, end: 3, status: "same" },
    },
    {
      name: "a cut short anchor where it was",
      text: file("a", "one", "two", "three"),
      start: 2,
      end: 4,
      anchor: file("one", "two", "thr"),
      want: { start: 2, end: 4, status: "same" },
    },
    { name: "lines no longer there", text: doc, start: 3, end: 5, anchor: file("func gone() {", "}"), want: { start: 3, end: 5, status: "changed" } },
    { name: "a one-line anchor no longer there", text: doc, start: 2, end: 2, anchor: "gone", want: { start: 2, end: 2, status: "changed" } },
    { name: "a blank first line isn't matched alone", text: doc, start: 2, end: 3, anchor: file("", "func gone() {"), want: { start: 2, end: 3, status: "changed" } },
    { name: "changed lines past the end of a shorter file", text: file("a", "b"), start: 5, end: 7, anchor: "gone", want: { start: 2, end: 2, status: "changed" } },
  ])("finds $name", ({ text, start, end, anchor, want }) => {
    expect(locateStep(text, start, end, anchor)).toEqual(want);
  });
});
