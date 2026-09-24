import { createContext, useContext, useEffect, useRef, useState } from "react";
import { useTheme } from "../../ThemeContext";
import { CopyButton } from "../shared/CopyButton";
import { componentHeight, componentMinHeight } from "./componentFence";

/** A component's composed document, with what it's shown under. */
export interface ShownComponent {
  template: string;
  title: string;
  doc: string;
}

/**
 * Opens a component across its chat pane. The workspace layout provides it
 * around each chat pane: it maximizes the pane and shows the component in
 * place of the chat until it's closed. Without a provider there's nowhere to
 * open it, and the button isn't shown.
 */
export const ComponentFocusContext = createContext<((c: ShownComponent) => void) | null>(null);

/**
 * A chat component posted by the chat_component MCP tool: the agent's
 * content, composed into a template's document by the backend, rendered in a
 * sandboxed frame. allow-scripts without allow-same-origin gives the document
 * an opaque origin, so its scripts run but can't reach the app. The document
 * posts its height, and the frame follows within bounds.
 */
export function ChatComponent({ template, title, doc }: ShownComponent) {
  const { colors } = useTheme();
  const open = useContext(ComponentFocusContext);
  const frame = useRef<HTMLIFrameElement>(null);
  const [height, setHeight] = useState(componentMinHeight * 2);
  const [hovered, setHovered] = useState(false);

  useEffect(() => {
    const onMessage = (e: MessageEvent) => {
      if (!frame.current || e.source !== frame.current.contentWindow) return;
      const h = componentHeight(e.data);
      if (h !== null) setHeight(h);
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, []);

  const label = title || template;

  return (
    <div
      data-testid="chat-component"
      data-template={template}
      style={{ margin: "8px 0", border: `1px solid ${colors.border}`, borderRadius: 8, overflow: "hidden", backgroundColor: colors.surface }}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
    >
      <ComponentHeader label={label} doc={doc} showActions={hovered}>
        {open && (
          <button
            type="button"
            data-testid="chat-component-expand"
            title="Open in the whole pane"
            aria-label="Open in the whole pane"
            style={headerButtonStyle(colors, hovered)}
            onClick={() => open({ template, title, doc })}
          >
            ⤢
          </button>
        )}
      </ComponentHeader>
      <iframe key={doc} ref={frame} title={label} data-testid="chat-component-frame" sandbox="allow-scripts" srcDoc={doc} style={{ display: "block", width: "100%", height, border: "none" }} />
    </div>
  );
}

/**
 * A component across its whole pane, in place of the chat: the same
 * sandboxed document, sized to the pane rather than to its content. Escape
 * or the close button returns to the chat.
 */
export function ChatComponentFull({ component, onClose }: { component: ShownComponent; onClose: () => void }) {
  const { colors } = useTheme();
  const label = component.title || component.template;

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div
      data-testid="chat-component-full"
      data-template={component.template}
      role="dialog"
      aria-label={label}
      style={{ position: "absolute", inset: 0, zIndex: 5, display: "flex", flexDirection: "column", backgroundColor: colors.surface }}
    >
      <ComponentHeader label={label} doc={component.doc} showActions>
        <button type="button" data-testid="chat-component-close" title="Back to the chat (Esc)" aria-label="Back to the chat" style={headerButtonStyle(colors, true)} onClick={onClose}>
          ✕
        </button>
      </ComponentHeader>
      <iframe key={component.doc} title={label} sandbox="allow-scripts" srcDoc={component.doc} style={{ flex: 1, minHeight: 0, width: "100%", border: "none" }} />
    </div>
  );
}

function ComponentHeader({ label, doc, showActions, children }: { label: string; doc: string; showActions: boolean; children: React.ReactNode }) {
  const { colors } = useTheme();
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "4px 8px 4px 12px", borderBottom: `1px solid ${colors.border}` }}>
      <span data-testid="chat-component-title" style={{ flex: 1, fontSize: 12, color: colors.textDim, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
        {label}
      </span>
      <CopyButton text={doc} visible={showActions} title="Copy source" />
      {children}
    </div>
  );
}

function headerButtonStyle(colors: ReturnType<typeof useTheme>["colors"], visible: boolean): React.CSSProperties {
  return {
    background: "none",
    border: `1px solid ${colors.border}`,
    borderRadius: 4,
    color: colors.textMuted,
    fontSize: 11,
    padding: "1px 6px",
    cursor: "pointer",
    opacity: visible ? 1 : 0,
    transition: "opacity 0.15s",
  };
}
