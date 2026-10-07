import { describe, expect, it } from "vitest";
import { type LangId, langIdFor } from "./editorLanguage";

describe("langIdFor", () => {
  it.each<[string, LangId | null]>([
    ["src/app.js", "javascript"],
    ["a.mjs", "javascript"],
    ["a.ts", "typescript"],
    ["src/App.tsx", "tsx"],
    ["cmd/loop/main.go", "go"],
    ["a.py", "python"],
    ["a.jsonl", "json"],
    ["README.md", "markdown"],
    ["a.scss", "css"],
    ["icon.svg", "html"],
    [".github/ci.yml", "yaml"],
    ["src/main.rs", "rust"],
    ["schema.sql", "sql"],
    ["Cargo.toml", "toml"],
    ["scripts/build.sh", "shell"],
    ["a.zsh", "shell"],
    ["home/.bashrc", "shell"],
    [".zshrc", "shell"],
    ["container/Dockerfile", "dockerfile"],
    ["Dockerfile.dev", "dockerfile"],
    ["container/chrome.Dockerfile", "dockerfile"],
    ["Containerfile", "dockerfile"],
    [".dockerignore", null],
    ["Makefile", null],
    ["notes.txt", null],
    ["", null],
  ])("%s → %s", (path, want) => {
    expect(langIdFor(path)).toBe(want);
  });
});
