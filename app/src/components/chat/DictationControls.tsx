import type { useDictation } from "../../hooks/useDictation";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { downloadProgressText, editButtonTip, micButtonTip, modelSizeLabel } from "./dictationModel";
import { HoverTip } from "./HoverTip";

type Dictation = ReturnType<typeof useDictation>;

const isMac = typeof navigator !== "undefined" && navigator.platform.includes("Mac");

/** The corner tab's buttons and its padding. */
const CORNER_BUTTON = 24;
const CORNER_PAD = 3;
/** The composer's own padding on the right and top (see ChatInput's inputWrapper). */
const COMPOSER_PAD_RIGHT = 14;

function cornerButtons(d: Dictation): number {
  if (d.availability === "unknown" || d.availability === "disabled") return 0;
  return d.canEdit ? 2 : 1;
}

/**
 * How far text at the top of the composer must keep from its right edge, past
 * the composer's own padding, to clear the corner tab: 0 without the tab.
 */
export function dictationCornerReserve(d: Dictation): number {
  const n = cornerButtons(d);
  if (n === 0) return 0;
  const width = n * CORNER_BUTTON + (n - 1) + 2 * CORNER_PAD + 1;
  return Math.max(0, width - COMPOSER_PAD_RIGHT + 6);
}

/**
 * The mic and voice edit buttons in a tab tucked into the composer's top
 * right corner: it shares the composer's outer corner radius and border, so
 * it reads as part of the frame. The composer must be position: relative,
 * with a 16px radius.
 */
export function DictationCorner({ d }: { d: Dictation }) {
  const { colors } = useTheme();
  const n = cornerButtons(d);
  if (n === 0) return null;
  return (
    <div
      data-testid="dictation-corner"
      style={{
        position: "absolute",
        top: 0,
        right: 0,
        zIndex: 1,
        display: "flex",
        alignItems: "center",
        gap: 0,
        padding: CORNER_PAD,
        background: colors.bg,
        borderLeft: `1px solid ${colors.border}`,
        borderBottom: `1px solid ${colors.border}`,
        // The outer corner follows the composer's (16px less its border).
        borderRadius: "0 15px 0 12px",
      }}
    >
      <DictationMicButton d={d} />
      {n === 2 && <span style={{ width: 1, height: 14, background: colors.border, flexShrink: 0 }} />}
      <PromptEditButton d={d} />
    </div>
  );
}

const DICTATION_KEYFRAMES = "@keyframes loop-dictation-pulse { 50% { opacity: 0.35; } }";

export function MicIcon({ size }: { size: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <rect x="9" y="2" width="6" height="12" rx="3" />
      <path d="M5 10v1a7 7 0 0 0 14 0v-1" />
      <path d="M12 18v4" />
    </svg>
  );
}

/**
 * The composer's mic switch: click to start and again to stop, or press
 * and hold to talk. Red and pulsing
 * while recording; dim and inert when the transcriber isn't installed (the
 * tip says why). Hidden while the daemon's dictation status is unknown, and
 * when dictation is turned off.
 */
export function DictationMicButton({ d }: { d: Dictation }) {
  const { colors } = useTheme();
  if (d.availability === "unknown" || d.availability === "disabled") return null;
  const disabled = d.availability === "unavailable";
  const recording = d.ui.phase === "recording";
  const c = recording ? colors.error : d.ui.phase === "transcribing" ? colors.active : colors.textDim;
  return (
    <HoverTip text={micButtonTip(d.availability, d.status, isMac)}>
      <style>{DICTATION_KEYFRAMES}</style>
      <button
        data-testid="dictation-mic"
        aria-label="Dictate"
        aria-pressed={recording}
        aria-disabled={disabled}
        data-phase={d.ui.phase}
        {...(disabled ? {} : d.micButton)}
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          width: CORNER_BUTTON,
          height: CORNER_BUTTON,
          padding: 0,
          flexShrink: 0,
          background: recording || d.ui.phase === "transcribing" ? colors.border : "transparent",
          border: "none",
          borderRadius: 8,
          color: c,
          opacity: disabled ? 0.4 : 1,
          cursor: disabled ? "default" : "pointer",
          touchAction: "none",
          animation: recording ? "loop-dictation-pulse 1s ease-in-out infinite" : undefined,
        }}
      >
        <MicIcon size={13} />
      </button>
    </HoverTip>
  );
}

