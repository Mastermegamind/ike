import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { viteSingleFile } from "vite-plugin-singlefile";

// `vite build`               → dist/ for embedding in the Ike control plane
// `vite build --mode single` → one self-contained HTML file (demo/preview)
export default defineConfig(({ mode }) => ({
  plugins: [react(), ...(mode === "single" ? [viteSingleFile()] : [])],
  base: "./",
  build: {
    outDir: mode === "single" ? "dist-single" : "dist",
    target: "es2022",
  },
  server: {
    // The control-plane API will listen here during development.
    proxy: { "/api": "http://127.0.0.1:8443" },
  },
}));
