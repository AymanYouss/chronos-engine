import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// In dev, proxy the inspector API to the local control plane so the SPA and the
// Go server can run side by side without CORS friction.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api": { target: "http://localhost:8080", changeOrigin: true },
      "/healthz": { target: "http://localhost:8080", changeOrigin: true },
    },
  },
  build: {
    outDir: "dist",
    sourcemap: false,
  },
});
