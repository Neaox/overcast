import { awsClients } from "../aws-clients"
import { apiFetch } from "./base"
import {
  CreateDBInstanceCommand,
  DescribeDBInstancesCommand,
  DeleteDBInstanceCommand,
  StartDBInstanceCommand,
  StopDBInstanceCommand,
  type CreateDBInstanceCommandInput,
} from "@aws-sdk/client-rds"

export const rds = {
  listInstances: async () => {
    const res = await awsClients.rds().send(new DescribeDBInstancesCommand({}))
    return res.DBInstances ?? []
  },

  describeInstance: async (identifier: string) => {
    const res = await awsClients
      .rds()
      .send(new DescribeDBInstancesCommand({ DBInstanceIdentifier: identifier }))
    const instances = res.DBInstances ?? []
    if (instances.length === 0) throw new Error(`DB instance "${identifier}" not found`)
    return instances[0]
  },

  createInstance: async (opts: CreateDBInstanceCommandInput) => {
    const res = await awsClients.rds().send(new CreateDBInstanceCommand(opts))
    return res.DBInstance!
  },

  deleteInstance: async (identifier: string) => {
    await awsClients.rds().send(
      new DeleteDBInstanceCommand({
        DBInstanceIdentifier: identifier,
        SkipFinalSnapshot: true,
      }),
    )
  },

  startInstance: async (identifier: string) => {
    const res = await awsClients
      .rds()
      .send(new StartDBInstanceCommand({ DBInstanceIdentifier: identifier }))
    return res.DBInstance!
  },

  stopInstance: async (identifier: string) => {
    const res = await awsClients
      .rds()
      .send(new StopDBInstanceCommand({ DBInstanceIdentifier: identifier }))
    return res.DBInstance!
  },

  getInstanceLogs: (id: string) =>
    apiFetch<{ instanceId: string; engine: string; logs: string }>(
      `/rds/instances/${encodeURIComponent(id)}/logs`,
    ),
}
