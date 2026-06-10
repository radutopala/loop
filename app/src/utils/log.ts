/**
 * Error reporter for fire-and-forget promises. `.catch(() => {})` hides real
 * failures (failed saves, dead endpoints) with no trace; use
 * `.catch(logErr("saving file"))` so best-effort calls stay non-fatal but
 * leave a console breadcrumb with context.
 */
export function logErr(context: string): (err: unknown) => void {
  return (err: unknown) => {
    console.warn(`[loop] ${context}:`, err);
  };
}
