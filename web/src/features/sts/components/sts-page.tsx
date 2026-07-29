import { useQuery } from "@tanstack/react-query"
import { Fingerprint, RefreshCw } from "lucide-react"
import { stsCallerIdentityQueryOptions } from "@/features/sts/data"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Definition, DefinitionList } from "@/components/ui/definition-card"
import { PageHeader, Spinner } from "@/components/ui/primitives"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { cn } from "@/lib/utils"
import { ArnText } from "@/components/ui/arn-link"

export function StsPage() {
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()
  const {
    data: identity,
    isLoading,
    isFetching,
    refetch,
  } = useQuery(stsCallerIdentityQueryOptions())

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="STS"
        description="Security Token Service — caller identity"
        actions={
          <div className="flex items-center gap-2">
            <ServiceDocsButton
              service="sts"
              label="STS"
              open={docsOpen}
              onOpen={openDocs}
              onClose={closeDocs}
            />
            <Button
              variant="ghost"
              size="sm"
              onClick={() => refetch()}
              disabled={isFetching}
              title="Refresh"
            >
              <RefreshCw className={cn("h-4 w-4", isFetching && "animate-spin")} />
            </Button>
          </div>
        }
      />

      {isLoading ? (
        <div className="flex justify-center py-24">
          <Spinner className="h-6 w-6" />
        </div>
      ) : identity ? (
        <Card>
          <CardContent className="flex flex-col gap-4 p-4">
            <div className="flex items-center gap-3">
              <Fingerprint className="h-8 w-8 text-fg-muted" />
              <div>
                <h2 className="font-mono text-sm font-medium text-fg">Caller Identity</h2>
                <p className="text-xs text-fg-muted">
                  The identity returned by <code className="text-xs">GetCallerIdentity</code>
                </p>
              </div>
            </div>
            <DefinitionList>
              <Definition label="Account" value={identity.Account} />
              <Definition label="User ID" value={identity.UserId} />
              <Definition
                label="ARN"
                value={identity.Arn ? <ArnText arn={identity.Arn} /> : null}
                full
              />
            </DefinitionList>
          </CardContent>
        </Card>
      ) : null}
    </div>
  )
}
