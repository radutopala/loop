import { describe, expect, it } from "vitest";
import { makeLeaf } from "../splitPane/treeOps";
import type { PaneNode } from "../types/panels";
import { replaceLeaf } from "./paneOps";

describe("replaceLeaf", () => {
  it("puts the new leaf in the old one's place and size", () => {
    const tree: PaneNode = { type: "split", direction: "horizontal", flex: 1, children: [makeLeaf("chat", "chat", 0.7), makeLeaf("git", "git", 0.3)] };
    expect(replaceLeaf(tree, "chat", makeLeaf("docker-agent-0", "docker-agent", 1, "fresh"))).toEqual({
      type: "split",
      direction: "horizontal",
      flex: 1,
      children: [{ type: "leaf", id: "docker-agent-0", panel: "docker-agent", flex: 0.7, openMode: "fresh" }, makeLeaf("git", "git", 0.3)],
    });
  });

  it("leaves a tree without the leaf as it is", () => {
    const tree = makeLeaf("chat", "chat");
    expect(replaceLeaf(tree, "git", makeLeaf("notes", "notes"))).toBe(tree);
  });
});
