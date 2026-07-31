package router

// EmulationTier describes how completely a service is emulated.
//
//   - "full"        — P1+P2 operations implemented; real SDK clients can use it.
//   - "partial"     — P1 operations implemented; basic workflows work.
//   - "inert"       — Full CRUD works, resources exist, but no side-effects or enforcement.
//   - "stub"        — Registered; all endpoints return 501 Not Implemented.
//   - "unsupported" — Not registered in Overcast; no backend handler exists.
type EmulationTier = string

const (
	TierFull        EmulationTier = "full"
	TierPartial     EmulationTier = "partial"
	TierInert       EmulationTier = "inert"
	TierStub        EmulationTier = "stub"
	TierUnsupported EmulationTier = "unsupported"
)

// ServiceTiers maps the canonical service name (as returned by Service.Name())
// to its current emulation tier. Update this whenever a service graduates
// from stub → partial → full.
//
// Every registered service must appear here: New falls back to TierStub for
// anything missing, so an omission silently reports a working service as
// "stub" in /_health. TestServiceTiers_coverEveryRegisteredService enforces it.
var ServiceTiers = map[string]EmulationTier{
	// Full — P1+P2 complete
	"s3":     TierFull,
	"sqs":    TierFull,
	"sns":    TierFull,
	"lambda": TierFull,

	// Partial — P1 complete
	"dynamodb":        TierPartial,
	"dynamodbstreams": TierPartial,
	"ses":             TierPartial,
	"secretsmanager":  TierPartial,
	"kinesis":         TierPartial,
	"pipes":           TierPartial,
	"logs":            TierPartial,
	"sts":             TierPartial,
	"kms":             TierPartial,
	"ssm":             TierPartial,
	"ec2":             TierPartial,
	"ecs":             TierPartial,
	"ecr":             TierPartial,
	"eks":             TierPartial,
	"rds":             TierPartial,
	"cloudformation":  TierPartial,
	"apigateway":      TierPartial,
	"cognito":         TierPartial,
	"scheduler":       TierPartial,
	"appconfigdata":   TierPartial,

	// Inert — full CRUD, resources stored, but no enforcement / side-effects
	"iam":           TierInert,
	"eventbridge":   TierInert,
	"stepfunctions": TierInert,
	"appsync":       TierInert,
	"cloudfront":    TierInert,

	// Stub — all 501, bar the odd hardcoded response that unblocks a bootstrap
	"waf":    TierStub,
	"shield": TierStub,
	// organizations answers exactly one op — DescribeOrganization, from a
	// hardcoded org with no stored state — so that CDK bootstrap gets past it.
	"organizations": TierStub,

	// Inert stubs — CRUD works for core resources, but no side-effects
	"cloudwatch":  TierInert,
	"acm":         TierInert,
	"opensearch":  TierInert,
	"appconfig":   TierInert,
	"glue":        TierInert,
	"firehose":    TierInert,
	"athena":      TierInert,
	"bedrock":     TierStub,
	"appregistry": TierInert,
	"elasticache": TierPartial,
	"msk":         TierPartial,
	"route53":     TierInert,
	"elbv2":       TierInert,
	"autoscaling": TierInert,
	"cloudtrail":  TierInert,
	"backup":      TierInert,
	"transfer":    TierInert,
	// efs is a full control-plane emulation with lifecycle states, but has no
	// NFS data plane — file systems are not mountable.
	"efs": TierPartial,
}

// ServiceGoalTiers maps each service to its aspirational emulation tier — the
// tier we intend to reach eventually. When ServiceTiers[svc] != ServiceGoalTiers[svc]
// the service is considered "work in progress" and the UI shows a WIP indicator.
//
// Services not listed here have an implicit goal of their current tier
// (i.e. no further improvement is planned or the service is already at its goal).
var ServiceGoalTiers = map[string]EmulationTier{
	// WIP — currently partial, goal is full
	"dynamodb":       TierFull,
	"secretsmanager": TierFull,

	// WIP — currently inert, goal is partial
	"iam":           TierPartial,
	"eventbridge":   TierPartial,
	"stepfunctions": TierPartial,
}
