package awsapi

// targetOperation, queryOperation, and the REST trie types are populated by
// awsmodelgen. They are intentionally private immutable generated data;
// Registry is the only runtime access point.
type targetOperation struct {
	Target       string
	ModelService string
	Operation    string
	Protocol     Protocol
	Ambiguous    bool
}

type queryOperation struct {
	Version      string
	Operation    string
	ModelService string
	Protocol     Protocol
	Ambiguous    bool
}

// restTrieNode stores indexes into the immutable literal-edge and operation
// tables. Parameter and greedy nodes use -1 when the corresponding edge is
// absent. Keeping only indexes here makes the generated trie compact and lets
// ClaimREST walk it without building maps during router startup.
type restTrieNode struct {
	LiteralStart   int
	LiteralEnd     int
	Parameter      int
	Greedy         int
	OperationStart int
	OperationEnd   int
}

type restTrieEdge struct {
	Segment string
	Node    int
}

type restOperation struct {
	Method       string
	Query        string
	ModelService string
	SigningName  string
	Operation    string
	Protocol     Protocol
	Ambiguous    bool
}

type rpcOperation struct {
	Protocol     Protocol
	ServiceShape string
	Operation    string
	ModelService string
	Ambiguous    bool
}

type serviceAlias struct {
	ModelService    string
	OvercastService string
}

// operationCollision is generated when distinct modeled services have the
// same wire identifier. Registry preserves the shared protocol/error profile
// but deliberately does not attribute the operation to either service.
type operationCollision struct {
	Key      string
	Services []string
}

// serviceAliases must stay strictly sorted by ModelService for binary search.
//
// "cognito-identity" (Cognito Federated Identities, identity pools) is
// deliberately absent: Overcast's cognito service implements only
// cognito-identity-provider (user pools), so identity-pool operations must
// keep their own identity rather than validate against the cognito key.
var serviceAliases = []serviceAlias{
	{ModelService: "api-gateway", OvercastService: "apigateway"},
	{ModelService: "apigatewayv2", OvercastService: "apigateway"},
	{ModelService: "auto-scaling", OvercastService: "autoscaling"},
	{ModelService: "bedrock-runtime", OvercastService: "bedrock"},
	{ModelService: "cognito-identity-provider", OvercastService: "cognito"},
	{ModelService: "dynamodb-streams", OvercastService: "dynamodbstreams"},
	{ModelService: "elastic-load-balancing-v2", OvercastService: "elbv2"},
	{ModelService: "kafka", OvercastService: "msk"},
	{ModelService: "route-53", OvercastService: "route53"},
	{ModelService: "secrets-manager", OvercastService: "secretsmanager"},
	{ModelService: "service-catalog-appregistry", OvercastService: "appregistry"},
	// Overcast's ses service implements both SES v1 (Query) and SES v2
	// (REST-JSON) operations under the single "ses" key.
	{ModelService: "sesv2", OvercastService: "ses"},
	{ModelService: "sfn", OvercastService: "stepfunctions"},
	// Overcast's waf service implements WAF v2 (AWSWAF_20190729), which the
	// model names "wafv2". The model's "waf" identity is WAF *Classic*
	// (AWSWAF_20150824) — unimplemented, and its operation names overlap with
	// v2 (CreateWebACL et al.), so it is diverted to the deliberately
	// unregistered "waf-classic" key to keep classic operations from silently
	// validating capability rows declared against the v2-implementing "waf".
	{ModelService: "waf", OvercastService: "waf-classic"},
	{ModelService: "wafv2", OvercastService: "waf"},
}
