import { useCallback, useEffect, useRef, useState } from "react";
import type { AppSettings, DaemonInfo, ImageBuildStatusData, ImageStatusResponse, ImageUpdateAvailableData } from "../types";
import { getImageStatus } from "../api/loopApi";
import { fetchConfigSchema, fetchGlobalConfig, saveGlobalConfig, fetchProjectConfig, saveProjectConfig, type ConfigSchema, type ConfigResponse } from "../api/configApi";
import { fonts, builtinThemes } from "../theme";
import type { ColorPalette } from "../theme";
import { useTheme } from "../ThemeContext";
import { ConfigForm } from "./ConfigForm";

function buildHeaderBtnStyle(colors: ColorPalette): React.CSSProperties {
  return {
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
}

interface SettingsProps {
  open: boolean;
  projectDirPath?: string | null;
  channelId?: string | null;
  sidebarOpen?: boolean;
  onToggleSidebar?: () => void;
  onOpenPalette?: () => void;
  onClose: () => void;
  onDaemonRestarted?: () => void;
  imageBuildStatus?: ImageBuildStatusData | null;
  imageUpdateAvailable?: ImageUpdateAvailableData | null;
  onRebuildImage?: () => void;
  onConfigDirtyChange?: (dirty: boolean) => void;
}

export function Settings({ open, projectDirPath, channelId, sidebarOpen, onToggleSidebar, onOpenPalette, onClose, onDaemonRestarted, imageBuildStatus, imageUpdateAvailable, onRebuildImage, onConfigDirtyChange }: SettingsProps) {
  const { colors, themeName, setThemeName, availableThemes } = useTheme();
  const [settings, setSettings] = useState<AppSettings>({ stopDaemonOnQuit: false, autoSaveOnBlur: true, previewTabs: true });
  const [daemonInfo, setDaemonInfo] = useState<DaemonInfo | null>(null);
  const [restarting, setRestarting] = useState(false);
  const [schema, setSchema] = useState<ConfigSchema | null>(null);
  const [globalConfig, setGlobalConfig] = useState<ConfigResponse | null>(null);
  const [projectConfig, setProjectConfig] = useState<ConfigResponse | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [imageStatus, setImageStatus] = useState<ImageStatusResponse | null>(null);
  const [configDirty, setConfigDirtyRaw] = useState(false);
  const [showDirtyModal, setShowDirtyModal] = useState(false);
  const setConfigDirty = (v: boolean) => { setConfigDirtyRaw(v); onConfigDirtyChange?.(v); };

  const headerBtnStyle = buildHeaderBtnStyle(colors);
  const hoverIn = (e: React.MouseEvent<HTMLButtonElement>) => {
    e.currentTarget.style.backgroundColor = colors.hoverBg;
    e.currentTarget.style.color = colors.textLight;
  };
  const hoverOut = (e: React.MouseEvent<HTMLButtonElement>) => {
    e.currentTarget.style.backgroundColor = "transparent";
    e.currentTarget.style.color = colors.textDim;
  };

  const loadAll = useCallback(() => {
    const api = window.loopAPI;
    const settingsP = api?.getSettings?.() ?? Promise.resolve(settings);
    const daemonP = api?.getDaemonInfo?.() ?? Promise.resolve(null);

    Promise.all([
      settingsP,
      daemonP,
      fetchConfigSchema().catch(() => null),
      fetchGlobalConfig().catch(() => null),
      getImageStatus().catch(() => null),
    ])
      .then(([s, d, sch, cfg, img]) => {
        setSettings(s);
        if (d) setDaemonInfo(d);
        if (sch) setSchema(sch);
        if (cfg) setGlobalConfig(cfg);
        if (img) setImageStatus(img);
        setLoaded(true);
      })
      .catch(() => setLoaded(true));
  }, []);

  useEffect(() => {
    if (!open) return;
    setLoaded(false);
    loadAll();
  }, [open, loadAll]);

  // Load project config when channelId changes.
  useEffect(() => {
    if (!open || !channelId) {
      setProjectConfig(null);
      return;
    }
    fetchProjectConfig(channelId)
      .then((c) => setProjectConfig(c))
      .catch(() => setProjectConfig(null));
  }, [open, channelId]);


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

  const handleSaveGlobalConfig = async (content: string): Promise<string | null> => {
    try {
      await saveGlobalConfig(content);
      return null;
    } catch (e: any) {
      return e.message ?? "Failed to save";
    }
  };

  const handleSaveProjectConfig = async (content: string): Promise<string | null> => {
    if (!channelId) return "No channel selected";
    try {
      await saveProjectConfig(channelId, content);
      return null;
    } catch (e: any) {
      return e.message ?? "Failed to save";
    }
  };

  const tryClose = useCallback(() => {
    if (configDirty) {
      setShowDirtyModal(true);
    } else {
      onClose();
    }
  }, [configDirty, onClose]);

  // Close on Escape.
  const tryCloseRef = useRef(tryClose);
  tryCloseRef.current = tryClose;
  useEffect(() => {
    if (!open) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") tryCloseRef.current();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [open]);

  if (!open) return null;

  return (
    <div
      style={{
        flex: 1,
        backgroundColor: colors.sidebar,
        display: "flex",
        flexDirection: "column",
        overflow: "hidden",
      }}
    >
      {/* Drag region */}
      <div
        style={{
          height: 38,
          flexShrink: 0,
          display: "flex",
          alignItems: "center",
          paddingLeft: sidebarOpen === false ? 76 : 4,
          // @ts-expect-error: WebKit-specific CSS property for Electron drag region
          WebkitAppRegion: "drag",
        }}
      >
        {onToggleSidebar && (
          <button
            onClick={onToggleSidebar}
            title="Toggle sidebar"
            style={{
              background: "none",
              border: "none",
              color: colors.textDim,
              cursor: "pointer",
              padding: "2px 4px",
              lineHeight: 1,
              borderRadius: 4,
              display: "flex",
              alignItems: "center",
              // @ts-expect-error: WebKit-specific CSS property
              WebkitAppRegion: "no-drag",
            }}
          >
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
              <rect x="3" y="3" width="18" height="18" rx="3" />
              <line x1="9" y1="3" x2="9" y2="21" />
              {sidebarOpen
                ? <polyline points="15,9 12,12 15,15" />
                : <polyline points="13,9 16,12 13,15" />
              }
            </svg>
          </button>
        )}
        {onOpenPalette && (
          <button
            onClick={onOpenPalette}
            title="Search messages (Cmd+K)"
            style={{
              background: "none",
              border: `1px solid ${colors.border}`,
              color: colors.textDim,
              cursor: "pointer",
              padding: "2px 8px",
              lineHeight: 1,
              borderRadius: 4,
              display: "flex",
              alignItems: "center",
              gap: 4,
              fontSize: 11,
              fontFamily: fonts.mono,
              marginLeft: 6,
              // @ts-expect-error: WebKit-specific CSS property
              WebkitAppRegion: "no-drag",
            }}
          >
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="11" cy="11" r="8" />
              <line x1="21" y1="21" x2="16.65" y2="16.65" />
            </svg>
            <span style={{ opacity: 0.7 }}>{navigator.platform.includes("Mac") ? "\u2318K" : "Ctrl+K"}</span>
          </button>
        )}
        <div style={{ flex: 1 }} />
      </div>

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
          height: 35,
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
          onClick={tryClose}
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
              description="Uninstalls the daemon service on quit. It will be re-installed on next app launch."
              checked={settings.stopDaemonOnQuit}
              onChange={() => handleToggle("stopDaemonOnQuit")}
            />

            <ToggleRow
              label="Auto-save editor on blur"
              description="Save open editor tabs when the window loses focus. Manual save with Cmd+S always works."
              checked={settings.autoSaveOnBlur}
              onChange={() => handleToggle("autoSaveOnBlur")}
            />

            <ToggleRow
              label="Preview tabs"
              description="Single-click opens files in a transient preview tab (italic title). Double-click or editing promotes to a permanent tab."
              checked={settings.previewTabs ?? true}
              onChange={() => handleToggle("previewTabs")}
            />

            {/* Docker Image section */}
            <SectionHeader>Docker Image</SectionHeader>

            <div style={{
              backgroundColor: colors.bg,
              borderRadius: 8,
              padding: "10px 12px",
              marginBottom: 12,
              fontSize: 12,
              fontFamily: fonts.mono,
            }}>
              <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 6 }}>
                <span style={{ color: colors.textDim }}>Status</span>
                <span style={{ display: "flex", alignItems: "center", gap: 6 }}>
                  <span style={{
                    width: 6,
                    height: 6,
                    borderRadius: "50%",
                    backgroundColor:
                      (imageBuildStatus?.state === "building") ? colors.warning
                      : (imageBuildStatus?.state === "failed") ? colors.error
                      : colors.active,
                    display: "inline-block",
                  }} />
                  <span style={{
                    color:
                      (imageBuildStatus?.state === "building") ? colors.warning
                      : (imageBuildStatus?.state === "failed") ? colors.error
                      : colors.active,
                    fontWeight: 500,
                  }}>
                    {imageBuildStatus?.state === "building" ? "Building"
                      : imageBuildStatus?.state === "failed" ? "Failed"
                      : "Ready"}
                    {imageBuildStatus?.phase ? ` — ${imageBuildStatus.phase}` : ""}
                  </span>
                </span>
              </div>
              {imageStatus?.versions && (
                <>
                  <div style={{ display: "flex", justifyContent: "space-between", alignItems: "baseline", gap: 12, marginBottom: 4 }}>
                    <span style={{ color: colors.textDim, flexShrink: 0 }}>Loop</span>
                    <span style={{ color: colors.text }}>{imageStatus.versions.loop_version || "unknown"}</span>
                  </div>
                  <div style={{ display: "flex", justifyContent: "space-between", alignItems: "baseline", gap: 12, marginBottom: 4 }}>
                    <span style={{ color: colors.textDim, flexShrink: 0 }}>Claude</span>
                    <span style={{ color: colors.text }}>{imageStatus.versions.claude_version || "unknown"}</span>
                  </div>
                  <div style={{ display: "flex", justifyContent: "space-between", alignItems: "baseline", gap: 12 }}>
                    <span style={{ color: colors.textDim, flexShrink: 0 }}>Built</span>
                    <span style={{ color: colors.text }}>{imageStatus.versions.built_at || "unknown"}</span>
                  </div>
                </>
              )}
            </div>

            {imageUpdateAvailable && (
              <div style={{
                backgroundColor: "rgba(255, 200, 50, 0.1)",
                border: `1px solid rgba(255, 200, 50, 0.3)`,
                borderRadius: 8,
                padding: "8px 12px",
                marginBottom: 12,
                fontSize: 12,
                color: colors.warning,
              }}>
                Claude Code update available: v{imageUpdateAvailable.current_version} → v{imageUpdateAvailable.latest_version}
              </div>
            )}

            {imageBuildStatus?.state === "failed" && imageBuildStatus.error && (
              <div style={{
                fontSize: 11,
                color: colors.error,
                marginBottom: 8,
              }}>
                {imageBuildStatus.error}
              </div>
            )}

            <div style={{ display: "flex", gap: 8, marginBottom: 12 }}>
              <button
                onClick={onRebuildImage}
                disabled={imageBuildStatus?.state === "building"}
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
                  color: imageBuildStatus?.state === "building" ? colors.textDim : colors.text,
                  fontSize: 12,
                  cursor: imageBuildStatus?.state === "building" ? "default" : "pointer",
                  fontFamily: "inherit",
                }}
                onMouseEnter={(e) => { if (imageBuildStatus?.state !== "building") e.currentTarget.style.borderColor = colors.textDim; }}
                onMouseLeave={(e) => { e.currentTarget.style.borderColor = colors.border; }}
              >
                <svg
                  width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"
                  style={imageBuildStatus?.state === "building" ? { animation: "spin 1s linear infinite" } : undefined}
                >
                  <path d="M21 12a9 9 0 1 1-3-6.7" />
                  <polyline points="21,3 21,9 15,9" />
                </svg>
                {imageBuildStatus?.state === "building" ? "Building..." : "Rebuild Image"}
              </button>
            </div>

            {/* Appearance section */}
            <SectionHeader>Appearance</SectionHeader>

            <div style={{
              padding: "10px 12px",
              backgroundColor: colors.bg,
              borderRadius: 8,
            }}>
              <div style={{ fontSize: 13, color: colors.text, marginBottom: 8 }}>Theme</div>
              <div style={{ display: "flex", gap: 8 }}>
                {availableThemes.map((t) => {
                  const palette = builtinThemes[t] ?? colors;
                  const isSelected = themeName === t;
                  return (
                    <button
                      key={t}
                      onClick={() => {
                        const updated = { ...settings, theme: t };
                        setSettings(updated);
                        setThemeName(t);
                        window.loopAPI?.saveSettings?.(updated);
                      }}
                      style={{
                        flex: 1,
                        border: `2px solid ${isSelected ? colors.active : colors.border}`,
                        borderRadius: 8,
                        padding: 0,
                        cursor: "pointer",
                        background: "none",
                        overflow: "hidden",
                      }}
                    >
                      {/* Mini preview */}
                      <div style={{
                        display: "flex",
                        height: 40,
                      }}>
                        <div style={{ width: "30%", backgroundColor: palette.sidebarNav }} />
                        <div style={{ flex: 1, backgroundColor: palette.bg, display: "flex", flexDirection: "column", justifyContent: "center", alignItems: "center", gap: 3, padding: 4 }}>
                          <div style={{ width: "60%", height: 3, borderRadius: 2, backgroundColor: palette.textMuted }} />
                          <div style={{ width: "40%", height: 3, borderRadius: 2, backgroundColor: palette.active }} />
                          <div style={{ width: "50%", height: 3, borderRadius: 2, backgroundColor: palette.textMuted }} />
                        </div>
                      </div>
                      <div style={{
                        fontSize: 11,
                        color: colors.text,
                        padding: "4px 0",
                        backgroundColor: colors.surface,
                        borderTop: `1px solid ${isSelected ? colors.active : colors.border}`,
                      }}>
                        {t.charAt(0).toUpperCase() + t.slice(1)}
                      </div>
                    </button>
                  );
                })}
              </div>
            </div>

            {/* Global config */}
            {globalConfig && (
              <ConfigForm
                title="Global Config"
                schema={schema}
                config={globalConfig}
                onSave={handleSaveGlobalConfig}
                isGlobal={true}
                colors={colors}
                onDirtyChange={setConfigDirty}
              />
            )}

            {/* Project config */}
            {projectDirPath && (
              <ConfigForm
                title="Project Config"
                schema={schema}
                config={projectConfig}
                onSave={handleSaveProjectConfig}
                isGlobal={false}
                colors={colors}
                onDirtyChange={setConfigDirty}
              />
            )}
          </>
        )}
      </div>

      {/* Inline keyframes for spinner */}
      <style>{`@keyframes spin { to { transform: rotate(360deg); } }`}</style>

      {/* Unsaved changes confirmation modal */}
      {showDirtyModal && (
        <div style={{ position: "fixed", inset: 0, zIndex: 9999, display: "flex", alignItems: "center", justifyContent: "center", backgroundColor: "rgba(0,0,0,0.5)" }}
          onClick={() => setShowDirtyModal(false)}>
          <div style={{ backgroundColor: colors.surface, borderRadius: 12, padding: "20px 24px", maxWidth: 360, boxShadow: "0 8px 32px rgba(0,0,0,0.3)" }}
            onClick={(e) => e.stopPropagation()}>
            <div style={{ fontSize: 14, fontWeight: 600, color: colors.text, marginBottom: 8 }}>Unsaved Changes</div>
            <div style={{ fontSize: 13, color: colors.textDim, marginBottom: 16, lineHeight: 1.5 }}>
              You have unsaved config changes. Discard them and close?
            </div>
            <div style={{ display: "flex", gap: 8, justifyContent: "flex-end" }}>
              <button onClick={() => setShowDirtyModal(false)} style={{
                padding: "6px 14px", backgroundColor: "transparent", border: `1px solid ${colors.border}`,
                borderRadius: 6, color: colors.text, fontSize: 12, cursor: "pointer", fontFamily: "inherit",
              }}>Cancel</button>
              <button onClick={() => { setShowDirtyModal(false); setConfigDirty(false); onClose(); }} style={{
                padding: "6px 14px", backgroundColor: colors.error, border: "none",
                borderRadius: 6, color: colors.white, fontSize: 12, fontWeight: 500, cursor: "pointer", fontFamily: "inherit",
              }}>Discard & Close</button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

function SectionHeader({ children }: { children: React.ReactNode }) {
  const { colors } = useTheme();
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


function ToggleRow({ label, description, checked, onChange }: {
  label: string;
  description?: string;
  checked: boolean;
  onChange: () => void;
}) {
  const { colors } = useTheme();
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
            backgroundColor: colors.white,
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
