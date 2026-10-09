import type { LeafNode, PaneNode } from "../types/panels";

/** Replaces the leaf id with newLeaf, which takes its place and size. */
export function replaceLeaf(node: PaneNode, id: string, newLeaf: LeafNode): PaneNode {
  if (node.type === "leaf") return node.id === id ? { ...newLeaf, flex: node.flex } : node;
  return { ...node, children: node.children.map((c) => replaceLeaf(c, id, newLeaf)) };
}
