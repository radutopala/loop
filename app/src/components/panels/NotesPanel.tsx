import "@fontsource/jetbrains-mono/400.css";
import { useCallback, useEffect, useRef, useState } from "react";
import { EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter, drawSelection } from "@codemirror/view";
import { EditorState, Compartment } from "@codemirror/state";
import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import { markdown } from "@codemirror/lang-markdown";
import { marked } from "marked";
import { fonts } from "../../theme";
import { useTheme } from "../../ThemeContext";
import { fetchFileContent, saveFileContent, createDir } from "../../api/loopApi";
import { buildEditorTheme } from "./editorTheme";
import { buildMarkdownStyles } from "./FilePanel";

const NOTES_PATH = ".loop/NOTES.md";
const SAVE_DEBOUNCE_MS = 1500;
const PREVIEW_DEBOUNCE_MS = 300;

interface NotesPanelProps {
  channelId: string;
}

export function NotesPanel({ channelId }: NotesPanelProps) {
  const { colors, fontSizes } = useTheme();
  const editorRef = useRef<HTMLDivElement>(null);
  const previewRef = useRef<HTMLDivElement>(null);
  const viewRef = useRef<EditorView | null>(null);
  const themeComp = useRef(new Compartment());
  const initializedRef = useRef(false);
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const previewTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const pendingContentRef = useRef<string | null>(null);
  const scrollSyncSource = useRef<"editor" | "preview" | null>(null);
  const [saveStatus, setSaveStatus] = useState<"idle" | "unsaved" | "saving" | "saved" | "error">("idle");
  const [viewMode, setViewMode] = useState<"editor" | "both" | "preview">("editor");
  const [previewHtml, setPreviewHtml] = useState("");

  const updatePreview = useCallback((doc: string) => {
    if (previewTimerRef.current) clearTimeout(previewTimerRef.current);
    previewTimerRef.current = setTimeout(() => {
      previewTimerRef.current = null;
      setPreviewHtml(marked.parse(doc, { async: false }) as string);
    }, PREVIEW_DEBOUNCE_MS);
  }, []);

  const doSave = useCallback(async (content: string) => {
    setSaveStatus("saving");
    try {
      await saveFileContent(channelId, NOTES_PATH, content);
      setSaveStatus("saved");
    } catch {
      // Directory may not exist yet — create .loop/ and retry once.
      try {
        await createDir(channelId, ".loop");
        await saveFileContent(channelId, NOTES_PATH, content);
        setSaveStatus("saved");
      } catch {
        setSaveStatus("error");
      }
    }
  }, [channelId]);

  const scheduleSave = useCallback((content: string) => {
    pendingContentRef.current = content;
    setSaveStatus("unsaved");
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => {
      debounceRef.current = null;
      pendingContentRef.current = null;
      doSave(content);
    }, SAVE_DEBOUNCE_MS);
  }, [doSave]);

  // Mount: fetch content and create editor.
  useEffect(() => {
    let cancelled = false;
    initializedRef.current = false;

    fetchFileContent(channelId, NOTES_PATH)
      .then(({ content }) => content)
      .catch(() => "") // 404 or error → empty
      .then((initialContent) => {
        if (cancelled || !editorRef.current) return;

        setPreviewHtml(marked.parse(initialContent, { async: false }) as string);

        const view = new EditorView({
          state: EditorState.create({
            doc: initialContent,
            extensions: [
              themeComp.current.of(buildEditorTheme(colors, fontSizes.panels)),
              markdown(),
              EditorView.lineWrapping,
              history(),
              keymap.of([...defaultKeymap, ...historyKeymap]),
              lineNumbers(),
              highlightActiveLine(),
              highlightActiveLineGutter(),
              drawSelection(),
              EditorView.updateListener.of((update) => {
                if (update.docChanged && initializedRef.current) {
                  const doc = update.state.doc.toString();
                  scheduleSave(doc);
                  updatePreview(doc);
                }
              }),
            ],
          }),
          parent: editorRef.current,
        });
        viewRef.current = view;

        // Scroll sync: editor → preview.
        const scroller = editorRef.current;
        const onEditorScroll = () => {
          if (scrollSyncSource.current === "preview" || !scroller) return;
          scrollSyncSource.current = "editor";
          const el = previewRef.current;
          if (el) {
            const pct = scroller.scrollTop / Math.max(1, scroller.scrollHeight - scroller.clientHeight);
            el.scrollTop = pct * (el.scrollHeight - el.clientHeight);
          }
          requestAnimationFrame(() => { scrollSyncSource.current = null; });
        };
        scroller.addEventListener("scroll", onEditorScroll);

        initializedRef.current = true;

        return () => {
          scroller.removeEventListener("scroll", onEditorScroll);
        };
      });

    return () => {
      cancelled = true;
      if (previewTimerRef.current) { clearTimeout(previewTimerRef.current); previewTimerRef.current = null; }
      // Flush pending save on unmount.
      if (debounceRef.current) {
        clearTimeout(debounceRef.current);
        debounceRef.current = null;
      }
      if (pendingContentRef.current !== null) {
        doSave(pendingContentRef.current);
        pendingContentRef.current = null;
      }
      viewRef.current?.destroy();
      viewRef.current = null;
    };
  }, [channelId]); // eslint-disable-line react-hooks/exhaustive-deps

  // Hot-swap theme.
  useEffect(() => {
    if (!viewRef.current) return;
    viewRef.current.dispatch({
      effects: themeComp.current.reconfigure(buildEditorTheme(colors, fontSizes.panels)),
    });
  }, [colors, fontSizes.panels]);

  const statusLabel = saveStatus === "saving" ? "Saving..."
    : saveStatus === "saved" ? "Saved"
    : saveStatus === "error" ? "Save failed"
    : saveStatus === "unsaved" ? "\u25CF" // bullet
    : "";

  return (
    <div data-testid="notes-panel" style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden", backgroundColor: colors.sidebar, zoom: fontSizes.panels / 12 }}>
      <div style={{
        height: 22,
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        padding: "0 10px",
        backgroundColor: colors.surface,
        borderBottom: `1px solid ${colors.border}`,
        flexShrink: 0,
        fontSize: 11,
        fontFamily: fonts.sans,
      }}>
        <span style={{ color: colors.textDim, opacity: 0.7 }}>.loop/NOTES.md</span>
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <div style={{ display: "flex", border: `1px solid ${colors.border}`, borderRadius: 4, overflow: "hidden" }}>
            {(["editor", "both", "preview"] as const).map((mode) => (
              <button
                key={mode}
                onClick={() => setViewMode(mode)}
                title={mode === "editor" ? "Editor only" : mode === "both" ? "Editor + Preview" : "Preview only"}
                style={{
                  fontSize: 10,
                  color: viewMode === mode ? colors.active : colors.textDim,
                  background: viewMode === mode ? `${colors.active}18` : "none",
                  border: "none",
                  borderRight: mode !== "preview" ? `1px solid ${colors.border}` : undefined,
                  cursor: "pointer",
                  padding: "2px 6px",
                  lineHeight: 1,
                }}
              >
                {mode === "editor" ? "Edit" : mode === "both" ? "Split" : "Preview"}
              </button>
            ))}
          </div>
          <span style={{
            color: saveStatus === "error" ? colors.error ?? "#f87171" : colors.textDim,
            opacity: saveStatus === "idle" ? 0 : 0.7,
            transition: "opacity 0.3s",
          }}>
            {statusLabel}
          </span>
        </div>
      </div>
      <div style={{ flex: 1, display: "flex", overflow: "hidden" }}>
        <div
          ref={editorRef}
          style={{
            flex: 1,
            overflow: "auto",
            display: viewMode === "preview" ? "none" : undefined,
          }}
        />
        {viewMode !== "editor" && previewHtml && (
          <>
            {viewMode === "both" && <div style={{ width: 1, backgroundColor: colors.border, flexShrink: 0 }} />}
            <div
              ref={previewRef}
              className="readme-content"
              dangerouslySetInnerHTML={{ __html: previewHtml }}
              onScroll={() => {
                if (scrollSyncSource.current === "editor") return;
                scrollSyncSource.current = "preview";
                const el = previewRef.current;
                const ed = editorRef.current;
                if (el && ed) {
                  const pct = el.scrollTop / Math.max(1, el.scrollHeight - el.clientHeight);
                  ed.scrollTop = pct * (ed.scrollHeight - ed.clientHeight);
                }
                requestAnimationFrame(() => { scrollSyncSource.current = null; });
              }}
              style={{
                flex: 1,
                overflow: "auto",
                padding: "12px 16px",
                color: colors.textLight,
                fontSize: 13,
                fontFamily: fonts.sans,
                lineHeight: 1.6,
              }}
            />
            <style>{buildMarkdownStyles(colors)}</style>
          </>
        )}
      </div>
    </div>
  );
}
