import type { PanelType } from "../types/panels";
import { EXCLUSIVE_PANELS, SINGLETON_PANELS } from "../types/panels";
import type { DropPosition, LeafNode, PaneNode, SplitDirection, SplitNode } from "./types";

export function makeLeaf(id: string, panel: PanelType, flex = 1, openMode?: import("../types/panels").AgentOpenMode): LeafNode {
  return { type: "leaf", id, panel, flex, ...(openMode ? { openMode } : {}) };
}

export function findLeafById(node: PaneNode, id: string): LeafNode | null {
  if (node.type === "leaf") return node.id === id ? node : null;
  for (const child of node.children) {
    const found = findLeafById(child, id);
    if (found) return found;
  }
  return null;
}

export function removeLeaf(node: PaneNode, id: string): PaneNode | null {
  if (node.type === "leaf") {
    return node.id === id ? null : node;
  }
  const remaining = node.children.map((c) => removeLeaf(c, id)).filter((c): c is PaneNode => c !== null);
  if (remaining.length === 0) return null;
  // Keep single-child splits intact: collapsing them changes the React tree
  // shape, which forces SplitPaneLayout's PaneTree to switch from its split
  // branch to its leaf branch — unmounting the surviving leaf's component
  // (e.g. EditorPanel) and dropping its in-flight state.
  return { ...node, children: remaining };
}

export function splitLeaf(node: PaneNode, leafId: string, direction: SplitDirection, newLeaf: LeafNode): PaneNode {
  if (node.type === "leaf") {
    if (node.id !== leafId) return node;
    return {
      type: "split" as const,
      direction,
      children: [
        { ...node, flex: 1 },
        { ...newLeaf, flex: 1 },
      ],
      flex: node.flex,
    };
  }
  return {
    ...node,
    children: node.children.map((c) => splitLeaf(c, leafId, direction, newLeaf)),
  };
}

/**
 * Adds leaf beside the anchor pane, or, with a null anchor, at the edge of the
 * whole tree: after it (right or below), or before it with before set. In a
 * split of the same direction the leaf joins the row or column as a sibling,
 * with the average share of the panes there, rather than splitting one of
 * them in two; elsewhere the anchor splits into the two.
 */
export function addBeside(node: PaneNode, anchorId: string | null, direction: SplitDirection, leaf: LeafNode, before: boolean): PaneNode {
  const wrap = (target: PaneNode): PaneNode => {
    const children = [{ ...target, flex: 1 }];
    children.splice(before ? 0 : 1, 0, { ...leaf, flex: 1 });
    return { type: "split", direction, children, flex: target.flex };
  };
  const join = (split: SplitNode, at: number): PaneNode => {
    const share = split.children.reduce((sum, c) => sum + c.flex, 0) / split.children.length || 1;
    const children = [...split.children];
    children.splice(at, 0, { ...leaf, flex: share });
    return { ...split, children };
  };
  if (anchorId === null) {
    if (node.type === "split" && node.direction === direction) return join(node, before ? 0 : node.children.length);
    return wrap(node);
  }
  const visit = (n: PaneNode): PaneNode => {
    if (n.type === "leaf") return n.id === anchorId ? wrap(n) : n;
    const i = n.children.findIndex((c) => c.type === "leaf" && c.id === anchorId);
    if (i >= 0 && n.direction === direction) return join(n, before ? i : i + 1);
    return { ...n, children: n.children.map(visit) };
  };
  return visit(node);
}

export function updateFlex(node: PaneNode, parentPath: number[], dividerIndex: number, newFlexA: number, newFlexB: number): PaneNode {
  if (parentPath.length === 0) {
    if (node.type !== "split") return node;
    return {
      ...node,
      children: node.children.map((c, i) => {
        if (i === dividerIndex) return { ...c, flex: newFlexA };
        if (i === dividerIndex + 1) return { ...c, flex: newFlexB };
        return c;
      }),
    };
  }
  if (node.type !== "split") return node;
  const [first, ...rest] = parentPath;
  return {
    ...node,
    children: node.children.map((c, i) => (i === first ? updateFlex(c, rest, dividerIndex, newFlexA, newFlexB) : c)),
  };
}

