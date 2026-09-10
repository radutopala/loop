import { useEffect, useState } from "react";
import { type ContainerStatsEntry, fetchContainerStats } from "../api/channels";

/** Per-container-type stats for one channel: agent (chat runs), shell
 *  (docker-agent terminal panes exec into the shared shell container) and
 *  chrome (the browser pane's sidecar). */
export interface ContainerStatsByType {
  agent?: ContainerStatsEntry;
  shell?: ContainerStatsEntry;
  chrome?: ContainerStatsEntry;
}

/**
 * Picks the container whose usage belongs in a panel's header. A browser pane
 * has its own container to answer for: Chrome is memory-capped separately from
 * the agent, and a page heavy enough to be OOM-killed takes the pane's tab with
 * it, so the number that predicts that has to be the sidecar's own.
 */
export function statsForPanel(panel: string, stats?: ContainerStatsByType): ContainerStatsEntry | undefined {
  if (panel === "chat") return stats?.agent;
  if (panel === "docker-agent") return stats?.shell;
  if (panel === "docker-browser") return stats?.chrome;
  return undefined;
}

const POLL_MS = 3000;

/**
 * Polls the channel's container stats while mounted, pausing when the window
 * is hidden. Docker's non-streaming stats endpoint takes ~1s server-side to
 * prime the CPU delta, so the effective refresh is POLL_MS + ~1s.
 */
export function useContainerStats(channelId: string | null): ContainerStatsByType {
  const [stats, setStats] = useState<ContainerStatsByType>({});

  useEffect(() => {
    setStats({});
    if (!channelId) return;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout>;

    const tick = async () => {
      if (!document.hidden) {
        try {
          const entries = await fetchContainerStats(channelId);
          if (cancelled) return;
          const next: ContainerStatsByType = {};
          for (const e of entries) {
            // Registry order is newest-last; keep the first entry per type
            // stable so the display doesn't flap between duplicate agents.
            if (e.type === "agent" && !next.agent) next.agent = e;
            if (e.type === "shell" && !next.shell) next.shell = e;
            if (e.type === "chrome" && !next.chrome) next.chrome = e;
          }
          setStats(next);
        } catch {
          if (!cancelled) setStats({});
        }
      }
      if (!cancelled) timer = setTimeout(tick, POLL_MS);
    };
    tick();

    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [channelId]);

  return stats;
}

/** Formats bytes as a compact "384M" / "1.5G" label. */
export function fmtBytes(n: number): string {
  if (n >= 1024 * 1024 * 1024) return `${(n / (1024 * 1024 * 1024)).toFixed(1)}G`;
  return `${Math.round(n / (1024 * 1024))}M`;
}
