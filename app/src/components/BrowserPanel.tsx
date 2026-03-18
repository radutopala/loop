import { useCallback, useEffect, useRef, useState } from "react";
import { useTheme } from "../ThemeContext";
import { useBrowserWs } from "../hooks/useBrowserWs";

interface BrowserPanelProps {
  channelId: string;
}

export function BrowserPanel({ channelId }: BrowserPanelProps) {
  const { colors } = useTheme();
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const canvasContainerRef = useRef<HTMLDivElement>(null);
  const urlInputRef = useRef<HTMLInputElement>(null);
  const [url, setUrl] = useState("");
  const [title, setTitle] = useState("");
  const [error, setError] = useState<string | null>(null);

  const {
    connected,
    started,
    startBrowser,
    stopBrowser,
    startStreaming,
    navigate,
    reload,
    goBack,
    goForward,
    sendInput,
  } = useBrowserWs({
    channelId,
    onFrame: useCallback((data: ArrayBuffer) => {
      const canvas = canvasRef.current;
      if (!canvas) return;
      const ctx = canvas.getContext("2d");
      if (!ctx) return;

      const blob = new Blob([data], { type: "image/jpeg" });
      const img = new Image();
      img.onload = () => {
        canvas.width = img.width;
        canvas.height = img.height;
        ctx.drawImage(img, 0, 0);
        URL.revokeObjectURL(img.src);
      };
      img.src = URL.createObjectURL(blob);
    }, []),
    onPageInfo: useCallback((pageUrl: string, pageTitle: string) => {
      setUrl(pageUrl);
      setTitle(pageTitle);
    }, []),
    onError: useCallback((msg: string) => {
      setError(msg);
    }, []),
    onStarted: useCallback(() => {
      setError(null);
    }, []),
  });

  // Auto-start browser when connected.
  useEffect(() => {
    if (connected && !started) {
      startBrowser();
    }
  }, [connected, started, startBrowser]);

  // Start streaming once browser is started, and re-request when the panel
  // becomes visible again (e.g. after a layout switch) to ensure frames flow.
  useEffect(() => {
    if (!started) return;
    startStreaming(1920, 1080);
    // Force Chrome to send a new frame by reloading the page.
    // Screencast only sends frames on page changes, so a static about:blank
    // won't produce frames after a reconnect without this.
    reload();
  }, [started, startStreaming, reload]);

  const handleNavigate = useCallback(
    (e: React.FormEvent) => {
      e.preventDefault();
      const input = urlInputRef.current;
      if (!input) return;
      let targetUrl = input.value.trim();
      if (!targetUrl) return;

      // Add protocol if missing.
      if (!/^https?:\/\//i.test(targetUrl)) {
        targetUrl = "https://" + targetUrl;
      }

      navigate(targetUrl);
    },
    [navigate],
  );

  // Forward mouse/keyboard events to the browser.
  const handleCanvasMouseEvent = useCallback(
    (e: React.MouseEvent<HTMLCanvasElement>) => {
      const canvas = canvasRef.current;
      if (!canvas) return;
      const rect = canvas.getBoundingClientRect();
      const scaleX = canvas.width / rect.width;
      const scaleY = canvas.height / rect.height;
      const x = (e.clientX - rect.left) * scaleX;
      const y = (e.clientY - rect.top) * scaleY;

      switch (e.type) {
        case "click":
          sendInput({ type: "click", x, y, button: "left", clickCount: 1 });
          break;
        case "dblclick":
          sendInput({ type: "click", x, y, button: "left", clickCount: 2 });
          break;
        case "contextmenu":
          e.preventDefault();
          sendInput({ type: "click", x, y, button: "right", clickCount: 1 });
          break;
        case "mousemove":
          sendInput({ type: "mousemove", x, y });
          break;
      }
    },
    [sendInput],
  );

  const handleCanvasWheel = useCallback(
    (e: React.WheelEvent<HTMLCanvasElement>) => {
      const canvas = canvasRef.current;
      if (!canvas) return;
      const rect = canvas.getBoundingClientRect();
      const scaleX = canvas.width / rect.width;
      const scaleY = canvas.height / rect.height;
      const x = (e.clientX - rect.left) * scaleX;
      const y = (e.clientY - rect.top) * scaleY;
      sendInput({ type: "scroll", x, y, deltaX: e.deltaX, deltaY: e.deltaY });
    },
    [sendInput],
  );

  const handleCanvasKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLCanvasElement>) => {
      e.preventDefault();
      e.stopPropagation();
      if (e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
        sendInput({ type: "typetext", text: e.key });
      } else {
        sendInput({ type: "keypress", key: e.key });
      }
    },
    [sendInput],
  );

  const toolbarStyle: React.CSSProperties = {
    display: "flex",
    alignItems: "center",
    gap: 4,
    padding: "4px 8px",
    backgroundColor: colors.surface,
    borderBottom: `1px solid ${colors.border}`,
    flexShrink: 0,
  };

  const navBtnStyle: React.CSSProperties = {
    background: "none",
    border: "none",
    color: colors.textDim,
    cursor: "pointer",
    padding: "4px 6px",
    borderRadius: 4,
    fontSize: 14,
    lineHeight: 1,
  };

  const urlBarStyle: React.CSSProperties = {
    flex: 1,
    padding: "4px 8px",
    backgroundColor: colors.sidebar,
    border: `1px solid ${colors.border}`,
    borderRadius: 4,
    color: colors.textLight,
    fontSize: 12,
    outline: "none",
  };

  return (
    <div
      style={{
        flex: 1,
        display: "flex",
        flexDirection: "column",
        backgroundColor: colors.sidebar,
        overflow: "hidden",
      }}
    >
      {/* Toolbar */}
      <div style={toolbarStyle}>
        <button
          style={navBtnStyle}
          onClick={goBack}
          title="Back"
        >
          ←
        </button>
        <button
          style={navBtnStyle}
          onClick={goForward}
          title="Forward"
        >
          →
        </button>
        <button
          style={navBtnStyle}
          onClick={reload}
          title="Reload"
        >
          ↻
        </button>
        <form onSubmit={handleNavigate} style={{ flex: 1, display: "flex" }}>
          <input
            ref={urlInputRef}
            type="text"
            style={urlBarStyle}
            defaultValue={url}
            placeholder="Enter URL..."
            onFocus={(e) => e.target.select()}
          />
        </form>
        {started ? (
          <button
            style={{ ...navBtnStyle, color: colors.error }}
            onClick={stopBrowser}
            title="Stop browser"
          >
            ✕
          </button>
        ) : (
          <button
            style={navBtnStyle}
            onClick={startBrowser}
            title="Start browser"
            disabled={!connected}
          >
            ▶
          </button>
        )}
      </div>

      {/* Title bar */}
      {title && (
        <div
          style={{
            padding: "2px 8px",
            fontSize: 11,
            color: colors.textDim,
            backgroundColor: colors.surface,
            borderBottom: `1px solid ${colors.border}`,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          {title}
        </div>
      )}

      {/* Error bar */}
      {error && (
        <div
          style={{
            padding: "4px 8px",
            fontSize: 12,
            color: colors.error,
            backgroundColor: colors.errorBannerBg,
            borderBottom: `1px solid ${colors.border}`,
          }}
        >
          {error}
        </div>
      )}

      {/* Canvas */}
      <div
        ref={canvasContainerRef}
        style={{
          flex: 1,
          overflow: "hidden",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          backgroundColor: colors.bg,
        }}
      >
        {started ? (
          <canvas
            ref={canvasRef}
            tabIndex={0}
            style={{
              maxWidth: "100%",
              maxHeight: "100%",
              outline: "none",
              cursor: "default",
            }}
          onClick={(e) => { handleCanvasMouseEvent(e); canvasRef.current?.focus(); }}
          onDoubleClick={handleCanvasMouseEvent}
          onContextMenu={handleCanvasMouseEvent}
          onMouseMove={handleCanvasMouseEvent}
          onWheel={handleCanvasWheel}
          onKeyDown={handleCanvasKeyDown}
        />
        ) : (
          <div
            style={{
              color: colors.textDim,
              fontSize: 14,
            }}
          >
            {connected
              ? "Starting browser..."
              : "Connecting..."}
          </div>
        )}
      </div>
    </div>
  );
}
