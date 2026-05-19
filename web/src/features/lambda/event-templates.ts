/**
 * AWS event templates for Lambda test invocations.
 *
 * Mirrors the template library in the AWS Console's Lambda Test tab.
 * Each template is a named JSON string that users can load into the editor.
 */

export interface EventTemplate {
  name: string
  category: string
  body: string
}

export const eventTemplates: EventTemplate[] = [
  // ── API Gateway ────────────────────────────────────────────────────────
  {
    name: "API Gateway AWS Proxy",
    category: "API Gateway",
    body: JSON.stringify(
      {
        resource: "/{proxy+}",
        path: "/hello/world",
        httpMethod: "POST",
        headers: {
          Accept: "text/html,application/xhtml+xml",
          "Accept-Encoding": "gzip, deflate, sdch",
          "Accept-Language": "en-US,en;q=0.8",
          "Cache-Control": "max-age=0",
          Host: "1234567890.execute-api.us-east-1.amazonaws.com",
          "X-Forwarded-For": "127.0.0.1, 127.0.0.2",
          "X-Forwarded-Port": "443",
          "X-Forwarded-Proto": "https",
        },
        queryStringParameters: { foo: "bar" },
        pathParameters: { proxy: "hello/world" },
        stageVariables: { baz: "qux" },
        requestContext: {
          accountId: "123456789012",
          resourceId: "123456",
          stage: "prod",
          requestId: "c6af9ac6-7b61-11e6-9a41-93e8deadbeef",
          identity: { sourceIp: "127.0.0.1", userAgent: "Custom User Agent String" },
          resourcePath: "/{proxy+}",
          httpMethod: "POST",
          apiId: "1234567890",
        },
        body: '{"test":"body"}',
        isBase64Encoded: false,
      },
      null,
      2,
    ),
  },
  {
    name: "API Gateway HTTP API v2",
    category: "API Gateway",
    body: JSON.stringify(
      {
        version: "2.0",
        routeKey: "$default",
        rawPath: "/my/path",
        rawQueryString: "parameter1=value1&parameter2=value2",
        headers: {
          "content-type": "application/json",
          "x-forwarded-for": "127.0.0.1",
          "x-forwarded-port": "443",
          "x-forwarded-proto": "https",
        },
        queryStringParameters: { parameter1: "value1", parameter2: "value2" },
        requestContext: {
          accountId: "123456789012",
          apiId: "api-id",
          domainName: "id.execute-api.us-east-1.amazonaws.com",
          http: {
            method: "POST",
            path: "/my/path",
            protocol: "HTTP/1.1",
            sourceIp: "127.0.0.1",
            userAgent: "agent",
          },
          requestId: "id",
          routeKey: "$default",
          stage: "$default",
          time: "12/Mar/2020:19:03:58 +0000",
          timeEpoch: 1583348638390,
        },
        body: '{"message": "hello"}',
        isBase64Encoded: false,
      },
      null,
      2,
    ),
  },

  // ── S3 ─────────────────────────────────────────────────────────────────
  {
    name: "S3 Put",
    category: "S3",
    body: JSON.stringify(
      {
        Records: [
          {
            eventVersion: "2.1",
            eventSource: "aws:s3",
            awsRegion: "us-east-1",
            eventTime: "2024-01-15T12:00:00.000Z",
            eventName: "ObjectCreated:Put",
            userIdentity: { principalId: "EXAMPLE" },
            requestParameters: { sourceIPAddress: "127.0.0.1" },
            responseElements: {
              "x-amz-request-id": "EXAMPLE123456789",
              "x-amz-id-2": "EXAMPLE123/5678abcdefghijklambdaisawesome/mnopqrstuvwxyzABCDEFGH",
            },
            s3: {
              s3SchemaVersion: "1.0",
              configurationId: "testConfigRule",
              bucket: {
                name: "my-bucket",
                ownerIdentity: { principalId: "EXAMPLE" },
                arn: "arn:aws:s3:::my-bucket",
              },
              object: {
                key: "test/key",
                size: 1024,
                eTag: "0123456789abcdef0123456789abcdef",
                sequencer: "0A1B2C3D4E5F678901",
              },
            },
          },
        ],
      },
      null,
      2,
    ),
  },
  {
    name: "S3 Delete",
    category: "S3",
    body: JSON.stringify(
      {
        Records: [
          {
            eventVersion: "2.1",
            eventSource: "aws:s3",
            awsRegion: "us-east-1",
            eventTime: "2024-01-15T12:00:00.000Z",
            eventName: "ObjectRemoved:Delete",
            userIdentity: { principalId: "EXAMPLE" },
            requestParameters: { sourceIPAddress: "127.0.0.1" },
            responseElements: {
              "x-amz-request-id": "EXAMPLE123456789",
              "x-amz-id-2": "EXAMPLE123/5678abcdefghijklambdaisawesome/mnopqrstuvwxyzABCDEFGH",
            },
            s3: {
              s3SchemaVersion: "1.0",
              configurationId: "testConfigRule",
              bucket: {
                name: "my-bucket",
                ownerIdentity: { principalId: "EXAMPLE" },
                arn: "arn:aws:s3:::my-bucket",
              },
              object: { key: "test/key", sequencer: "0A1B2C3D4E5F678901" },
            },
          },
        ],
      },
      null,
      2,
    ),
  },

  // ── SQS ────────────────────────────────────────────────────────────────
  {
    name: "SQS",
    category: "SQS",
    body: JSON.stringify(
      {
        Records: [
          {
            messageId: "059f36b4-87a3-44ab-83d2-661975830a7d",
            receiptHandle: "AQEBwJnKyrHigUMZj6rYigCgxlaS3SLy0a...",
            body: "Test message.",
            attributes: {
              ApproximateReceiveCount: "1",
              SentTimestamp: "1545082649636",
              SenderId: "AIDAIENQZJOLO23YVJ4VO",
              ApproximateFirstReceiveTimestamp: "1545082649636",
            },
            messageAttributes: {},
            md5OfBody: "e4e68fb7bd0e697a0ae8f1bb342846b3",
            eventSource: "aws:sqs",
            eventSourceARN: "arn:aws:sqs:us-east-1:123456789012:my-queue",
            awsRegion: "us-east-1",
          },
        ],
      },
      null,
      2,
    ),
  },

  // ── SNS ────────────────────────────────────────────────────────────────
  {
    name: "SNS",
    category: "SNS",
    body: JSON.stringify(
      {
        Records: [
          {
            EventVersion: "1.0",
            EventSubscriptionArn:
              "arn:aws:sns:us-east-1:123456789012:my-topic:2bcfbf39-05c3-41de-beaa-fcfcc21c8f55",
            EventSource: "aws:sns",
            Sns: {
              SignatureVersion: "1",
              Timestamp: "2024-01-15T12:00:00.000Z",
              MessageId: "95df01b4-ee98-5cb9-9903-4c221d41eb5e",
              Message: "Hello from SNS!",
              Type: "Notification",
              TopicArn: "arn:aws:sns:us-east-1:123456789012:my-topic",
              Subject: "TestInvoke",
            },
          },
        ],
      },
      null,
      2,
    ),
  },

  // ── DynamoDB ───────────────────────────────────────────────────────────
  {
    name: "DynamoDB Update",
    category: "DynamoDB",
    body: JSON.stringify(
      {
        Records: [
          {
            eventID: "1",
            eventVersion: "1.0",
            dynamodb: {
              Keys: { Id: { N: "101" } },
              NewImage: { Message: { S: "New item!" }, Id: { N: "101" } },
              StreamViewType: "NEW_AND_OLD_IMAGES",
              SequenceNumber: "111",
              SizeBytes: 26,
            },
            awsRegion: "us-east-1",
            eventName: "INSERT",
            eventSourceARN:
              "arn:aws:dynamodb:us-east-1:123456789012:table/my-table/stream/2024-01-15T00:00:00.000",
            eventSource: "aws:dynamodb",
          },
        ],
      },
      null,
      2,
    ),
  },

  // ── CloudWatch Scheduled Event ─────────────────────────────────────────
  {
    name: "EventBridge (CloudWatch Events)",
    category: "EventBridge",
    body: JSON.stringify(
      {
        version: "0",
        id: "53dc4d37-cffa-4f76-80c9-8b7d4a4d2eaa",
        "detail-type": "Scheduled Event",
        source: "aws.events",
        account: "123456789012",
        time: "2024-01-15T12:00:00Z",
        region: "us-east-1",
        resources: ["arn:aws:events:us-east-1:123456789012:rule/my-scheduled-rule"],
        detail: {},
      },
      null,
      2,
    ),
  },

  // ── CloudWatch Logs ────────────────────────────────────────────────────
  {
    name: "CloudWatch Logs",
    category: "CloudWatch",
    body: JSON.stringify(
      {
        awslogs: {
          data: "H4sIAAAAAAAAAHWPwQqCQBCGX0Xm7uCuuj5A0y1telerorCGDOJ...",
        },
      },
      null,
      2,
    ),
  },

  // ── ALB ────────────────────────────────────────────────────────────────
  {
    name: "Application Load Balancer",
    category: "ALB",
    body: JSON.stringify(
      {
        requestContext: {
          elb: {
            targetGroupArn:
              "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/my-tg/50dc6c495c0c9188",
          },
        },
        httpMethod: "GET",
        path: "/lambda",
        queryStringParameters: { query: "1234ABCD" },
        headers: {
          connection: "keep-alive",
          host: "lambda-alb-123578498.us-east-1.elb.amazonaws.com",
          "user-agent": "Mozilla/5.0",
          accept: "text/html,application/xhtml+xml",
          "accept-language": "en-US,en;q=0.8",
          "accept-encoding": "gzip, deflate",
          "x-forwarded-for": "72.21.198.66",
          "x-forwarded-port": "443",
          "x-forwarded-proto": "https",
        },
        body: null,
        isBase64Encoded: false,
      },
      null,
      2,
    ),
  },

  // ── Hello World ────────────────────────────────────────────────────────
  {
    name: "Hello World",
    category: "General",
    body: JSON.stringify(
      {
        key1: "value1",
        key2: "value2",
        key3: "value3",
      },
      null,
      2,
    ),
  },
]

/** Unique sorted categories from the templates list. */
export const templateCategories = [...new Set(eventTemplates.map((t) => t.category))].sort()
