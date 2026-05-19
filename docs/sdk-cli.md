# Using AWS SDKs and CLI with Overcast

Overcast is a drop-in local AWS endpoint. Point any AWS SDK or the AWS CLI at
`http://localhost:4566` and use it exactly as you would against real AWS.

---

## AWS CLI

### `--endpoint-url` flag

AWS CLI v2 accepts `--endpoint-url` as a global flag on any command:

```bash
aws --endpoint-url http://localhost:4566 s3 ls
aws --endpoint-url http://localhost:4566 sqs list-queues
aws --endpoint-url http://localhost:4566 dynamodb list-tables
```

This is the simplest way to try Overcast without changing any configuration.

### Environment variables (recommended for CI)

Setting `AWS_ENDPOINT_URL` avoids repeating the flag on every command:

```bash
export AWS_ENDPOINT_URL=http://localhost:4566
export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_DEFAULT_REGION=us-east-1
```

Then use the CLI normally:

```bash
aws s3 mb s3://my-bucket
aws s3 cp file.txt s3://my-bucket/
aws sqs create-queue --queue-name my-queue
aws dynamodb create-table \
  --table-name users \
  --attribute-definitions AttributeName=id,AttributeType=S \
  --key-schema AttributeName=id,KeyType=HASH \
  --billing-mode PAY_PER_REQUEST
```

### AWS CLI profile

Add to `~/.aws/config`:

```ini
[profile overcast]
aws_access_key_id = test
aws_secret_access_key = test
region = us-east-1
endpoint_url = http://localhost:4566
```

Then:

```bash
aws --profile overcast s3 ls
```

### S3 path-style addressing

Overcast uses path-style S3 URLs by default (`http://localhost:4566/bucket/key`).
The CLI respects this automatically when using `--endpoint-url` or
`AWS_ENDPOINT_URL`.

