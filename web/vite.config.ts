import { resolve } from "node:path"
import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig } from "vite"

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": resolve(import.meta.dirname, "./src"),
    },
  },
  // Relative asset URLs keep the UI working behind a path-prefixing proxy.
  base: "./",
  build: {
    // Embedded into the Go binary by internal/web.
    outDir: "../internal/web/dist",
    emptyOutDir: true,
  },
  server: {
    // `npm run dev` expects the Go server on its default address.
    proxy: { "/api": "http://127.0.0.1:8080" },
  },
})
