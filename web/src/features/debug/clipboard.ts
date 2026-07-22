export const debugClipboard = {
  writeText(text: string): Promise<void> {
    return navigator.clipboard.writeText(text)
  },
}
