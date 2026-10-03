/** The composer's Continue button: a double chevron, "keep going", drawn like the Learn and Explain icons. */
export function ContinueIcon({ size }: { size: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.25" strokeLinecap="round" strokeLinejoin="round">
      <path d="M6 17l5-5-5-5" />
      <path d="M13 17l5-5-5-5" />
    </svg>
  );
}
