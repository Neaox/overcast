package state

// Tier classifies a namespace for the HybridStore's memory management strategy.
type Tier int

const (
	// TierHot namespaces are always held in memory and never evicted.
	// These contain resource definitions (queues, topics, tables, etc.) which
	// are small, finite, and needed for instant topology/dashboard renders.
	TierHot Tier = iota

	// TierCached namespaces are served from an LRU-bounded memory cache with
	// SQLite as the overflow tier. When the memory budget is exceeded, least
	// recently accessed entries are evicted from memory but remain on disk.
	TierCached
)

// namespaceTiers maps every known namespace to its tier.
// New namespaces default to TierHot (safe — keeps data in memory).
var namespaceTiers = map[string]Tier{
	// ── S3 ──────────────────────────────────────────────────────────────
	"s3:buckets":       TierHot,
	"s3:notifications": TierHot,
	"s3:objects":       TierCached,
	"s3:multipart":     TierCached,
	"s3:parts":         TierCached,

	// ── SQS ─────────────────────────────────────────────────────────────
	"sqs:queues":   TierHot,
	"sqs:messages": TierCached,
	"sqs:dedup":    TierCached,

	// ── SNS ─────────────────────────────────────────────────────────────
	"sns:topics":        TierHot,
	"sns:subscriptions": TierHot,

	// ── DynamoDB ────────────────────────────────────────────────────────
	"dynamodb:tables": TierHot,
	// DynamoDB items/streams use dedicated backends, not state.Store.

	// ── Lambda ──────────────────────────────────────────────────────────
	"lambda:functions":        TierHot,
	"lambda:versions":         TierHot,
	"lambda:aliases":          TierHot,
	"lambda:esm":              TierHot,
	"lambda:layers":           TierHot,
	"lambda:layer-counters":   TierCached,
	"lambda:version-counters": TierCached,
	"lambda:invocations":      TierCached,
	"lambda:test-events":      TierCached,

	// ── CloudWatch Logs ─────────────────────────────────────────────────
	"logs:groups":  TierHot,
	"logs:streams": TierHot,
	"logs:events":  TierCached,

	// ── Kinesis ─────────────────────────────────────────────────────────
	"kinesis:streams": TierHot,
	"kinesis:records": TierCached,

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

	// ── EC2 ─────────────────────────────────────────────────────────────
	"ec2:vpcs":            TierHot,
	"ec2:subnets":         TierHot,
	"ec2:security-groups": TierHot,

	// ── Pipes ───────────────────────────────────────────────────────────
	"pipes:pipes": TierHot,

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
