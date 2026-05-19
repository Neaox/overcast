import { useCallback } from "react"
import { Spinner } from "@/components/ui/primitives"
import { CodeBrowser } from "@/components/ui/code-browser"
import { lambda } from "@/services/api"
import type { LambdaFunctionSource } from "@/types"

export function CodeTab({
  source,
  sourceLoading,
  sourceError,
  currentEditorValue,
  setEditedFiles,
  setActiveFilePath,
  name,
}: {
  source: LambdaFunctionSource | undefined
  sourceLoading: boolean
  sourceError: boolean
  currentEditorValue: string
  setEditedFiles: React.Dispatch<React.SetStateAction<Record<string, string>>>
  setActiveFilePath: (path: string) => void
  name: string
}) {
  const loadFile = useCallback(
    async (path: string) => {
      const data = await lambda.getSource(name, path)
      return { content: data.source, language: data.language }
    },
    [name],
  )

  if (sourceLoading) {
    return (
      <div className="flex items-center justify-center py-16">
        <Spinner className="h-5 w-5" />
      </div>
    )
  }
  if (sourceError) {
    return <p className="text-sm text-danger">Failed to load source. Is the emulator running?</p>
  }

  return (
    <CodeBrowser
      files={(source?.files ?? []).map((f) => ({ name: f.name, size: f.size }))}
      initialFile={source?.filename}
      initialValue={currentEditorValue}
      language={source?.language}
      loadFile={loadFile}
      onChange={(path, value) => setEditedFiles((prev) => ({ ...prev, [path]: value }))}
      onActiveFileChange={setActiveFilePath}
      height="65vh"
    />
  )
}
