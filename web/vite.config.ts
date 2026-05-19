import fs from "node:fs"
import path from "node:path"
import { defineConfig } from "vite"
import type { PluginOption } from "vite"

export default defineConfig(async () => {
  const usePolling = needsPolling()
  const enableDevtools = process.env.DEVTOOLS === "1"

  // Load plugins in parallel to cut ~10s off startup (they were loading
  // sequentially as top-level imports, each with large dep trees).
  const pluginLoaders: Promise<unknown>[] = [
    import("@tanstack/router-plugin/vite"),
    import("@vitejs/plugin-react"),
    import("@tailwindcss/vite"),
    import("./api/src/vite-plugin"),
    import("vite-plugin-mkcert"),
  ]
  if (enableDevtools) {
    pluginLoaders.push(import("@tanstack/devtools-vite"))
  }

  const results = await Promise.all(pluginLoaders)
  const { default: tanstackRouter } = results[0] as {
    default: typeof import("@tanstack/router-plugin/vite").default
  }
  const { default: react } = results[1] as {
    default: typeof import("@vitejs/plugin-react").default
  }
  const tailwindcss = (results[2] as { default: typeof import("@tailwindcss/vite").default })
    .default
  const { honoDevPlugin } = results[3] as {
    honoDevPlugin: typeof import("./api/src/vite-plugin").honoDevPlugin
  }
  const mkcert = (results[4] as { default: typeof import("vite-plugin-mkcert").default }).default

  const plugins: PluginOption[] = []

  if (enableDevtools) {
    const { devtools } = results[5] as {
      devtools: typeof import("@tanstack/devtools-vite").devtools
    }
    plugins.push(devtools() as PluginOption)
  }

  plugins.push(
    mkcert({ savePath: path.resolve(__dirname, "../.cert") }),
    tanstackRouter({
      routesDirectory: "./src/routes",
      generatedRouteTree: "./src/routeTree.gen.ts",
      autoCodeSplitting: true,
      routeFileIgnorePattern: String.raw`(^|/)?.*\.(test|spec)\.(ts|tsx)$`,
    }),
    honoDevPlugin(),
    react(),
    tailwindcss(),
  )
  return {
    plugins,
    resolve: {
      alias: {
        "@": path.resolve(__dirname, "./src"),
      },
      // Reduce filesystem checks during module resolution (Vite perf guide).
      // The project uses .tsx/.ts exclusively — skip probing .mjs/.mts/.jsx.
      extensions: [".ts", ".tsx", ".js", ".json"],
    },
    optimizeDeps: {
      // Only pre-bundle deps needed for the shell/initial page load.
      // Route-specific deps (monaco, xyflow, etc.) are discovered on demand
      // and benefit from autoCodeSplitting.
      include: [
        "react",
        "react-dom",
        "react-simple-code-editor",
        "@tanstack/react-query",
        "@tanstack/react-router",
        "lucide-react",
      ],
      // Don't block startup waiting for a full crawl of all routes.
      holdUntilCrawlEnd: false,
    },
    server: {
      port: 3000,
      host: true,
      open: false,
      watch: usePolling ? { usePolling: true, interval: 1000 } : {},
      // Pre-transform the app shell so the first page load is fast.
      warmup: {
        clientFiles: [
          "./src/main.tsx",
          "./src/routes/__root.tsx",
          "./src/routes/index.tsx",
          "./src/components/layout/app-shell.tsx",
        ],
      },
    },
  }
})

// Detect whether the workspace is on a host-mounted volume (Windows/macOS)
// where native fs.watch does not work reliably. If /.dockerenv exists we are
// in a container; if /proc/1/mountinfo lists an overlay or 9p/grpcfuse mount
// for the workspace root we are likely on a host volume.
function needsPolling(): boolean {
  try {
    if (!fs.existsSync("/.dockerenv")) return false
    const mountInfo = fs.readFileSync("/proc/1/mountinfo", "utf-8")
    const cwd = process.cwd()
    return mountInfo.split("\n").some((line) => {
      if (!line.includes(cwd) && !line.includes("/workspace")) return false
      return /grpcfuse|9p|vboxsf|fuse\.osxfs|cifs|smb/i.test(line)
    })
  } catch {
    return false
  }
}
