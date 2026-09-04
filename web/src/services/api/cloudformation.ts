import { awsClients } from "../aws-clients"
import { apiFetch } from "./base"
import type { DiagnosticProvenance } from "@/types"
import {
  DescribeStacksCommand,
  ListStacksCommand,
  CreateStackCommand,
  UpdateStackCommand,
  DeleteStackCommand,
  GetTemplateCommand,
  ListStackResourcesCommand,
  DescribeStackEventsCommand,
  ValidateTemplateCommand,
  type CreateStackCommandInput,
  type UpdateStackCommandInput,
  type StackResourceSummary,
  type StackEvent,
} from "@aws-sdk/client-cloudformation"

// ─── Deploy diagnostics (emulator-only payload) ─────────────────────────────

/**
 * One labelled measurement — an exit code, a task ARN, a restart count.
 *
 * `hint` disambiguates a label that repeats within a section: three containers
 * in one task each have an "Exit code", and the hint is what says which.
 */
export interface StackDiagnosticsFact {
  label: string
  value: string
  hint?: string
}

/** One timestamped line from a service's own event feed. */
export interface StackDiagnosticsEvent {
  at: string
  message: string
}

/**
 * Captured output from something that has since been deleted.
 *
 * `truncated` is set when the server kept only a tail — the journal is capped
 * in bytes so a chatty container cannot crowd out older deploys — and
 * `capturedAt` is when the copy was taken, which is not the same instant as
 * the enclosing payload's `capturedAt` and is the only honest answer to "how
 * current is this?" for a container that no longer exists.
 */
export interface StackDiagnosticsLog {
  label?: string
  text: string
  truncated?: boolean
  capturedAt?: string
}

/** Fields every section carries regardless of what it holds. */
interface StackDiagnosticsSectionBase {
  /** Stable and unique within its resource; safe as a React key. */
  id: string
  title: string
  provenance: DiagnosticProvenance
  /** Optional one-line gloss in Overcast's own voice. */
  note?: string
}

/**
 * A section of a resource's diagnosis, discriminated on `kind`.
 *
 * Modelled as a real discriminated union rather than one interface with three
 * optional payload fields, so the renderer's switch is exhaustive: a fourth
 * collector kind added server-side becomes a compile error at every `switch`
 * that has to grow, instead of a branch that silently renders nothing.
 */
export type StackDiagnosticsSection =
  | (StackDiagnosticsSectionBase & { kind: "facts"; facts: StackDiagnosticsFact[] })
  | (StackDiagnosticsSectionBase & { kind: "events"; events: StackDiagnosticsEvent[] })
  | (StackDiagnosticsSectionBase & { kind: "log"; log: StackDiagnosticsLog })

/** One stack resource's diagnosis: what CloudFormation said, plus the evidence. */
export interface StackDiagnosticsResource {
  logicalId: string
  physicalId?: string
  type: string
  /** What CloudFormation recorded — the AWS surface, repeated for comparison. */
  statusReason?: string
  /**
   * Optional, because the server omits it (`json:"sections,omitempty"`) for a
   * resource type no collector covers — today anything but AWS::ECS::Service.
   * Such a resource is still a legitimate entry: it carries the reason
   * CloudFormation recorded and offers no evidence beyond it.
   */
  sections?: StackDiagnosticsSection[]
}

/**
 * The emulator-only answer to "why did this deploy fail?".
 *
 * A rollback deletes the resources whose logs held the answer, so by the time
 * anyone opens the console the only surviving sentence is whatever
 * CloudFormation's vocabulary could carry — for a wedged ECS service, "is
 * unable to consistently start tasks successfully", which is byte-correct AWS
 * behaviour and almost useless for debugging. This payload is what Overcast
 * captured on the way out, and every section of it is tagged with where it
 * came from so the reader can tell which parts they would also have had in
 * real AWS. See docs/plans/cfn-deploy-diagnostics.md.
 */
export interface StackDiagnostics {
  stackName: string
  stackId?: string
  operation: "CREATE" | "UPDATE" | "DELETE"
  /** The stack's status at the moment capture ran. */
  stackStatus: string
  capturedAt: string
  /** The sentence CloudFormation itself recorded, shown next to what Overcast found. */
  awsReason?: string
  /**
   * One-sentence answer, always `overcast-inference`. Omitted when nothing
   * could be inferred, in which case the tab leads with the sections.
   */
  headline?: string
  /** What real AWS would have left you with instead. Rendered at the foot. */
  counterfactual: string
  resources: StackDiagnosticsResource[]
}

