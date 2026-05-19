/**
 * useLambdaInstances — fetches Lambda instances via React Query and groups
 * them by functionName.
 *
 * Freshness is driven by SSE lifecycle events (Acquired/Released/Evicted)
 * through the useQuerySync mapping. The refetchInterval is a safety net
 * for missed events only.
 */
import { useMemo } from "react"
import { useQuery, queryOptions } from "@tanstack/react-query"
import { lambdaInstances } from "@/services/api"
import type { LambdaInstance } from "@/types"

export const lambdaInstanceKeys = {
  instances: () => ["lambda", "instances"] as const,
}

export type InstancesByFunction = Record<string, LambdaInstance[] | undefined>

export function useLambdaInstances(): InstancesByFunction {
  const { data: instances = [] } = useQuery(
    queryOptions({
      queryKey: lambdaInstanceKeys.instances(),
      queryFn: () => lambdaInstances.list(),
      refetchInterval: 30_000,
      staleTime: 0,
    }),
  )

  return useMemo(() => {
    const grouped: InstancesByFunction = {}
    for (const inst of instances) {
      if (grouped[inst.functionName] == null) {
        grouped[inst.functionName] = [inst]
      } else {
        grouped[inst.functionName]!.push(inst)
      }
    }
    return grouped
  }, [instances])
}
