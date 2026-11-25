import type { CanvasTile } from "../../canvas/types";
import { pluginPaneKey } from "../../plugins/PluginPanelsContext";
import { canAddPanel, collectLeaves } from "../../splitPane/treeOps";
import type { LeafNode, PanelType, PaneNode } from "../../types/panels";

/** Per-storageKey counters so IDs don't collide across channels. */
const idCounters = new Map<string, number>();

function nextId(channelId: string, panel: PanelType): string {
  const key = `layout-${channelId}`;
  const n = idCounters.get(key) ?? 0;
  idCounters.set(key, n + 1);
  return `${panel}-${n}`;
}

/** Raises channelId's id counter past every numbered leaf id in tree. */
export function initIdCounter(channelId: string, tree: PaneNode) {
  const key = `layout-${channelId}`;
  const cur = idCounters.get(key) ?? 0;
  let max = 0;
  for (const leaf of collectLeaves(tree)) {
    const num = parseInt(leaf.id.slice(leaf.id.lastIndexOf("-") + 1), 10);
    if (!isNaN(num) && num > max) max = num;
  }
  // Only increase, never decrease (other layouts may have higher IDs)
  if (max + 1 > cur) idCounters.set(key, max + 1);
}

/** The id of a new leaf of panel: a singleton's is its panel, any other's
 *  is numbered per channel. */
export function leafIdForPanel(channelId: string, panel: PanelType): string {
  if (panel === "chat" || panel === "editor" || panel === "memory" || panel === "git" || panel === "sessions" || panel === "notes" || panel === "audit") {
    return panel;
  }
  return nextId(channelId, panel);
}

/** The plugin panels the tiles show, as plugin/panel. */
export function shownPluginTiles(tiles: CanvasTile[]): Set<string> {
  return new Set(tiles.flatMap((t) => (t.panel === "plugin" && t.plugin ? [pluginPaneKey(t.plugin)] : [])));
}

/**
 * Insert `newLeaf` on the opposite horizontal side of the leaf identified by
 * `anchorId`. If the anchor's parent is a horizontal split, the new leaf is
 * appended at whichever end is farther from the anchor. Otherwise the anchor
 * is wrapped into a new horizontal split with the new leaf placed to its
 * right (the typical layout when chat is on the left).
 */
export function insertOppositeHorizontal(node: PaneNode, anchorId: string, newLeaf: LeafNode): PaneNode {
  if (node.type === "leaf") {
    if (node.id !== anchorId) return node;
    return {
      type: "split",
      direction: "horizontal",
      flex: node.flex,
      children: [
        { ...node, flex: 1 },
        { ...newLeaf, flex: 1 },
      ],
    };
  }
  const directIdx = node.children.findIndex((c) => c.type === "leaf" && c.id === anchorId);
  if (directIdx !== -1) {
    if (node.direction === "horizontal") {
      const half = (node.children.length - 1) / 2;
      const appendRight = directIdx <= half;
      // Match the existing siblings' scale so the new leaf is visible. Using
      // `flex: 1` against siblings whose flex values are 50+ would compress the
      // new pane to ~1% of the container width.
      const totalFlex = node.children.reduce((sum, c) => sum + c.flex, 0);
      const avgFlex = totalFlex / node.children.length;
      const inserted: LeafNode = { ...newLeaf, flex: avgFlex };
      return {
        ...node,
        children: appendRight ? [...node.children, inserted] : [inserted, ...node.children],
      };
    }
    return {
      ...node,
      children: node.children.map((c, i) => {
        if (i !== directIdx) return c;
        return {
          type: "split" as const,
          direction: "horizontal" as const,
          flex: c.flex,
          children: [
            { ...c, flex: 1 },
            { ...newLeaf, flex: 1 },
          ],
        };
      }),
    };
  }
  return { ...node, children: node.children.map((c) => insertOppositeHorizontal(c, anchorId, newLeaf)) };
}

/** tree with a file tree put left of the editor leaf, unless the tree has one. */
export function withDefaultFileTreeForEditor(tree: PaneNode, editorLeafId: string, fileTreeLeafId: string): PaneNode {
  if (!canAddPanel(tree, "file-tree")) return tree;
  const transform = (n: PaneNode): PaneNode => {
    if (n.type === "leaf") {
      if (n.id !== editorLeafId) return n;
      return {
        type: "split",
        direction: "horizontal",
        flex: n.flex,
        children: [
          { type: "leaf", id: fileTreeLeafId, panel: "file-tree", flex: 25 },
          { ...n, flex: 75 },
        ],
      };
    }
    return { ...n, children: n.children.map(transform) };
  };
  return transform(tree);
}
