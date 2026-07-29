import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Key, RefreshCw, Trash2 } from "lucide-react"
import { ArnText } from "@/components/ui/arn-link"
import {
  kmsKeyDetailQueryOptions,
  kmsKeys,
  enableKeyMutationOptions,
  disableKeyMutationOptions,
  scheduleKeyDeletionMutationOptions,
} from "@/features/kms/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { useState } from "react"
import { Button } from "@/components/ui/button"
import { Definition, DefinitionCard } from "@/components/ui/definition-card"
import { Badge } from "@/components/ui/badge"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { ApplicationOwnershipBanner } from "@/components/application-ownership-banner"
import { formatDate } from "@/lib/format"
import { cn } from "@/lib/utils"

interface Props {
  keyId: string
}

export function KmsKeyDetail({ keyId }: Props) {
  const navigate = useNavigate()
  const [showDelete, setShowDelete] = useState(false)

  const { data: key, isLoading, isFetching, refetch } = useQuery(kmsKeyDetailQueryOptions(keyId))

  const enableMut = useResourceMutation({
    options: enableKeyMutationOptions(),
    invalidateKeys: [kmsKeys.key(keyId), kmsKeys.keys()],
    successTitle: "Key enabled",
  })

  const disableMut = useResourceMutation({
    options: disableKeyMutationOptions(),
    invalidateKeys: [kmsKeys.key(keyId), kmsKeys.keys()],
    successTitle: "Key disabled",
  })

  const deleteMut = useResourceMutation({
    options: scheduleKeyDeletionMutationOptions(),
    invalidateKeys: [kmsKeys.key(keyId), kmsKeys.keys()],
    successTitle: "Key scheduled for deletion",
    onSuccess: () => {
      setShowDelete(false)
      void navigate({ to: "/kms" })
    },
  })

  if (isLoading) {
    return (
      <div className="flex w-full justify-center py-24">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  if (!key) {
    return (
      <div className="flex w-full flex-col gap-4">
        <EmptyState
          icon={<Key className="h-8 w-8 opacity-40" />}
          title="Key not found"
          description={`No key with ID "${keyId}" exists.`}
        />
      </div>
    )
  }

  const stateVariant =
    key.metadata?.KeyState === "Enabled"
      ? "default"
      : key.metadata?.KeyState === "Disabled"
        ? "outline"
        : "warning"

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title={key.metadata?.KeyId ?? keyId}
        description={key.metadata?.Description || undefined}
        actions={
          <div className="flex items-center gap-2">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => refetch()}
              disabled={isFetching}
              title="Refresh"
            >
              <RefreshCw className={cn("h-4 w-4", isFetching && "animate-spin")} />
            </Button>
            {key.metadata?.KeyState === "Disabled" && (
              <Button
                size="sm"
                variant="secondary"
                onClick={() => enableMut.mutate(keyId)}
                disabled={enableMut.isPending}
              >
                Enable
              </Button>
            )}
            {key.metadata?.KeyState === "Enabled" && (
              <Button
                size="sm"
                variant="secondary"
                onClick={() => disableMut.mutate(keyId)}
                disabled={disableMut.isPending}
              >
                Disable
              </Button>
            )}
            {key.metadata?.KeyState !== "PendingDeletion" && (
              <Button size="sm" variant="danger" onClick={() => setShowDelete(true)}>
                <Trash2 className="mr-1.5 h-3.5 w-3.5" />
                Schedule deletion
              </Button>
            )}
          </div>
        }
      />

      <ApplicationOwnershipBanner candidates={[key.metadata?.Arn, key.metadata?.KeyId, keyId]} />

      <DefinitionCard>
        <Definition label="Key ID" value={key.metadata?.KeyId} />
        <Definition
          label="State"
          value={<Badge variant={stateVariant}>{key.metadata?.KeyState}</Badge>}
        />
        <Definition label="Key spec" value={key.metadata?.KeySpec} />
        <Definition label="Key usage" value={key.metadata?.KeyUsage} />
        <Definition label="Origin" value={key.metadata?.Origin} />
        <Definition label="Key manager" value={key.metadata?.KeyManager} />
        <Definition label="Created" value={formatDate(key.metadata?.CreationDate)} />
        {key.metadata?.DeletionDate && (
          <Definition label="Scheduled deletion" value={formatDate(key.metadata.DeletionDate)} />
        )}
        <Definition
          label="ARN"
          value={key.metadata?.Arn ? <ArnText arn={key.metadata.Arn} /> : null}
          full
        />
      </DefinitionCard>

      {key.aliases.length > 0 && (
        <section className="flex flex-col gap-2">
          <h2 className="font-mono text-sm font-medium text-fg">Aliases</h2>
          <div className="flex flex-wrap gap-2">
            {key.aliases.map((alias) => (
              <Badge key={alias} variant="outline">
                {alias}
              </Badge>
            ))}
          </div>
        </section>
      )}

      <ConfirmDialog
        open={showDelete}
        onOpenChange={(open) => !open && setShowDelete(false)}
        title="Schedule Key Deletion"
        description={
          <>
            Schedule key <span className="font-mono font-semibold">{keyId}</span> for deletion? The
            key will be deleted after a 7-day waiting period.
          </>
        }
        confirmLabel="Schedule deletion"
        variant="danger"
        isPending={deleteMut.isPending}
        onConfirm={() => deleteMut.mutate(keyId)}
      />
    </div>
  )
}
