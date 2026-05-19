import fs from "node:fs";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

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
    proxy: {
      "/events": "http://localhost:7777",
      "/results": "http://localhost:7777",
      "/suites": "http://localhost:7777",
      "/cancel": "http://localhost:7777",
      "/registry": "http://localhost:7777",
      "/queue": "http://localhost:7777",
      "/run": "http://localhost:7777",
      "/config": "http://localhost:7777",
      "/open": "http://localhost:7777",
      "/mcp": "http://localhost:7777",
    },
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
