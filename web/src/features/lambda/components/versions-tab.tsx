import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/primitives"
import { Input } from "@/components/ui/input"
import {
  versionsQueryOptions,
  aliasesQueryOptions,
  publishVersionMutationOptions,
  createAliasMutationOptions,
  updateAliasMutationOptions,
  deleteAliasMutationOptions,
  lambdaKeys,
} from "@/features/lambda/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import type { FunctionAlias } from "@/types"

export function VersionsTab({ name }: { name: string }) {
  const { data: versions = [], isLoading: versionsLoading } = useQuery(versionsQueryOptions(name))
  const { data: aliases = [], isLoading: aliasesLoading } = useQuery(aliasesQueryOptions(name))

  const [publishDesc, setPublishDesc] = useState("")
  const [showPublishForm, setShowPublishForm] = useState(false)

  // Alias form state
  const [aliasName, setAliasName] = useState("")
  const [aliasVersion, setAliasVersion] = useState("")
  const [editingAlias, setEditingAlias] = useState<string | null>(null)

  const { mutate: publish, isPending: isPublishing } = useResourceMutation({
    options: publishVersionMutationOptions(),
    invalidateKeys: [lambdaKeys.versions(name)],
    successTitle: "Published",
    successDescription: () => "",
    errorTitle: "Publish failed",
    onSuccess: () => {
      setPublishDesc("")
      setShowPublishForm(false)
    },
  })

  const { mutate: createAlias, isPending: isCreatingAlias } = useResourceMutation({
    options: createAliasMutationOptions(),
    invalidateKeys: [lambdaKeys.aliases(name)],
    successTitle: "Alias created",
    errorTitle: "Create alias failed",
    onSuccess: () => {
      setAliasName("")
      setAliasVersion("")
    },
  })

  const { mutate: updateAlias, isPending: isUpdatingAlias } = useResourceMutation({
    options: updateAliasMutationOptions(),
    invalidateKeys: [lambdaKeys.aliases(name)],
    successTitle: "Alias updated",
    errorTitle: "Update alias failed",
    onSuccess: () => {
      setEditingAlias(null)
      setAliasVersion("")
    },
  })

  const { mutate: deleteAlias } = useResourceMutation({
    options: deleteAliasMutationOptions(),
    invalidateKeys: [lambdaKeys.aliases(name)],
    successTitle: "Alias deleted",
    successDescription: (vars) => vars.aliasName,
    errorTitle: "Delete alias failed",
  })

  const handleStartEditAlias = (a: FunctionAlias) => {
    setEditingAlias(a.Name ?? "")
    setAliasVersion(a.FunctionVersion ?? "")
  }

  const publishedVersions = versions.filter((v) => v.Version !== "$LATEST")

  return (
    <div className="flex flex-col gap-6">
      {/* ── Published versions ───────────────────────────── */}
      <div className="flex flex-col gap-3">
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-medium text-fg">Published versions</h3>
          <Button variant="secondary" size="sm" onClick={() => setShowPublishForm((v) => !v)}>
            + Publish version
          </Button>
        </div>

        {showPublishForm && (
          <div className="flex items-end gap-2 rounded-md border border-border bg-bg-elevated p-3">
            <div className="flex flex-1 flex-col gap-1">
              <label className="text-xs text-fg-muted">Description (optional)</label>
              <Input
                value={publishDesc}
                onChange={(e) => setPublishDesc(e.target.value)}
                placeholder="e.g. initial release"
              />
            </div>
            <Button
              size="sm"
              disabled={isPublishing}
              onClick={() => publish({ name, description: publishDesc || undefined })}
            >
              {isPublishing ? <Spinner className="mr-1 h-3 w-3" /> : null}
              Publish
            </Button>
          </div>
        )}

        {versionsLoading ? (
          <Spinner className="h-4 w-4" />
        ) : publishedVersions.length === 0 ? (
          <p className="text-sm text-fg-muted">No published versions yet.</p>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border text-xs text-fg-muted">
                <th className="pb-1 text-left font-medium">Version</th>
                <th className="pb-1 text-left font-medium">Description</th>
                <th className="pb-1 text-left font-mono font-medium">ARN</th>
                <th className="pb-1 text-left font-medium">SHA-256</th>
              </tr>
            </thead>
            <tbody>
              {publishedVersions.map((v) => (
                <tr key={v.Version} className="border-b border-border/50 last:border-0">
                  <td className="py-1.5 pr-4 font-mono">{v.Version}</td>
                  <td className="py-1.5 pr-4 text-fg-muted">{v.Description || "—"}</td>
                  <td
                    className="max-w-[22ch] truncate py-1.5 pr-4 font-mono text-xs"
                    title={v.FunctionArn}
                  >
                    {v.FunctionArn}
                  </td>
                  <td className="py-1.5 font-mono text-xs text-fg-muted">
                    {v.CodeSha256?.slice(0, 12)}…
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {/* ── Aliases ──────────────────────────────────────── */}
      <div className="flex flex-col gap-3">
        <h3 className="text-sm font-medium text-fg">Aliases</h3>

        {/* Create alias form */}
        <div className="flex items-end gap-2 rounded-md border border-border bg-bg-elevated p-3">
          <div className="flex flex-col gap-1">
            <label className="text-xs text-fg-muted">Alias name</label>
            <Input
              value={aliasName}
              onChange={(e) => setAliasName(e.target.value)}
              placeholder="prod"
              className="w-32"
            />
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs text-fg-muted">Points to version</label>
            <Input
              value={aliasVersion}
              onChange={(e) => setAliasVersion(e.target.value)}
              placeholder="1"
              className="w-24"
            />
          </div>
          <Button
            size="sm"
            disabled={!aliasName.trim() || !aliasVersion.trim() || isCreatingAlias}
            onClick={() =>
              createAlias({
                FunctionName: name,
                Name: aliasName.trim(),
                FunctionVersion: aliasVersion.trim(),
              })
            }
          >
            {isCreatingAlias ? <Spinner className="mr-1 h-3 w-3" /> : null}
            Create
          </Button>
        </div>

        {aliasesLoading ? (
          <Spinner className="h-4 w-4" />
        ) : aliases.length === 0 ? (
          <p className="text-sm text-fg-muted">No aliases yet.</p>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border text-xs text-fg-muted">
                <th className="pb-1 text-left font-medium">Name</th>
                <th className="pb-1 text-left font-medium">Version</th>
                <th className="pb-1 text-left font-medium">Description</th>
                <th className="pb-1" />
              </tr>
            </thead>
            <tbody>
              {aliases.map((a) => (
                <tr key={a.Name} className="border-b border-border/50 last:border-0">
                  <td className="py-1.5 pr-4 font-mono">{a.Name}</td>
                  <td className="py-1.5 pr-4">
                    {editingAlias === a.Name ? (
                      <div className="flex items-center gap-1">
                        <Input
                          value={aliasVersion}
                          onChange={(e) => setAliasVersion(e.target.value)}
                          className="w-20"
                          autoFocus
                        />
                        <Button
                          size="sm"
                          disabled={!aliasVersion.trim() || isUpdatingAlias}
                          onClick={() =>
                            updateAlias({
                              FunctionName: name,
                              Name: a.Name ?? "",
                              FunctionVersion: aliasVersion.trim(),
                            })
                          }
                        >
                          Save
                        </Button>
                        <Button variant="ghost" size="sm" onClick={() => setEditingAlias(null)}>
                          Cancel
                        </Button>
                      </div>
                    ) : (
                      <button
                        className="font-mono text-accent hover:underline"
                        onClick={() => handleStartEditAlias(a)}
                        title="Click to change target version"
                      >
                        {a.FunctionVersion}
                      </button>
                    )}
                  </td>
                  <td className="py-1.5 pr-4 text-fg-muted">{a.Description || "—"}</td>
                  <td className="py-1.5 text-right">
                    <button
                      className="text-xs text-fg-muted hover:text-danger"
                      onClick={() => deleteAlias({ functionName: name, aliasName: a.Name ?? "" })}
                      title="Delete alias"
                    >
                      Delete
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  )
}
