import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      "/api": "http://127.0.0.1:8100",
    },
  },
  build: {
    outDir: "dist",
    // .gitkeep must survive so go:embed compiles before the first build.
    emptyOutDir: false,
  },
  test: {
    environment: "node",
  },
});
