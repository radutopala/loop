/** The explanation's page with a question mark, in the composer's Explain switch, the turns' Explain action and the Explain pane. */
export function ExplainIcon({ size }: { size: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
      <path d="M9.5 11a2.5 2.5 0 1 1 3.5 2.3c-.6.3-1 .8-1 1.5" />
      <path d="M12 18h.01" />
    </svg>
  );
}