export const cloudformation = {
  // ── Emulator-only (BFF) ──────────────────────────────────────────────────

  /**
   * The deploy diagnostics journal for a stack, or `null` when there is none.
   *
   * A `404` is the ordinary case, not a failure: most stacks have never failed,
   * and the server answers `{"error": "..."}` to say so. Letting that throw
   * would put an error state on the healthy path and make "no diagnostics"
   * indistinguishable from a server that is actually broken, so the status is
   * accepted and the body is identified positively — a payload is a payload
   * because it carries `stackName`, and anything else, including an empty body
   * from an older server, is treated as "nothing to show" rather than as a
   * half-rendered tab.
   */
  getStackDiagnostics: async (
    stackName: string,
    stackId?: string,
  ): Promise<StackDiagnostics | null> => {
    // The path segment stays the plain name — an ARN's embedded `/`s cannot
    // travel as one route segment — so a caller that already resolved the
    // stack's ARN (the only handle a DELETE_COMPLETE stack answers to, #829)
    // passes it as a query override instead.
    const qs = stackId ? `?stackId=${encodeURIComponent(stackId)}` : ""
    const body = await apiFetch<StackDiagnostics | { error?: string }>(
      `/cloudformation/stacks/${encodeURIComponent(stackName)}/diagnostics${qs}`,
      undefined,
      { acceptStatuses: [404] },
    )
    return "stackName" in body ? body : null
  },

  // ── Standard AWS SDK ops ─────────────────────────────────────────────────
  listStacks: async () => {
    const res = await awsClients.cloudformation().send(new ListStacksCommand({}))
    return res.StackSummaries ?? []
  },

  describeStack: async (stackName: string) => {
    const res = await awsClients
      .cloudformation()
      .send(new DescribeStacksCommand({ StackName: stackName }))
    const stack = res.Stacks?.[0]
    if (!stack) throw new Error(`Stack ${stackName} not found`)
    return stack
  },

  createStack: async (
    opts: Pick<
      CreateStackCommandInput,
      "StackName" | "TemplateBody" | "Parameters" | "Tags" | "Capabilities"
    >,
  ) => {
    const res = await awsClients.cloudformation().send(new CreateStackCommand(opts))
    return res.StackId ?? ""
  },

  updateStack: async (
    opts: Pick<
      UpdateStackCommandInput,
      "StackName" | "TemplateBody" | "Parameters" | "Tags" | "Capabilities"
    >,
  ) => {
    const res = await awsClients.cloudformation().send(new UpdateStackCommand(opts))
    return res.StackId ?? ""
  },

  deleteStack: async (stackName: string) => {
    await awsClients.cloudformation().send(new DeleteStackCommand({ StackName: stackName }))
  },

  getTemplate: async (stackName: string) => {
    const res = await awsClients
      .cloudformation()
      .send(new GetTemplateCommand({ StackName: stackName }))
    return res.TemplateBody ?? ""
  },

  listStackResources: async (stackName: string) => {
    const client = awsClients.cloudformation()
    const all: StackResourceSummary[] = []
    let nextToken: string | undefined
    do {
      const res = await client.send(
        new ListStackResourcesCommand({ StackName: stackName, NextToken: nextToken }),
      )
      all.push(...(res.StackResourceSummaries ?? []))
      nextToken = res.NextToken
    } while (nextToken)
    return all
  },

  describeStackEvents: async (
    stackName: string,
    nextToken?: string,
  ): Promise<{ events: StackEvent[]; nextToken: string | undefined }> => {
    const client = awsClients.cloudformation()
    const res = await client.send(
      new DescribeStackEventsCommand({ StackName: stackName, NextToken: nextToken }),
    )
    return {
      events: res.StackEvents ?? [],
      nextToken: res.NextToken,
    }
  },

  validateTemplate: async (templateBody: string) => {
    await awsClients
      .cloudformation()
      .send(new ValidateTemplateCommand({ TemplateBody: templateBody }))
  },
}
