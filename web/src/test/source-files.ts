import { readdirSync, statSync } from "node:fs"
import { join } from "node:path"

/**
 * Every `.ts`/`.tsx` file under `dir`, recursively.
 *
 * For the handful of tests that assert over the source tree itself rather than
 * over rendered output — the undeclared-colour-token check in
 * `styles/global.test.ts`, the direct-`navigator.clipboard` check in
 * `lib/clipboard.test.ts`. Paths are returned relative to the process cwd,
 * which vitest sets to `web/`, so they read as `src/…` in failure messages.
 */
export function sourceFiles(dir: string, acc: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const path = join(dir, entry)
    if (statSync(path).isDirectory()) sourceFiles(path, acc)
    else if (/\.tsx?$/.test(path)) acc.push(path)
  }
  return acc
}
