import { storageGetJSON, storageSetJSON } from "../../utils/storage";

// Draft text per channel — persisted to localStorage across app restarts.
// The composer restores a channel's draft when it mounts, so writing one
// before switching to a channel prefills its composer.
const DRAFT_KEY = "loop-chat-drafts";
export const draftText = {
  get(channelId: string): string | undefined {
    const drafts = storageGetJSON<Record<string, string>>(DRAFT_KEY);
    return drafts?.[channelId];
  },
  set(channelId: string, text: string) {
    const drafts = storageGetJSON<Record<string, string>>(DRAFT_KEY) ?? {};
    drafts[channelId] = text;
    storageSetJSON(DRAFT_KEY, drafts);
  },
  delete(channelId: string) {
    const drafts = storageGetJSON<Record<string, string>>(DRAFT_KEY) ?? {};
    delete drafts[channelId];
    storageSetJSON(DRAFT_KEY, drafts);
  },
};
