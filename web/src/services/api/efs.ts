import { awsClients } from "../aws-clients"
import {
  CreateFileSystemCommand,
  DeleteFileSystemCommand,
  DescribeAccessPointsCommand,
  DescribeFileSystemsCommand,
  DescribeMountTargetsCommand,
  type CreateFileSystemCommandInput,
} from "@aws-sdk/client-efs"

export const efs = {
  listFileSystems: async () => {
    const res = await awsClients.efs().send(new DescribeFileSystemsCommand({}))
    return res.FileSystems ?? []
  },

  describeFileSystem: async (id: string) => {
    const res = await awsClients.efs().send(new DescribeFileSystemsCommand({ FileSystemId: id }))
    const systems = res.FileSystems ?? []
    if (systems.length === 0) throw new Error(`File system "${id}" not found`)
    return systems[0]
  },

  listMountTargets: async (fileSystemId: string) => {
    const res = await awsClients
      .efs()
      .send(new DescribeMountTargetsCommand({ FileSystemId: fileSystemId }))
    return res.MountTargets ?? []
  },

  listAccessPoints: async (fileSystemId: string) => {
    const res = await awsClients
      .efs()
      .send(new DescribeAccessPointsCommand({ FileSystemId: fileSystemId }))
    return res.AccessPoints ?? []
  },

  createFileSystem: async (opts: CreateFileSystemCommandInput) => {
    const res = await awsClients.efs().send(new CreateFileSystemCommand(opts))
    return res
  },

  deleteFileSystem: async (id: string) => {
    await awsClients.efs().send(new DeleteFileSystemCommand({ FileSystemId: id }))
  },
}
