import fs from "node:fs";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// The compat server picks its port at runtime (a fixed one collides when two
// sessions run side by side), so it tells Vite where it landed via
// COMPAT_SERVER_URL. Falling back to :7777 keeps a bare `npm run dev` working.
const compatServer = process.env.COMPAT_SERVER_URL ?? "http://localhost:7777";

// Paths the dashboard calls on the compat server rather than on itself.
const apiPaths = [
  "/events",
  "/results",
  "/suites",
  "/cancel",
  "/registry",
  "/queue",
  "/run",
  "/config",
  "/open",
  "/mcp",
];

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  server: {
    host: true,
    watch: needsPolling() ? { usePolling: true, interval: 1000 } : {},
    // Forward API calls to the compat server during `vite dev`.
    proxy: Object.fromEntries(apiPaths.map((path) => [path, compatServer])),
  },
});

// Detect whether the workspace is on a host-mounted volume (Windows/macOS)
// where native fs.watch does not work reliably. If /.dockerenv exists we are
// in a container; if /proc/1/mountinfo lists an overlay or 9p/grpcfuse mount
// for the workspace root we are likely on a host volume.
function needsPolling(): boolean {
  try {
    if (!fs.existsSync("/.dockerenv")) return false;
    const mountInfo = fs.readFileSync("/proc/1/mountinfo", "utf-8");
    const cwd = process.cwd();
    return mountInfo.split("\n").some((line) => {
      if (!line.includes(cwd) && !line.includes("/workspace")) return false;
      return /grpcfuse|9p|vboxsf|fuse\.osxfs|cifs|smb/i.test(line);
    });
  } catch {
    return false;
  }
}
