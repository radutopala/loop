/** Shared Loop branding mark — infinity icon with soft glow + "Loop" text. */
export function LoopLogo() {
  const d = "M12 12c-2-2.67-4-4-6-4a4 4 0 1 0 0 8c2 0 4-1.33 6-4Zm0 0c2 2.67 4 4 6 4a4 4 0 0 0 0-8c-2 0-4 1.33-6 4Z";
  return (
    <div style={{ display: "flex", flexDirection: "column", alignItems: "center", opacity: 0.18 }}>
      <svg width="64" height="36" viewBox="2 6 20 12" fill="none" strokeLinecap="round" strokeLinejoin="round">
        <defs>
          <filter id="loop-glow">
            <feGaussianBlur stdDeviation="0.6" result="blur" />
            <feMerge>
              <feMergeNode in="blur" />
              <feMergeNode in="SourceGraphic" />
            </feMerge>
          </filter>
        </defs>
        <path d={d} stroke="white" strokeWidth="1.5" filter="url(#loop-glow)" />
      </svg>
      <span style={{ color: "white", fontSize: 16, fontWeight: 600, letterSpacing: 1, marginTop: 4 }}>Loop</span>
    </div>
  );
}
