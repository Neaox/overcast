/** A DynamoDB attribute value in SDK wire format, e.g. { S: "hello" } or { N: "42" } */
export type DynamoAttrValue =
  | { S: string }
  | { N: string }
  | { BOOL: boolean }
  | { NULL: boolean }
  | { B: string } // base64
  | { SS: string[] }
  | { NS: string[] }
  | { BS: string[] }
  | { L: DynamoAttrValue[] }
  | { M: Record<string, DynamoAttrValue> }

/** A DynamoDB item — attribute name → DynamoDB JSON attribute value */
export type DynamoItem = Record<string, DynamoAttrValue | undefined>

export interface DynamoKeySchema {
  attributeName: string | undefined
  keyType: string | undefined
}

export interface DynamoAttrDef {
  attributeName: string | undefined
  attributeType: string | undefined
}

export interface DynamoGSI {
  indexName: string | undefined
  keySchema: DynamoKeySchema[]
  itemCount: number
}

export interface DynamoTable {
  tableName: string
  tableStatus: string
  tableArn: string
  itemCount: number
  tableSizeBytes: number
  keySchema: DynamoKeySchema[]
  attributeDefinitions: DynamoAttrDef[]
  billingMode: string
  creationDateTime: string
  globalSecondaryIndexes: DynamoGSI[]
  localSecondaryIndexes: DynamoGSI[]
  streamSpecification?: {
    streamEnabled: boolean
    streamViewType?: string
  }
  latestStreamArn?: string
}
