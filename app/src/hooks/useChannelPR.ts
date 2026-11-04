import { useEffect, useState } from "react";
import { fetchPR, type PRInfo } from "../api/git";
import { getPRStatus, type PRStatus } from "../api/pr";

/**
 * The PR open on a channel's branch, looked up while enabled and again when
 * the branch changes; null without one or when the lookup fails. The daemon
 * caches lookups, so several views asking for the same channel cost one
 * `gh` call. With delayMs, it asks only once enabled has held that long.
 */
export function useChannelPR(channelId: string, branch: string | undefined, enabled = true, delayMs = 0): PRInfo | null {
  const [pr, setPR] = useState<PRInfo | null>(null);
  useEffect(() => {
    if (!enabled || !channelId || !branch) {
      setPR(null);
      return;
    }
    let cancelled = false;
    const lookup = () =>
      fetchPR(channelId)
        .then((res) => {
          if (!cancelled) setPR(res.present && res.pr ? res.pr : null);
        })
        .catch(() => {
          if (!cancelled) setPR(null);
        });
    const timer = delayMs > 0 ? setTimeout(lookup, delayMs) : undefined;
    if (!timer) void lookup();
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [channelId, branch, enabled, delayMs]);
  return pr;
}

/**
 * The watcher's status of a channel's PR/MR, looked up while enabled; null
 * without one or when the lookup fails.
 */
export function useChannelPRStatus(channelId: string, enabled: boolean): PRStatus | null {
  const [status, setStatus] = useState<PRStatus | null>(null);
  useEffect(() => {
    if (!enabled || !channelId) {
      setStatus(null);
      return;
    }
    let cancelled = false;
    getPRStatus(channelId)
      .then((res) => {
        if (!cancelled) setStatus(res);
      })
      .catch(() => {
        if (!cancelled) setStatus(null);
      });
    return () => {
      cancelled = true;
    };
  }, [channelId, enabled]);
  return status;
}
