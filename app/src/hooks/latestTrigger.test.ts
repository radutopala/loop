import { describe, expect, it } from "vitest";
import type { TimelineItem } from "../types";
import { latestTrigger } from "./latestTrigger";

const item = (id: number, trigger?: string): TimelineItem => ({ kind: "compacting", position: id, id, trigger_msg_id: trigger });

describe("latestTrigger", () => {
  it.each([
    { name: "empty list", list: [], want: undefined },
    { name: "no linked row", list: [item(1), item(2)], want: undefined },
    { name: "last linked row wins", list: [item(1, "a"), item(2, "b"), item(3)], want: "b" },
  ])("$name", ({ list, want }) => {
    expect(latestTrigger(list)).toBe(want);
  });
});
