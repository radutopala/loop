import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

// Two projects:
// - unit: pure frontend logic (diff parsing, gutter mark computation, …) in
//   plain node, so anything under test must not touch window/document at
//   import time. A .test.ts file that needs a DOM opts in with a
//   `// @vitest-environment jsdom` pragma.
// - components: React component tests (*.test.tsx) with Testing Library in
//   jsdom; src/test/setup.ts unmounts after each test.
// Browser-level behavior stays in the BDD suite (test/component/features/frontend).
// `npm run test:coverage` measures both projects together.
export default defineConfig({
  test: {
    coverage: {
      provider: "v8",
      include: ["src/**", "electron/**", "packages/**/src/**"],
      exclude: ["**/*.test.*", "src/test/**", "src/main.tsx", "src/vite-env.d.ts", "**/*.d.ts", "src/assets/**"],
      reporter: ["text-summary", "json-summary", "html"],
      thresholds: {
        statements: 100,
        branches: 100,
        functions: 100,
        lines: 100,
      },
    },
    projects: [
      {
        test: {
          name: "unit",
          environment: "node",
          include: ["src/**/*.test.ts", "packages/**/*.test.ts", "electron/**/*.test.ts"],
        },
      },
      {
        plugins: [react()],
        test: {
          name: "components",
          environment: "jsdom",
          include: ["src/**/*.test.tsx"],
          setupFiles: ["src/test/setup.ts"],
        },
      },
    ],
  },
});
