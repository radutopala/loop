import { defineConfig } from "vitest/config";

// Unit tests for pure frontend logic (diff parsing, gutter mark computation,
// …). Runs in plain node — no DOM, no Electron — so anything under test must
// not touch window/document at import time. Browser-level behavior stays in
// the BDD suite (test/component/features/frontend).
export default defineConfig({
  test: {
    environment: "node",
    include: ["src/**/*.test.ts"],
  },
});
