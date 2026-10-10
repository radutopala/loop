import { useEffect, useRef } from "react";
import { getApiToken, getWsUrl, refreshApiToken, wsProtocols } from "../api/api";
import { checkFilesExist } from "../api/files";
import { fetchPlaygroundItems } from "../api/playground";
import type { FileLinkOpenDetail } from "../components/chat/FileLink";
import { DEFAULT_TIMEOUT_MS, runSteps } from "./executor";
import { getTerminalInput, getWorkspace, subscribeUiChanged } from "./registry";
import type { UiHost, UiStep } from "./types";

const CLIENT_ID_KEY = "loop-ui-client-id";
const STATE_DEBOUNCE_MS = 100;

/** This window's client id: kept for the window's life, so a reload keeps
 *  it, and its own in each window. */
function clientId(): string {
  try {
    const saved = sessionStorage.getItem(CLIENT_ID_KEY);
    if (saved) return saved;
    const id = crypto.randomUUID();
    sessionStorage.setItem(CLIENT_ID_KEY, id);
    return id;
  } catch {
    return crypto.randomUUID();
  }
}

interface CommandMessage {
  type: "command";
  id: string;
  steps: UiStep[];
  timeout_ms?: number;
}

/**
 * Connects this window to the daemon's UI bridge (/api/ws/ui): it reports
 * the window's focus, open channel, tabs and panes as they change, and runs
 * the commands sent to it (loop ui:run, POST /api/ui/commands) one at a time.
 */
export function useUiBridge(selectedId: string | null, selectChannel: (id: string) => Promise<void>): void {
  const selectedRef = useRef(selectedId);
  selectedRef.current = selectedId;
  const selectRef = useRef(selectChannel);
  selectRef.current = selectChannel;
  const changedRef = useRef<() => void>(() => {});

  // The open channel changing changes the state.
  useEffect(() => changedRef.current(), [selectedId]);

  useEffect(() => {
    const id = clientId();
    const host: UiHost = {
      selectedChannelId: () => selectedRef.current,
      selectChannel: (ch) => selectRef.current(ch),
      workspace: getWorkspace,
      terminal: getTerminalInput,
      openFile: async (channelId, path, line) => {
        const [found] = await checkFilesExist(channelId, [path]);
        if (!found?.exists || found.root_index === undefined || found.rel_path === undefined) throw new Error(`no file "${path}" in the channel's roots`);
        const detail: FileLinkOpenDetail = { channelId, target: { rootIndex: found.root_index, relPath: found.rel_path }, line: line ?? null };
        window.dispatchEvent(new CustomEvent<FileLinkOpenDetail>("loop:open-file", { detail }));
      },
      playgrounds: async (channelId) => (await fetchPlaygroundItems(channelId)).map((p) => ({ name: p.name, scope: p.scope })),
      now: () => Date.now(),
      sleep: (ms) => new Promise((resolve) => setTimeout(resolve, ms)),
    };

    let ws: WebSocket | null = null;
    let stopped = false;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
    let reconnectDelay = 1000;
    let stateTimer: ReturnType<typeof setTimeout> | null = null;
    let lastSent = "";
    // Commands run one after another, in the order they come.
    let queue: Promise<void> = Promise.resolve();

    const state = () => {
      const channelId = selectedRef.current;
      const view = channelId ? getWorkspace(channelId)?.view() : undefined;
      return {
        focused: document.hasFocus() && document.visibilityState === "visible",
        channel_id: channelId ?? "",
        ...(view ?? {}),
      };
    };

    const sendState = () => {
      stateTimer = null;
      if (ws?.readyState !== WebSocket.OPEN) return;
      const msg = JSON.stringify({ type: "state", state: state() });
      if (msg === lastSent) return;
      lastSent = msg;
      ws.send(msg);
    };

    const changed = () => {
      if (stateTimer) return;
      stateTimer = setTimeout(sendState, STATE_DEBOUNCE_MS);
    };
    changedRef.current = changed;

    const run = (sock: WebSocket, cmd: CommandMessage) => {
      queue = queue.then(async () => {
        let reply: object;
        try {
          const steps = Array.isArray(cmd.steps) ? cmd.steps : [];
          reply = { type: "result", id: cmd.id, results: await runSteps(steps, host, cmd.timeout_ms ?? DEFAULT_TIMEOUT_MS) };
        } catch (err) {
          reply = { type: "result", id: cmd.id, results: [], error: err instanceof Error ? err.message : String(err) };
        }
        if (sock.readyState === WebSocket.OPEN) sock.send(JSON.stringify(reply));
        changed();
      });
    };

    const connect = async () => {
      if (!getApiToken()) await refreshApiToken().catch(() => "");
      if (stopped) return;
      const sock = new WebSocket(`${getWsUrl()}/api/ws/ui`, wsProtocols());
      ws = sock;
      sock.onopen = () => {
        reconnectDelay = 1000;
        lastSent = "";
        sock.send(JSON.stringify({ type: "hello", client_id: id }));
        sendState();
      };
      sock.onmessage = (event) => {
        let msg: CommandMessage;
        try {
          msg = JSON.parse(event.data);
        } catch {
          return;
        }
        if (msg.type === "command") run(sock, msg);
      };
      sock.onerror = () => {};
      sock.onclose = () => {
        if (ws === sock) ws = null;
        if (stopped) return;
        reconnectTimer = setTimeout(() => void connect(), reconnectDelay);
        reconnectDelay = Math.min(reconnectDelay * 2, 10_000);
      };
    };
    void connect();

    const unsubscribe = subscribeUiChanged(changed);
    window.addEventListener("focus", changed);
    window.addEventListener("blur", changed);
    document.addEventListener("visibilitychange", changed);
    return () => {
      stopped = true;
      unsubscribe();
      window.removeEventListener("focus", changed);
      window.removeEventListener("blur", changed);
      document.removeEventListener("visibilitychange", changed);
      if (reconnectTimer) clearTimeout(reconnectTimer);
      if (stateTimer) clearTimeout(stateTimer);
      changedRef.current = () => {};
      ws?.close();
    };
  }, []);
}