export function collectLeaves(node: PaneNode): LeafNode[] {
  if (node.type === "leaf") return [node];
  return node.children.flatMap(collectLeaves);
}

export function findLastLeaf(node: PaneNode): LeafNode | null {
  if (node.type === "leaf") return node;
  for (let i = node.children.length - 1; i >= 0; i--) {
    const child = node.children[i];
    if (!child) continue;
    const found = findLastLeaf(child);
    if (found) return found;
  }
  return null;
}

export function leafCount(node: PaneNode): number {
  if (node.type === "leaf") return 1;
  return node.children.reduce((s, c) => s + leafCount(c), 0);
}

export function swapLeavesInTree(tree: PaneNode, idA: string, idB: string): PaneNode {
  const leafA = findLeafById(tree, idA);
  const leafB = findLeafById(tree, idB);
  if (!leafA || !leafB) return tree;
  const replaceLeaf = (node: PaneNode, targetId: string, replacement: LeafNode): PaneNode => {
    if (node.type === "leaf") {
      return node.id === targetId ? { ...replacement, flex: node.flex } : node;
    }
    return { ...node, children: node.children.map((c) => replaceLeaf(c, targetId, replacement)) };
  };
  const tempId = `__swap_temp_${Date.now()}`;
  let result = replaceLeaf(tree, idA, { ...leafA, id: tempId });
  result = replaceLeaf(result, idB, { ...leafB, id: idA, panel: leafB.panel });
  result = replaceLeaf(result, tempId, { ...leafA, id: idB, panel: leafA.panel });
  return result;
}

export function moveLeaf(tree: PaneNode, dragId: string, dropId: string, position: Exclude<DropPosition, "center">): PaneNode {
  const dragLeaf = findLeafById(tree, dragId);
  if (!dragLeaf) return tree;
  let result = removeLeaf(tree, dragId);
  if (!result) return tree;
  const direction: SplitDirection = position === "top" || position === "bottom" ? "vertical" : "horizontal";
  const insertBefore = position === "top" || position === "left";
  const insertLeafAt = (node: PaneNode): PaneNode => {
    if (node.type === "leaf") {
      if (node.id !== dropId) return node;
      const children = insertBefore ? [makeLeaf(dragId, dragLeaf.panel), { ...node, flex: 1 }] : [{ ...node, flex: 1 }, makeLeaf(dragId, dragLeaf.panel)];
      return { type: "split" as const, direction, children, flex: node.flex };
    }
    return { ...node, children: node.children.map((c) => insertLeafAt(c)) };
  };
  result = insertLeafAt(result);
  return result;
}

export function collectPanelTypes(node: PaneNode): Set<PanelType> {
  const types = new Set<PanelType>();
  for (const leaf of collectLeaves(node)) {
    types.add(leaf.panel);
  }
  return types;
}

export function canAddPanel(tree: PaneNode | null, panel: PanelType): boolean {
  if (!tree) return true;
  const used = collectPanelTypes(tree);
  if ((SINGLETON_PANELS as string[]).includes(panel) && used.has(panel)) {
    return false;
  }
  // Check exclusive groups — if any panel in the same group is present, block.
  for (const group of EXCLUSIVE_PANELS) {
    if (group.includes(panel) && group.some((p) => p !== panel && used.has(p))) {
      return false;
    }
  }
  return true; // multi-instance: always allowed
}

export function hasAgentLeaf(node: PaneNode): boolean {
  if (node.type === "leaf") return node.panel === "docker-agent" || node.panel === "docker-shell";
  return node.children.some(hasAgentLeaf);
}

/**
 * Each pane's share of its split as a whole percent, from the panes'
 * effective flex values (0 for a minimized pane). All zeros when no pane
 * has any flex.
 */
export function flexPercents(flexes: number[]): number[] {
  const total = flexes.reduce((s, f) => s + f, 0);
  return flexes.map((f) => (total > 0 ? Math.round((f / total) * 100) : 0));
}
