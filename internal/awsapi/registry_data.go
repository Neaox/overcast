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
	{ModelService: "sfn", OvercastService: "stepfunctions"},
}
