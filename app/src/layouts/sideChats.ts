import { createSideChat, deleteSideChat, listSideChats, type SideChat, type SideChatMode } from "../api/sideChats";
import { isCanvasNode, type LayoutRoot } from "../canvas/types";
import { collectLeaves } from "../splitPane/treeOps";
import { storageGetJSON, storageSetJSON } from "../utils/storage";

/** The side chat each of a channel's chat-side panes shows, by pane id. */
export type PaneSideChats = Record<string, string>;

/** What side chats are kept with: the daemon's side-chats API, and the
 *  storage that remembers which side chat each pane shows. */
export interface SideChatDeps {
  create: (channelId: string, mode: SideChatMode) => Promise<string>;
  list: (channelId: string) => Promise<SideChat[]>;
  remove: (sideChannelId: string) => Promise<void>;
  load: (channelId: string) => PaneSideChats;
  save: (channelId: string, panes: PaneSideChats) => void;
}

/** sideChatsKey is where a channel's PaneSideChats are kept. */
export function sideChatsKey(channelId: string): string {
  return `side-chats:${channelId}`;
}

/** sideChatLeafIds returns the ids of the chat-side panes the layouts hold,
 *  split and canvas alike. */
export function sideChatLeafIds(layouts: LayoutRoot[]): Set<string> {
  const ids = new Set<string>();
  for (const root of layouts) {
    const panes = isCanvasNode(root) ? root.tiles : collectLeaves(root);
    for (const p of panes) if (p.panel === "chat-side") ids.add(p.id);
  }
  return ids;
}

/**
 * SideChats ties the chat-side panes to their side chats: a pane's side
 * chat is started the first time it opens and kept for it after that, and
 * goes when the pane closes.
 */
export class SideChats {
  private readonly deps: SideChatDeps;
  // A pane opening twice at once (a remount) starts one side chat.
  private readonly opening = new Map<string, Promise<string>>();

  constructor(deps: SideChatDeps) {
    this.deps = deps;
  }

  /** open returns the side chat channelId's pane leafId shows, starting one
   *  in mode when it has none yet. */
  open(channelId: string, leafId: string, mode: SideChatMode): Promise<string> {
    const stored = this.deps.load(channelId)[leafId];
    if (stored) return Promise.resolve(stored);
    const key = `${channelId}:${leafId}`;
    const pending = this.opening.get(key);
    if (pending) return pending;
    const started = this.deps
      .create(channelId, mode)
      .then((id) => {
        this.deps.save(channelId, { ...this.deps.load(channelId), [leafId]: id });
        return id;
      })
      .finally(() => this.opening.delete(key));
    this.opening.set(key, started);
    return started;
  }

  /** close ends the side chat of channelId's pane leafId, if it has one:
   *  one still starting too, once it has. */
  async close(channelId: string, leafId: string): Promise<void> {
    await this.opening.get(`${channelId}:${leafId}`)?.catch(() => undefined);
    const { [leafId]: id, ...rest } = this.deps.load(channelId);
    if (!id) return;
    this.deps.save(channelId, rest);
    await this.deps.remove(id);
  }

  /** sweep deletes channelId's side chats no pane shows any more: ones whose
   *  pane was closed while Loop wasn't there to end them (a tab removed with
   *  the app closed, a reset layout). The panes gone are forgotten, and so
   *  are side chats gone already. leafIds returns the chat-side panes the
   *  channel's layouts hold, asked once the list is in, so a pane opened
   *  meanwhile keeps its side chat. Returns the ids deleted. */
  async sweep(channelId: string, leafIds: () => Set<string>): Promise<string[]> {
    const all = (await this.deps.list(channelId)).map((c) => c.channel_id);
    // A side chat still starting is in the list before it's kept for its pane.
    await Promise.allSettled(this.opening.values());
    const live = leafIds();
    const exists = new Set(all);
    const panes = this.deps.load(channelId);
    const kept: PaneSideChats = {};
    for (const [leafId, id] of Object.entries(panes)) if (live.has(leafId) && exists.has(id)) kept[leafId] = id;
    if (Object.keys(kept).length !== Object.keys(panes).length) this.deps.save(channelId, kept);
    const shown = new Set(Object.values(kept));
    const deleted: string[] = [];
    for (const id of all.filter((id) => !shown.has(id))) {
      await this.deps.remove(id);
      deleted.push(id);
    }
    return deleted;
  }
}

/** sideChats keeps the app's side chats. */
export const sideChats = new SideChats({
  create: createSideChat,
  list: listSideChats,
  remove: deleteSideChat,
  load: (channelId) => storageGetJSON<PaneSideChats>(sideChatsKey(channelId)) ?? {},
  save: (channelId, panes) => storageSetJSON(sideChatsKey(channelId), panes),
});
