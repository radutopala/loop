import { useCallback, useEffect, useRef, useState } from "react";
import type { AppSettings, ConfigInfo, DaemonInfo } from "../types";
import { colors, fonts } from "../theme";

const MIN_WIDTH = 320;
const MAX_WIDTH_PERCENT = 0.45;
const WIDTH_STORAGE_KEY = "loop-settings-panel-width";

function loadWidth(): number {
  try {
    const stored = localStorage.getItem(WIDTH_STORAGE_KEY);
    if (stored) {
      const w = parseInt(stored, 10);
      if (w >= MIN_WIDTH) return w;
    }
  } catch { /* ignore */ }
  return Math.floor(window.innerWidth * MAX_WIDTH_PERCENT);
}

function saveWidth(w: number) {
  try {
    localStorage.setItem(WIDTH_STORAGE_KEY, String(w));
  } catch { /* ignore */ }
}

interface SettingsProps {
  open: boolean;
  projectDirPath?: string | null;
  onClose: () => void;
  onDaemonRestarted?: () => void;
}

export function Settings({ open, projectDirPath, onClose, onDaemonRestarted }: SettingsProps) {
  const [width, setWidth] = useState(loadWidth);
  const [resizing, setResizing] = useState(false);
  const [settings, setSettings] = useState<AppSettings>({ stopDaemonOnQuit: false });
  const [daemonInfo, setDaemonInfo] = useState<DaemonInfo | null>(null);
  const [restarting, setRestarting] = useState(false);
  const [globalConfig, setGlobalConfig] = useState<ConfigInfo | null>(null);
  const [projectConfig, setProjectConfig] = useState<ConfigInfo | null>(null);
  const [loaded, setLoaded] = useState(false);

  const loadAll = useCallback(() => {
    const api = window.loopAPI;
    if (!api?.getSettings || !api?.getDaemonInfo) {
      setLoaded(true);
      return;
    }
    Promise.all([
      api.getSettings(),
      api.getDaemonInfo(),
      api.getConfig?.() ?? Promise.resolve(null),
    ])
      .then(([s, d, c]) => {
        setSettings(s);
        setDaemonInfo(d);
        if (c) setGlobalConfig(c);
        setLoaded(true);
      })
      .catch(() => setLoaded(true));
  }, []);

  useEffect(() => {
    if (!open) return;
    setLoaded(false);
    loadAll();
  }, [open, loadAll]);

  // Load project config when projectDirPath changes.
  useEffect(() => {
    if (!open || !projectDirPath) {
      setProjectConfig(null);
      return;
    }
    window.loopAPI?.getProjectConfig?.(projectDirPath)
      .then((c) => setProjectConfig(c))
      .catch(() => setProjectConfig(null));
  }, [open, projectDirPath]);

  const handleToggle = async (key: keyof AppSettings) => {
    const updated = { ...settings, [key]: !settings[key] };
    setSettings(updated);
    await window.loopAPI?.saveSettings?.(updated);
  };

  const handleRestart = async () => {
    setRestarting(true);
    try {
      const info = await window.loopAPI?.restartDaemon?.();
      if (info) setDaemonInfo(info);
      onDaemonRestarted?.();
    } catch { /* ignore */ }
    setRestarting(false);
  };

  const handleSaveConfig = async (filePath: string, content: string): Promise<string | null> => {
    const result = await window.loopAPI?.saveConfig?.(filePath, content);
    if (result && !result.ok) return result.error ?? "Failed to save";
    return null;
  };

  const handleMouseDown = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault();
      setResizing(true);
      const startX = e.clientX;
      const startWidth = width;

      let lastWidth = startWidth;
      const onMouseMove = (ev: MouseEvent) => {
        const maxWidth = window.innerWidth * MAX_WIDTH_PERCENT;
        const newWidth = Math.min(maxWidth, Math.max(MIN_WIDTH, startWidth - (ev.clientX - startX)));
        lastWidth = newWidth;
        setWidth(newWidth);
      };

      const onMouseUp = () => {
        setResizing(false);
        saveWidth(lastWidth);
        document.removeEventListener("mousemove", onMouseMove);
        document.removeEventListener("mouseup", onMouseUp);
      };

      document.addEventListener("mousemove", onMouseMove);
      document.addEventListener("mouseup", onMouseUp);
    },
    [width],
  );

  // Close on Escape.
  const closeRef = useRef(onClose);
  closeRef.current = onClose;
  useEffect(() => {
    if (!open) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") closeRef.current();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [open]);

  if (!open) return null;

  return (
    <div
      style={{
        width,
        minWidth: MIN_WIDTH,
        maxWidth: `${MAX_WIDTH_PERCENT * 100}vw`,
        flexShrink: 0,
        backgroundColor: colors.sidebar,
        display: "flex",
        flexDirection: "column",
        overflow: "hidden",
        position: "relative",
        userSelect: resizing ? "none" : undefined,
        borderLeft: `1px solid ${colors.border}`,
      }}
    >
      {/* Resize handle */}
      <div
        onMouseDown={handleMouseDown}
        style={{
          position: "absolute",
          top: 0,
          left: 0,
          width: 4,
          height: "100%",
          cursor: "col-resize",
          backgroundColor: resizing ? colors.textDim : "transparent",
          zIndex: 1,
        }}
        onMouseEnter={(e) => { (e.currentTarget as HTMLDivElement).style.backgroundColor = colors.textDim; }}
        onMouseLeave={(e) => { if (!resizing) (e.currentTarget as HTMLDivElement).style.backgroundColor = "transparent"; }}
      />

      {/* Drag region for macOS title bar alignment */}
      <div
        style={{
          height: 38,
          flexShrink: 0,
          // @ts-expect-error: WebKit-specific CSS property for Electron drag region
          WebkitAppRegion: "drag",
        }}
      />

      {/* Header */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          padding: "3px 12px",
          borderBottom: `1px solid ${colors.border}`,
          flexShrink: 0,
          boxSizing: "border-box",
          height: 39,
        }}
      >
        <span
          style={{
            fontSize: 10,
            fontWeight: 700,
            color: colors.textDim,
            textTransform: "uppercase",
            letterSpacing: 1,
          }}
        >
          Settings
        </span>
        <button
          onClick={onClose}
          title="Close panel"
          style={headerBtnStyle}
          onMouseEnter={hoverIn}
          onMouseLeave={hoverOut}
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <line x1="18" y1="6" x2="6" y2="18" />
            <line x1="6" y1="6" x2="18" y2="18" />
          </svg>
        </button>
      </div>

      {/* Body */}
      <div style={{ flex: 1, overflow: "auto", padding: "16px 16px 24px" }}>
        {!loaded ? (
          <div style={{ color: colors.textDim, fontSize: 13, padding: "20px 0", textAlign: "center" }}>Loading...</div>
        ) : (
          <>
            {/* Daemon section */}
            <SectionHeader>Daemon</SectionHeader>

            {daemonInfo && (
              <div style={{
                backgroundColor: colors.bg,
                borderRadius: 8,
                padding: "10px 12px",
                marginBottom: 12,
                fontSize: 12,
                fontFamily: fonts.mono,
              }}>
                <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: daemonInfo.binaryPath ? 6 : 0 }}>
                  <span style={{ color: colors.textDim }}>Status</span>
                  <span style={{ display: "flex", alignItems: "center", gap: 6 }}>
                    <span style={{
                      width: 6,
                      height: 6,
                      borderRadius: "50%",
                      backgroundColor: daemonInfo.running ? colors.active : colors.error,
                      display: "inline-block",
                    }} />
                    <span style={{
                      color: daemonInfo.running ? colors.active : colors.error,
                      fontWeight: 500,
                    }}>
                      {daemonInfo.running ? "Running" : "Stopped"}
                    </span>
                    {daemonInfo.managed && (
                      <span style={{ color: colors.textDim, fontSize: 11 }}>(managed)</span>
                    )}
                  </span>
                </div>
                {daemonInfo.binaryPath && (
                  <div style={{ display: "flex", justifyContent: "space-between", alignItems: "baseline", gap: 12 }}>
                    <span style={{ color: colors.textDim, flexShrink: 0 }}>Binary</span>
                    <span style={{ color: colors.text, wordBreak: "break-all", textAlign: "right" }}>{daemonInfo.binaryPath}</span>
                  </div>
                )}
              </div>
            )}

            <div style={{ display: "flex", gap: 8, marginBottom: 12 }}>
              <button
                onClick={handleRestart}
                disabled={restarting}
                style={{
                  flex: 1,
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "center",
                  gap: 6,
                  padding: "8px 12px",
                  backgroundColor: colors.bg,
                  border: `1px solid ${colors.border}`,
                  borderRadius: 8,
                  color: restarting ? colors.textDim : colors.text,
                  fontSize: 12,
                  cursor: restarting ? "default" : "pointer",
                  fontFamily: "inherit",
                }}
                onMouseEnter={(e) => { if (!restarting) e.currentTarget.style.borderColor = colors.textDim; }}
                onMouseLeave={(e) => { e.currentTarget.style.borderColor = colors.border; }}
              >
                <svg
                  width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"
                  style={restarting ? { animation: "spin 1s linear infinite" } : undefined}
                >
                  <path d="M21 12a9 9 0 1 1-3-6.7" />
                  <polyline points="21,3 21,9 15,9" />
                </svg>
                {restarting ? "Restarting..." : "Restart Daemon"}
              </button>
            </div>

            <ToggleRow
              label="Stop daemon when app quits"
              description="Only when the app started the daemon itself."
              checked={settings.stopDaemonOnQuit}
              onChange={() => handleToggle("stopDaemonOnQuit")}
            />

            {/* Global config */}
            {globalConfig && (
              <EditableConfigSection
                title="Global Config"
                config={globalConfig}
                onSave={handleSaveConfig}
              />
            )}

            {/* Project config */}
            {projectDirPath && (
              <EditableConfigSection
                title="Project Config"
                config={projectConfig}
                emptyText={`No .loop/config.json found — click Edit to create one.`}
                onSave={handleSaveConfig}
              />
            )}
          </>
        )}
      </div>

      {/* Inline keyframes for spinner */}
      <style>{`@keyframes spin { to { transform: rotate(360deg); } }`}</style>
    </div>
  );
}

