import { defineConfig } from "vite";
import { copyFileSync, mkdirSync } from "node:fs";
import react from "@vitejs/plugin-react";
import electron from "vite-plugin-electron";
import renderer from "vite-plugin-electron-renderer";

// Copy the CJS preload to dist-electron. vite-plugin-electron outputs ESM which
// Electron cannot require() as a preload (package.json has "type": "module").
// Using .cjs extension forces CommonJS regardless.
mkdirSync("dist-electron", { recursive: true });
copyFileSync("electron/preload.cjs", "dist-electron/preload.cjs");

export default defineConfig({
  plugins: [
    react(),
    electron([
      {
        entry: "electron/main.ts",
        vite: {
          build: {
            outDir: "dist-electron",
            rollupOptions: {
              external: ["electron"],
            },
          },
        },
      },
    ]),
    renderer(),
  ],
  base: "./",
  server: {
    allowedHosts: ["host.docker.internal"],
  },
  build: {
    outDir: "dist",
  },
});
