export interface SQSQueue {
  name: string
  url: string
  arn: string
  visibilityTimeout: number
  approximateNumberOfMessages: number
  approximateNumberOfMessagesNotVisible: number
  createdTimestamp: string
}

export interface SQSQueueDetail extends SQSQueue {
  messageRetentionPeriod: number
  receiveMessageWaitTimeSeconds: number
  delaySeconds: number
  maximumMessageSize: number
  redrivePolicy?: { deadLetterTargetArn: string; maxReceiveCount: number } | null
}

export interface SQSMessageAttribute {
  dataType: string
  stringValue: string
}

export interface SQSMessage {
  messageId: string
  receiptHandle: string
  body: string
  md5OfBody: string
  attributes: Record<string, string | undefined>
  messageAttributes: Record<string, SQSMessageAttribute>
  inflight: boolean
  /** True when invisible due to a send-side delay (never been received). */
  delayed: boolean
  visibleAfter: number
  approximateReceiveCount: number
}
