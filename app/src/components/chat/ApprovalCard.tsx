import { useEffect, useRef, useState } from "react";
import { GateApprovalGoneError, type GateDecision } from "../../api/gate";
import { resolveGateApproval, sendCommand, sendMessage } from "../../api/loopApi";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import type { GateApprovalRequestedData } from "../../types";
import { ContextMenu, type MenuItem } from "../shared/ContextMenu";

export function ApprovalCard({
  data,
  channelId,
  onResolved,
  onDenyWithPrompt,
  style,
}: {
  data: GateApprovalRequestedData;
  channelId: string;
  onResolved?: () => void;
  /** When provided, "Deny with prompt" routes the typed text here instead of
   * posting it as a chat message. Terminal panes use this to inject the prompt
   * into the originating pane's stdin so the in-pane agent (e.g. Claude Code
   * TUI) receives it, rather than the chat agent. */
  onDenyWithPrompt?: (text: string) => void | Promise<void>;
  style?: React.CSSProperties;
}) {
  const { colors } = useTheme();
  const cardRef = useRef<HTMLDivElement>(null);
  // Bring the whole card (including the Allow/Deny buttons) into view once it has
  // rendered at full height. The chat's own auto-scroll can fall short here (the
  // triggering user bubble renders just after the card), so jump the scroll
  // container to its true bottom — and do it deferred (rAF) so this is the final,
  // winning scroll even when the user had scrolled up.
  useEffect(() => {
    const id = requestAnimationFrame(() => {
      let el = cardRef.current?.parentElement ?? null;
      while (el && getComputedStyle(el).overflowY !== "auto") el = el.parentElement;
      if (el) el.scrollTop = el.scrollHeight;
      else cardRef.current?.scrollIntoView({ block: "end" });
    });
    return () => cancelAnimationFrame(id);
  }, []);
  const [sending, setSending] = useState<GateDecision | "deny-with-prompt" | "deny-and-stop" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [showPrompt, setShowPrompt] = useState(false);
  const [prompt, setPrompt] = useState("");

  // The deny variants live behind a caret next to Deny rather than as their
  // own pills: plain Deny is the only non-terminal one and by far the common
  // choice, and four side-by-side deny buttons made the card read as if they
  // were unrelated options.
  const caretRef = useRef<HTMLButtonElement | null>(null);
  const [menuPos, setMenuPos] = useState<{ x: number; y: number } | null>(null);
  const openDenyMenu = () => {
    const el = caretRef.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    setMenuPos({ x: r.left, y: r.bottom + 2 });
  };

  // Expiry: the gate auto-denies at data.expires_at. Track it locally so the
  // card greys out on time even if the gate.approval_resolved event was
  // missed (WS drop, subscription race), then retract shortly after so a
  // dead card can never sit around eating clicks.
  const [expired, setExpired] = useState(false);
  useEffect(() => {
    if (!data.expires_at) return;
    const remaining = new Date(data.expires_at).getTime() - Date.now();
    if (remaining <= 0) {
      setExpired(true);
      return;
    }
    const t = setTimeout(() => setExpired(true), remaining);
    return () => clearTimeout(t);
  }, [data.expires_at]);
  useEffect(() => {
    if (!expired) return;
    // Leave the expired card visible briefly so the user sees what happened,
    // then let the store drop it.
    const t = setTimeout(() => onResolved?.(), 15_000);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [expired]);

  const resolve = async (decision: GateDecision) => {
    setSending(decision);
    setError(null);
    try {
      await resolveGateApproval(data.req_id, decision);
      onResolved?.();
    } catch (e) {
      if (e instanceof GateApprovalGoneError) {
        // The request died (timeout / container exit) before the click
        // landed. Show the expired state instead of an error on a card
        // that can never be resolved.
        setExpired(true);
        setSending(null);
        return;
      }
      setError(e instanceof Error ? e.message : String(e));
      setSending(null);
    }
  };

  // Deny the request and end the run outright, rather than letting the agent
  // carry on from the denial. The orchestrator's drain loop claims the next
  // queued message as soon as the cancelled run returns, so this is the way
  // to abandon what the agent is doing and move on to what's waiting behind
  // it. Deny lands first so the agent sees a clean tool-denied result while
  // its container is torn down, matching the deny-with-prompt ordering.
  const denyAndStop = async () => {
    setSending("deny-and-stop");
    setError(null);
    try {
      await resolveGateApproval(data.req_id, "deny");
      await sendCommand(channelId, "stop");
      onResolved?.();
    } catch (e) {
      if (e instanceof GateApprovalGoneError) {
        setExpired(true);
        setSending(null);
        return;
      }
      setError(e instanceof Error ? e.message : String(e));
      setSending(null);
    }
  };

  const denyWithPrompt = async () => {
    const text = prompt.trim();
    if (!text) return;
    setSending("deny-with-prompt");
    setError(null);
    try {
      await resolveGateApproval(data.req_id, "deny");
      if (onDenyWithPrompt) {
        await onDenyWithPrompt(text);
      } else {
        // Cancel the now-resumed run and queue the prompt as the latest user
        // message; the next run will see the deny tool result plus this new
        // instruction at the top of the agent's context.
        await sendMessage(channelId, text, undefined, true);
      }
      onResolved?.();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setSending(null);
    }
  };

  // Each entry re-checks `sending`: the caret is disabled while a decision is
  // in flight, but one can start between opening the menu and clicking an item
  // (the card also resolves on a peer's click via gate.approval_resolved).
  const denyMenuItems: MenuItem[] = [
    {
      label: "Deny for session",
      danger: true,
      onClick: () => {
        if (sending === null) void resolve("deny-session");
      },
    },
    // Chat/review only: `/loop stop` cancels the orchestrator run that owns the
    // message queue. A terminal pane's agent isn't that run (it's a TUI on the
    // pane's stdin, with nothing queued behind it), and those panes are exactly
    // the ones that pass onDenyWithPrompt.
    ...(onDenyWithPrompt
      ? []
      : [
          {
            label: "Deny & stop run",
            danger: true,
            onClick: () => {
              if (sending === null) void denyAndStop();
            },
          },
        ]),
    {
      label: "Deny with prompt…",
      danger: true,
      separator: true,
      onClick: () => {
        if (sending !== null) return;
        setShowPrompt(true);
        setError(null);
      },
    },
  ];

  const label = data.kind ? data.kind.toUpperCase() : "APPROVAL";

  return (
    <div
      ref={cardRef}
      style={{
        margin: "8px 16px",
        padding: "12px 16px",
        borderRadius: 8,
        border: `1px solid ${colors.warning}`,
        backgroundColor: colors.surface,
        ...style,
      }}
    >
      <div style={{ fontSize: 11, fontWeight: 700, color: colors.warning, textTransform: "uppercase", letterSpacing: 1, marginBottom: 8 }}>Gate · {label}</div>
      <div style={{ fontSize: 13, color: colors.text, marginBottom: 4, fontFamily: fonts.mono, wordBreak: "break-word" }}>{data.target}</div>
      {data.message && <div style={{ fontSize: 12, color: colors.textDim, marginBottom: 10 }}>{data.message}</div>}
      {data.details && Object.keys(data.details).length > 0 && (
        <div
          style={{
            fontSize: 12,
            fontFamily: fonts.mono,
            marginBottom: 10,
            padding: "6px 10px",
            borderRadius: 6,
            backgroundColor: colors.codeBlockBg,
            color: colors.textDim,
          }}
        >
          {Object.keys(data.details)
            .sort()
            .map((k) => (
              <div key={k} style={{ wordBreak: "break-word" }}>
                <span style={{ color: colors.text }}>{k}</span>: {data.details![k]}
              </div>
            ))}
        </div>
      )}
      {expired ? (
        <div data-testid="approval-expired" style={{ fontSize: 12, fontFamily: fonts.mono, color: colors.textDim, fontStyle: "italic" }}>
          Expired — the request timed out and was denied.
        </div>
      ) : (
        <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
          <ApprovalButton label="Allow once" decision="once" busy={sending === "once"} disabled={sending !== null} onClick={resolve} variant="primary" />
          <ApprovalButton label="Allow for session" decision="session" busy={sending === "session"} disabled={sending !== null} onClick={resolve} variant="secondary" />
          {/* Split button: the default action is the plain, non-terminal deny —
              block this one call and let the agent carry on. The variants that
              change what happens *after* the denial hang off the caret. */}
          <div style={{ display: "flex", alignItems: "stretch" }}>
            <ApprovalButton
              label="Deny"
              decision="deny"
              busy={sending === "deny"}
              disabled={sending !== null}
              onClick={resolve}
              variant="danger"
              title="Deny this request; the agent keeps going from the denial"
              style={{ borderTopRightRadius: 0, borderBottomRightRadius: 0, borderRight: "none" }}
            />
            <button
              ref={caretRef}
              data-testid="approval-deny-caret"
              onClick={openDenyMenu}
              disabled={sending !== null}
              aria-label="More deny options"
              title="More deny options"
              style={{
                padding: "4px 6px",
                fontSize: 12,
                fontFamily: fonts.mono,
                border: `1px solid ${colors.warning}`,
                borderRadius: 12,
                borderTopLeftRadius: 0,
                borderBottomLeftRadius: 0,
                backgroundColor: colors.warning,
                color: "#fff",
                cursor: sending !== null ? "default" : "pointer",
                opacity: sending !== null ? 0.5 : 1,
              }}
            >
              ▾
            </button>
          </div>
          {sending === "deny-session" || sending === "deny-and-stop" ? <span style={{ alignSelf: "center", fontSize: 12, fontFamily: fonts.mono, color: colors.textDim }}>...</span> : null}
        </div>
      )}
      {!expired && showPrompt && (
        <div style={{ marginTop: 10, display: "flex", flexDirection: "column", gap: 6 }}>
          <textarea
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                void denyWithPrompt();
              }
            }}
            placeholder="Tell the agent what to do instead (Enter to send, Shift+Enter for newline)…"
            rows={3}
            disabled={sending !== null}
            style={{
              fontFamily: fonts.mono,
              fontSize: 12,
              padding: 8,
              borderRadius: 6,
              border: `1px solid ${colors.border}`,
              backgroundColor: colors.codeBlockBg,
              color: colors.text,
              resize: "vertical",
            }}
          />
          <div style={{ display: "flex", gap: 8, justifyContent: "flex-end" }}>
            <button
              onClick={() => {
                setShowPrompt(false);
                setPrompt("");
              }}
              disabled={sending !== null}
              style={{
                padding: "4px 12px",
                fontSize: 12,
                fontFamily: fonts.mono,
                border: `1px solid ${colors.border}`,
                borderRadius: 12,
                backgroundColor: "transparent",
                color: colors.textDim,
                cursor: sending !== null ? "default" : "pointer",
                opacity: sending !== null ? 0.5 : 1,
              }}
            >
              Cancel
            </button>
            <button
              onClick={() => void denyWithPrompt()}
              disabled={sending !== null || prompt.trim().length === 0}
              style={{
                padding: "4px 12px",
                fontSize: 12,
                fontFamily: fonts.mono,
                border: `1px solid ${colors.warning}`,
                borderRadius: 12,
                backgroundColor: colors.warning,
                color: "#fff",
                cursor: sending !== null || prompt.trim().length === 0 ? "default" : "pointer",
                opacity: sending !== null || prompt.trim().length === 0 ? 0.5 : sending === "deny-with-prompt" ? 0.7 : 1,
              }}
            >
              {sending === "deny-with-prompt" ? "..." : "Deny & send prompt"}
            </button>
          </div>
        </div>
      )}
      {error && (
        <div style={{ marginTop: 8, display: "flex", alignItems: "flex-start", gap: 8, justifyContent: "space-between" }}>
          <div style={{ fontSize: 11, color: colors.warning, fontFamily: fonts.mono, flex: 1, wordBreak: "break-word" }}>{error}</div>
          <button
            onClick={() => onResolved?.()}
            style={{
              padding: "2px 10px",
              fontSize: 11,
              fontFamily: fonts.mono,
              border: `1px solid ${colors.border}`,
              borderRadius: 12,
              backgroundColor: "transparent",
              color: colors.textDim,
              cursor: "pointer",
              flexShrink: 0,
            }}
          >
            Dismiss
          </button>
        </div>
      )}
      {menuPos && <ContextMenu x={menuPos.x} y={menuPos.y} onClose={() => setMenuPos(null)} items={denyMenuItems} />}
    </div>
  );
}

function ApprovalButton({
  label,
  decision,
  busy,
  disabled,
  onClick,
  variant,
  title,
  style,
}: {
  label: string;
  decision: GateDecision;
  busy: boolean;
  disabled: boolean;
  onClick: (d: GateDecision) => void;
  variant: "primary" | "secondary" | "danger";
  title?: string;
  /** Merged last, so a split-button caller can flatten the adjoining corners. */
  style?: React.CSSProperties;
}) {
  const { colors } = useTheme();
  const accent = variant === "primary" ? colors.active : variant === "danger" ? colors.warning : colors.border;
  const textColor = variant === "secondary" ? colors.text : "#fff";
  const bg = variant === "secondary" ? "transparent" : accent;
  return (
    <button
      onClick={() => onClick(decision)}
      disabled={disabled}
      title={title}
      style={{
        padding: "4px 12px",
        fontSize: 12,
        fontFamily: fonts.mono,
        border: `1px solid ${accent}`,
        borderRadius: 12,
        backgroundColor: bg,
        color: textColor,
        cursor: disabled ? "default" : "pointer",
        opacity: disabled && !busy ? 0.5 : busy ? 0.7 : 1,
        ...style,
      }}
    >
      {busy ? "..." : label}
    </button>
  );
}