function SectionHeader({ children }: { children: React.ReactNode }) {
  return (
    <div style={{
      fontSize: 11,
      fontWeight: 700,
      color: colors.textDim,
      textTransform: "uppercase",
      letterSpacing: 1,
      marginBottom: 10,
      marginTop: 4,
    }}>
      {children}
    </div>
  );
}

function EditableConfigSection({ title, config, emptyText, onSave }: {
  title: string;
  config: ConfigInfo | null;
  emptyText?: string;
  onSave: (filePath: string, content: string) => Promise<string | null>;
}) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  const startEditing = () => {
    setDraft(config?.content ?? "{\n  \n}\n");
    setEditing(true);
    setError(null);
    setTimeout(() => textareaRef.current?.focus(), 0);
  };

  const handleSave = async () => {
    if (!config?.path) return;
    setSaving(true);
    setError(null);
    const err = await onSave(config.path, draft);
    setSaving(false);
    if (err) {
      setError(err);
    } else {
      if (config) config.content = draft;
      setEditing(false);
    }
  };

  const handleCancel = () => {
    setEditing(false);
    setError(null);
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    // Cmd+S to save
    if ((e.metaKey || e.ctrlKey) && e.key === "s") {
      e.preventDefault();
      handleSave();
    }
    if (e.key === "Escape") {
      e.stopPropagation();
      handleCancel();
    }
    // Tab inserts two spaces
    if (e.key === "Tab") {
      e.preventDefault();
      const ta = textareaRef.current;
      if (!ta) return;
      const start = ta.selectionStart;
      const end = ta.selectionEnd;
      const val = draft;
      setDraft(val.substring(0, start) + "  " + val.substring(end));
      setTimeout(() => { ta.selectionStart = ta.selectionEnd = start + 2; }, 0);
    }
  };

  return (
    <div style={{ marginTop: 20 }}>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 6 }}>
        <SectionHeader>{title}</SectionHeader>
        {!editing && (
          <button
            onClick={startEditing}
            style={{
              background: "none",
              border: "none",
              color: colors.textDim,
              cursor: "pointer",
              padding: "2px 6px",
              fontSize: 11,
              borderRadius: 4,
              fontFamily: "inherit",
            }}
            onMouseEnter={(e) => { e.currentTarget.style.color = colors.textLight; e.currentTarget.style.backgroundColor = colors.hoverBg; }}
            onMouseLeave={(e) => { e.currentTarget.style.color = colors.textDim; e.currentTarget.style.backgroundColor = "transparent"; }}
          >
            Edit
          </button>
        )}
      </div>
      <div style={{
        fontSize: 11,
        fontFamily: fonts.mono,
        color: colors.textDim,
        marginBottom: 6,
        overflow: "hidden",
        textOverflow: "ellipsis",
        whiteSpace: "nowrap",
      }}>
        {config?.path}
      </div>

      {editing ? (
        <div>
          <textarea
            ref={textareaRef}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={handleKeyDown}
            spellCheck={false}
            style={{
              width: "100%",
              minHeight: 200,
              maxHeight: 400,
              backgroundColor: colors.bg,
              border: `1px solid ${error ? colors.error : colors.border}`,
              borderRadius: 8,
              padding: "10px 12px",
              fontSize: 12,
              fontFamily: fonts.mono,
              color: colors.text,
              lineHeight: 1.5,
              resize: "vertical",
              outline: "none",
              boxSizing: "border-box",
            }}
          />
          {error && (
            <div style={{ fontSize: 11, color: colors.error, marginTop: 4 }}>{error}</div>
          )}
          <div style={{ display: "flex", gap: 8, marginTop: 8, justifyContent: "flex-end" }}>
            <button
              onClick={handleCancel}
              style={{
                padding: "5px 12px",
                backgroundColor: "transparent",
                border: `1px solid ${colors.border}`,
                borderRadius: 6,
                color: colors.text,
                fontSize: 12,
                cursor: "pointer",
                fontFamily: "inherit",
              }}
            >
              Cancel
            </button>
            <button
              onClick={handleSave}
              disabled={saving}
              style={{
                padding: "5px 12px",
                backgroundColor: colors.active,
                border: "none",
                borderRadius: 6,
                color: "#fff",
                fontSize: 12,
                fontWeight: 500,
                cursor: saving ? "default" : "pointer",
                opacity: saving ? 0.6 : 1,
                fontFamily: "inherit",
              }}
            >
              {saving ? "Saving..." : "Save"}
            </button>
          </div>
          <div style={{ fontSize: 10, color: colors.textDim, marginTop: 4, textAlign: "right" }}>
            {navigator.platform.includes("Mac") ? "\u2318S" : "Ctrl+S"} to save
          </div>
        </div>
      ) : (
        <pre style={{
          backgroundColor: colors.bg,
          borderRadius: 8,
          padding: "10px 12px",
          margin: 0,
          fontSize: 12,
          fontFamily: fonts.mono,
          color: config?.content ? colors.text : colors.textDim,
          lineHeight: 1.5,
          overflowX: "auto",
          whiteSpace: "pre-wrap",
          wordBreak: "break-all",
          maxHeight: 300,
          overflowY: "auto",
        }}>
          {config?.content ?? emptyText ?? "File not found"}
        </pre>
      )}
    </div>
  );
}

