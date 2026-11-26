import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

const server = "http://127.0.0.1:7000";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: { proxy: { "/api": server, "/github": server } },
  build: { outDir: "dist", emptyOutDir: true, assetsDir: "assets" },
  test: { environment: "jsdom", globals: true, setupFiles: ["src/test-setup.ts"] },
});
