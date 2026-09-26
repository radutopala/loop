import { useCallback, useEffect, useRef, useState } from "react";
import { holdQueuedMessage, releaseQueuedHold, updateQueuedMessage } from "../api/loopApi";
import type { Message } from "../types";
import { logErr } from "../utils/log";

// The backend hold lasts 5 minutes; renewing well inside that keeps an open
// edit from lapsing while a stalled request or two still has time to land.
const HOLD_RENEW_MS = 2 * 60 * 1000;

export const EDIT_STARTED_NOTICE = "That message left the queue — it started or was removed — so the edit wasn't applied. Your text is still here to send as a new message.";

export type QueuedEditSaveResult = "saved" | "started";

export interface QueuedEdit {
  // The queued message being edited in the composer, or null.
  editing: Message | null;
  // Set when an edit ended because the agent got to the message first.
  notice: string | null;
  // Holds the message and enters edit mode. Resolves false when the message
  // already started — the caller shows that instead of opening the edit.
  start: (msg: Message) => Promise<boolean>;
  save: (content: string) => Promise<QueuedEditSaveResult>;
  cancel: () => void;
  dismissNotice: () => void;
}

// useQueuedEdit runs the "edit a queued message" flow. Editing takes a hold
// on the backend so the drain can't start the message mid-edit (the hold
// also stops anything queued behind it, keeping queue order). The hold is a
// lease: renewed while the edit is open, released on save or cancel, and left
// to lapse if the app goes away. Whichever of "claim" and "hold/save" reaches
// the database first wins; when the claim wins, the edit ends with a notice
// and the composer keeps the user's text.
export function useQueuedEdit(channelId: string | null, queued: Message[]): QueuedEdit {
  const [editing, setEditing] = useState<Message | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const editingRef = useRef<{ channelId: string; msgId: string } | null>(null);
  editingRef.current = editing && channelId ? { channelId, msgId: editing.msg_id } : null;

  const endAsStarted = useCallback(() => {
    setEditing(null);
    setNotice(EDIT_STARTED_NOTICE);
  }, []);

  const start = useCallback(
    async (msg: Message) => {
      if (!channelId) return false;
      if (!(await holdQueuedMessage(channelId, msg.msg_id))) return false;
      setNotice(null);
      setEditing(msg);
      return true;
    },
    [channelId],
  );

  const save = useCallback(
    async (content: string): Promise<QueuedEditSaveResult> => {
      const cur = editingRef.current;
      if (!cur) return "started";
      if (await updateQueuedMessage(cur.channelId, cur.msgId, content)) {
        setEditing(null);
        return "saved";
      }
      endAsStarted();
      return "started";
    },
    [endAsStarted],
  );

  const cancel = useCallback(() => {
    const cur = editingRef.current;
    if (cur) releaseQueuedHold(cur.channelId, cur.msgId).catch(logErr("releasing queued message"));
    setEditing(null);
  }, []);

  const dismissNotice = useCallback(() => setNotice(null), []);

  // Keep the lease alive while the edit is open.
  const editingId = editing?.msg_id;
  useEffect(() => {
    if (!editingId || !channelId) return;
    const timer = setInterval(() => {
      holdQueuedMessage(channelId, editingId)
        .then((held) => {
          if (!held) endAsStarted();
        })
        .catch(logErr("renewing queued message hold"));
    }, HOLD_RENEW_MS);
    return () => clearInterval(timer);
  }, [channelId, editingId, endAsStarted]);

  // The message left the queue mid-edit — deleted elsewhere, or claimed by a
  // run after the hold lapsed. Nothing to save it into any more.
  useEffect(() => {
    if (editing && !queued.some((m) => m.msg_id === editing.msg_id && !m.is_running)) endAsStarted();
  }, [queued, editing, endAsStarted]);

  // Switching channels abandons the edit: release it rather than leaving the
  // old channel's queue stalled until the lease runs out.
  useEffect(() => {
    return () => {
      const cur = editingRef.current;
      if (cur) releaseQueuedHold(cur.channelId, cur.msgId).catch(logErr("releasing queued message"));
      setEditing(null);
      setNotice(null);
    };
  }, [channelId]);

  return { editing, notice, start, save, cancel, dismissNotice };
}
