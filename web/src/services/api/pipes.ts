import { awsClients } from "../aws-clients"
import { apiFetch } from "./base"
import {
  ListPipesCommand,
  CreatePipeCommand,
  DescribePipeCommand,
  DeletePipeCommand,
} from "@aws-sdk/client-pipes"

/**
 * A pipe's three legs with the types the emulator resolved from their ARNs,
 * plus whether the combination is one it can actually run.
 *
 * Served by the emulator's console endpoint rather than the SDK: DescribePipe
 * returns ARNs, and "is this pipe wired or merely stored" is an emulator fact
 * with no AWS field to carry it.
 */
export interface PipeWiring {
  Name: string
  Arn: string
  CurrentState: string
  DesiredState: string
  Source: string
  SourceType: string
  Enrichment?: string
  EnrichmentType?: string
  Target: string
  TargetType: string
  /** False for a pipe whose stored wiring the emulator cannot run. */
  Wired: boolean
  /** Why an unwired pipe will never fire. */
  Reason?: string
}

/** The outcome of one pipe execution. */
export interface PipeDelivery {
  Region: string
  Pipe: string
  SourceArn: string
  TargetArn: string
  TargetType: string
  Records: number
  /** "delivered" | "filtered" | "failed" | "dlq" */
  Outcome: string
  /** How many delivery attempts the execution took. */
  Attempts: number
  Error?: string
  Time: string
}

export const pipes = {
  listPipes: async () => {
    const res = await awsClients.pipes().send(new ListPipesCommand({}))
    return res.Pipes ?? []
  },

  createPipe: async (
    name: string,
    sourceArn: string,
    targetArn: string,
    enrichmentArn?: string,
  ) => {
    return await awsClients.pipes().send(
      new CreatePipeCommand({
        Name: name,
        Source: sourceArn,
        Target: targetArn,
        Enrichment: enrichmentArn || undefined,
        RoleArn: "arn:aws:iam::000000000000:role/overcast-stub",
      }),
    )
  },

  describePipe: async (name: string) => {
    return await awsClients.pipes().send(new DescribePipeCommand({ Name: name }))
  },

  deletePipe: async (name: string) => {
    await awsClients.pipes().send(new DeletePipeCommand({ Name: name }))
  },

  /** Every pipe's resolved wiring, from the emulator's console endpoint. */
  listWiring: async () => {
    const res = await apiFetch<{ Pipes?: PipeWiring[] }>("/pipes/wiring")
    return res.Pipes ?? []
  },

  /**
   * Recent executions of one pipe, newest first. The emulator keeps a bounded
   * in-memory ring, so this is empty after a restart.
   */
  listDeliveries: async (name: string) => {
    const res = await apiFetch<{ Deliveries?: PipeDelivery[] }>(
      `/pipes/deliveries?pipe=${encodeURIComponent(name)}`,
    )
    return res.Deliveries ?? []
  },
}
