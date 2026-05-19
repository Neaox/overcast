export type MessageKind = "email" | "sms" | "webhook" | "push"

export interface CapturedMessage {
  id: string
  /** Discriminates email vs SMS. Absent on legacy stored messages — treat as email. */
  kind?: MessageKind
  /** Service that sent the message, e.g. "sns", "ses", "cognito". */
  source?: string
  from: string
  to: string[]
  subject?: string
  textBody: string
  htmlBody?: string
  receivedAt: string
  raw?: string
  /** SNS MessageId shared by all deliveries for a single Publish call. */
  groupId?: string
  /** Short topic name associated with groupId. */
  groupTopic?: string
}
