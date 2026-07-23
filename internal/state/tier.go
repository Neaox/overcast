package state

// Tier classifies a namespace for the HybridStore's memory management strategy.
type Tier int

const (
	// TierHot namespaces are always held in memory and never evicted.
	// These contain resource definitions (queues, topics, tables, etc.) which
	// are small, finite, and needed for instant topology/dashboard renders.
	TierHot Tier = iota

	// TierCached namespaces are read straight from SQLite on every access
	// (HybridStore's lazy SQLite-backed path — see
	// shouldReadHybridNamespaceFromSQLite in hybrid.go), overlaid with a small
	// pending-write cache for changes not yet flushed. There is currently no
	// in-memory LRU cache in front of SQLite for these namespaces — every read
	// not covered by the pending overlay is a SQLite round trip. An
	// LRU-bounded cache tier is a possible future enhancement, not
	// implemented today.
	TierCached
)

// namespaceTiers maps every known namespace to its tier.
// New namespaces default to TierHot (safe — keeps data in memory).
var namespaceTiers = map[string]Tier{
	// ── ACM ─────────────────────────────────────────────────────────────
	"acm:certs": TierHot,
	"acm:tags":  TierHot,

	// ── API Gateway ─────────────────────────────────────────────────────
	"apigw:restapis":          TierHot,
	"apigw:resources":         TierHot,
	"apigw:stages":            TierHot,
	"apigw:deployments":       TierHot,
	"apigw:v2apis":            TierHot,
	"apigw:v2routes":          TierHot,
	"apigw:v2integrations":    TierHot,
	"apigw:v2stages":          TierHot,
	"apigw:v2deployments":     TierHot,
	"apigw:apikeys":           TierHot,
	"apigw:usageplans":        TierHot,
	"apigw:authorizers":       TierHot,
	"apigw:v2authorizers":     TierHot,
	"apigw:models":            TierHot,
	"apigw:requestvalidators": TierHot,
	"apigw:domainnames":       TierHot,
	"apigw:basepathmappings":  TierHot,
	"apigw:vpclinks":          TierHot,
	"apigw:resourcetags":      TierHot,
	"apigw:v2domainnames":     TierHot,
	"apigw:v2vpclinks":        TierHot,
	"apigw:v2apimappings":     TierHot,
	"apigw:v2tags":            TierHot,

	// ── AppConfig ───────────────────────────────────────────────────────
	"appconfig:apps":         TierHot,
	"appconfig:envs":         TierHot,
	"appconfig:profiles":     TierHot,
	"appconfig:hcvcounters":  TierHot,
	"appconfig:hcversions":   TierCached,
	"appconfigdata:sessions": TierHot,

	// ── AppRegistry ─────────────────────────────────────────────────────
	"appregistry:applications":                 TierHot,
	"appregistry:associations":                 TierHot,
	"appregistry:attribute-groups":             TierHot,
	"appregistry:attribute-group-associations": TierHot,

	// ── S3 ──────────────────────────────────────────────────────────────
	"s3:buckets":       TierHot,
	"s3:notifications": TierHot,
	"s3:objects":       TierCached,
	"s3:multipart":     TierCached,
	"s3:parts":         TierCached,

	// ── SQS ─────────────────────────────────────────────────────────────
	"sqs:queues":           TierHot,
	"sqs:purge":            TierHot,
	"sqs:messages":         TierCached,
	"sqs:dedup":            TierCached,
	"sqs:receive-attempts": TierCached,

	// ── SNS ─────────────────────────────────────────────────────────────
	"sns:topics":        TierHot,
	"sns:subscriptions": TierHot,

	// ── DynamoDB ────────────────────────────────────────────────────────
	"dynamodb:tables": TierHot,
	// DynamoDB items/streams use dedicated backends, not state.Store.

	// ── Lambda ──────────────────────────────────────────────────────────
	"lambda:functions":               TierHot,
	"lambda:versions":                TierHot,
	"lambda:aliases":                 TierHot,
	"lambda:esm":                     TierHot,
	"lambda:layers":                  TierHot,
	"lambda:provisioned-concurrency": TierHot,
	"lambda:layer-counters":          TierCached,
	"lambda:version-counters":        TierCached,
	"lambda:invocations":             TierCached,
	"lambda:test-events":             TierCached,

	// ── CloudWatch Logs ─────────────────────────────────────────────────
	"logs:groups":  TierHot,
	"logs:streams": TierHot,
	"logs:events":  TierCached,

	// ── Kinesis ─────────────────────────────────────────────────────────
	"kinesis:streams": TierHot,
	"kinesis:records": TierCached,

	// ── Athena ──────────────────────────────────────────────────────────
	"athena:workgroups": TierHot,
	"athena:queries":    TierCached,

	// ── Auto Scaling ────────────────────────────────────────────────────
	"autoscaling:groups":        TierHot,
	"autoscaling:launchconfigs": TierHot,
	"autoscaling:policies":      TierHot,
	"autoscaling:hooks":         TierHot,
	"autoscaling:grouptags":     TierHot,

	// ── Backup ──────────────────────────────────────────────────────────
	"backup:vaults": TierHot,
	"backup:plans":  TierHot,

	// ── CloudFormation Events ───────────────────────────────────────────
	"cfn:events": TierCached,

	// ── CloudFront ──────────────────────────────────────────────────────
	"cloudfront": TierHot,

	// ── CloudTrail ──────────────────────────────────────────────────────
	"cloudtrail:trails": TierHot,

	// ── CloudWatch ──────────────────────────────────────────────────────
	"cloudwatch:alarms":     TierHot,
	"cloudwatch:tags":       TierHot,
	"cloudwatch:metrics":    TierCached,
	"cloudwatch:metricdata": TierCached,

	// ── Cognito ─────────────────────────────────────────────────────────
	"cognito:pools":          TierHot,
	"cognito:clients:lookup": TierHot,
	"cognito:tokens":         TierHot,
	"cognito:domains":        TierHot,
	"cognito:authcodes":      TierHot,
	"cognito:loginsessions":  TierHot,
	"cognito:sigkeys":        TierHot,

	// ── KMS ─────────────────────────────────────────────────────────────
	"kms": TierHot,

	// ── SSM ─────────────────────────────────────────────────────────────
	"ssm": TierHot,

	// ── Secrets Manager ─────────────────────────────────────────────────
	"secretsmanager:secrets": TierHot,

	// ── SES ─────────────────────────────────────────────────────────────
	"ses:identities": TierHot,
	"ses:templates":  TierHot,

	// ── IAM (global) ────────────────────────────────────────────────────
	"iam:users":    TierHot,
	"iam:roles":    TierHot,
	"iam:policies": TierHot,
	"iam:groups":   TierHot,
	"iam:profiles": TierHot,
	"iam:sessions": TierHot,

	// ── EC2 ─────────────────────────────────────────────────────────────
	"ec2:vpcs":                     TierHot,
	"ec2:subnets":                  TierHot,
	"ec2:security-groups":          TierHot,
	"ec2:instances":                TierHot,
	"ec2:keypairs":                 TierHot,
	"ec2:route-tables":             TierHot,
	"ec2:internet-gateways":        TierHot,
	"ec2:vpn-gateways":             TierHot,
	"ec2:vpc-peering-connections":  TierHot,
	"ec2:tags":                     TierHot,
	"ec2:elastic-ips":              TierHot,
	"ec2:nat-gateways":             TierHot,
	"ec2:network-interfaces":       TierHot,
	"ec2:vpc-endpoints":            TierHot,
	"ec2:vpc-ip-translations":      TierHot,
	"ec2:vpc-ip-translations-real": TierHot,

	// ── ECR ─────────────────────────────────────────────────────────────
	"ecr:repositories": TierHot,
	"ecr:tags":         TierHot,
	"ecr:policies":     TierHot,
	"ecr:lifecycle":    TierHot,
	"ecr:images":       TierCached,

	// ── ECS ─────────────────────────────────────────────────────────────
	"ecs:clusters":            TierHot,
	"ecs:task-definitions":    TierHot,
	"ecs:task-def-families":   TierHot,
	"ecs:services":            TierHot,
	"ecs:tags":                TierHot,
	"ecs:capacity-providers":  TierHot,
	"ecs:task-sets":           TierHot,
	"ecs:account-settings":    TierHot,
	"ecs:container-instances": TierHot,
	"ecs:tasks":               TierCached,

	// ── EKS ─────────────────────────────────────────────────────────────
	"eks:clusters":                TierHot,
	"eks:nodegroups":              TierHot,
	"eks:updates":                 TierHot,
	"eks:tags":                    TierHot,
	"eks:fargate":                 TierHot,
	"eks:addons":                  TierHot,
	"eks:idpconfigs":              TierHot,
	"eks:accessentries":           TierHot,
	"eks:accesspolicies":          TierHot,
	"eks:podidentityassociations": TierHot,

	// ── ElastiCache / ELBv2 ─────────────────────────────────────────────
	"elasticache:clusters":           TierHot,
	"elasticache:replication-groups": TierHot,
	"elasticache:serverless-caches":  TierHot,
	"elasticache:subnet-groups":      TierHot,
	"elasticache:parameter-groups":   TierHot,
	"elasticache:tags":               TierHot,
	"elasticache:ports":              TierHot,
	"elbv2:loadbalancers":            TierHot,
	"elbv2:targetgroups":             TierHot,
	"elbv2:listeners":                TierHot,
	"elbv2:targets":                  TierHot,

	// ── Firehose / Glue ─────────────────────────────────────────────────
	"firehose:streams": TierHot,
	"glue:databases":   TierHot,
	"glue:tables":      TierHot,

	// ── Pipes ───────────────────────────────────────────────────────────
	"pipes:pipes": TierHot,

	// ── MSK / OpenSearch / RDS ──────────────────────────────────────────
	"msk:clusters":         TierHot,
	"msk:configurations":   TierHot,
	"msk:tags":             TierHot,
	"msk:ports":            TierHot,
	"opensearch:domains":   TierHot,
	"opensearch:tags":      TierHot,
	"rds:instances":        TierHot,
	"rds:clusters":         TierHot,
	"rds:ports":            TierHot,
	"rds:subnet-groups":    TierHot,
	"rds:parameter-groups": TierHot,

	// ── Route53 ─────────────────────────────────────────────────────────
	"route53:zones":   TierHot,
	"route53:changes": TierHot,
	"route53:rrsets":  TierCached,

	// ── Shield / Transfer / WAF ─────────────────────────────────────────
	"shield:protections": TierHot,
	"transfer:servers":   TierHot,
	"transfer:users":     TierHot,
	"waf:webacls":        TierHot,

	// ── EventBridge / Scheduler ────────────────────────────────────────
	"eb:buses":            TierHot,
	"eb:rules":            TierHot,
	"eb:tags":             TierHot,
	"eb:targets":          TierHot,
	"eb:last-fire":        TierHot,
	"eb:next-fire":        TierHot,
	"scheduler:groups":    TierHot,
	"scheduler:schedules": TierHot,
	"scheduler:tags":      TierHot,
	"scheduler:last_fire": TierHot,

	// ── CloudFormation ──────────────────────────────────────────────────
	"cfn:stacks":     TierHot,
	"cfn:changesets": TierHot,

	// ── Step Functions ──────────────────────────────────────────────────
	"stepfunctions": TierHot,

	// ── AppSync ─────────────────────────────────────────────────────────
	"appsync": TierHot,
}

// TierFor returns the tier for a namespace. Unknown namespaces default to TierHot.
func TierFor(namespace string) Tier {
	if t, ok := namespaceTiers[namespace]; ok {
		return t
	}
	return TierHot
}
