/**
 * AppRegistry API client — raw fetch against the emulator.
 *
 * Unlike other services, AppRegistry is not wired through the AWS JS SDK
 * (no `@aws-sdk/client-service-catalog-appregistry` dependency), so this
 * module hits the emulator's REST endpoints directly using the endpoint
 * resolver for the base URL.
 */

import { endpointResolver } from "../discovery"

export interface Application {
  id: string
  name: string
  arn: string
  description?: string
  tags?: Record<string, string>
  applicationTag?: Record<string, string>
  creationTime: number
  lastUpdateTime: number
  associatedResourceCount?: number
}

export interface AssociatedResource {
  name: string
  arn: string
  resourceType: string
  creationTime: number
}

async function arFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const ep = endpointResolver.get()
  const res = await fetch(`${ep.baseUrl}${path}`, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      "x-overcast-region": ep.region,
      ...init?.headers,
    },
  })
  if (!res.ok) {
    const body = (await res.json().catch(() => ({}))) as {
      message?: string
      __type?: string
    }
    throw new Error(body.message ?? body.__type ?? `HTTP ${res.status}`)
  }
  const text = await res.text()
  return text ? (JSON.parse(text) as T) : ({} as T)
}

export const appregistry = {
  listApplications: async (): Promise<Application[]> => {
    const res = await arFetch<{ applications: Application[] }>("/applications")
    return res.applications ?? []
  },

  getApplication: async (identifier: string): Promise<Application> => {
    return arFetch<Application>(`/applications/${encodeURIComponent(identifier)}`)
  },

  createApplication: async (input: {
    name: string
    description?: string
    tags?: Record<string, string>
  }): Promise<Application> => {
    const res = await arFetch<{ application: Application }>("/applications", {
      method: "POST",
      body: JSON.stringify(input),
    })
    return res.application
  },

  deleteApplication: async (identifier: string): Promise<void> => {
    await arFetch(`/applications/${encodeURIComponent(identifier)}`, {
      method: "DELETE",
    })
  },

  listAssociatedResources: async (identifier: string): Promise<AssociatedResource[]> => {
    const res = await arFetch<{ resources: AssociatedResource[] }>(
      `/applications/${encodeURIComponent(identifier)}/resources`,
    )
    return res.resources ?? []
  },
}
