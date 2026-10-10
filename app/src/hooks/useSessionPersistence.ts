import type { RefObject } from "react";
import { useCallback, useEffect, useRef } from "react";
import type { TerminalTarget } from "../types";
import type { AgentOpenMode } from "../types/panels";
import { startTerminalClaims } from "./terminalClaims";

/** Where the session maps are kept, so a reload reattaches each pane to its
 *  session rather than starting a new one and leaving the old one running.
 *  sessionStorage is per window, so two windows don't share them. */
const SESSIONS_STORAGE_KEY = "loop.terminalSessions";

type StoredSessions = { ids: [string, string][]; starts: [string, number][] };

function loadStoredSessions(): StoredSessions {
  try {
    const raw = sessionStorage.getItem(SESSIONS_STORAGE_KEY);
    if (raw) {
      const parsed = JSON.parse(raw) as Partial<StoredSessions>;
      return { ids: parsed.ids ?? [], starts: parsed.starts ?? [] };
    }
  } catch {
    // No storage, or a corrupt entry: start empty.
  }
  return { ids: [], starts: [] };
}

const stored = loadStoredSessions();

/** Module-level map so session IDs survive component remounts.
 *  Keyed by `${channelId}:${target}` so agent and host sessions are tracked independently. */
const sessionsByChannel = new Map<string, string>(stored.ids);

/** Module-level map of session start timestamps (epoch ms) so timers survive remounts. */
const sessionStartTimes = new Map<string, number>(stored.starts);

function saveSessions() {
  try {
    sessionStorage.setItem(SESSIONS_STORAGE_KEY, JSON.stringify({ ids: [...sessionsByChannel], starts: [...sessionStartTimes] }));
  } catch {
    // Storage full or blocked: the maps still work for this page load.
  }
}

function sessionKey(channelId: string, target: TerminalTarget, instanceId?: string): string {
  const base = `${channelId}:${target}`;
  return instanceId ? `${base}:${instanceId}` : base;
}

/** Removes and returns the session stored for a terminal pane, so a pane
 *  that isn't mounted can still have its session closed. */
export function takeStoredSession(channelId: string, target: TerminalTarget, instanceId?: string): string | null {
  const key = sessionKey(channelId, target, instanceId);
  const sid = sessionsByChannel.get(key) ?? null;
  sessionsByChannel.delete(key);
  sessionStartTimes.delete(key);
  saveSessions();
  return sid;
}

/** The session IDs this window holds, on tabs it isn't showing too. */
export function heldSessionIds(): string[] {
  return [...sessionsByChannel.values()];
}

type GetTerminalSize = (() => { cols: number; rows: number } | null) | undefined;

/**
 * Tracks the active terminal session ID across WebSocket reconnects
 * and component remounts (channel switching).
 * On open, sends either an "attach" (existing session) or "create" (new session).
 */
export function useSessionPersistence(
  channelId: string | null,
  target: TerminalTarget,
  getTerminalSizeRef?: RefObject<GetTerminalSize>,
  instanceId?: string,
  /** Claude Code session ID to resume (overrides the channel's stored session). */
  claudeSessionId?: string,
  /** Start a fresh session, ignoring the channel's stored session. */
  newSession?: boolean,
  /** Explicit command to run instead of the interactive Claude bootstrap. */
  cmd?: string[],
  /** Agent terminal open mode (resume / fork / fresh). */
  openMode?: AgentOpenMode,
  /** Workspace root index for shell panes (0 = primary dir, 1+ = extra_dirs). */
  rootIndex?: number,
) {
  const key = channelId ? sessionKey(channelId, target, instanceId) : null;
  const sessionIdRef = useRef<string | null>(key ? (sessionsByChannel.get(key) ?? null) : null);
  /** Set to true after kill to prevent auto-creating a new session on reconnect. */
  const killedRef = useRef(false);

  useEffect(() => {
    startTerminalClaims(heldSessionIds);
  }, []);

  /** Called from the message dispatcher when the server confirms or clears a session. */
  const setSessionId = useCallback(
    (id: string | null) => {
      sessionIdRef.current = id;
      if (key) {
        if (id) {
          sessionsByChannel.set(key, id);
          // Record start time if this is a new session.
          if (!sessionStartTimes.has(key)) {
            sessionStartTimes.set(key, Date.now());
          }
        } else {
          sessionsByChannel.delete(key);
          sessionStartTimes.delete(key);
        }
        saveSessions();
      }
    },
    [key],
  );

  /** Returns the stored start timestamp for the current session, if any. */
  const getStartTime = useCallback((): number | undefined => {
    return key ? sessionStartTimes.get(key) : undefined;
  }, [key]);

  /** Mark the session as killed so reconnect doesn't auto-create. */
  const markKilled = useCallback(() => {
    killedRef.current = true;
  }, []);

  /** Sends the correct handshake message when the WebSocket opens. */
  const handleOpen = useCallback(
    (ws: WebSocket) => {
      if (sessionIdRef.current) {
        ws.send(
          JSON.stringify({
            type: "attach",
            session_id: sessionIdRef.current,
            target,
            ...(channelId ? { channel_id: channelId } : {}),
            ...(target === "agent" && instanceId ? { agent_id: instanceId } : {}),
          }),
        );
      } else if (channelId && !killedRef.current) {
        const size = getTerminalSizeRef?.current?.();
        ws.send(
          JSON.stringify({
            type: "create",
            channel_id: channelId,
            target,
            ...(target === "agent" && instanceId ? { agent_id: instanceId, leaf_id: instanceId } : {}),
            ...(claudeSessionId ? { session_id: claudeSessionId } : {}),
            ...(newSession ? { new_session: true } : {}),
            ...(openMode ? { open_mode: openMode } : {}),
            ...(rootIndex ? { root_index: rootIndex } : {}),
            ...(cmd && cmd.length > 0 ? { cmd } : {}),
            ...size,
          }),
        );
      }
    },
    [channelId, target, instanceId, claudeSessionId, newSession, openMode, rootIndex, cmd, getTerminalSizeRef],
  );

  return { sessionIdRef, killedRef, setSessionId, handleOpen, markKilled, getStartTime };
}
