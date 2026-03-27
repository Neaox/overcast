import path from "path"
import { defineConfig } from "vite"
import react from "@vitejs/plugin-react"
import tailwindcss from "@tailwindcss/vite"
import tanstackRouter from "@tanstack/router-plugin/vite"
import { honoDevPlugin } from "./api/src/vite-plugin"

export default defineConfig({
  plugins: [
    tanstackRouter({
      routesDirectory: "./src/routes",
      generatedRouteTree: "./src/routeTree.gen.ts",
    }),
    honoDevPlugin(),
    react(),
    tailwindcss(),
  ],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    port: 3000,
    host: true,
    watch: {
      usePolling: true,
    },
  },
})
