import { useCallback, useEffect, useRef, useState } from "react";
import { type BuiltinKind, restoreBuiltins } from "../../api/builtins";
import { type ConfigResponse, type ConfigSchema, fetchConfigSchema, fetchGlobalConfig, fetchProjectConfig, saveGlobalConfig, saveProjectConfig } from "../../api/configApi";
import { getImageStatus } from "../../api/loopApi";
import { DEFAULT_FONT_SIZES, useTheme } from "../../ThemeContext";
import type { ColorPalette } from "../../theme";
import { fonts } from "../../theme";
import type { Channel, DaemonInfo, ImageBuildStatusData, ImageStatusResponse, ImageUpdateAvailableData } from "../../types";
import { logErr } from "../../utils/log";
import { ChannelHeaderInfo } from "../layout/ChannelHeaderInfo";
import { ConfigForm, type ConfigFormHandle, getSections } from "./ConfigForm";

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
  channel?: Channel;
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

export function Settings({
  open,
  projectDirPath,
  channelId,
  channel,
  sidebarOpen,
  onOpenPalette,
  onClose,
  onDaemonRestarted,
  imageBuildStatus,
  imageUpdateAvailable,
  onRebuildImage,
  onConfigDirtyChange,
}: SettingsProps) {
  const { colors, setThemeName, setFontSizes, setIslands } = useTheme();
  const [daemonInfo, setDaemonInfo] = useState<DaemonInfo | null>(null);
  const [restarting, setRestarting] = useState(false);
  const [schema, setSchema] = useState<ConfigSchema | null>(null);
  const [globalConfig, setGlobalConfig] = useState<ConfigResponse | null>(null);
  const [projectConfig, setProjectConfig] = useState<ConfigResponse | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [imageStatus, setImageStatus] = useState<ImageStatusResponse | null>(null);
  const [globalDirty, setGlobalDirty] = useState(false);
  const [projectDirty, setProjectDirty] = useState(false);
  const configDirty = globalDirty || projectDirty;
  const [showDirtyModal, setShowDirtyModal] = useState(false);
  const [saving, setSaving] = useState(false);
  const [activeSection, setActiveSection] = useState("Desktop");
  // Scope per-kind so an in-flight "Restore Workflows" can't bleed into the
  // Shortcuts bar (or vice versa) when the user switches tabs mid-flight.
  // A single shared `restoring`/`restoreMsg` would render the Shortcuts bar
  // as "Restoring…" while the Workflows POST is still in flight.
  const [restoringByKind, setRestoringByKind] = useState<Record<BuiltinKind, boolean>>({ workflows: false, shortcuts: false });
  const [restoreMsgByKind, setRestoreMsgByKind] = useState<Record<BuiltinKind, string | null>>({ workflows: null, shortcuts: null });
  const globalFormRef = useRef<ConfigFormHandle>(null);
  const projectFormRef = useRef<ConfigFormHandle>(null);

  // handleRestoreBuiltins awaits two async calls before consulting globalDirty
  // and would otherwise capture the value at click time. If the user starts
  // typing in the form during the ~1–2s POST + refetch window, their unsaved
  // edits would be silently overwritten by setGlobalConfig(fresh). Reading
  // through a ref instead always sees the current value.
  const globalDirtyRef = useRef(globalDirty);
  useEffect(() => {
    globalDirtyRef.current = globalDirty;
  }, [globalDirty]);

  useEffect(() => {
    onConfigDirtyChange?.(configDirty);
  }, [configDirty, onConfigDirtyChange]);

  // Reset the restore-builtins toast when the user navigates away.
  useEffect(() => {
    setRestoreMsgByKind({ workflows: null, shortcuts: null });
  }, [activeSection]);

  // Build section groups for sidebar nav.
  const HARDCODED_GLOBAL = ["Daemon", "Docker Image"];
  const globalSchemaSections = getSections(schema, true);
  const projectSchemaSections = projectDirPath ? getSections(schema, false) : [];

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
    const daemonP = window.loopAPI?.getDaemonInfo?.() ?? Promise.resolve(null);

    Promise.all([daemonP, fetchConfigSchema().catch(() => null), fetchGlobalConfig().catch(() => null), getImageStatus().catch(() => null)])
      .then(([d, sch, cfg, img]) => {
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

  // Refresh image status when a build completes.
  useEffect(() => {
    if (imageBuildStatus?.state === "completed") {
      getImageStatus().then(setImageStatus).catch(logErr("fetching image status"));
    }
  }, [imageBuildStatus]);

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

  const handleRestart = async () => {
    setRestarting(true);
    try {
      const info = await window.loopAPI?.restartDaemon?.();
      if (info) setDaemonInfo(info);
      onDaemonRestarted?.();
    } catch {
      /* ignore */
    }
    setRestarting(false);
  };

  const handleSaveGlobalConfig = async (content: string): Promise<string | null> => {
    try {
      await saveGlobalConfig(content);
      // Apply desktop settings live (theme, font sizes).
      try {
        const parsed = JSON.parse(content);
        const desktop = parsed.desktop;
        if (desktop) {
          setThemeName(desktop.theme || "dark");
          setFontSizes(desktop.font_sizes ? { ...DEFAULT_FONT_SIZES, ...desktop.font_sizes } : { ...DEFAULT_FONT_SIZES });
          setIslands(desktop.islands ?? false);
        }
      } catch {
        /* ignore parse errors */
      }
      // Re-fetch so other sections see the updated values.
      fetchGlobalConfig().then(setGlobalConfig).catch(logErr("re-fetching global config"));
      return null;
    } catch (e: any) {
      return e.message ?? "Failed to save";
    }
  };

  const handleRestoreBuiltins = async (kind: BuiltinKind) => {
    setRestoringByKind((prev) => ({ ...prev, [kind]: true }));
    setRestoreMsgByKind((prev) => ({ ...prev, [kind]: null }));
    let msg: string;
    try {
      const result = await restoreBuiltins(kind);
      // Re-fetch global config so the form picks up newly-seeded entries.
      // Skip the overwrite if the user has unsaved global edits in the form —
      // setGlobalConfig would silently blow them away. The newly-seeded items
      // will be picked up on the next reopen / save cycle instead.
      const fresh = await fetchGlobalConfig().catch(() => null);
      if (fresh && !globalDirtyRef.current) setGlobalConfig(fresh);
      const added = result.added.length ? `Added: ${result.added.join(", ")}` : null;
      const patched = result.patched.length ? `Patched: ${result.patched.join(", ")}` : null;
      const skipped = result.skipped.length ? `Already present: ${result.skipped.join(", ")}` : null;
      msg = [added, patched, skipped].filter(Boolean).join(" · ") || "Nothing to restore";
    } catch (e: any) {
      msg = e?.message ?? "Restore failed";
    } finally {
      setRestoringByKind((prev) => ({ ...prev, [kind]: false }));
    }
    setRestoreMsgByKind((prev) => ({ ...prev, [kind]: msg }));
  };

  const handleSaveProjectConfig = async (content: string): Promise<string | null> => {
    if (!channelId) return "No channel selected";
    try {
      await saveProjectConfig(channelId, content);
      // Re-fetch so other sections see the updated values.
      fetchProjectConfig(channelId).then(setProjectConfig).catch(logErr("re-fetching project config"));
      return null;
    } catch (e: any) {
      return e.message ?? "Failed to save";
    }
  };

  const handleFloatingSave = async () => {
    setSaving(true);
    if (globalDirty) await globalFormRef.current?.save();
    if (projectDirty) await projectFormRef.current?.save();
    setSaving(false);
  };

  const handleFloatingCancel = () => {
    globalFormRef.current?.cancel();
    projectFormRef.current?.cancel();
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
      data-testid="settings-panel"
      style={{
        flex: 1,
        backgroundColor: colors.sidebar,
        display: "flex",
        flexDirection: "column",
        overflow: "hidden",
        borderRadius: colors.islandRadius,
        boxShadow: colors.islandShadow,
        border: colors.islandBorder,
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
          WebkitAppRegion: "drag",
        }}
      >
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
        {channel && <ChannelHeaderInfo channel={channel} colors={colors} />}
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
        <button onClick={tryClose} title="Close panel" style={headerBtnStyle} onMouseEnter={hoverIn} onMouseLeave={hoverOut}>
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <line x1="18" y1="6" x2="6" y2="18" />
            <line x1="6" y1="6" x2="18" y2="18" />
          </svg>
        </button>
      </div>

      {/* Body: two-column layout */}
      <div style={{ flex: 1, display: "flex", overflow: "hidden" }}>
        {/* Left: section nav */}
        {loaded && (
          <div
            style={{
              width: 150,
              flexShrink: 0,
              borderRight: `1px solid ${colors.border}`,
              overflow: "auto",
              padding: "8px 0",
            }}
          >
            {/* Global group */}
            <NavGroupLabel colors={colors}>Global</NavGroupLabel>
            {[...HARDCODED_GLOBAL, ...globalSchemaSections].map((name) => (
              <NavButton key={name} name={name} active={activeSection === name} colors={colors} onClick={() => setActiveSection(name)} />
            ))}
            {globalConfig && <NavButton name="JSON" active={activeSection === "__global_json__"} colors={colors} onClick={() => setActiveSection("__global_json__")} />}

            {/* Project group */}
            {projectSchemaSections.length > 0 && (
              <>
                <div style={{ height: 1, backgroundColor: colors.border, margin: "8px 12px" }} />
                <NavGroupLabel colors={colors}>Project</NavGroupLabel>
                {projectSchemaSections.map((name) => (
                  <NavButton key={`proj_${name}`} name={name} active={activeSection === `__proj_${name}`} colors={colors} onClick={() => setActiveSection(`__proj_${name}`)} />
                ))}
                <NavButton name="JSON" active={activeSection === "__project_json__"} colors={colors} onClick={() => setActiveSection("__project_json__")} />
              </>
            )}
          </div>
        )}

        {/* Right: section content */}
        <div
          style={{
            flex: 1,
            display: "flex",
            flexDirection: "column",
            overflow: activeSection.includes("json") ? "hidden" : "auto",
            padding: "16px 16px 24px",
          }}
        >
          {!loaded ? (
            <div style={{ color: colors.textDim, fontSize: 13, padding: "20px 0", textAlign: "center" }}>Loading...</div>
          ) : (
            <>
              {activeSection === "Daemon" && <DaemonSection colors={colors} daemonInfo={daemonInfo} restarting={restarting} onRestart={handleRestart} />}

              {activeSection === "Docker Image" && (
                <DockerImageSection colors={colors} imageBuildStatus={imageBuildStatus} imageStatus={imageStatus} imageUpdateAvailable={imageUpdateAvailable} onRebuildImage={onRebuildImage} />
              )}

              {activeSection === "__global_json__" && globalConfig && (
                <ConfigForm
                  ref={globalFormRef}
                  title="Global Config"
                  schema={schema}
                  config={globalConfig}
                  onSave={handleSaveGlobalConfig}
                  isGlobal={true}
                  colors={colors}
                  onDirtyChange={setGlobalDirty}
                  jsonOnly
                />
              )}

              {globalConfig && globalSchemaSections.includes(activeSection) && (
                <>
                  {(activeSection === "Workflows" || activeSection === "Prompt Shortcuts") &&
                    (() => {
                      const k: BuiltinKind = activeSection === "Workflows" ? "workflows" : "shortcuts";
                      return <RestoreBuiltinsBar colors={colors} kind={k} restoring={restoringByKind[k]} message={restoreMsgByKind[k]} onClick={handleRestoreBuiltins} />;
                    })()}
                  <ConfigForm
                    ref={globalFormRef}
                    title="Global Config"
                    schema={schema}
                    config={globalConfig}
                    onSave={handleSaveGlobalConfig}
                    isGlobal={true}
                    colors={colors}
                    onDirtyChange={setGlobalDirty}
                    visibleSection={activeSection}
                  />
                </>
              )}

              {activeSection === "__project_json__" && projectConfig && (
                <ConfigForm
                  ref={projectFormRef}
                  title="Project Config"
                  schema={schema}
                  config={projectConfig}
                  onSave={handleSaveProjectConfig}
                  isGlobal={false}
                  colors={colors}
                  onDirtyChange={setProjectDirty}
                  jsonOnly
                />
              )}

              {activeSection.startsWith("__proj_") && projectConfig && (
                <ConfigForm
                  ref={projectFormRef}
                  title="Project Config"
                  schema={schema}
                  config={projectConfig}
                  onSave={handleSaveProjectConfig}
                  isGlobal={false}
                  colors={colors}
                  onDirtyChange={setProjectDirty}
                  visibleSection={activeSection.replace("__proj_", "")}
                />
              )}
            </>
          )}
        </div>
      </div>

      {/* Floating save/cancel bar */}
      {configDirty && (
        <div
          style={{
            padding: "10px 16px",
            borderTop: `1px solid ${colors.border}`,
            display: "flex",
            gap: 8,
            justifyContent: "flex-end",
            alignItems: "center",
            backgroundColor: colors.sidebar,
            flexShrink: 0,
          }}
        >
          <button
            onClick={handleFloatingCancel}
            style={{
              padding: "6px 14px",
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
            onClick={handleFloatingSave}
            disabled={saving}
            style={{
              padding: "6px 14px",
              backgroundColor: colors.active,
              border: "none",
              borderRadius: 6,
              color: colors.white,
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
      )}

      {/* Inline keyframes for spinner */}
      <style>{`@keyframes spin { to { transform: rotate(360deg); } }`}</style>

      {/* Unsaved changes confirmation modal */}
      {showDirtyModal && (
        <div
          style={{ position: "fixed", inset: 0, zIndex: 9999, display: "flex", alignItems: "center", justifyContent: "center", backgroundColor: "rgba(0,0,0,0.5)" }}
          onClick={() => setShowDirtyModal(false)}
        >
          <div style={{ backgroundColor: colors.surface, borderRadius: 12, padding: "20px 24px", maxWidth: 360, boxShadow: "0 8px 32px rgba(0,0,0,0.3)" }} onClick={(e) => e.stopPropagation()}>
            <div style={{ fontSize: 14, fontWeight: 600, color: colors.text, marginBottom: 8 }}>Unsaved Changes</div>
            <div style={{ fontSize: 13, color: colors.textDim, marginBottom: 16, lineHeight: 1.5 }}>You have unsaved config changes. Discard them and close?</div>
            <div style={{ display: "flex", gap: 8, justifyContent: "flex-end" }}>
              <button
                onClick={() => setShowDirtyModal(false)}
                style={{
                  padding: "6px 14px",
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
                onClick={() => {
                  setShowDirtyModal(false);
                  handleFloatingCancel();
                  onClose();
                }}
                style={{
                  padding: "6px 14px",
                  backgroundColor: colors.error,
                  border: "none",
                  borderRadius: 6,
                  color: colors.white,
                  fontSize: 12,
                  fontWeight: 500,
                  cursor: "pointer",
                  fontFamily: "inherit",
                }}
              >
                Discard & Close
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

function RestoreBuiltinsBar({
  colors,
  kind,
  restoring,
  message,
  onClick,
}: {
  colors: ColorPalette;
  kind: BuiltinKind;
  restoring: boolean;
  message: string | null;
  onClick: (kind: BuiltinKind) => void;
}) {
  const label = kind === "workflows" ? "Restore built-in workflows" : "Restore built-in shortcuts";
  return (
    <div
      data-testid={`restore-builtins-${kind}`}
      style={{
        display: "flex",
        gap: 10,
        alignItems: "center",
        marginBottom: 12,
        padding: "8px 10px",
        backgroundColor: colors.bg,
        border: `1px solid ${colors.border}`,
        borderRadius: 6,
      }}
    >
      <button
        onClick={() => onClick(kind)}
        disabled={restoring}
        style={{
          padding: "5px 10px",
          backgroundColor: "transparent",
          border: `1px solid ${colors.border}`,
          borderRadius: 5,
          color: colors.text,
          fontSize: 11,
          cursor: restoring ? "default" : "pointer",
          opacity: restoring ? 0.6 : 1,
          fontFamily: "inherit",
        }}
      >
        {restoring ? "Restoring…" : label}
      </button>
      <span style={{ fontSize: 11, color: colors.textDim, lineHeight: 1.4 }}>{message ?? "Re-adds any built-ins missing from your config. Items you kept (or modified) are left untouched."}</span>
    </div>
  );
}

function NavGroupLabel({ colors, children }: { colors: ColorPalette; children: React.ReactNode }) {
  return (
    <div
      style={{
        fontSize: 9,
        fontWeight: 700,
        color: colors.textDim,
        textTransform: "uppercase",
        letterSpacing: 1,
        padding: "6px 16px 2px",
      }}
    >
      {children}
    </div>
  );
}

function NavButton({ name, active, colors, onClick }: { name: string; active: boolean; colors: ColorPalette; onClick: () => void }) {
  const testId = `settings-nav-${name.toLowerCase().replace(/\s+/g, "-")}`;
  return (
    <button
      data-testid={testId}
      onClick={onClick}
      style={{
        display: "block",
        width: "100%",
        textAlign: "left",
        padding: "6px 16px",
        border: "none",
        background: active ? colors.hoverBg : "transparent",
        color: active ? colors.text : colors.textDim,
        fontSize: 12,
        fontWeight: active ? 600 : 400,
        cursor: "pointer",
        fontFamily: "inherit",
        borderLeft: active ? `2px solid ${colors.active}` : "2px solid transparent",
      }}
    >
      {name}
    </button>
  );
}

function DaemonSection({ colors, daemonInfo, restarting, onRestart }: { colors: ColorPalette; daemonInfo: DaemonInfo | null; restarting: boolean; onRestart: () => void }) {
  return (
    <>
      {daemonInfo && (
        <div
          style={{
            backgroundColor: colors.bg,
            borderRadius: 8,
            padding: "10px 12px",
            marginBottom: 12,
            fontSize: 12,
            fontFamily: fonts.mono,
          }}
        >
          <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: daemonInfo.binaryPath ? 6 : 0 }}>
            <span style={{ color: colors.textDim }}>Status</span>
            <span style={{ display: "flex", alignItems: "center", gap: 6 }}>
              <span
                style={{
                  width: 6,
                  height: 6,
                  borderRadius: "50%",
                  backgroundColor: daemonInfo.running ? colors.active : colors.error,
                  display: "inline-block",
                }}
              />
              <span
                style={{
                  color: daemonInfo.running ? colors.active : colors.error,
                  fontWeight: 500,
                }}
              >
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
          onClick={onRestart}
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
          onMouseEnter={(e) => {
            if (!restarting) e.currentTarget.style.borderColor = colors.textDim;
          }}
          onMouseLeave={(e) => {
            e.currentTarget.style.borderColor = colors.border;
          }}
        >
          <svg
            width="12"
            height="12"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
            style={restarting ? { animation: "spin 1s linear infinite" } : undefined}
          >
            <path d="M21 12a9 9 0 1 1-3-6.7" />
            <polyline points="21,3 21,9 15,9" />
          </svg>
          {restarting ? "Restarting..." : "Restart Daemon"}
        </button>
      </div>
    </>
  );
}

function DockerImageSection({
  colors,
  imageBuildStatus,
  imageStatus,
  imageUpdateAvailable,
  onRebuildImage,
}: {
  colors: ColorPalette;
  imageBuildStatus?: ImageBuildStatusData | null;
  imageStatus: ImageStatusResponse | null;
  imageUpdateAvailable?: ImageUpdateAvailableData | null;
  onRebuildImage?: () => void;
}) {
  return (
    <>
      <div
        style={{
          backgroundColor: colors.bg,
          borderRadius: 8,
          padding: "10px 12px",
          marginBottom: 12,
          fontSize: 12,
          fontFamily: fonts.mono,
        }}
      >
        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 6 }}>
          <span style={{ color: colors.textDim }}>Status</span>
          <span style={{ display: "flex", alignItems: "center", gap: 6 }}>
            <span
              style={{
                width: 6,
                height: 6,
                borderRadius: "50%",
                backgroundColor: imageBuildStatus?.state === "building" ? colors.warning : imageBuildStatus?.state === "failed" ? colors.error : colors.active,
                display: "inline-block",
              }}
            />
            <span
              style={{
                color: imageBuildStatus?.state === "building" ? colors.warning : imageBuildStatus?.state === "failed" ? colors.error : colors.active,
                fontWeight: 500,
              }}
            >
              {imageBuildStatus?.state === "building" ? "Building" : imageBuildStatus?.state === "failed" ? "Failed" : "Ready"}
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
        <div
          style={{
            backgroundColor: "rgba(255, 200, 50, 0.1)",
            border: `1px solid rgba(255, 200, 50, 0.3)`,
            borderRadius: 8,
            padding: "8px 12px",
            marginBottom: 12,
            fontSize: 12,
            color: colors.warning,
          }}
        >
          Claude Code update available: v{imageUpdateAvailable.current_version} → v{imageUpdateAvailable.latest_version}
        </div>
      )}

      {imageBuildStatus?.state === "failed" && imageBuildStatus.error && (
        <div
          style={{
            fontSize: 11,
            color: colors.error,
            marginBottom: 8,
          }}
        >
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
          onMouseEnter={(e) => {
            if (imageBuildStatus?.state !== "building") e.currentTarget.style.borderColor = colors.textDim;
          }}
          onMouseLeave={(e) => {
            e.currentTarget.style.borderColor = colors.border;
          }}
        >
          <svg
            width="12"
            height="12"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
            style={imageBuildStatus?.state === "building" ? { animation: "spin 1s linear infinite" } : undefined}
          >
            <path d="M21 12a9 9 0 1 1-3-6.7" />
            <polyline points="21,3 21,9 15,9" />
          </svg>
          {imageBuildStatus?.state === "building" ? "Building..." : "Rebuild Image"}
        </button>
      </div>
    </>
  );
}
