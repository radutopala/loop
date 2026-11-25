import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { channel } from "../../test/chat-bits-sidebar-list-channel";
import type { Channel } from "../../types";
import { SessionKindIcon } from "./SessionKindIcon";

const kinds = ["thread", "channel", "worktree", "task", "ephemeral"];

function shownKinds() {
  return kinds.filter((k) => screen.queryByTestId(`session-kind-${k}`));
}

describe("SessionKindIcon", () => {
  it.each<[string, Partial<Channel>, boolean, string[]]>([
    ["a channel's #", {}, false, ["channel"]],
    ["nothing for a plain thread in the tree", { parent_id: "p" }, false, []],
    ["a bubble for a plain thread outside the tree", { parent_id: "p" }, true, ["thread"]],
    ["a branch for a worktree", { parent_id: "p", worktree: true }, true, ["worktree"]],
    ["a clock for a task thread", { parent_id: "p", task_id: 3 }, true, ["task"]],
    ["only the return arrow for an ephemeral task thread", { parent_id: "p", task_id: 3, name: "[ephemeral] x" }, true, ["ephemeral"]],
  ])("shows %s", (_, fields, showPlainThread, want) => {
    render(<SessionKindIcon channel={channel(fields)} showPlainThread={showPlainThread} />);
    expect(shownKinds()).toEqual(want);
  });
});
