import { act, cleanup, renderHook } from "@testing-library/react";
import type React from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { GLOBAL_CONFIG_SAVED_EVENT } from "../api/configApi";
import { type DictationStatus, fetchDictationStatus, openDictationSocket, startDictationModelDownload } from "../api/dictation";
import { MicCapture } from "../components/chat/dictationCapture";
import { DictationController, type DictationDeps, type DictationUiState, IDLE_DICTATION } from "../components/chat/dictationController";
import { deferred } from "../test/hooks-b-fixtures";
import { useDictation } from "./useDictation";

vi.mock("../api/dictation", () => ({ fetchDictationStatus: vi.fn(), openDictationSocket: vi.fn(), startDictationModelDownload: vi.fn() }));
vi.mock("../components/chat/dictationCapture", () => ({ MicCapture: vi.fn(class {}) }));
// The controller is driven through the deps the hook hands it, so each test
// can play the controller's side directly.
vi.mock("../components/chat/dictationController", async (orig) => ({
  ...(await orig<typeof import("../components/chat/dictationController")>()),
  DictationController: vi.fn(
    class {
      hold = { busy: false, blur: vi.fn(), pointerDown: vi.fn(), pointerUp: vi.fn(), pointerCancel: vi.fn() };
      init = vi.fn(async () => {});
      dispose = vi.fn();
      keyDown = vi.fn(() => false);
      keyUp = vi.fn(() => false);
      dismiss = vi.fn();
      undoEdit = vi.fn();
    },
  ),
}));

interface FakeController {
  hold: { busy: boolean; blur: ReturnType<typeof vi.fn>; pointerDown: ReturnType<typeof vi.fn>; pointerUp: ReturnType<typeof vi.fn>; pointerCancel: ReturnType<typeof vi.fn> };
  init: ReturnType<typeof vi.fn>;
  dispose: ReturnType<typeof vi.fn>;
  keyDown: ReturnType<typeof vi.fn>;
  keyUp: ReturnType<typeof vi.fn>;
  dismiss: ReturnType<typeof vi.fn>;
  undoEdit: ReturnType<typeof vi.fn>;
}

function status(over: Partial<DictationStatus> = {}): DictationStatus {
  return {
    enabled: true,
    available: true,
    reason: "",
    model: "base",
    model_ready: true,
    model_size_bytes: 0,
    download: { active: false, received: 0, total: 0, error: "" },
    running: false,
    send_phrases: null,
    ...over,
  };
}

function controller(): { ctrl: FakeController; deps: DictationDeps } {
  const mock = vi.mocked(DictationController).mock;
  return { ctrl: mock.instances.at(-1) as unknown as FakeController, deps: mock.calls.at(-1)![0] };
}

function setup(opts: { ta?: HTMLTextAreaElement | null; send?: () => void; editPrompt?: DictationDeps["editPrompt"] } = {}) {
  const ta = opts.ta === undefined ? document.createElement("textarea") : opts.ta;
  if (ta) document.body.appendChild(ta);
  const inputRef = { current: ta };
  const setText = vi.fn();
  const hook = renderHook(({ send, edit }) => useDictation(inputRef, setText, send, edit), { initialProps: { send: opts.send, edit: opts.editPrompt } });
  return { ...hook, inputRef, setText, ta, ...controller() };
}

function key(type: "keydown" | "keyup", code = "AltRight") {
  const e = new KeyboardEvent(type, { code, key: "Alt", bubbles: true, cancelable: true });
  window.dispatchEvent(e);
  return e;
}

function pointer(button: number, capture = true) {
  return {
    button,
    pointerId: 3,
    preventDefault: vi.fn(),
    currentTarget: capture ? { setPointerCapture: vi.fn() } : {},
  } as unknown as React.PointerEvent<HTMLElement> & { preventDefault: ReturnType<typeof vi.fn> };
}

