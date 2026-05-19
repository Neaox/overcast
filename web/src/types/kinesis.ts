export interface KinesisStream {
  name: string
  arn: string
  status: string
  shardCount: number
  retentionHours: number
  createdAt?: number
}

export interface KinesisShard {
  shardId: string
  parentShardId?: string
  adjacentParentShardId?: string
  startingHashKey: string
  endingHashKey: string
  startingSeqNo: string
  endingSeqNo?: string
}

export interface KinesisStreamDetail extends KinesisStream {
  shards: KinesisShard[]
  tags: Record<string, string>
}