export function EditVoiceIcon({ size }: { size: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M12 20h9" />
      <path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z" />
    </svg>
  );
}

/**
 * The composer's voice edit switch, next to the mic: like the mic, but the
 * speech is an instruction that rewrites the draft. Shown when the composer
 * can edit and dictation is on; dim and inert when the transcriber isn't
 * installed.
 */
export function PromptEditButton({ d }: { d: Dictation }) {
  const { colors } = useTheme();
  if (!d.canEdit || d.availability === "unknown" || d.availability === "disabled") return null;
  const disabled = d.availability === "unavailable";
  const editHold = d.ui.mode === "edit" && d.ui.phase !== "idle";
  const recording = editHold && d.ui.phase === "recording";
  const c = recording ? colors.error : editHold ? colors.active : colors.textDim;
  return (
    <HoverTip text={editButtonTip(d.availability, d.status, isMac)}>
      <style>{DICTATION_KEYFRAMES}</style>
      <button
        data-testid="prompt-edit-voice"
        aria-label="Edit by voice"
        aria-pressed={recording}
        aria-disabled={disabled || (d.busy && !editHold)}
        data-phase={editHold ? d.ui.phase : "idle"}
        {...(disabled ? {} : d.editButton)}
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          width: CORNER_BUTTON,
          height: CORNER_BUTTON,
          padding: 0,
          flexShrink: 0,
          background: editHold ? colors.border : "transparent",
          border: "none",
          borderRadius: 8,
          color: c,
          opacity: disabled ? 0.4 : 1,
          cursor: disabled ? "default" : "pointer",
          touchAction: "none",
          animation: recording || d.ui.phase === "editing" ? "loop-dictation-pulse 1s ease-in-out infinite" : undefined,
        }}
      >
        <EditVoiceIcon size={13} />
      </button>
    </HoverTip>
  );
}

/** Level meter: a short bar filled to the input level. */
function LevelMeter({ level, color, track }: { level: number; color: string; track: string }) {
  // Speech RMS sits well below full scale; stretch it so the bar moves.
  const pct = Math.min(100, Math.round(Math.sqrt(level) * 160));
  return (
    <span data-testid="dictation-level" style={{ display: "inline-block", width: 48, height: 4, borderRadius: 2, background: track, overflow: "hidden", flexShrink: 0 }}>
      <span style={{ display: "block", width: `${pct}%`, height: "100%", background: color, transition: "width 80ms linear" }} />
    </span>
  );
}

/**
 * The line above the composer while dictating (listening with a level
 * meter, then transcribing; an edit hold shows its instruction as it's
 * heard, then while it's applied, then Undo) or after it asked for something: the one-time
 * model download and its progress, the microphone being off, the
 * transcriber missing, or a brief error.
 */