describe("useDictation", () => {
  beforeEach(() => {
    vi.mocked(fetchDictationStatus).mockResolvedValue(status());
    vi.mocked(startDictationModelDownload).mockResolvedValue();
  });
  afterEach(() => {
    cleanup();
    document.body.innerHTML = "";
    vi.useRealTimers();
    vi.mocked(fetchDictationStatus).mockReset();
    vi.mocked(startDictationModelDownload).mockReset();
    vi.mocked(DictationController).mockClear();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  describe("status", () => {
    it("reads the status on mount and inits the controller, disposing it on unmount", async () => {
      const { result, ctrl, unmount } = setup();
      await act(async () => {});
      expect(fetchDictationStatus).toHaveBeenCalled();
      expect(ctrl.init).toHaveBeenCalled();
      expect(result.current.status).toEqual(status());
      expect(result.current.availability).toBe("ready");
      expect(result.current.busy).toBe(false);
      expect(result.current.downloading).toBe(false);
      expect(result.current.canEdit).toBe(false);
      unmount();
      expect(ctrl.dispose).toHaveBeenCalled();
    });

    it("asks again every 5s until the daemon answers", async () => {
      vi.useFakeTimers();
      vi.mocked(fetchDictationStatus).mockRejectedValueOnce(new Error("starting")).mockRejectedValueOnce(new Error("starting"));
      const { result } = setup();
      await act(async () => {});
      expect(result.current.status).toBeNull();
      expect(result.current.availability).toBe("unknown");
      await act(async () => vi.advanceTimersByTimeAsync(5000));
      expect(result.current.status).toBeNull();
      await act(async () => vi.advanceTimersByTimeAsync(5000));
      expect(result.current.status).toEqual(status());
      const calls = vi.mocked(fetchDictationStatus).mock.calls.length;
      await act(async () => vi.advanceTimersByTimeAsync(20000));
      expect(fetchDictationStatus).toHaveBeenCalledTimes(calls);
    });

    it.each([GLOBAL_CONFIG_SAVED_EVENT, "focus"])("reads the status again on %s", async (event) => {
      setup();
      await act(async () => {});
      vi.mocked(fetchDictationStatus).mockResolvedValue(status({ enabled: false }));
      await act(async () => {
        window.dispatchEvent(new Event(event));
      });
      expect(fetchDictationStatus).toHaveBeenCalledTimes(2);
    });

    it("follows a download started elsewhere", async () => {
      vi.useFakeTimers();
      vi.mocked(fetchDictationStatus)
        .mockResolvedValueOnce(status({ model_ready: false, download: { active: true, received: 1, total: 10, error: "" } }))
        .mockResolvedValueOnce(status());
      const { result } = setup();
      await act(async () => {});
      expect(result.current.downloading).toBe(true);
      expect(result.current.availability).toBe("downloading");
      await act(async () => vi.advanceTimersByTimeAsync(500));
      expect(result.current.downloading).toBe(false);
      expect(result.current.availability).toBe("ready");
    });

    it("ignores a status that lands after unmount", async () => {
      const late = deferred<DictationStatus>();
      vi.mocked(fetchDictationStatus).mockReturnValue(late.promise);
      const { result, unmount } = setup();
      unmount();
      await act(async () => late.resolve(status()));
      expect(result.current.status).toBeNull();
    });
  });

  describe("controller deps", () => {
    it("reads the textarea's text and selection, or empty without one", async () => {
      const { ta, deps } = setup();
      ta!.value = "hello";
      ta!.setSelectionRange(1, 3);
      expect(deps.read()).toEqual({ text: "hello", start: 1, end: 3 });

      cleanup();
      const none = setup({ ta: null });
      expect(none.deps.read()).toEqual({ text: "", start: 0, end: 0 });
    });

    it("reads the text's end when the selection is unknown", () => {
      const inputRef = { current: { value: "abc", selectionStart: null, selectionEnd: null } as unknown as HTMLTextAreaElement };
      renderHook(() => useDictation(inputRef, vi.fn()));
      expect(controller().deps.read()).toEqual({ text: "abc", start: 3, end: 3 });
    });

    it("writes through setText, then focuses and selects a frame later", () => {
      vi.useFakeTimers();
      const { ta, deps, setText, inputRef } = setup();
      const focus = vi.spyOn(ta!, "focus");
      const select = vi.spyOn(ta!, "setSelectionRange");
      deps.write("new text", 2, 4);
      expect(setText).toHaveBeenCalledWith("new text");
      act(() => vi.advanceTimersToNextFrame());
      expect(focus).toHaveBeenCalled();
      expect(select).toHaveBeenCalledWith(2, 4);

      // Gone by the next frame: nothing to focus.
      deps.write("again", 0, 0);
      inputRef.current = null;
      act(() => vi.advanceTimersToNextFrame());
      expect(focus).toHaveBeenCalledTimes(1);
    });

    it("opens the stream and the mic capture", () => {
      const ws = {} as WebSocket;
      vi.mocked(openDictationSocket).mockReturnValue(ws);
      const { deps } = setup();
      expect(deps.openSocket()).toBe(ws);
      expect(openDictationSocket).toHaveBeenCalledWith();
      expect(deps.createCapture()).toBeInstanceOf(MicCapture);
    });

    it("grants the mic in a plain tab and asks the app bridge otherwise", async () => {
      const { deps } = setup();
      expect(await deps.micAccess(true)).toBe("granted");
      const microphoneAccess = vi.fn().mockResolvedValue("denied");
      vi.stubGlobal("loopAPI", { microphoneAccess });
      expect(await deps.micAccess(true)).toBe("denied");
      expect(microphoneAccess).toHaveBeenCalledWith(true);
    });

    it("tells availability, send phrases and refreshes from the latest status", async () => {
      const { deps } = setup();
      expect(deps.availability()).toBe("unknown");
      expect(deps.sendPhrases()).toEqual([]);
      await act(async () => {});
      expect(deps.availability()).toBe("ready");
      expect(deps.sendPhrases()).toEqual([]);

      vi.mocked(fetchDictationStatus).mockResolvedValue(status({ send_phrases: ["send it"] }));
      await act(async () => deps.refresh());
      expect(deps.sendPhrases()).toEqual(["send it"]);
    });

    it("sends through the latest send, and does nothing without one", () => {
      const first = vi.fn();
      const later = vi.fn();
      const { deps, rerender } = setup({ send: first });
      rerender({ send: later, edit: undefined });
      deps.send();
      expect(later).toHaveBeenCalled();
      expect(first).not.toHaveBeenCalled();
      rerender({ send: undefined, edit: undefined });
      expect(() => deps.send()).not.toThrow();
    });

    it("offers edit holds only while the composer passes editPrompt", () => {
      const edit = vi.fn();
      const { deps, rerender, result } = setup({ editPrompt: edit });
      expect(deps.editPrompt).toBe(edit);
      expect(result.current.canEdit).toBe(true);
      rerender({ send: undefined, edit: undefined });
      expect(deps.editPrompt).toBeUndefined();
      expect(result.current.canEdit).toBe(false);
    });

    it("shows the controller's state while mounted", () => {
      const { deps, result, unmount } = setup();
      const recording: DictationUiState = { ...IDLE_DICTATION, phase: "recording" };
      act(() => deps.onState(recording));
      expect(result.current.ui).toEqual(recording);
      expect(result.current.busy).toBe(true);
      unmount();
      expect(() => deps.onState(IDLE_DICTATION)).not.toThrow();
    });
  });

  describe("keys", () => {
    it("passes keys to the controller only while the composer has focus or a hold runs", () => {
      const { ta, ctrl } = setup();
      key("keydown");
      expect(ctrl.keyDown).not.toHaveBeenCalled();

      ta!.focus();
      const ignored = key("keydown");
      expect(ctrl.keyDown).toHaveBeenCalledTimes(1);
      expect(ignored.defaultPrevented).toBe(false);

      ctrl.keyDown.mockReturnValue(true);
      ta!.blur();
      ctrl.hold.busy = true;
      const taken = key("keydown");
      expect(taken.defaultPrevented).toBe(true);
    });

    it("passes key releases only while a hold runs", () => {
      const { ctrl } = setup();
      key("keyup");
      expect(ctrl.keyUp).not.toHaveBeenCalled();

      ctrl.hold.busy = true;
      expect(key("keyup").defaultPrevented).toBe(false);
      ctrl.keyUp.mockReturnValue(true);
      expect(key("keyup").defaultPrevented).toBe(true);
      expect(ctrl.keyUp).toHaveBeenCalledTimes(2);
    });

    it("lets the hold know when the window loses focus", () => {
      const { ctrl, unmount } = setup();
      window.dispatchEvent(new Event("blur"));
      expect(ctrl.hold.blur).toHaveBeenCalledTimes(1);
      unmount();
      window.dispatchEvent(new Event("blur"));
      expect(ctrl.hold.blur).toHaveBeenCalledTimes(1);
    });
  });

  describe("notices", () => {
    it("clears an error after 5s", () => {
      vi.useFakeTimers();
      const { deps, ctrl } = setup();
      act(() => deps.onState({ ...IDLE_DICTATION, error: "mic failed" }));
      act(() => vi.advanceTimersByTime(4999));
      expect(ctrl.dismiss).not.toHaveBeenCalled();
      act(() => vi.advanceTimersByTime(1));
      expect(ctrl.dismiss).toHaveBeenCalledTimes(1);
    });

    it("clears the model notice once the model is there", async () => {
      vi.mocked(fetchDictationStatus).mockResolvedValue(status({ model_ready: false }));
      const { deps, ctrl } = setup();
      await act(async () => {});
      act(() => deps.onState({ ...IDLE_DICTATION, notice: "needs_model" }));
      expect(ctrl.dismiss).not.toHaveBeenCalled();
      vi.mocked(fetchDictationStatus).mockResolvedValue(status());
      await act(async () => deps.refresh());
      expect(ctrl.dismiss).toHaveBeenCalledTimes(1);
    });

    it("dismiss clears the download error and the controller's notice", async () => {
      vi.mocked(startDictationModelDownload).mockRejectedValue(new Error("disk full"));
      const { result, ctrl } = setup();
      await act(async () => result.current.startDownload());
      expect(result.current.downloadError).toBe("disk full");
      act(() => result.current.dismiss());
      expect(result.current.downloadError).toBeNull();
      expect(ctrl.dismiss).toHaveBeenCalled();
    });
  });

  describe("startDownload", () => {
    it("downloads the model, showing progress until it's ready", async () => {
      vi.useFakeTimers();
      vi.mocked(fetchDictationStatus)
        .mockResolvedValueOnce(status({ model_ready: false }))
        .mockResolvedValueOnce(status({ model_ready: false, download: { active: true, received: 5, total: 10, error: "" } }))
        .mockResolvedValueOnce(status());
      const { result } = setup();
      await act(async () => {});
      let done!: Promise<void>;
      act(() => {
        done = result.current.startDownload();
      });
      expect(result.current.downloading).toBe(true);
      await act(async () => vi.advanceTimersByTimeAsync(0));
      expect(startDictationModelDownload).toHaveBeenCalled();
      expect(result.current.status?.download.received).toBe(5);
      await act(async () => {
        await vi.advanceTimersByTimeAsync(500);
        await done;
      });
      expect(result.current.downloading).toBe(false);
      expect(result.current.availability).toBe("ready");
      expect(result.current.downloadError).toBeNull();
    });

    it("keeps a non-Error failure as text", async () => {
      vi.mocked(startDictationModelDownload).mockRejectedValue("nope");
      const { result } = setup();
      await act(async () => result.current.startDownload());
      expect(result.current.downloadError).toBe("nope");
      expect(result.current.downloading).toBe(false);
    });

    it.each([
      ["succeeds", () => Promise.resolve()],
      ["fails", () => Promise.reject(new Error("x"))],
    ])("stops quietly when unmounted while the download start %s", async (_name, start) => {
      const started = deferred<void>();
      vi.mocked(startDictationModelDownload).mockReturnValue(started.promise);
      const { result, unmount } = setup();
      await act(async () => {});
      const calls = vi.mocked(fetchDictationStatus).mock.calls.length;
      const done = result.current.startDownload();
      unmount();
      start().then(started.resolve, started.reject);
      await act(async () => done);
      expect(fetchDictationStatus).toHaveBeenCalledTimes(calls);
      expect(result.current.downloadError).toBeNull();
    });

    it("drops progress that lands after unmount", async () => {
      const poll = deferred<DictationStatus>();
      const { result, unmount } = setup();
      await act(async () => {});
      vi.mocked(fetchDictationStatus).mockReturnValueOnce(poll.promise);
      let done!: Promise<void>;
      act(() => {
        done = result.current.startDownload();
      });
      await act(async () => {});
      unmount();
      await act(async () => {
        poll.resolve(status({ model_ready: false, model: "late" }));
        await done;
      });
      expect(result.current.status?.model).toBe("base");
    });
  });

  describe("hold buttons", () => {
    it.each([
      ["micButton", "dictate"],
      ["editButton", "edit"],
    ] as const)("%s holds in %s mode on a primary press", (button, mode) => {
      const { result, ctrl } = setup();
      const handlers = result.current[button];
      const secondary = pointer(2);
      handlers.onPointerDown(secondary);
      expect(secondary.preventDefault).not.toHaveBeenCalled();
      expect(ctrl.hold.pointerDown).not.toHaveBeenCalled();

      const primary = pointer(0);
      handlers.onPointerDown(primary);
      expect(primary.preventDefault).toHaveBeenCalled();
      expect((primary.currentTarget as unknown as { setPointerCapture: ReturnType<typeof vi.fn> }).setPointerCapture).toHaveBeenCalledWith(3);
      expect(ctrl.hold.pointerDown).toHaveBeenCalledWith(mode);

      handlers.onPointerDown(pointer(0, false));
      expect(ctrl.hold.pointerDown).toHaveBeenCalledTimes(2);

      handlers.onPointerUp();
      handlers.onPointerCancel();
      expect(ctrl.hold.pointerUp).toHaveBeenCalled();
      expect(ctrl.hold.pointerCancel).toHaveBeenCalled();

      const mouse = { preventDefault: vi.fn() } as unknown as React.MouseEvent;
      handlers.onMouseDown(mouse);
      expect(mouse.preventDefault).toHaveBeenCalled();
    });

    it("undoes the last edit through the controller", () => {
      const { result, ctrl } = setup();
      result.current.undoEdit();
      expect(ctrl.undoEdit).toHaveBeenCalled();
    });
  });
});
