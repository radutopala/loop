/**
 * Expand/collapse triangle, pointed wherever the caller needs it.
 *
 * An SVG rather than a ▶/▼ glyph: the glyph sits high and left inside its em
 * box, so centring the box still leaves the triangle looking off-centre next
 * to a label — and rotating a glyph turns that horizontal bias into a vertical
 * one. The path below is centred in its own viewBox (x 3–7, y 1.5–8.5 of 10),
 * so it stays centred at any rotation.
 *
 * `deg` is the rotation applied to the base right-pointing triangle: `0` for
 * right, `90` for down, `-90` for up.
 */
export function Chevron({ deg, size = 10, style }: { deg: number; size?: number; style?: React.CSSProperties }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 10 10"
      fill="currentColor"
      style={{ flexShrink: 0, display: "block", transform: deg === 0 ? "none" : `rotate(${deg}deg)`, transition: "transform 0.1s", ...style }}
    >
      <path d="M3 1.5 L7 5 L3 8.5 Z" />
    </svg>
  );
}
