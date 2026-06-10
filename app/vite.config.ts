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

// LOOP_NO_ELECTRON=1 skips the Electron plugins so `vite` serves the renderer
// as a plain browser app — `npm run dev` inside a Linux/agent container would
// otherwise auto-launch the host-platform Electron binary and crash. The
// browser-targeted vite.browser.config.ts used by the BDD harness covers the
// same need for tests; this flag covers interactive dev.
const noElectron = process.env.LOOP_NO_ELECTRON === "1";

export default defineConfig({
  plugins: [
    react(),
    ...(noElectron
      ? []
      : [
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
        ]),
  ],
  base: "./",
  server: {
    allowedHosts: ["host.docker.internal"],
    proxy: {
      "/api": {
        target: process.env.LOOP_API_URL || "http://localhost:8222",
        changeOrigin: true,
        ws: true,
      },
    },
  },
  build: {
    outDir: "dist",
  },
});
