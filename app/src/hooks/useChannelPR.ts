import { useEffect, useState } from "react";
import { fetchPR, type PRInfo } from "../api/git";

/**
 * The PR open on a channel's branch, looked up while enabled and again when
 * the branch changes; null without one or when the lookup fails. The daemon
 * caches lookups, so several views asking for the same channel cost one
 * `gh` call.
 */
export function useChannelPR(channelId: string, branch: string | undefined, enabled = true): PRInfo | null {
  const [pr, setPR] = useState<PRInfo | null>(null);
  useEffect(() => {
    if (!enabled || !channelId || !branch) {
      setPR(null);
      return;
    }
    let cancelled = false;
    fetchPR(channelId)
      .then((res) => {
        if (!cancelled) setPR(res.present && res.pr ? res.pr : null);
      })
      .catch(() => {
        if (!cancelled) setPR(null);
      });
    return () => {
      cancelled = true;
    };
  }, [channelId, branch, enabled]);
  return pr;
}
