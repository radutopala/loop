/**
 * The home directory that path and its display form share, where the
 * display form shows the home as ~ ("/Users/me/dev/x", "~/dev/x" →
 * "/Users/me"). Undefined when path isn't under it.
 */
export function homeOf(path: string | undefined, displayPath: string | undefined): string | undefined {
  if (!path || !displayPath) return undefined;
  if (displayPath === "~") return path;
  if (!displayPath.startsWith("~/")) return undefined;
  const rest = displayPath.slice(1);
  return path.endsWith(rest) && path.length > rest.length ? path.slice(0, -rest.length) : undefined;
}

/** path with home shown as ~, as the daemon shows it: unchanged when it
 *  isn't under home, or home isn't known. */
export function tildePath(path: string, home: string | undefined): string {
  if (!home) return path;
  if (path === home) return "~";
  return path.startsWith(`${home}/`) ? `~${path.slice(home.length)}` : path;
}