export function DictationBanner({ d }: { d: Dictation }) {
  const { colors } = useTheme();
  const { ui } = d;
  const base: React.CSSProperties = {
    display: "flex",
    alignItems: "center",
    gap: 8,
    width: "100%",
    padding: `4px ${dictationCornerReserve(d)}px 8px 0`,
    boxSizing: "border-box",
    borderBottom: `1px solid ${colors.border}`,
    marginBottom: 8,
    fontSize: 12,
    color: colors.textMuted,
    fontFamily: fonts.sans,
  };
  const button: React.CSSProperties = {
    background: "none",
    border: "none",
    color: colors.textLight,
    cursor: "pointer",
    padding: "0 2px",
    fontSize: 12,
    flexShrink: 0,
    fontFamily: fonts.sans,
  };
  const close = (
    <button onClick={d.dismiss} style={{ ...button, color: colors.textDim, fontSize: 14, lineHeight: 1 }} title="Dismiss">
      &times;
    </button>
  );

  if (ui.mode === "edit" && (ui.phase === "recording" || ui.phase === "transcribing")) {
    return (
      <div data-testid="dictation-banner" data-state={`edit-${ui.phase}`} style={base}>
        <span
          style={{
            width: 8,
            height: 8,
            borderRadius: 4,
            background: ui.phase === "recording" ? colors.error : colors.active,
            flexShrink: 0,
            animation: "loop-dictation-pulse 1s ease-in-out infinite",
          }}
        />
        <span style={{ color: colors.text, flexShrink: 0 }}>Edit:</span>
        <span
          data-testid="prompt-edit-instruction"
          style={{
            flex: 1,
            minWidth: 0,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
            color: ui.instruction ? colors.text : colors.textDim,
            fontStyle: ui.instruction ? undefined : "italic",
          }}
        >
          {ui.instruction || "say how to change the draft…"}
        </span>
        {ui.phase === "recording" && <LevelMeter level={ui.level} color={colors.active} track={colors.border} />}
        <span style={{ color: colors.textDim, flexShrink: 0 }}>{ui.phase === "recording" ? "Release to apply · Esc to cancel" : "Transcribing…"}</span>
      </div>
    );
  }
  if (ui.phase === "editing") {
    return (
      <div data-testid="dictation-banner" data-state="editing" style={base}>
        <span style={{ width: 8, height: 8, borderRadius: 4, background: colors.active, flexShrink: 0, animation: "loop-dictation-pulse 1s ease-in-out infinite" }} />
        <span style={{ color: colors.text, flexShrink: 0 }}>Editing:</span>
        <span data-testid="prompt-edit-instruction" style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {ui.instruction}
        </span>
        <span style={{ color: colors.textDim, flexShrink: 0 }}>Esc to cancel</span>
      </div>
    );
  }
  if (ui.phase === "recording") {
    return (
      <div data-testid="dictation-banner" data-state="recording" style={base}>
        <span style={{ width: 8, height: 8, borderRadius: 4, background: colors.error, flexShrink: 0, animation: "loop-dictation-pulse 1s ease-in-out infinite" }} />
        <span style={{ color: colors.text }}>Listening…</span>
        <LevelMeter level={ui.level} color={colors.active} track={colors.border} />
        <span style={{ flex: 1, color: colors.textDim }}>Release to finish · Esc to cancel</span>
      </div>
    );
  }
  if (ui.phase === "transcribing") {
    return (
      <div data-testid="dictation-banner" data-state="transcribing" style={base}>
        <span style={{ flex: 1 }}>Transcribing…</span>
      </div>
    );
  }
  if (ui.notice === "needs_model" && d.status && !d.status.model_ready) {
    const err = d.downloadError || d.status.download.error;
    return (
      <div data-testid="dictation-banner" data-state="needs-model" style={base}>
        {d.downloading ? (
          <span style={{ flex: 1 }}>Dictation model: {downloadProgressText(d.status)}</span>
        ) : (
          <>
            <span style={{ flex: 1, color: err ? colors.error : undefined }}>{err ? `Model download failed: ${err}` : `Dictation needs a one-time ${modelSizeLabel(d.status)} model download`}</span>
            <button data-testid="dictation-download" onClick={() => void d.startDownload()} style={button}>
              {err ? "Retry" : "Download"}
            </button>
          </>
        )}
        {close}
      </div>
    );
  }
  if (ui.notice === "unavailable") {
    return (
      <div data-testid="dictation-banner" data-state="unavailable" style={base}>
        <span style={{ flex: 1 }}>Dictation is unavailable: the transcriber (whisper-server) isn't installed next to Loop or on PATH.</span>
        {close}
      </div>
    );
  }
  if (ui.notice === "mic_denied") {
    const hint = window.loopAPI && isMac ? "Turn on Loop in System Settings → Privacy & Security → Microphone, then try again." : "Allow microphone access for this app, then try again.";
    return (
      <div data-testid="dictation-banner" data-state="mic-denied" style={base}>
        <span style={{ flex: 1, color: colors.warning }}>Microphone access is off. {hint}</span>
        {close}
      </div>
    );
  }
  if (ui.undo && !ui.error) {
    return (
      <div data-testid="dictation-banner" data-state="edited" style={base}>
        <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>Edited: {ui.instruction}</span>
        <button data-testid="prompt-edit-undo" onClick={d.undoEdit} style={button}>
          Undo
        </button>
      </div>
    );
  }
  if (ui.error) {
    return (
      <div data-testid="dictation-banner" data-state="error" style={base}>
        <span style={{ flex: 1, color: colors.error }}>{ui.error}</span>
        {close}
      </div>
    );
  }
  return null;
}
