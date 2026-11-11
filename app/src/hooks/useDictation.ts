import { type RefObject, useCallback, useEffect, useRef, useState } from "react";
import { GLOBAL_CONFIG_SAVED_EVENT } from "../api/configApi";
import { type DictationStatus, fetchDictationStatus, openDictationSocket, startDictationModelDownload } from "../api/dictation";
import { MicCapture } from "../components/chat/dictationCapture";
import { DictationController, type DictationUiState, IDLE_DICTATION, type MicAccess } from "../components/chat/dictationController";
import { dictationAvailability, downloadDictationModel, MODEL_POLL_MS } from "../components/chat/dictationModel";

const ERROR_SHOW_MS = 5000;
const STATUS_RETRY_MS = 5000;

async function micAccess(ask: boolean): Promise<MicAccess> {
  // A plain browser tab asks through getUserMedia itself.
  const api = window.loopAPI?.microphoneAccess;
  if (!api) return "granted";
  return (await api(ask)) as MicAccess;
}

/**
 * Push-to-talk dictation for one composer: the daemon's status and model
 * download, and a DictationController writing into the textarea through
 * setText, and send for a dictation that ends with a send phrase. The hold key is watched on the window in the capture phase so its
 * release, Esc and other keys are seen wherever focus goes; a hold only
 * begins while this composer's textarea has focus.
 */
export function useDictation(inputRef: RefObject<HTMLTextAreaElement | null>, setText: (text: string) => void, send?: () => void) {
  const [status, setStatus] = useState<DictationStatus | null>(null);
  const [ui, setUi] = useState<DictationUiState>(IDLE_DICTATION);
  const [downloading, setDownloading] = useState(false);
  const [downloadError, setDownloadError] = useState<string | null>(null);
  const statusRef = useRef(status);
  statusRef.current = status;
  const setTextRef = useRef(setText);
  setTextRef.current = setText;
  const sendRef = useRef(send);
  sendRef.current = send;
  const mounted = useRef(true);

  const ctrlRef = useRef<DictationController | null>(null);
  if (!ctrlRef.current) {
    ctrlRef.current = new DictationController({
      read: () => {
        const ta = inputRef.current;
        const text = ta?.value ?? "";
        return { text, start: ta?.selectionStart ?? text.length, end: ta?.selectionEnd ?? text.length };
      },
      write: (text, start, end) => {
        setTextRef.current(text);
        requestAnimationFrame(() => {
          const ta = inputRef.current;
          if (!ta) return;
          ta.focus();
          ta.setSelectionRange(start, end);
        });
      },
      openSocket: () => openDictationSocket(),
      createCapture: () => new MicCapture(),
      micAccess,
      availability: () => dictationAvailability(statusRef.current),
      refresh: async () => {
        await refreshRef.current();
      },
      sendPhrases: () => statusRef.current?.send_phrases ?? [],
      send: () => sendRef.current?.(),
      onState: (s) => {
        if (mounted.current) setUi(s);
      },
    });
  }
  const ctrl = ctrlRef.current;

  const refresh = useCallback(async () => {
    try {
      const s = await fetchDictationStatus();
      if (mounted.current) setStatus(s);
      return s;
    } catch {
      return null;
    }
  }, []);
  const refreshRef = useRef(refresh);
  refreshRef.current = refresh;

  // Until the daemon answers (it may be starting or restarting), ask again.
  useEffect(() => {
    if (status) return;
    const t = setInterval(() => void refresh(), STATUS_RETRY_MS);
    return () => clearInterval(t);
  }, [status, refresh]);

  // dictation.enabled has no daemon event: read the status again after a
  // settings save (turning it on starts the model download) and when the
  // window comes back.
  useEffect(() => {
    const again = () => void refresh();
    window.addEventListener(GLOBAL_CONFIG_SAVED_EVENT, again);
    window.addEventListener("focus", again);
    return () => {
      window.removeEventListener(GLOBAL_CONFIG_SAVED_EVENT, again);
      window.removeEventListener("focus", again);
    };
  }, [refresh]);

  useEffect(() => {
    mounted.current = true;
    void refresh();
    void ctrl.init();
    return () => {
      mounted.current = false;
      ctrl.dispose();
    };
  }, [ctrl, refresh]);

  useEffect(() => {
    const takes = () => ctrl.hold.busy || document.activeElement === inputRef.current;
    const down = (e: KeyboardEvent) => {
      if (!takes()) return;
      if (ctrl.keyDown(e)) {
        e.preventDefault();
        e.stopPropagation();
      }
    };
    const up = (e: KeyboardEvent) => {
      if (!ctrl.hold.busy) return;
      if (ctrl.keyUp(e)) {
        e.preventDefault();
        e.stopPropagation();
      }
    };
    const blur = () => ctrl.hold.blur();
    window.addEventListener("keydown", down, true);
    window.addEventListener("keyup", up, true);
    window.addEventListener("blur", blur);
    return () => {
      window.removeEventListener("keydown", down, true);
      window.removeEventListener("keyup", up, true);
      window.removeEventListener("blur", blur);
    };
  }, [ctrl, inputRef]);

  // A download started elsewhere (another composer or window) still shows
  // its progress here.
  useEffect(() => {
    if (downloading || !status?.download.active) return;
    const t = setTimeout(() => void refresh(), MODEL_POLL_MS);
    return () => clearTimeout(t);
  }, [downloading, status, refresh]);

  // An error shows briefly, then clears.
  useEffect(() => {
    if (!ui.error) return;
    const t = setTimeout(() => ctrl.dismiss(), ERROR_SHOW_MS);
    return () => clearTimeout(t);
  }, [ui.error, ctrl]);

  // A notice about the model clears itself once the model is there.
  useEffect(() => {
    if (ui.notice === "needs_model" && status?.model_ready) ctrl.dismiss();
  }, [ui.notice, status?.model_ready, ctrl]);

  const startDownload = useCallback(async () => {
    setDownloading(true);
    setDownloadError(null);
    try {
      await downloadDictationModel({
        start: startDictationModelDownload,
        status: fetchDictationStatus,
        onProgress: (s) => mounted.current && setStatus(s),
        sleep: (ms) => new Promise((r) => setTimeout(r, ms)),
        cancelled: () => !mounted.current,
      });
    } catch (err) {
      if (mounted.current) setDownloadError(err instanceof Error ? err.message : String(err));
    } finally {
      if (mounted.current) setDownloading(false);
    }
  }, []);

  /** Pointer handlers for the mic button: click to toggle, or press and hold to talk. */
  const micButton = {
    onPointerDown: (e: React.PointerEvent<HTMLElement>) => {
      if (e.button !== 0) return;
      // Keep focus (and the caret) in the composer.
      e.preventDefault();
      e.currentTarget.setPointerCapture?.(e.pointerId);
      ctrl.hold.pointerDown();
    },
    onPointerUp: () => ctrl.hold.pointerUp(),
    onPointerCancel: () => ctrl.hold.pointerCancel(),
    onMouseDown: (e: React.MouseEvent) => e.preventDefault(),
  };

  return {
    status,
    availability: dictationAvailability(status),
    ui,
    busy: ui.phase !== "idle",
    downloading: downloading || !!status?.download.active,
    downloadError,
    startDownload,
    dismiss: () => {
      setDownloadError(null);
      ctrl.dismiss();
    },
    micButton,
  };
}
