/**
 * Ephemeral module-level store for pending upload files.
 *
 * File objects cannot be serialised into URL search params, so we stash them
 * here for the lifetime of a navigation to the put-object screen. The screen
 * calls `take()` once on mount, which clears the store.
 */

interface PendingUpload {
  files: File[]
  prefix: string
}

let pending: PendingUpload | null = null

export const uploadStore = {
  set(files: File[], prefix: string): void {
    console.log("[upload-store] set", files.length, "files, prefix:", JSON.stringify(prefix))
    pending = { files, prefix }
  },
  take(): PendingUpload | null {
    const p = pending
    pending = null
    console.log("[upload-store] take →", p ? `${p.files.length} files` : "null")
    return p
  },
}