Overcast also supports **virtual-hosted-style** requests (`http://mybucket.localhost:4566/key`),
including the `.s3.` variants used by AWS SDK v3. If you are using CDK and
running on Windows or macOS, see the
[CDK S3 asset upload troubleshooting](./cdk.md#s3-asset-upload-fails-on-windows-or-macos)
section — `*.localhost` subdomains do not resolve on those platforms and require
the `OVERCAST_HOSTNAME` workaround.

---

## Node.js (AWS SDK v3)

```typescript
import { S3Client, CreateBucketCommand } from "@aws-sdk/client-s3";

const s3 = new S3Client({
  endpoint: "http://localhost:4566",
  region: "us-east-1",
  credentials: { accessKeyId: "test", secretAccessKey: "test" },
  forcePathStyle: true,
});

await s3.send(new CreateBucketCommand({ Bucket: "my-bucket" }));
```

The same pattern works for any service client — just set `endpoint`:

```typescript
import { DynamoDBClient } from "@aws-sdk/client-dynamodb";
import { SQSClient } from "@aws-sdk/client-sqs";
import { LambdaClient } from "@aws-sdk/client-lambda";

const dynamodb = new DynamoDBClient({
  endpoint: "http://localhost:4566",
  region: "us-east-1",
  credentials: { accessKeyId: "test", secretAccessKey: "test" },
});

const sqs = new SQSClient({
  endpoint: "http://localhost:4566",
  region: "us-east-1",
  credentials: { accessKeyId: "test", secretAccessKey: "test" },
});
```

### Using `AWS_ENDPOINT_URL`

AWS SDK v3 (v3.451.0+) respects the `AWS_ENDPOINT_URL` environment variable.
Set it once and skip per-client endpoint configuration:

```bash
export AWS_ENDPOINT_URL=http://localhost:4566
export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
```

```typescript
// No endpoint needed — the SDK reads AWS_ENDPOINT_URL
const s3 = new S3Client({ region: "us-east-1", forcePathStyle: true });
```

---

## Python (boto3)

```python
import boto3

s3 = boto3.client(
    's3',
    endpoint_url='http://localhost:4566',
    region_name='us-east-1',
    aws_access_key_id='test',
    aws_secret_access_key='test',
)

s3.create_bucket(Bucket='my-bucket')
s3.put_object(Bucket='my-bucket', Key='hello.txt', Body=b'hello')
```

Or set `AWS_ENDPOINT_URL` and use the default session:

```bash
export AWS_ENDPOINT_URL=http://localhost:4566
export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
```

```python
import boto3
s3 = boto3.client('s3', region_name='us-east-1')
```

---

## Go (AWS SDK v2)

```go
package main

import (
    "context"
    "fmt"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/credentials"
    "github.com/aws/aws-sdk-go-v2/service/s3"
)

func main() {
    cfg, _ := config.LoadDefaultConfig(context.TODO(),
        config.WithRegion("us-east-1"),
        config.WithCredentialsProvider(
            credentials.NewStaticCredentialsProvider("test", "test", ""),
        ),
    )

    client := s3.NewFromConfig(cfg, func(o *s3.Options) {
        o.BaseEndpoint = aws.String("http://localhost:4566")
        o.UsePathStyle = true
    })

    _, err := client.CreateBucket(context.TODO(), &s3.CreateBucketInput{
        Bucket: aws.String("my-bucket"),
    })
    fmt.Println("Created bucket:", err)
}
```

---

## Java (AWS SDK v2)

```java
import software.amazon.awssdk.auth.credentials.AwsBasicCredentials;
import software.amazon.awssdk.auth.credentials.StaticCredentialsProvider;
import software.amazon.awssdk.regions.Region;
import software.amazon.awssdk.services.s3.S3Client;
import software.amazon.awssdk.services.s3.S3Configuration;
import java.net.URI;

S3Client s3 = S3Client.builder()
    .endpointOverride(URI.create("http://localhost:4566"))
    .region(Region.US_EAST_1)
    .credentialsProvider(StaticCredentialsProvider.create(
        AwsBasicCredentials.create("test", "test")))
    .serviceConfiguration(S3Configuration.builder()
        .pathStyleAccessEnabled(true)
        .build())
    .build();

s3.createBucket(b -> b.bucket("my-bucket"));
```

---

## .NET (AWS SDK)

```csharp
using Amazon.S3;
using Amazon.Runtime;

var config = new AmazonS3Config
{
    ServiceURL = "http://localhost:4566",
    ForcePathStyle = true,
};

var credentials = new BasicAWSCredentials("test", "test");
var client = new AmazonS3Client(credentials, config);

await client.PutBucketAsync("my-bucket");
```

---

## Rust (AWS SDK)

```rust
use aws_config::BehaviorVersion;
use aws_sdk_s3::config::{Credentials, Region};

let creds = Credentials::new("test", "test", None, None, "overcast");
let config = aws_config::defaults(BehaviorVersion::latest())
    .region(Region::new("us-east-1"))
    .credentials_provider(creds)
    .endpoint_url("http://localhost:4566")
    .load()
    .await;

let s3 = aws_sdk_s3::Client::new(&config);
s3.create_bucket().bucket("my-bucket").send().await.unwrap();
```

---

## Terraform / OpenTofu

Overcast works with the AWS provider's custom endpoints:

```hcl
provider "aws" {
  access_key = "test"
  secret_key = "test"
  region     = "us-east-1"

  s3_use_path_style           = true
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true

  endpoints {
    s3             = "http://localhost:4566"
    sqs            = "http://localhost:4566"
    dynamodb       = "http://localhost:4566"
    lambda         = "http://localhost:4566"
    iam            = "http://localhost:4566"
    sts            = "http://localhost:4566"
    cloudformation = "http://localhost:4566"
    # ... add endpoints for all services you use
  }
}
```

---

## General notes

### Credentials

Overcast accepts any credentials. Use `test`/`test` or any non-empty strings.
SigV4 signatures are accepted but not validated (unless `OVERCAST_SIGV4_VALIDATE`
is enabled in a future version).

### Single endpoint

All AWS services are served from one endpoint (`http://localhost:4566`). You do
not need per-service ports or URLs.

### Account ID and region

Overcast returns `000000000000` as the account ID and `us-east-1` as the
region by default. These appear in ARNs and STS responses. Override with
`OVERCAST_ACCOUNT_ID` and `OVERCAST_DEFAULT_REGION`.

### HTTPS

For HTTPS, configure `OVERCAST_TLS_CERT` and `OVERCAST_TLS_KEY`. See the
[root README](../README.md#https--tls) for details.
