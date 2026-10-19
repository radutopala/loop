import { createContext, useCallback, useEffect, useMemo, useState } from "react";
import { type ComposerSwitch, type ComposerSwitchState, fetchComposerSwitches, turnComposerSwitch } from "../api/composerSwitches";
import { PLUGINS_CHANGED } from "../api/plugins";
import { switchKey, withSwitchState } from "../components/chat/composerSwitches";
import { useNotificationsState } from "../notifications/useNotifications";
import type { WSEvent } from "../types";
import type { ChatEventListener } from "./useChatStateStore";
import { fetchWithRetry } from "./useLearn";

export interface ComposerSwitchesView {
  /** The chat's switches, in plugin order. */
  switches: ComposerSwitch[];
  /** Switches whose click the plugin hasn't answered yet, by switchKey. */
  pending: ReadonlySet<string>;
  /** The error of a switch's last click, by switchKey, until its next. */
  errors: ReadonlyMap<string, string>;
  /** Asks the switch's plugin to turn it over. */
  turn: (sw: ComposerSwitch) => Promise<void>;
}

/** The selected chat's ComposerSwitchesView, for the composer's pills. */
export const ComposerSwitchesContext = createContext<ComposerSwitchesView | null>(null);

/**
 * Follows the composer switches plugins put in a chat: listed again when
 * the plugins change or the WS reconnects (wsOpens), and updated by the
 * plugin.composer_switch events the plugins' changes send. A click shows
 * the switch turned while its plugin answers, and turns it back if that
 * fails.
 */
export function useComposerSwitches(channelId: string, subscribeChatEvents?: (listener: ChatEventListener) => () => void, wsOpens = 0): ComposerSwitchesView {
  const [switches, setSwitches] = useState<ComposerSwitch[]>([]);
  const [pending, setPending] = useState<ReadonlySet<string>>(new Set());
  const [errors, setErrors] = useState<ReadonlyMap<string, string>>(new Map());
  const { pluginsEpoch } = useNotificationsState();
  const [localEpoch, setLocalEpoch] = useState(0);

  useEffect(() => {
    const bump = () => setLocalEpoch((n) => n + 1);
    window.addEventListener(PLUGINS_CHANGED, bump);
    return () => window.removeEventListener(PLUGINS_CHANGED, bump);
  }, []);

  useEffect(() => {
    setSwitches([]);
    setPending(new Set());
    setErrors(new Map());
  }, [channelId]);

  useEffect(
    () => fetchWithRetry(() => fetchComposerSwitches(channelId), "listing composer switches", setSwitches),
    // pluginsEpoch and localEpoch list them again when the plugins change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [channelId, wsOpens, pluginsEpoch, localEpoch],
  );

  useEffect(() => {
    if (!subscribeChatEvents) return;
    return subscribeChatEvents((event: WSEvent) => {
      if (event.channel_id !== channelId || event.type !== "plugin.composer_switch") return;
      const sw = event.data as ComposerSwitch;
      setSwitches((cur) => withSwitchState(cur, switchKey(sw), sw));
    });
  }, [channelId, subscribeChatEvents]);

  const turn = useCallback(
    async (sw: ComposerSwitch) => {
      const key = switchKey(sw);
      const before: ComposerSwitchState = { on: sw.on, status: sw.status, title: sw.title };
      setPending((cur) => new Set(cur).add(key));
      setErrors((cur) => withoutKey(cur, key));
      setSwitches((cur) => withSwitchState(cur, key, { ...before, on: !sw.on, status: "" }));
      try {
        const state = await turnComposerSwitch(channelId, sw.plugin_id, sw.switch_id, !sw.on);
        setSwitches((cur) => withSwitchState(cur, key, state));
      } catch (e) {
        setSwitches((cur) => withSwitchState(cur, key, before));
        setErrors((cur) => new Map(cur).set(key, e instanceof Error ? e.message : String(e)));
      } finally {
        setPending((cur) => {
          const next = new Set(cur);
          next.delete(key);
          return next;
        });
      }
    },
    [channelId],
  );

  return useMemo(() => ({ switches, pending, errors, turn }), [switches, pending, errors, turn]);
}

function withoutKey<V>(m: ReadonlyMap<string, V>, key: string): ReadonlyMap<string, V> {
  if (!m.has(key)) return m;
  const next = new Map(m);
  next.delete(key);
  return next;
}
