/**
 * Imports all search contributors, triggering their `registerContributor` side-effects.
 * Import this file once from the application entry point or search component.
 */
// TODO(priority:P2): the s3, sqs, sns, kinesis, lambda, secretsmanager and logs contributors build cacheKey as [service, resource, baseUrl] — and logs omits the endpoint entirely — so none of them match the feature query keys' required [baseUrl, region, ...] shape; global search therefore always misses the cache and refetches, and it ignores the selected region.
import "./s3"
import "./sqs"
import "./sns"
import "./dynamodb"
import "./kinesis"
import "./lambda"
import "./secretsmanager"
import "./logs"
import "./elasticache"
import "./msk"
import "./ecr"
import "./waf"
import "./inbox"
import "./docs"