function ToggleRow({ label, description, checked, onChange }: {
  label: string;
  description?: string;
  checked: boolean;
  onChange: () => void;
}) {
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        gap: 12,
        padding: "10px 12px",
        backgroundColor: colors.bg,
        borderRadius: 8,
        cursor: "pointer",
      }}
      onClick={onChange}
    >
      <div>
        <div style={{ fontSize: 13, color: colors.text }}>{label}</div>
        {description && (
          <div style={{ fontSize: 11, color: colors.textDim, marginTop: 2 }}>{description}</div>
        )}
      </div>
      <div
        style={{
          width: 36,
          height: 20,
          borderRadius: 10,
          backgroundColor: checked ? colors.active : colors.border,
          position: "relative",
          flexShrink: 0,
          transition: "background-color 0.2s",
        }}
      >
        <div
          style={{
            width: 16,
            height: 16,
            borderRadius: "50%",
            backgroundColor: "#fff",
            position: "absolute",
            top: 2,
            left: checked ? 18 : 2,
            transition: "left 0.2s",
          }}
        />
      </div>
    </div>
  );
}

const headerBtnStyle: React.CSSProperties = {
  background: "none",
  border: "none",
  color: colors.textDim,
  cursor: "pointer",
  padding: 4,
  lineHeight: 1,
  borderRadius: 4,
  display: "flex",
  alignItems: "center",
};

function hoverIn(e: React.MouseEvent<HTMLButtonElement>) {
  e.currentTarget.style.backgroundColor = colors.hoverBg;
  e.currentTarget.style.color = colors.textLight;
}

function hoverOut(e: React.MouseEvent<HTMLButtonElement>) {
  e.currentTarget.style.backgroundColor = "transparent";
  e.currentTarget.style.color = colors.textDim;
}
