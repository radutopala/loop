import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Browser-only Vite config (no Electron plugins).
// Used by `make app-dev-docker` to serve the frontend in a plain browser.
const apiTarget = process.env.LOOP_API_URL || "http://localhost:8222";

export default defineConfig({
  plugins: [react()],
  base: "./",
  server: {
    host: "0.0.0.0",
    port: 5173,
    allowedHosts: ["host.docker.internal"],
    proxy: {
      "/api": {
        target: apiTarget,
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: "dist",
  },
});
