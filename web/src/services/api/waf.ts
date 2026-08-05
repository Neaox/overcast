import {
  CreateWebACLCommand,
  DeleteWebACLCommand,
  GetWebACLCommand,
  ListWebACLsCommand,
  type CreateWebACLCommandInput,
} from "@aws-sdk/client-wafv2"
import { awsClients } from "../aws-clients"

export type WAFScope = "REGIONAL" | "CLOUDFRONT"

export interface CreateWebACLValues {
  name: string
  scope: WAFScope
  description?: string
}

export interface WebACLSummary {
  id: string
  name: string
  scope: WAFScope
  arn: string
  description: string
  lockToken: string
}

export interface WebACLDetail extends WebACLSummary {
  defaultAction: unknown
  visibilityConfig: unknown
  rules: unknown[]
}

export function buildCreateWebACLInput(values: CreateWebACLValues): CreateWebACLCommandInput {
  return {
    Name: values.name,
    Scope: values.scope,
    ...(values.description ? { Description: values.description } : {}),
    DefaultAction: { Allow: {} },
    Rules: [],
    VisibilityConfig: {
      CloudWatchMetricsEnabled: false,
      MetricName: values.name,
      SampledRequestsEnabled: false,
    },
  }
}

function summary(
  value: {
    Id?: string
    Name?: string
    ARN?: string
    Description?: string
    LockToken?: string
  },
  scope: WAFScope,
): WebACLSummary {
  return {
    id: value.Id ?? "",
    name: value.Name ?? "",
    scope,
    arn: value.ARN ?? "",
    description: value.Description ?? "",
    lockToken: value.LockToken ?? "",
  }
}

export const waf = {
  listWebACLs: async (scope: WAFScope): Promise<WebACLSummary[]> => {
    const response = await awsClients.wafv2().send(new ListWebACLsCommand({ Scope: scope }))
    return (response.WebACLs ?? []).map((value) => summary(value, scope))
  },

  listAllWebACLs: async (): Promise<WebACLSummary[]> => {
    const [regional, global] = await Promise.all([
      waf.listWebACLs("REGIONAL"),
      waf.listWebACLs("CLOUDFRONT"),
    ])
    return [...regional, ...global]
  },

  getWebACL: async ({
    id,
    name,
    scope,
  }: Pick<WebACLSummary, "id" | "name" | "scope">): Promise<WebACLDetail> => {
    const response = await awsClients
      .wafv2()
      .send(new GetWebACLCommand({ Id: id, Name: name, Scope: scope }))
    if (!response.WebACL) throw new Error(`Web ACL ${name} not found`)
    return {
      ...summary({ ...response.WebACL, LockToken: response.LockToken }, scope),
      defaultAction: response.WebACL.DefaultAction,
      visibilityConfig: response.WebACL.VisibilityConfig,
      rules: response.WebACL.Rules ?? [],
    }
  },

  createWebACL: async (values: CreateWebACLValues): Promise<WebACLSummary> => {
    const response = await awsClients
      .wafv2()
      .send(new CreateWebACLCommand(buildCreateWebACLInput(values)))
    if (!response.Summary) throw new Error("CreateWebACL returned no summary")
    return summary(response.Summary, values.scope)
  },

  deleteWebACL: async (value: WebACLSummary): Promise<void> => {
    await awsClients.wafv2().send(
      new DeleteWebACLCommand({
        Id: value.id,
        Name: value.name,
        Scope: value.scope,
        LockToken: value.lockToken,
      }),
    )
  },
}
