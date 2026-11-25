export type LangId = "javascript" | "typescript" | "tsx" | "go" | "python" | "json" | "markdown" | "css" | "html" | "yaml" | "rust" | "sql" | "toml" | "shell" | "dockerfile";

const BY_EXTENSION: Record<string, LangId> = {
  js: "javascript",
  jsx: "javascript",
  mjs: "javascript",
  cjs: "javascript",
  ts: "typescript",
  tsx: "tsx",
  go: "go",
  py: "python",
  json: "json",
  jsonl: "json",
  md: "markdown",
  mdx: "markdown",
  css: "css",
  scss: "css",
  html: "html",
  htm: "html",
  svg: "html",
  yaml: "yaml",
  yml: "yaml",
  rs: "rust",
  sql: "sql",
  toml: "toml",
  sh: "shell",
  bash: "shell",
  zsh: "shell",
  dockerfile: "dockerfile",
  containerfile: "dockerfile",
};

const SHELL_FILES = new Set([".bashrc", ".bash_profile", ".zshrc", ".zprofile", ".profile"]);

/**
 * The language a file is highlighted as, from its name. Dockerfiles are
 * matched by name as well as extension: "Dockerfile", "Containerfile",
 * "Dockerfile.dev" and "chrome.Dockerfile" all count.
 */
export function langIdFor(path: string): LangId | null {
  const slash = path.lastIndexOf("/");
  const name = (slash < 0 ? path : path.slice(slash + 1)).toLowerCase();
  if (SHELL_FILES.has(name)) return "shell";
  const base = name.split(".")[0];
  if (base === "dockerfile" || base === "containerfile") return "dockerfile";
  const dot = name.lastIndexOf(".");
  if (dot < 0) return null;
  return BY_EXTENSION[name.slice(dot + 1)] ?? null;
}
