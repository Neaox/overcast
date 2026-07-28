package awsapi

// targetOperation and queryOperation are populated by awsmodelgen. They are
// intentionally private immutable generated data; Registry is the only
// runtime access point.
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
	{ModelService: "auto-scaling", OvercastService: "autoscaling"},
	{ModelService: "dynamodb-streams", OvercastService: "dynamodbstreams"},
	{ModelService: "elastic-load-balancing-v2", OvercastService: "elbv2"},
	{ModelService: "kafka", OvercastService: "msk"},
	{ModelService: "route-53", OvercastService: "route53"},
	{ModelService: "secrets-manager", OvercastService: "secretsmanager"},
	{ModelService: "service-catalog-appregistry", OvercastService: "appregistry"},
	{ModelService: "sfn", OvercastService: "stepfunctions"},
}
